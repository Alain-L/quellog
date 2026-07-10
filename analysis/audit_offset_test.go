package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// TestSQLExecutionsMixedZonePreservesWallClock is the sql-executions twin of
// the lock/temp mixed-zone guards (see mixedzone_test.go): two executions
// logged at the same local "10:00:00" but in different zones (CEST then UTC)
// must each render their own wall-clock, not both rebased into the first
// event's zone. The compact executions store used to reconstruct every row
// through the FIRST appended event's location.
func TestSQLExecutionsMixedZonePreservesWallClock(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	utc := time.FixedZone("UTC", 0)

	a := NewSQLAnalyzer()
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   "LOG:  duration: 5.234 ms  statement: SELECT 1",
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, utc),
		PID:       "200",
		Message:   "LOG:  duration: 9.876 ms  statement: SELECT 2",
	})
	m := a.Finalize()

	var got []time.Time
	m.IterateExecutions(func(e QueryExecution) bool {
		got = append(got, e.Timestamp)
		return true
	})
	if len(got) != 2 {
		t.Fatalf("got %d executions, want 2", len(got))
	}
	for i, want := range []string{"2026-07-15 10:00:00", "2026-07-15 10:00:00"} {
		if g := got[i].Format(jsonTSLayout); g != want {
			t.Errorf("execution %d rendered %q, want %q (regression: rebased into first event's zone)", i, g, want)
		}
	}
	// Zero-cost storage keeps wall-clock-as-UTC, not the true instant, so two
	// events at the same local 10:00 (different zones) render identically and
	// compare equal — absolute-instant ordering is intentionally not retained.
}

// TestSQLExecutionsMergePreservesOffset proves a PID-sharded fold keeps each
// row's own offset: the merge re-appends via ForEach → append, and append
// re-reads the offset from the round-tripped Timestamp's zone.
func TestSQLExecutionsMergePreservesOffset(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	utc := time.FixedZone("UTC", 0)

	a := NewSQLAnalyzer()
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   "LOG:  duration: 5.234 ms  statement: SELECT 1",
	})
	b := NewSQLAnalyzer()
	b.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, utc),
		PID:       "200",
		Message:   "LOG:  duration: 9.876 ms  statement: SELECT 2",
	})
	a.Merge(b)
	m := a.Finalize()

	seen := 0
	m.IterateExecutions(func(e QueryExecution) bool {
		if g := e.Timestamp.Format(jsonTSLayout); g != "2026-07-15 10:00:00" {
			t.Errorf("merged execution rendered %q, want %q", g, "2026-07-15 10:00:00")
		}
		seen++
		return true
	})
	if seen != 2 {
		t.Fatalf("got %d merged executions, want 2", seen)
	}
}

// TestConnectionsMixedZonePreservesWallClock covers both received timestamps
// and session events: each must render in its own captured offset, not the
// first event's zone.
func TestConnectionsMixedZonePreservesWallClock(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	utc := time.FixedZone("UTC", 0)

	a := NewConnectionAnalyzer()
	// Two received connections at the same local 10:00:00, different zones.
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   "LOG:  connection received: host=1.2.3.4",
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, utc),
		PID:       "200",
		Message:   "LOG:  connection received: host=1.2.3.5",
	})
	// Two disconnects (5s sessions) at the same local 10:00:10, different zones.
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 10, 0, cest),
		PID:       "100",
		Message:   "LOG:  disconnection: session time: 0:00:05.000 user=u database=d host=1.2.3.4",
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 10, 0, utc),
		PID:       "200",
		Message:   "LOG:  disconnection: session time: 0:00:05.000 user=u database=d host=1.2.3.5",
	})
	m := a.Finalize()

	var recv []time.Time
	m.IterateConnections(func(ts time.Time) bool {
		recv = append(recv, ts)
		return true
	})
	if len(recv) != 2 {
		t.Fatalf("got %d received, want 2", len(recv))
	}
	for i := range recv {
		if g := recv[i].Format(jsonTSLayout); g != "2026-07-15 10:00:00" {
			t.Errorf("received %d rendered %q, want %q (regression: rebased into first event's zone)", i, g, "2026-07-15 10:00:00")
		}
	}
	// (Both received store wall-clock-as-UTC, so they render identically and
	// compare equal; absolute-instant ordering is intentionally not retained.)

	var sessions []SessionEvent
	m.IterateSessionEvents(func(se SessionEvent) bool {
		sessions = append(sessions, se)
		return true
	})
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	for i, se := range sessions {
		if g := se.StartTime.Format(jsonTSLayout); g != "2026-07-15 10:00:05" {
			t.Errorf("session %d start rendered %q, want %q", i, g, "2026-07-15 10:00:05")
		}
		if g := se.EndTime.Format(jsonTSLayout); g != "2026-07-15 10:00:10" {
			t.Errorf("session %d end rendered %q, want %q", i, g, "2026-07-15 10:00:10")
		}
	}
}

