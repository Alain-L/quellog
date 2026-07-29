package analysis

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// runShardedCheckpoints feeds entries through n PID-sharded
// CheckpointAnalyzers and folds them back into one, returning the merged
// Finalize output.
func runShardedCheckpoints(entries []parser.LogEntry, n int) CheckpointMetrics {
	shards := make([]*CheckpointAnalyzer, n)
	for i := range shards {
		shards[i] = NewCheckpointAnalyzer()
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

func runSingleCheckpoints(entries []parser.LogEntry) CheckpointMetrics {
	a := NewCheckpointAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// checkpointAssertParity compares single-pass and sharded metrics. The only
// accumulated-sum float field is TotalWriteTimeSeconds; it is compared with
// almostEqual then zeroed so the remaining structural comparison can use a
// strict reflect.DeepEqual. MaxWriteTimeSeconds is a max over individually
// parsed values, so it stays bit-identical and is left in the DeepEqual.
func checkpointAssertParity(t *testing.T, label string, single, sharded CheckpointMetrics) {
	t.Helper()
	if !almostEqual(single.TotalWriteTimeSeconds, sharded.TotalWriteTimeSeconds) {
		t.Errorf("%s: TotalWriteTimeSeconds mismatch: single=%v sharded=%v",
			label, single.TotalWriteTimeSeconds, sharded.TotalWriteTimeSeconds)
	}
	single.TotalWriteTimeSeconds = 0
	sharded.TotalWriteTimeSeconds = 0
	if !reflect.DeepEqual(single, sharded) {
		t.Errorf("%s: CheckpointMetrics mismatch:\n single =%+v\n sharded=%+v", label, single, sharded)
	}
}

// TestCheckpointShardMergeParity_Synthetic forces the cross-shard case:
// multiple distinct backend PIDs each emit a full checkpoint lifecycle
// interleaved in chronological order, plus frequency warnings. Real logs use
// one checkpointer PID, but the merge path — especially the ordered-slice
// merge-sort of events / typeEvents / walDistances / warningEvents — must
// hold regardless, so varied PIDs are assigned deliberately to spread the
// stream across shards.
func TestCheckpointShardMergeParity_Synthetic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	var entries []parser.LogEntry
	ts := 0
	add := func(pid, msg string) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        msg,
			IsContinuation: false,
			PID:            pid,
		})
		ts++
	}

	cpTypes := []string{"time", "wal", "immediate force wait", "shutdown immediate"}
	pids := []string{"101", "102", "103", "104", "105", "106", "107", "108"}

	for round := 0; round < 4; round++ {
		for k, pid := range pids {
			cpType := cpTypes[(round+k)%len(cpTypes)]
			if (round+k)%3 == 0 {
				interval := 20 + (round*7+k)%40
				add(pid, fmt.Sprintf("checkpoints are occurring too frequently (%d seconds apart)", interval))
			}
			add(pid, "checkpoint starting: "+cpType)
			dist := 1000 + (round*13+k)*97
			est := 2000 + (round*11+k)*53
			buffers := 100 + (round*5+k)*7
			write := 0.100 + float64(round)*0.013 + float64(k)*0.007
			sync := 0.020
			total := write + sync
			add(pid, fmt.Sprintf(
				"checkpoint complete: wrote %d buffers (10.0%%); 0 WAL file(s) added, 0 removed, 1 recycled; write=%.3f s, sync=%.3f s, total=%.3f s; sync files=10, longest=0.001 s, average=0.001 s; distance=%d kB, estimate=%d kB; lsn=0/1, redo lsn=0/1",
				buffers, write, sync, total, dist, est))
		}
	}

	const n = 4
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[shardForPID(pid, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	single := runSingleCheckpoints(entries)
	sharded := runShardedCheckpoints(entries, n)

	if single.CompleteCount == 0 || len(single.WALDistances) == 0 || single.WarningCount == 0 {
		t.Fatalf("synthetic stream produced no checkpoint state: %+v", single)
	}

	checkpointAssertParity(t, "synthetic", single, sharded)

	checkpointAssertAscending(t, sharded.Events)
	checkpointAssertAscending(t, sharded.WarningEvents)
	for _, ev := range sharded.TypeEvents {
		checkpointAssertAscending(t, ev)
	}
	prev := time.Time{}
	for i, w := range sharded.WALDistances {
		if i > 0 && w.Timestamp.Before(prev) {
			t.Errorf("WALDistances not ascending at %d: %v before %v", i, w.Timestamp, prev)
		}
		prev = w.Timestamp
	}
}

func checkpointAssertAscending(t *testing.T, times []time.Time) {
	t.Helper()
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			t.Errorf("time series not ascending at %d: %v before %v", i, times[i], times[i-1])
		}
	}
}

// TestCheckpointShardMergeParity_Fixtures proves parity on real parsed
// corpora across several shard counts. Real logs use one checkpointer PID, so
// they may not spread; the test still verifies single==sharded and skips
// gracefully when a fixture has no checkpoint content.
func TestCheckpointShardMergeParity_Fixtures(t *testing.T) {
	fixtures := []string{
		"stderr.log",
		"test_summary.log",
	}
	checked := false
	for _, fx := range fixtures {
		path := filepath.Join("..", "test", "testdata", fx)
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := runSingleCheckpoints(entries)
		if single.CompleteCount == 0 && single.WarningCount == 0 {
			t.Logf("%s: no checkpoint content, skipping", fx)
			continue
		}
		checked = true
		for _, n := range []int{1, 2, 3, 4, 8} {
			sharded := runShardedCheckpoints(entries, n)
			checkpointAssertParity(t, fmt.Sprintf("%s n=%d", fx, n), single, sharded)
		}
	}
	if !checked {
		t.Skip("no fixture contained checkpoint content")
	}
}
