package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// almostEqual reports whether two float64 values are equal within a small
// relative tolerance. Accumulated float sums (durations, sizes) are summed
// in a different order by the sharded path than by the single pass, and
// float addition is not associative — the results can differ in the last
// ULPs. Such differences are far below output rounding and must NOT fail a
// parity test. Use this for ACCUMULATED-SUM float fields only; floats
// derived from integer counts (percentages) are bit-identical and should
// be compared exactly via reflect.DeepEqual.
func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	if d <= 1e-9 {
		return true
	}
	m := a
	if b > m {
		m = b
	}
	if m < 0 {
		m = -m
	}
	return d <= 1e-6*m
}

// runSharded feeds entries through n PID-sharded EventAnalyzers and merges
// them back into one, returning the merged Finalize output. This is the
// data-parallel path under test: each shard reads each of its entries once
// (single pass), then Merge recombines.
// stampSeqs assigns each entry its global stream position, mirroring what
// StreamingAnalyzer.ProcessBatch does in production. Several analyzers rely on
// Seq to keep a deterministic cross-shard choice (e.g. the first event example,
// the last auto_explain plan); without stamping every entry would carry Seq 0
// and those tie-breaks would collapse onto shard 0.
func stampSeqs(entries []parser.LogEntry) {
	for i := range entries {
		entries[i].Seq = int64(i)
	}
}

func runShardedEvents(entries []parser.LogEntry, n int) ([]EventSummary, []EventStat) {
	stampSeqs(entries)
	shards := make([]*EventAnalyzer, n)
	for i := range shards {
		shards[i] = NewEventAnalyzer()
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

func runSingleEvents(entries []parser.LogEntry) ([]EventSummary, []EventStat) {
	stampSeqs(entries)
	a := NewEventAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// TestEventShardMergeParity_Synthetic forces the hard case: one error
// pattern emitted by many backends (so it spreads across shards) with each
// occurrence followed by its STATEMENT continuation (so per-PID pairing is
// exercised). The raw messages differ per backend but normalize to the
// same pattern, so the merged Example must be the globally-earliest one.
func TestEventShardMergeParity_Synthetic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	var entries []parser.LogEntry
	ts := 0
	add := func(pid, msg string, cont bool) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        msg,
			IsContinuation: cont,
			PID:            pid,
		})
		ts++
	}

	// 12 backends, interleaved, each: ERROR (same pattern, distinct literal)
	// then its STATEMENT continuation. Interleaving across PIDs is what a
	// real multi-backend log looks like and what sharding must survive.
	pids := []string{"101", "102", "103", "104", "105", "106", "107", "108", "109", "110", "111", "112"}
	for round := 0; round < 3; round++ {
		for k, pid := range pids {
			add(pid, "ERROR:  duplicate key value violates unique constraint \"users_pkey\"", false)
			// STATEMENT continuation with a per-backend literal that
			// normalizes to one shared query.
			_ = k
			add(pid, "STATEMENT:  INSERT INTO users (id, name) VALUES ("+pid+", 'x')", true)
		}
	}
	// A couple of WARNINGs and LOGs to populate the severity summary.
	add("201", "WARNING:  there is already a transaction in progress", false)
	add("202", "LOG:  database system is ready to accept connections", false)

	// Sanity: the chosen PIDs must actually spread across the shards,
	// otherwise the cross-shard merge would not be exercised.
	const n = 4
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[shardForPID(pid, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	sumS, statS := runSingleEvents(entries)
	sumM, statM := runShardedEvents(entries, n)

	if !reflect.DeepEqual(sumS, sumM) {
		t.Errorf("EventSummary mismatch:\n single=%+v\n shard =%+v", sumS, sumM)
	}
	if !reflect.DeepEqual(statS, statM) {
		t.Errorf("EventStat mismatch:\n single=%+v\n shard =%+v", statS, statM)
	}
}

// TestEventShardMergeParity_Fixtures proves byte-for-byte parity on real
// parsed corpora across several shard counts.
func TestEventShardMergeParity_Fixtures(t *testing.T) {
	fixtures := []string{
		"stderr.log",
		"sql_extended.log",
		"repro_fatal.log",
		"test_summary.log",
	}
	spread := false
	for _, fx := range fixtures {
		path := filepath.Join("..", "test", "testdata", fx)
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			t.Logf("%s: no entries parsed, skipping", fx)
			continue
		}
		sumS, statS := runSingleEvents(entries)
		for _, n := range []int{2, 3, 4, 8} {
			// Track whether at least one fixture/shard-count actually
			// distributes entries, so the suite is not vacuous.
			used := map[int]bool{}
			for i := range entries {
				used[shardForPID(entries[i].PID, n)] = true
			}
			if len(used) > 1 {
				spread = true
			}
			sumM, statM := runShardedEvents(entries, n)
			if !reflect.DeepEqual(sumS, sumM) {
				t.Errorf("%s n=%d: EventSummary mismatch:\n single=%+v\n shard =%+v", fx, n, sumS, sumM)
			}
			if !reflect.DeepEqual(statS, statM) {
				t.Errorf("%s n=%d: EventStat mismatch", fx, n)
			}
		}
	}
	if !spread {
		t.Fatal("no fixture distributed entries across shards; parity test was vacuous")
	}
}

// parseFixtureEntries runs the stderr parser over a fixture and collects
// every emitted entry in stream order.
func parseFixtureEntries(t *testing.T, path string) []parser.LogEntry {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Logf("fixture %s not present, skipping", path)
		return nil
	}
	p := &parser.StderrParser{}
	out := make(chan []parser.LogEntry, 64)
	var entries []parser.LogEntry
	done := make(chan struct{})
	go func() {
		for b := range out {
			entries = append(entries, b...)
		}
		close(done)
	}()
	if err := p.Parse(path, out); err != nil {
		close(out)
		<-done
		t.Fatalf("parse %s: %v", path, err)
	}
	close(out)
	<-done
	return entries
}
