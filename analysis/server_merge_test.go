package analysis

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

func runShardedServer(entries []parser.LogEntry, n int) ServerMetrics {
	shards := make([]*ServerAnalyzer, n)
	for i := range shards {
		shards[i] = NewServerAnalyzer()
	}
	for i := range entries {
		e := &entries[i]
		shards[shardForPID(e.PID, n)].Process(e)
	}
	merged := shards[0]
	for i := 1; i < n; i++ {
		merged.Merge(shards[i])
	}
	return merged.Finalize()
}

func runSingleServer(entries []parser.LogEntry) ServerMetrics {
	a := NewServerAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// TestServerShardMergeParity_Synthetic spreads server-lifecycle events across
// many PIDs (real postmaster events share one PID, but the merge path must
// hold regardless). It deliberately emits MORE than serverTimelineCap
// timeline-pushing events so the cap re-truncation after the merge-sort is
// exercised: the merged timeline must equal the single-pass earliest-cap
// events. ServerMetrics has no accumulated-float fields, so reflect.DeepEqual
// is an exact parity check.
func TestServerShardMergeParity_Synthetic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	var entries []parser.LogEntry
	ts := 0
	add := func(pid, body string) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        "[" + pid + "] LOG:  " + body,
			IsContinuation: false,
			PID:            pid,
		})
		ts++
	}
	addWarn := func(pid, body string) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        "[" + pid + "] WARNING:  " + body,
			IsContinuation: false,
			PID:            pid,
		})
		ts++
	}

	// Timeline-pushing lifecycle events, round-robin across PIDs, enough
	// rounds to comfortably exceed serverTimelineCap (50).
	pids := []string{"301", "302", "303", "304", "305", "306", "307", "308", "309", "310", "311", "312"}
	kinds := []func(pid string){
		func(pid string) { add(pid, "database system is ready to accept connections") },      // start
		func(pid string) { add(pid, "received SIGHUP, reloading configuration files") },      // reload
		func(pid string) { add(pid, "received fast shutdown request") },                      // shutdown fast
		func(pid string) { add(pid, "database system was interrupted; last known up at X") }, // interrupted
		func(pid string) {
			add(pid, "database system was not properly shut down; automatic recovery in progress")
		},
		func(pid string) {
			add(pid, "server process (PID 99"+pid+") was terminated by signal 11: Segmentation fault")
		},
	}
	for round := 0; round < 6; round++ {
		for k, pid := range pids {
			kinds[(round+k)%len(kinds)](pid)
		}
	}

	// Non-timeline events too: shutdown-complete, parameter changes (ordered
	// by timestamp), immediate/smart shutdowns, aux-process exits, signals.
	add("301", "database system is shut down")
	add("302", "received immediate shutdown request")
	add("303", "received smart shutdown request")
	add("304", "server process (PID 12345) was terminated by signal 9: Killed")
	add("305", "archiver process (PID 222) exited with exit code 1")
	addWarn("306", "parameter \"work_mem\" changed to \"16MB\"")
	addWarn("307", "parameter \"shared_buffers\" changed to \"2GB\"")
	add("308", "parameter \"log_min_duration_statement\" changed to \"250ms\"")

	const n = 4
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[shardForPID(pid, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s)", len(seen))
	}

	single := runSingleServer(entries)
	if !single.HasAny() || len(single.Timeline) != serverTimelineCap {
		t.Fatalf("synthetic stream did not exceed timeline cap: HasAny=%v timeline=%d (want cap %d)",
			single.HasAny(), len(single.Timeline), serverTimelineCap)
	}

	for _, nn := range []int{2, 3, 4, 8} {
		sharded := runShardedServer(entries, nn)
		if !reflect.DeepEqual(single, sharded) {
			t.Errorf("n=%d: ServerMetrics mismatch:\n single =%+v\n sharded=%+v", nn, single, sharded)
		}
	}
}

// TestServerShardMergeParity_Fixtures proves parity on real parsed corpora
// that carry server-lifecycle lines; skips gracefully otherwise.
func TestServerShardMergeParity_Fixtures(t *testing.T) {
	fixtures := []string{"stderr.log", "test_summary.log", "sql_extended.log"}
	checked := false
	for _, fx := range fixtures {
		path := filepath.Join("..", "test", "testdata", fx)
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := runSingleServer(entries)
		if !single.HasAny() {
			t.Logf("%s: no server-lifecycle content, skipping", fx)
			continue
		}
		checked = true
		for _, n := range []int{2, 3, 4, 8} {
			sharded := runShardedServer(entries, n)
			if !reflect.DeepEqual(single, sharded) {
				t.Errorf("%s n=%d: ServerMetrics mismatch", fx, n)
			}
		}
	}
	if !checked {
		t.Skip("no fixture contained server-lifecycle content")
	}
}
