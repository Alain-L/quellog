package analysis

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// runShardedReplication feeds entries through n PID-sharded
// ReplicationAnalyzers and folds them back into one, returning the merged
// Finalize output. This is the data-parallel path under test: each shard
// processes its entries once (single pass), then Merge recombines.
func runShardedReplication(entries []parser.LogEntry, n int) ReplicationMetrics {
	shards := make([]*ReplicationAnalyzer, n)
	for i := range shards {
		shards[i] = NewReplicationAnalyzer()
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

func runSingleReplication(entries []parser.LogEntry) ReplicationMetrics {
	a := NewReplicationAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// replAssertParity compares single-pass and sharded metrics. Structural
// fields go through reflect.DeepEqual; there are no accumulated-sum float
// fields in ReplicationMetrics, so almostEqual is not needed here (all
// counters are integer and bit-reproducible). Events are compared with an
// extra ordering note: the synthetic corpus uses strictly increasing
// timestamps so the merge-sort order is unambiguous.
func replAssertParity(t *testing.T, label string, single, sharded ReplicationMetrics) {
	t.Helper()
	if !reflect.DeepEqual(single.Markers, sharded.Markers) {
		t.Errorf("%s: Markers mismatch:\n single=%+v\n shard =%+v", label, single.Markers, sharded.Markers)
	}
	if !reflect.DeepEqual(single.HourCounts, sharded.HourCounts) {
		t.Errorf("%s: HourCounts mismatch:\n single=%+v\n shard =%+v", label, single.HourCounts, sharded.HourCounts)
	}
	if !reflect.DeepEqual(single.Events, sharded.Events) {
		t.Errorf("%s: Events mismatch:\n single=%+v\n shard =%+v", label, single.Events, sharded.Events)
	}
	if !single.LastTermination.Equal(sharded.LastTermination) {
		t.Errorf("%s: LastTermination mismatch: single=%v shard=%v", label, single.LastTermination, sharded.LastTermination)
	}
	if single.PeakHourLabel != sharded.PeakHourLabel || single.PeakHourCount != sharded.PeakHourCount {
		t.Errorf("%s: PeakHour mismatch: single=(%q,%d) shard=(%q,%d)",
			label, single.PeakHourLabel, single.PeakHourCount, sharded.PeakHourLabel, sharded.PeakHourCount)
	}
	if single.HasAny != sharded.HasAny {
		t.Errorf("%s: HasAny mismatch: single=%v shard=%v", label, single.HasAny, sharded.HasAny)
	}
	if !reflect.DeepEqual(single.ConflictQueries, sharded.ConflictQueries) {
		t.Errorf("%s: ConflictQueries mismatch:\n single=%+v\n shard =%+v",
			label, replDumpConflicts(single.ConflictQueries), replDumpConflicts(sharded.ConflictQueries))
	}
}

func replDumpConflicts(m map[string]*ReplicationConflictQueryStat) []ReplicationConflictQueryStat {
	out := make([]ReplicationConflictQueryStat, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	return out
}

// TestReplicationShardMergeParity_Synthetic exercises every code path of
// Merge: marker/hour count sums, the events merge-sort, the
// lastTermination running max, peak-hour derivation, and conflict-query
// folding (including the same normalized query surfacing from several
// backends across shards, plus a per-PID STATEMENT continuation that must
// stay co-located with its conflict marker on one shard).
func TestReplicationShardMergeParity_Synthetic(t *testing.T) {
	base := time.Unix(0, 0).UTC()
	var entries []parser.LogEntry
	sec := 0
	add := func(pid, msg string, cont bool) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(sec) * time.Second),
			Message:        msg,
			IsContinuation: cont,
			PID:            pid,
		})
		sec++ // strictly increasing timestamps → unambiguous merge order
	}

	// Backends spread across shards. Each line carries a rich prefix so the
	// replication marker lives well past byte 0 (exercising the prefilter's
	// scan window). Severity + marker substrings are what Process keys on.
	llp := func(pid string) string {
		return "db=app,user=repl,app=walreceiver,client=10.0.0.2,xid=0 "
	}

	pids := []string{"201", "202", "203", "204", "205", "206", "207", "208"}

	// Stream starts (LOG) from many backends → drive PeakHourLabel.
	for _, pid := range pids {
		add(pid, "LOG:  "+llp(pid)+"started streaming WAL from primary at 0/3000000 on timeline 1", false)
	}

	// A WAL-receive failure (termination) on one backend, with a later one
	// on another backend so LastTermination tracks the max across shards.
	add("202", "FATAL:  "+llp("202")+"could not receive data from WAL stream: server closed the connection", false)
	add("207", "LOG:  "+llp("207")+"replication terminated by primary server", false)

	// Walsender timeout (termination) — different marker family.
	add("204", "FATAL:  "+llp("204")+"terminating walsender process due to replication timeout", false)

	// Recovery conflicts on multiple backends, EACH followed by its own
	// STATEMENT continuation (same PID). The first two run the SAME query
	// (must fold to one ConflictQueries entry with Count=2); the third runs
	// a different query.
	add("203", "ERROR:  "+llp("203")+"canceling statement due to conflict with recovery", false)
	add("203", "STATEMENT:  SELECT * FROM orders WHERE id = 42", true)

	add("206", "ERROR:  "+llp("206")+"canceling statement due to conflict with recovery", false)
	add("206", "STATEMENT:  SELECT * FROM orders WHERE id = 99", true)

	add("208", "FATAL:  "+llp("208")+"terminating connection due to conflict with recovery", false)
	add("208", "STATEMENT:  DELETE FROM sessions WHERE expired", true)

	// Slot invalidation (needs "replication slot" on the line).
	add("205", "LOG:  "+llp("205")+"invalidating replication slot \"phys_1\": replication slot \"phys_1\" has been invalidated", false)

	// Recovery pause / resume (LOG informational).
	add("201", "LOG:  "+llp("201")+"recovery has paused", false)
	add("201", "LOG:  "+llp("201")+"recovery is resuming", false)

	// Noise lines that must be ignored (no marker, or marker without the
	// required co-occurring substring).
	add("301", "LOG:  database system is ready to accept connections", false)
	add("302", "ERROR:  some logical decoding plugin has been invalidated", false) // no "replication slot"

	const n = 4

	// Sanity: PIDs must actually spread across >= 2 shards, otherwise the
	// cross-shard merge is never exercised.
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[shardForPID(pid, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	single := runSingleReplication(entries)

	// Guard against a vacuous test: the corpus must produce real signal.
	if !single.HasAny || len(single.Events) == 0 || len(single.ConflictQueries) == 0 {
		t.Fatalf("synthetic corpus produced no signal: HasAny=%v events=%d conflicts=%d",
			single.HasAny, len(single.Events), len(single.ConflictQueries))
	}
	if single.LastTermination.IsZero() {
		t.Fatalf("expected a termination event to set LastTermination")
	}

	for _, nn := range []int{2, 3, 4, 8} {
		sharded := runShardedReplication(entries, nn)
		replAssertParity(t, "synthetic n="+strconv.Itoa(nn), single, sharded)
	}
}

// TestReplicationShardMergeParity_Fixtures proves parity on real parsed
// corpora that contain replication markers. Skips gracefully when no
// fixture carries replication signal.
func TestReplicationShardMergeParity_Fixtures(t *testing.T) {
	dir := filepath.Join("..", "test", "testdata")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	regr, _ := filepath.Glob(filepath.Join(dir, "regressions", "*", "*.log"))
	matches = append(matches, regr...)

	markers := []string{
		"started streaming WAL from primary",
		"could not receive data from WAL stream",
		"replication terminated by primary server",
		"terminating walsender process due to replication timeout",
		"unexpected EOF on standby connection",
		"conflict with recovery",
		"has been invalidated",
		"recovery has paused",
	}
	hasReplMarker := func(entries []parser.LogEntry) bool {
		for i := range entries {
			m := entries[i].Message
			for _, sub := range markers {
				if strings.Contains(m, sub) {
					return true
				}
			}
		}
		return false
	}

	tested := false
	for _, path := range matches {
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 || !hasReplMarker(entries) {
			continue
		}
		spread := false
		single := runSingleReplication(entries)
		if !single.HasAny {
			continue
		}
		for _, n := range []int{2, 3, 4, 8} {
			used := map[int]bool{}
			for i := range entries {
				used[shardForPID(entries[i].PID, n)] = true
			}
			if len(used) > 1 {
				spread = true
			}
			sharded := runShardedReplication(entries, n)
			replAssertParity(t, filepath.Base(path)+" n="+strconv.Itoa(n), single, sharded)
		}
		if spread {
			tested = true
		}
	}
	if !tested {
		t.Skip("no fixture with replication markers spread across shards; covered by the synthetic test")
	}
}
