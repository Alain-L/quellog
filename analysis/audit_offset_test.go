package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

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

// TestEventsTrueInstantEpoch guards R3: top_events[].timestamps must be
// stored as the TRUE Unix-millisecond instant (entry.UnixMilli), not the
// wall-clock-as-UTC shift. A CEST (+02:00) 10:00:00 event is the absolute
// instant 08:00:00Z, so the stored epoch must be that instant — the --json
// timestamps stay a genuine epoch, matching v0.11.0.
func TestEventsTrueInstantEpoch(t *testing.T) {
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
	// The true instant: 10:00 CEST is 08:00 UTC.
	wantInstant := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC).UnixMilli()
	if got != wantInstant {
		t.Errorf("stored epoch = %d, want true instant %d (08:00:00Z)", got, wantInstant)
	}
	if got != entry.Timestamp.UnixMilli() {
		t.Errorf("stored epoch %d != entry.UnixMilli %d; the true instant was not stored", got, entry.Timestamp.UnixMilli())
	}
	// And it must NOT be the wall-clock reinterpreted as UTC (10:00Z): that
	// was the reverted regression, off by the +2h offset.
	wallAsUTC := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC).UnixMilli()
	if got == wallAsUTC {
		t.Errorf("stored epoch equals the wall-as-UTC value %d; the offset shift was not reverted", wallAsUTC)
	}
}
