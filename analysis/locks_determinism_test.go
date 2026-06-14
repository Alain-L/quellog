package analysis

import (
	"reflect"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// TestLockBlockingResolutionDeterministic guards against the pre-existing
// non-determinism in cross-PID blocking/relation resolution: when a backend
// held several concurrent waiting locks, processBlockingDetail /
// processRelationContext picked a matching activeLock via `for range map {
// ...; break }`, whose target varied with Go's randomized map iteration order.
// That made the acquired event's blocking_pid / blocking_query / relation
// differ run-to-run on lock-heavy logs.
//
// The fix selects the backend's most recent waiting lock (largest
// waitingEventID) deterministically. This test processes a fixed scenario
// through many fresh analyzers and asserts every run yields identical metrics.
func TestLockBlockingResolutionDeterministic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	var entries []parser.LogEntry
	n := 0
	add := func(pid, msg string) {
		entries = append(entries, parser.LogEntry{
			Timestamp: base.Add(time.Duration(n) * time.Second),
			Message:   msg,
			PID:       pid,
		})
		n++
	}

	// Two backends, each holding TWO concurrent waiting locks (distinct
	// resources → distinct activeLock entries) when a DETAIL/CONTEXT arrives.
	// This is the exact shape that exercised the randomized map pick.
	for _, pid := range []string{"12345", "67890"} {
		add(pid, "process "+pid+" still waiting for ShareLock on transaction 111 after 1000.000 ms")
		add(pid, "process "+pid+" still waiting for AccessExclusiveLock on relation 222 after 1500.000 ms")
		// DETAIL + CONTEXT now resolve against TWO live waiting locks for pid.
		add(pid, "Process holding the lock: 999. Wait queue: "+pid+".")
		add(pid, "while locking tuple (0,1) in relation \"mytable\"")
		// Acquire both — the acquired events carry whatever the locks captured.
		add(pid, "process "+pid+" acquired ShareLock on transaction 111 after 2000.000 ms")
		add(pid, "process "+pid+" acquired AccessExclusiveLock on relation 222 after 2500.000 ms")
	}

	run := func() LockMetrics {
		a := NewLockAnalyzer()
		for i := range entries {
			a.Process(&entries[i])
		}
		return a.Finalize()
	}

	first := run()
	// Sanity: the scenario must actually produce lock events, else vacuous.
	if len(first.Events) == 0 {
		t.Fatalf("scenario produced no lock events: %+v", first)
	}
	for i := 0; i < 50; i++ {
		got := run()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("lock metrics differ across runs (non-deterministic resolution)\n run0=%+v\n run%d=%+v",
				first.Events, i+1, got.Events)
		}
	}
}
