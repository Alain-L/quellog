package analysis

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// tempfileRunSingle processes every entry through one analyzer.
func tempfileRunSingle(entries []parser.LogEntry) TempFileMetrics {
	a := NewTempFileAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// tempfileRunSharded shards entries by PID across n analyzers, processes each
// shard, folds shards 1..n-1 into shard 0 via Merge (repeated pairwise), then
// finalizes shard 0. Returns the metrics and the number of shards that
// actually received entries.
func tempfileRunSharded(entries []parser.LogEntry, n int) (TempFileMetrics, int) {
	shards := make([]*TempFileAnalyzer, n)
	for i := range shards {
		shards[i] = NewTempFileAnalyzer()
	}
	seen := make(map[int]struct{})
	for i := range entries {
		s := shardForPID(entries[i].PID, n)
		seen[s] = struct{}{}
		shards[s].Process(&entries[i])
	}
	for i := 1; i < n; i++ {
		shards[0].Merge(shards[i])
	}
	return shards[0].Finalize(), len(seen)
}

// tempfileAssertMetricsEqual compares two TempFileMetrics for parity. The
// analyzer carries no accumulated-float sums (totalSize is int64 and each
// TempFileEvent.Size is a per-event verbatim float), so every field is
// bit-exact and reflect.DeepEqual covers the whole structure.
//
// One documented exception: the Events slice is ordered by Timestamp, but
// TempFileEvent has no stream-position key. When two events from DIFFERENT
// backends (PIDs) share the exact same timestamp, the single pass keeps them
// in arrival order while the sharded path keeps them in shard-fold order — the
// two orderings of an equal-timestamp run are not reconstructable from the
// timestamp alone. The events multiset is always identical; only the intra-
// timestamp order can differ. We therefore canonicalize both Events slices to
// a total order (Timestamp, Size, QueryID) before comparing, which collapses
// that benign ambiguity while still catching any real divergence.
func tempfileAssertMetricsEqual(t *testing.T, label string, want, got TempFileMetrics) {
	t.Helper()
	tempfileCanonicalizeEvents(want.Events)
	tempfileCanonicalizeEvents(got.Events)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: metrics mismatch\n single  = %+v\n sharded = %+v", label, want, got)
	}
}

// tempfileCanonicalizeEvents sorts events by (Timestamp, Size, QueryID) so the
// benign intra-timestamp ordering ambiguity between the single and sharded
// paths does not fail the comparison. Stable so it never reorders events that
// already compare equal on all three keys.
func tempfileCanonicalizeEvents(ev []TempFileEvent) {
	sort.SliceStable(ev, func(i, j int) bool {
		if !ev[i].Timestamp.Equal(ev[j].Timestamp) {
			return ev[i].Timestamp.Before(ev[j].Timestamp)
		}
		if ev[i].Size != ev[j].Size {
			return ev[i].Size < ev[j].Size
		}
		return ev[i].QueryID < ev[j].QueryID
	})
}

// TestTempFileShardMergeParity_Synthetic exercises the hard cases: multiple
// backends spread across shards, each emitting a "temporary file ... size NNN"
// line followed by its STATEMENT continuation (Pattern 1), with ascending
// timestamps and several distinct query shapes that fold into shared stats.
func TestTempFileShardMergeParity_Synthetic(t *testing.T) {
	base := time.Date(2025, 11, 30, 21, 10, 0, 0, time.UTC)
	type line struct {
		pid string
		msg string
	}

	// Two query shapes: backends emit either an INSERT or a SELECT. The
	// per-backend literal differs (so raw queries vary) but they normalize to
	// a shared pattern, forcing queryStats keys to collide across shards.
	tempLine := func(pid string, size int) string {
		return `LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp` + pid + `.0", size ` + itoa(size)
	}
	stmtInsert := func(pid string) string {
		return `STATEMENT:  INSERT INTO big (id) VALUES (` + pid + `)`
	}
	stmtSelect := func(pid string) string {
		return `STATEMENT:  SELECT * FROM big WHERE id = ` + pid
	}

	pids := []string{"101", "102", "103", "104", "105", "106", "107", "108"}

	var lines []line
	for round := 0; round < 3; round++ {
		for k, pid := range pids {
			size := 100000 + round*1000 + k*7
			lines = append(lines, line{pid, tempLine(pid, size)})
			if k%2 == 0 {
				lines = append(lines, line{pid, stmtInsert(pid)})
			} else {
				lines = append(lines, line{pid, stmtSelect(pid)})
			}
		}
	}

	// Build entries with ascending timestamps. The temp-file line and its
	// STATEMENT continuation carry the same PID (via the [pid] prefix) so the
	// Pattern 1 pairing stays inside one shard.
	entries := make([]parser.LogEntry, 0, len(lines))
	for i, l := range lines {
		full := "[" + l.pid + "] " + l.msg
		ts := base.Add(time.Duration(i) * time.Second)
		cont := strings.HasPrefix(l.msg, "STATEMENT:")
		entries = append(entries, parser.NewLogEntry(ts, full, cont))
	}

	for i := range entries {
		if entries[i].PID == "" {
			t.Fatalf("entry %d: PID not extracted from %q", i, entries[i].Message)
		}
	}

	single := tempfileRunSingle(entries)
	if single.Count == 0 || len(single.Events) == 0 || len(single.QueryStats) == 0 {
		t.Fatalf("synthetic fixture produced degenerate metrics: %+v", single)
	}

	for _, n := range []int{2, 3, 4} {
		// Recompute single per iteration so a parity comparison can never leak
		// in-place mutation across iterations (matches vacuum_merge_test.go).
		singleN := tempfileRunSingle(entries)
		sharded, spread := tempfileRunSharded(entries, n)
		if spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", n, spread)
		}
		tempfileAssertMetricsEqual(t, "synthetic n="+itoa(n), singleN, sharded)
	}
}

// TestTempFileShardMergeParity_Fixtures proves parity on real parsed corpora
// that carry temp-file lines, across several shard counts.
func TestTempFileShardMergeParity_Fixtures(t *testing.T) {
	candidates := []string{
		"../test/testdata/stderr.log",
		"../test/testdata/syslog.log",
		"../test/testdata/syslog_bsd.log",
		"../test/testdata/syslog_rfc5424.log",
	}

	ranAny := false
	for _, path := range candidates {
		if !fixtureHasTempFile(path) {
			continue
		}
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := tempfileRunSingle(entries)
		if single.Count == 0 {
			continue
		}
		ranAny = true

		for _, n := range []int{2, 4, 8} {
			singleN := tempfileRunSingle(entries)
			sharded, _ := tempfileRunSharded(entries, n)
			tempfileAssertMetricsEqual(t, path+" n="+itoa(n), singleN, sharded)
		}
	}

	if !ranAny {
		t.Skip("no fixture with temp-file metrics found")
	}
}

// fixtureHasTempFile reports whether the file exists and contains any
// temp-file marker, without fully parsing it.
func fixtureHasTempFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), tempFileMarker)
}
