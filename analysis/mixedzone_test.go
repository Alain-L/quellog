package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// jsonTSLayout is the zone-less layout the output formatters use for temp-file
// and lock event timestamps (see output/json.go streamTempFileEventsJSON /
// streamLockEventsJSON). Only the offset — never the zone name — reaches the
// rendered digits, which is why per-event FixedZone materialization is
// byte-transparent for single-offset logs.
const jsonTSLayout = "2006-01-02 15:04:05"

// TestTempFileMixedZonePreservesWallClock guards R4 finding 14: compact
// temp-file events used to rebase every timestamp into the FIRST event's
// timezone, so a later event logged at a different offset showed the wrong
// wall-clock digits. Two events logged at the same local "10:00:00" but in
// different zones (CEST then UTC) must each render "10:00:00" — not the second
// one rebased into the first's +02:00 (which would read "12:00:00").
func TestTempFileMixedZonePreservesWallClock(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600) // +02:00
	utc := time.FixedZone("UTC", 0)        // +00:00

	// Same local wall-clock, different zones -> different absolute instants.
	t1 := time.Date(2026, 7, 15, 10, 0, 0, 0, cest)
	t2 := time.Date(2026, 7, 15, 10, 0, 0, 0, utc)

	a := NewTempFileAnalyzer()
	a.Process(&parser.LogEntry{
		Timestamp: t1,
		PID:       "100",
		Message:   `LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp100.0", size 1048576`,
	})
	a.Process(&parser.LogEntry{
		Timestamp: t2,
		PID:       "200",
		Message:   `LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp200.0", size 2097152`,
	})
	m := a.Finalize()

	if len(m.Events) != 2 {
		t.Fatalf("got %d temp-file events, want 2", len(m.Events))
	}
	if got := m.Events[0].Timestamp.Format(jsonTSLayout); got != "2026-07-15 10:00:00" {
		t.Errorf("event0 (CEST) rendered %q, want %q", got, "2026-07-15 10:00:00")
	}
	if got := m.Events[1].Timestamp.Format(jsonTSLayout); got != "2026-07-15 10:00:00" {
		t.Errorf("event1 (UTC) rendered %q, want %q (regression: rebased into first event's zone)", got, "2026-07-15 10:00:00")
	}
	// The absolute ordering/instant must survive: 10:00 CEST is two hours
	// before 10:00 UTC.
	if !m.Events[0].Timestamp.Before(m.Events[1].Timestamp) {
		t.Errorf("absolute ordering lost: event0 (%v) should precede event1 (%v)",
			m.Events[0].Timestamp, m.Events[1].Timestamp)
	}
}

// TestTempFileSingleZoneByteTransparent documents the byte-transparency
// argument: on a single-offset log every event materializes at the same offset,
// so the rendered digits equal the original local time and match the previous
// shared-location behaviour exactly.
func TestTempFileSingleZoneByteTransparent(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	a := NewTempFileAnalyzer()
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   `LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp100.0", size 1048576`,
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 30, 0, 0, cest),
		PID:       "100",
		Message:   `LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp101.0", size 2097152`,
	})
	m := a.Finalize()
	if len(m.Events) != 2 {
		t.Fatalf("got %d temp-file events, want 2", len(m.Events))
	}
	for i, want := range []string{"2026-07-15 10:00:00", "2026-07-15 10:30:00"} {
		if got := m.Events[i].Timestamp.Format(jsonTSLayout); got != want {
			t.Errorf("event%d rendered %q, want %q", i, got, want)
		}
	}
}

// TestLockMixedZonePreservesWallClock is the lock-analyzer twin of the
// temp-file regression: two "acquired" events at the same local "10:00:00" but
// in different zones must each keep their own wall-clock digits.
func TestLockMixedZonePreservesWallClock(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	utc := time.FixedZone("UTC", 0)

	a := NewLockAnalyzer()
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, cest),
		PID:       "100",
		Message:   "process 100 acquired ShareLock on transaction 42 after 1000.000 ms",
	})
	a.Process(&parser.LogEntry{
		Timestamp: time.Date(2026, 7, 15, 10, 0, 0, 0, utc),
		PID:       "200",
		Message:   "process 200 acquired ShareLock on transaction 43 after 2000.000 ms",
	})
	m := a.Finalize()

	if len(m.Events) != 2 {
		t.Fatalf("got %d lock events, want 2", len(m.Events))
	}
	if got := m.Events[0].Timestamp.Format(jsonTSLayout); got != "2026-07-15 10:00:00" {
		t.Errorf("event0 (CEST) rendered %q, want %q", got, "2026-07-15 10:00:00")
	}
	if got := m.Events[1].Timestamp.Format(jsonTSLayout); got != "2026-07-15 10:00:00" {
		t.Errorf("event1 (UTC) rendered %q, want %q (regression: rebased into first event's zone)", got, "2026-07-15 10:00:00")
	}
	if !m.Events[0].Timestamp.Before(m.Events[1].Timestamp) {
		t.Errorf("absolute ordering lost: event0 (%v) should precede event1 (%v)",
			m.Events[0].Timestamp, m.Events[1].Timestamp)
	}
}