// TestConnectionsOrphanFlag proves the flushed-orphan sessions carry
// Orphan == true while genuine disconnects carry Orphan == false, so the
// report can exclude orphans by flag instead of an end-timestamp compare.
func TestConnectionsOrphanFlag(t *testing.T) {
	utc := time.FixedZone("UTC", 0)

	a := NewConnectionAnalyzer()
	// PID 100 connects and never disconnects -> orphan at Finalize.
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, utc),
		PID:       "100",
		Message:   "LOG:  connection received: host=1.2.3.4",
	})
	// PID 200 connects and disconnects with a session time -> real session.
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 3, 0, utc),
		PID:       "200",
		Message:   "LOG:  connection received: host=1.2.3.5",
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 10, 0, utc),
		PID:       "200",
		Message:   "LOG:  disconnection: session time: 0:00:05.000 user=u database=d host=1.2.3.5",
	})
	m := a.Finalize()

	var real, orphan []SessionEvent
	m.IterateSessionEvents(func(se SessionEvent) bool {
		if se.Orphan {
			orphan = append(orphan, se)
		} else {
			real = append(real, se)
		}
		return true
	})
	if len(real) != 1 {
		t.Fatalf("got %d real sessions, want 1", len(real))
	}
	if len(orphan) != 1 {
		t.Fatalf("got %d orphan sessions, want 1", len(orphan))
	}
	// The genuine disconnect keeps its PostgreSQL-reported 5s window.
	if g := real[0].StartTime.Format(jsonTSLayout); g != "2026-07-15 10:00:05" {
		t.Errorf("real session start = %q, want %q", g, "2026-07-15 10:00:05")
	}
	// The orphan was opened at its received time and closed at the last
	// observed timestamp.
	if g := orphan[0].StartTime.Format(jsonTSLayout); g != "2026-07-15 10:00:00" {
		t.Errorf("orphan start = %q, want %q", g, "2026-07-15 10:00:00")
	}
	// DisconnectionCount counts only the genuine disconnect, not the orphan.
	if m.DisconnectionCount != 1 {
		t.Errorf("DisconnectionCount = %d, want 1 (orphan must not count)", m.DisconnectionCount)
	}
}

// TestEventsWallClockAsUTCEpoch guards FIX #3: top_events[].timestamps must be
// stored as the wall-clock-as-UTC epoch (entry.UnixMilli + offset), not the
// true instant, so the web report's UTC getters render the log's own clock
// consistently with the per-event lock/temp offsets.
func TestEventsWallClockAsUTCEpoch(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	entry := &parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   `ERROR:  duplicate key value violates unique constraint "users_pkey"`,
	}

	a := NewEventAnalyzer()
	a.Process(entry)
	_, stats := a.Finalize()

	if len(stats) != 1 {
		t.Fatalf("got %d event stats, want 1", len(stats))
	}
	if len(stats[0].Timestamps) != 1 {
		t.Fatalf("got %d timestamps, want 1", len(stats[0].Timestamps))
	}
	got := stats[0].Timestamps[0]
	// Wall clock 10:00 CEST, reinterpreted as UTC.
	wantWallAsUTC := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC).UnixMilli()
	if got != wantWallAsUTC {
		t.Errorf("stored epoch = %d, want wall-as-UTC %d", got, wantWallAsUTC)
	}
	// And it must NOT be the true instant (08:00 UTC): the whole point is the
	// +2h shift by the event's own offset.
	if got == entry.Timestamp.UnixMilli() {
		t.Errorf("stored epoch equals the true instant %d; the offset shift was not applied", entry.Timestamp.UnixMilli())
	}
	if delta := got - entry.Timestamp.UnixMilli(); delta != int64(2*3600*1000) {
		t.Errorf("offset shift = %d ms, want %d ms", delta, int64(2*3600*1000))
	}
}
