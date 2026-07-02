package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// TestLockRelockCountsAsNewEpisode guards the counter fix: a backend that
// re-locks the same resource in a later episode must count as a NEW lock, not
// as a repeat of the stale activeLock entry. Before the fix, the stale entry
// (never purged after acquisition) made the second "acquired" bump
// acquiredEvents without bumping totalEvents, so acquiredEvents > totalEvents
// and the invariant total >= acquired broke (masked by a clamp on the derived
// WaitingEvents).
func TestLockRelockCountsAsNewEpisode(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()

	run := func(msgs []string) LockMetrics {
		a := NewLockAnalyzer()
		for i, m := range msgs {
			e := parser.LogEntry{
				Timestamp: base.Add(time.Duration(i) * time.Second),
				Message:   m,
				PID:       "100",
			}
			a.Process(&e)
		}
		return a.Finalize()
	}

	t.Run("relock with waiting", func(t *testing.T) {
		// Same PID + AccessExclusiveLock + relation 16384, twice.
		m := run([]string{
			"process 100 still waiting for AccessExclusiveLock on relation 16384 of database 5 after 1000.000 ms",
			"process 100 acquired AccessExclusiveLock on relation 16384 of database 5 after 3000.000 ms",
			"process 100 still waiting for AccessExclusiveLock on relation 16384 of database 5 after 1000.000 ms",
			"process 100 acquired AccessExclusiveLock on relation 16384 of database 5 after 5000.000 ms",
		})
		if m.TotalEvents < m.AcquiredEvents {
			t.Fatalf("invariant broken: total=%d < acquired=%d", m.TotalEvents, m.AcquiredEvents)
		}
		if m.TotalEvents != 2 || m.AcquiredEvents != 2 || m.WaitingEvents != 0 {
			t.Fatalf("got total=%d acquired=%d waiting=%d, want 2/2/0", m.TotalEvents, m.AcquiredEvents, m.WaitingEvents)
		}
	})

	t.Run("direct re-acquire without waiting", func(t *testing.T) {
		// Fast acquisitions (no "still waiting") of the same key, twice.
		m := run([]string{
			"process 100 acquired ShareLock on transaction 42 after 0.100 ms",
			"process 100 acquired ShareLock on transaction 42 after 0.200 ms",
		})
		if m.TotalEvents < m.AcquiredEvents {
			t.Fatalf("invariant broken: total=%d < acquired=%d", m.TotalEvents, m.AcquiredEvents)
		}
		if m.TotalEvents != 2 || m.AcquiredEvents != 2 {
			t.Fatalf("got total=%d acquired=%d, want 2/2", m.TotalEvents, m.AcquiredEvents)
		}
	})
}
