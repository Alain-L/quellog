package analysis

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// sqlRunSingle processes every entry through one analyzer.
func sqlRunSingle(entries []parser.LogEntry) SQLMetrics {
	a := NewSQLAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// sqlRunSharded shards entries by PID across n analyzers, processes each
// shard, folds shards 1..n-1 into shard 0 via Merge, then finalizes shard 0.
// Returns the metrics and the number of shards that actually received entries.
func sqlRunSharded(entries []parser.LogEntry, n int) (SQLMetrics, int) {
	shards := make([]*SQLAnalyzer, n)
	for i := range shards {
		shards[i] = NewSQLAnalyzer()
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

// sqlAssertMetricsEqual compares two SQLMetrics for parity. The accumulated
// float sums (durations) are summed in a different order by the sharded path
// and float addition is not associative, so those are checked with almostEqual
// then zeroed before a structural reflect.DeepEqual covers the integer / bit
// exact remainder. The private compactExecutions store has a layout-dependent
// internal representation (dictionary ordering, chunk boundaries) that differs
// between the single and sharded paths even when observably identical, so it is
// never DeepEqual'd directly: its observable content (event count + the sorted
// multiset of durations) is compared explicitly.
func sqlAssertMetricsEqual(t *testing.T, label string, want, got SQLMetrics) {
	t.Helper()

	// Global accumulated-sum floats.
	if !almostEqual(want.SumQueryDuration, got.SumQueryDuration) {
		t.Errorf("%s: SumQueryDuration: want=%v got=%v", label, want.SumQueryDuration, got.SumQueryDuration)
	}
	// Min/Max are selected values, not sums: bit-identical.
	if want.MinQueryDuration != got.MinQueryDuration {
		t.Errorf("%s: MinQueryDuration: want=%v got=%v", label, want.MinQueryDuration, got.MinQueryDuration)
	}
	if want.MaxQueryDuration != got.MaxQueryDuration {
		t.Errorf("%s: MaxQueryDuration: want=%v got=%v", label, want.MaxQueryDuration, got.MaxQueryDuration)
	}
	// Median / P99 derive from the same sorted multiset → bit-identical.
	if want.MedianQueryDuration != got.MedianQueryDuration {
		t.Errorf("%s: MedianQueryDuration: want=%v got=%v", label, want.MedianQueryDuration, got.MedianQueryDuration)
	}
	if want.P99QueryDuration != got.P99QueryDuration {
		t.Errorf("%s: P99QueryDuration: want=%v got=%v", label, want.P99QueryDuration, got.P99QueryDuration)
	}

	// Observable executions store: same event count and same sorted multiset
	// of durations.
	if want.ExecutionCount() != got.ExecutionCount() {
		t.Errorf("%s: ExecutionCount: want=%d got=%d", label, want.ExecutionCount(), got.ExecutionCount())
	}
	wd := want.ExecutionDurations()
	gd := got.ExecutionDurations()
	sort.Float64s(wd)
	sort.Float64s(gd)
	if !reflect.DeepEqual(wd, gd) {
		t.Errorf("%s: execution duration multiset mismatch (len %d vs %d)", label, len(wd), len(gd))
	}

	// Per-query stats: float TotalTime/AvgTime/MaxTime normalized via
	// almostEqual then zeroed; the rest compared structurally.
	sqlNormalizeQueryStats(t, label, want.QueryStats, got.QueryStats)
	if !reflect.DeepEqual(want.QueryStats, got.QueryStats) {
		t.Errorf("%s: QueryStats mismatch", label)
	}

	// Per-type stats: same treatment.
	sqlNormalizeTypeStats(t, label, want.QueryTypeStats, got.QueryTypeStats)
	if !reflect.DeepEqual(want.QueryTypeStats, got.QueryTypeStats) {
		t.Errorf("%s: QueryTypeStats mismatch\n single  = %+v\n sharded = %+v", label, want.QueryTypeStats, got.QueryTypeStats)
	}

	// Per-dimension breakdowns: TotalTime is an accumulated sum.
	sqlNormalizeTypeCounts(t, label, want.QueryTypesByDatabase, got.QueryTypesByDatabase)
	sqlNormalizeTypeCounts(t, label, want.QueryTypesByUser, got.QueryTypesByUser)
	sqlNormalizeTypeCounts(t, label, want.QueryTypesByHost, got.QueryTypesByHost)
	sqlNormalizeTypeCounts(t, label, want.QueryTypesByApp, got.QueryTypesByApp)
	if !reflect.DeepEqual(want.QueryTypesByDatabase, got.QueryTypesByDatabase) {
		t.Errorf("%s: QueryTypesByDatabase mismatch", label)
	}
	if !reflect.DeepEqual(want.QueryTypesByUser, got.QueryTypesByUser) {
		t.Errorf("%s: QueryTypesByUser mismatch", label)
	}
	if !reflect.DeepEqual(want.QueryTypesByHost, got.QueryTypesByHost) {
		t.Errorf("%s: QueryTypesByHost mismatch", label)
	}
	if !reflect.DeepEqual(want.QueryTypesByApp, got.QueryTypesByApp) {
		t.Errorf("%s: QueryTypesByApp mismatch", label)
	}

	// Remaining scalar / integer fields.
	if want.TotalQueries != got.TotalQueries {
		t.Errorf("%s: TotalQueries: want=%d got=%d", label, want.TotalQueries, got.TotalQueries)
	}
	if want.UniqueQueries != got.UniqueQueries {
		t.Errorf("%s: UniqueQueries: want=%d got=%d", label, want.UniqueQueries, got.UniqueQueries)
	}
	if !want.StartTimestamp.Equal(got.StartTimestamp) {
		t.Errorf("%s: StartTimestamp: want=%v got=%v", label, want.StartTimestamp, got.StartTimestamp)
	}
	if !want.EndTimestamp.Equal(got.EndTimestamp) {
		t.Errorf("%s: EndTimestamp: want=%v got=%v", label, want.EndTimestamp, got.EndTimestamp)
	}
}

// sqlNormalizeQueryStats checks the accumulated-float fields inside the
// per-query stat maps with almostEqual, then zeroes them in place so the
// caller's reflect.DeepEqual compares the rest exactly. The SlowestRun
// DurationMs is a selected (not summed) value, so it is compared exactly via
// the structural DeepEqual and left untouched here.
func sqlNormalizeQueryStats(t *testing.T, label string, want, got map[string]*QueryStat) {
	t.Helper()
	for k, av := range want {
		bv, ok := got[k]
		if !ok {
			continue // structural DeepEqual will report the missing key
		}
		if !almostEqual(av.TotalTime, bv.TotalTime) {
			t.Errorf("%s: query %q TotalTime: %v vs %v", label, k, av.TotalTime, bv.TotalTime)
		}
		if !almostEqual(av.AvgTime, bv.AvgTime) {
			t.Errorf("%s: query %q AvgTime: %v vs %v", label, k, av.AvgTime, bv.AvgTime)
		}
		if !almostEqual(av.MaxTime, bv.MaxTime) {
			t.Errorf("%s: query %q MaxTime: %v vs %v", label, k, av.MaxTime, bv.MaxTime)
		}
		av.TotalTime, bv.TotalTime = 0, 0
		av.AvgTime, bv.AvgTime = 0, 0
		av.MaxTime, bv.MaxTime = 0, 0
	}
}

// sqlNormalizeTypeStats does the same for the per-type aggregate maps.
func sqlNormalizeTypeStats(t *testing.T, label string, want, got map[string]*QueryTypeStat) {
	t.Helper()
	for k, av := range want {
		bv, ok := got[k]
		if !ok {
			continue
		}
		if !almostEqual(av.TotalTime, bv.TotalTime) {
			t.Errorf("%s: type %q TotalTime: %v vs %v", label, k, av.TotalTime, bv.TotalTime)
		}
		if !almostEqual(av.AvgTime, bv.AvgTime) {
			t.Errorf("%s: type %q AvgTime: %v vs %v", label, k, av.AvgTime, bv.AvgTime)
		}
		if !almostEqual(av.MaxTime, bv.MaxTime) {
			t.Errorf("%s: type %q MaxTime: %v vs %v", label, k, av.MaxTime, bv.MaxTime)
		}
		av.TotalTime, bv.TotalTime = 0, 0
		av.AvgTime, bv.AvgTime = 0, 0
		av.MaxTime, bv.MaxTime = 0, 0
	}
}

// sqlNormalizeTypeCounts does the same for the nested per-dimension maps.
func sqlNormalizeTypeCounts(t *testing.T, label string, want, got map[string]map[string]*QueryTypeCount) {
	t.Helper()
	for dim, wantInner := range want {
		gotInner, ok := got[dim]
		if !ok {
			continue
		}
		for qt, av := range wantInner {
			bv, ok := gotInner[qt]
			if !ok {
				continue
			}
			if !almostEqual(av.TotalTime, bv.TotalTime) {
				t.Errorf("%s: dim %q/%q TotalTime: %v vs %v", label, dim, qt, av.TotalTime, bv.TotalTime)
			}
			av.TotalTime, bv.TotalTime = 0, 0
		}
	}
}

// TestSQLShardMergeParity_Synthetic forces the hard cases for the SQL
// analyzer: the SAME normalized query emitted by many backends (so queryStats
// folds across shards), extended-protocol execute: entries each followed by a
// "DETAIL: parameters:" continuation on the SAME PID (so SlowestRun pairing is
// exercised and must survive sharding), auto_explain plan: lines preceding a
// statement on the same PID, varied durations so min/max/slowest are
// meaningful, and a per-prefix database/user/app so the dimension breakdowns
// fold. PIDs are chosen to spread across shards.
func TestSQLShardMergeParity_Synthetic(t *testing.T) {
	base := time.Date(2025, 11, 30, 21, 10, 0, 0, time.UTC)
	var entries []parser.LogEntry
	ts := 0
	add := func(pid, msg string, cont bool) {
		full := "[" + pid + "] " + msg
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        full,
			IsContinuation: cont,
			PID:            pid,
		})
		ts++
	}

	pids := []string{"101", "102", "103", "104", "105", "106", "107", "108"}
	// Each backend executes the SAME parameterized SELECT (folds across
	// shards) with a distinct duration and bound parameter, immediately
	// followed by its DETAIL parameters continuation on the same PID.
	for k, pid := range pids {
		dur := 1.0 + float64(k)*3.5 // ascending, so the last backend is slowest
		add(pid,
			"user=app,db=appdb,app=web LOG:  duration: "+ftoa(dur)+" ms  execute <unnamed>: SELECT * FROM users WHERE id = $1",
			false)
		add(pid,
			"user=app,db=appdb,app=web DETAIL:  parameters: $1 = '"+pid+"'",
			true)
	}

	// A few simple-protocol statements (no params) for a different query,
	// from a couple of the same backends, with an auto_explain plan in front
	// of one of them on the same PID.
	add("101", "user=app,db=appdb,app=web LOG:  duration: 0.500 ms  plan:  Query Text: INSERT INTO orders (x) VALUES (1)\nSeq Scan on orders  (cost=0.00..1.00 rows=1)", false)
	add("101", "user=app,db=appdb,app=web LOG:  duration: 7.250 ms  statement: INSERT INTO orders (x) VALUES (1)", false)
	add("102", "user=app,db=appdb,app=web LOG:  duration: 9.750 ms  statement: INSERT INTO orders (x) VALUES (2)", false)

	// A third distinct query from a backend that may shard apart.
	add("107", "user=admin,db=appdb,app=cron LOG:  duration: 42.000 ms  statement: UPDATE accounts SET balance = balance + 1 WHERE id = 5", false)

	// Sanity: PIDs must spread across >= 2 shards or the cross-shard merge is
	// not exercised.
	const n = 4
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[shardForPID(pid, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	// Every entry must carry a PID for the sharding to be meaningful.
	for i := range entries {
		if entries[i].PID == "" {
			t.Fatalf("entry %d has empty PID: %q", i, entries[i].Message)
		}
	}

	single := sqlRunSingle(entries)
	if single.TotalQueries == 0 || single.UniqueQueries < 3 {
		t.Fatalf("degenerate synthetic metrics: total=%d unique=%d", single.TotalQueries, single.UniqueQueries)
	}
	// The slowest run of the shared SELECT must have been paired with its
	// parameters (proves DETAIL pairing fired at all).
	slowestParamSeen := false
	for _, st := range single.QueryStats {
		if st.SlowestRun != nil && st.SlowestRun.Parameters != "" {
			slowestParamSeen = true
		}
	}
	if !slowestParamSeen {
		t.Fatal("synthetic fixture never paired a DETAIL parameters line; SlowestRun untested")
	}

	for _, nShards := range []int{2, 3, 4} {
		// Recompute single per iteration: the comparison zeroes accumulated
		// floats in place, which would corrupt a shared single across runs.
		singleN := sqlRunSingle(entries)
		sharded, spread := sqlRunSharded(entries, nShards)
		if spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", nShards, spread)
		}
		sqlAssertMetricsEqual(t, "synthetic n="+itoa(nShards), singleN, sharded)
	}
}

// TestSQLShardMergeParity_Fixtures proves parity on the real parsed SQL
// corpora across several shard counts. Fixtures with no SQL are skipped.
func TestSQLShardMergeParity_Fixtures(t *testing.T) {
	fixtures := []string{
		"sql_simple.log",
		"sql_prepared.log",
		"sql_extended.log",
		"sql_normalization_edge_cases.log",
		"stderr.log",
	}
	ranAny := false
	spread := false
	for _, fx := range fixtures {
		path := filepath.Join("..", "test", "testdata", fx)
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			t.Logf("%s: no entries parsed, skipping", fx)
			continue
		}
		single := sqlRunSingle(entries)
		if single.TotalQueries == 0 {
			t.Logf("%s: no SQL durations, skipping", fx)
			continue
		}
		ranAny = true
		for _, n := range []int{2, 3, 4, 8} {
			used := map[int]bool{}
			for i := range entries {
				used[shardForPID(entries[i].PID, n)] = true
			}
			if len(used) > 1 {
				spread = true
			}
			singleN := sqlRunSingle(entries)
			sharded, _ := sqlRunSharded(entries, n)
			sqlAssertMetricsEqual(t, fx+" n="+itoa(n), singleN, sharded)
		}
	}
	if !ranAny {
		t.Skip("no fixture yielded SQL metrics")
	}
	if !spread {
		t.Fatal("no fixture distributed entries across shards; parity test was vacuous")
	}
}

// ftoa formats a float with three decimals for synthetic log lines, matching
// PostgreSQL's "duration: X.XXX ms" rendering closely enough for the parser.
func ftoa(f float64) string {
	// Round to micro then format; small helper avoids importing strconv just
	// for the test fixtures.
	scaled := int64(f*1000 + 0.5)
	whole := scaled / 1000
	frac := scaled % 1000
	return itoa(int(whole)) + "." + pad3(int(frac))
}

// pad3 left-pads n (0..999) to exactly three digits.
func pad3(n int) string {
	if n < 0 {
		n = 0
	}
	s := itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
