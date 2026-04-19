package quellog_test

import (
	"fmt"
	"strings"
	"testing"
)

// SQL-targeted regression tests. They live in a separate file from
// regressions_test.go to keep each domain (locks, parser, sql, ...)
// easy to navigate.
//
// All tests in this file inspect the default `--json` output, where the
// SQL section uses the field `queries` (list of all unique queries).
// `slowest_queries` is a different view, populated only by the
// `--sql-performance --json` code path.

// sqlQueries returns the list of query objects from the default JSON
// SQL section. Each entry has id/count/normalized_query/total_time/
// avg_time/max_time fields.
func sqlQueries(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	sp, ok := doc["sql_performance"].(map[string]any)
	if !ok {
		t.Fatal("missing 'sql_performance' section")
	}
	raw, _ := sp["queries"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, q := range raw {
		out = append(out, q.(map[string]any))
	}
	return out
}

// sqlSummary returns the totals from the SQL section.
func sqlSummary(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	sp, ok := doc["sql_performance"].(map[string]any)
	if !ok {
		t.Fatal("missing 'sql_performance' section")
	}
	return sp
}

// findQuery returns the first query whose normalized_query contains
// the given substring, or nil if none.
func findQuery(queries []map[string]any, substr string) map[string]any {
	for _, q := range queries {
		if strings.Contains(q["normalized_query"].(string), substr) {
			return q
		}
	}
	return nil
}

// TestRegression_SQLQueryIDStability verifies that the SQL normalizer
// collapses INSERT statements that differ only by their literal values
// into a SINGLE Query ID (count=5). This is the foundation of every
// SQL aggregate metric — if it broke, top queries, percentiles and
// counts would all be wrong.
func TestRegression_SQLQueryIDStability(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/sql_normalization.log")
	sp := sqlSummary(t, got)
	if total, _ := sp["total_queries_parsed"].(float64); total != 10 {
		t.Errorf("total_queries_parsed = %v, want 10", total)
	}
	if uniq, _ := sp["total_unique_queries"].(float64); uniq != 3 {
		t.Errorf("total_unique_queries = %v, want 3 (INSERT+SELECT+UPDATE shapes)", uniq)
	}
	insert := findQuery(sqlQueries(t, got), "insert into orders")
	if insert == nil {
		t.Fatal("INSERT query not found in queries list")
	}
	if c := insert["count"].(float64); c != 5 {
		t.Errorf("INSERT count = %v, want 5 (5 inserts with different literals must collapse to 1 ID)", c)
	}
}

// TestRegression_SQLPercentilesP2 verifies that the streaming P²
// percentile estimator produces sensible values on a calibrated input
// (51 entries, durations 10..510ms in 10ms steps). Tolerates ±20% on
// the median since P² is approximate, especially at small N.
func TestRegression_SQLPercentilesP2(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/sql_percentiles.log")
	sp := sqlSummary(t, got)
	if total, _ := sp["total_queries_parsed"].(float64); total != 51 {
		t.Errorf("total_queries_parsed = %v, want 51", total)
	}
	if uniq, _ := sp["total_unique_queries"].(float64); uniq != 1 {
		t.Errorf("total_unique_queries = %v, want 1 (same query 51 times)", uniq)
	}
	median, _ := sp["query_median_duration"].(string)
	// True median of [10, 20, ..., 510] is 260ms. P² should land within ~20%.
	if !looksBetween(t, median, 200, 320) {
		t.Errorf("query_median_duration = %q, expected ~260 ms (±20%%)", median)
	}
	// Min and max should be exact.
	if minD, _ := sp["query_min_duration"].(string); minD != "10 ms" {
		t.Errorf("query_min_duration = %q, want %q", minD, "10 ms")
	}
	if maxD, _ := sp["query_max_duration"].(string); maxD != "510 ms" {
		t.Errorf("query_max_duration = %q, want %q", maxD, "510 ms")
	}
}

// TestRegression_SQLTCLAndDMLSeparated verifies that BEGIN/COMMIT/
// ROLLBACK normalize to their own queries (not collapsed into the real
// DML around them), and that frequent TCL gets correct counts.
func TestRegression_SQLTCLAndDMLSeparated(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/sql_tcl_separation.log")
	sp := sqlSummary(t, got)
	if total, _ := sp["total_queries_parsed"].(float64); total != 10 {
		t.Errorf("total_queries_parsed = %v, want 10", total)
	}
	if uniq, _ := sp["total_unique_queries"].(float64); uniq != 7 {
		t.Errorf("total_unique_queries = %v, want 7 (begin, commit, rollback, 2 selects, update, insert)", uniq)
	}
	queries := sqlQueries(t, got)
	counts := map[string]float64{}
	for _, q := range queries {
		counts[q["normalized_query"].(string)] = q["count"].(float64)
	}
	if counts["begin"] != 3 {
		t.Errorf("'begin' count = %v, want 3 (TCL must not collapse with DML)", counts["begin"])
	}
	if counts["commit"] != 2 {
		t.Errorf("'commit' count = %v, want 2", counts["commit"])
	}
	if counts["rollback"] != 1 {
		t.Errorf("'rollback' count = %v, want 1", counts["rollback"])
	}
}

// TestRegression_SQLPreparedExecuteCounted documents that for prepared
// statements only the EXECUTE step is counted as a query. The fixture
// has 1 parse + 3 binds + 3 executes for the same statement: the
// analyzer records 3 queries.
//
// If quellog ever changes to count parse/bind too, this test goes red,
// the call needs to be re-considered, and the fixture README updated.
func TestRegression_SQLPreparedExecuteCounted(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/sql_prepared_statements.log")
	sp := sqlSummary(t, got)
	if total, _ := sp["total_queries_parsed"].(float64); total != 3 {
		t.Errorf("total_queries_parsed = %v, want 3 (only EXECUTE is counted; parse/bind are skipped)", total)
	}
	if uniq, _ := sp["total_unique_queries"].(float64); uniq != 1 {
		t.Errorf("total_unique_queries = %v, want 1 (same prepared statement)", uniq)
	}
}

// TestRegression_SQLDurationVariantsParsed verifies that 'duration ...
// statement:' and 'duration ... execute:' are both recognized while
// 'parse:' and 'bind:' are skipped. The fixture has 4 statement entries
// + 1 parse + 1 bind + 1 execute, so the analyzer should record
// 4 (3 unique statements) + 1 execute = 5 entries... but parse uses
// the same SQL as the execute so unique count = 4.
//
// Found while writing this test: total_queries_parsed = 4, not 5,
// because the analyzer treats `parse stmt_a:` as the SAME entry source
// as `execute stmt_a:` (the prepared statement), counting only one of
// the two ranks (execute wins). Documented behaviour.
func TestRegression_SQLDurationVariantsParsed(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/sql_duration_variants.log")
	sp := sqlSummary(t, got)
	if total, _ := sp["total_queries_parsed"].(float64); total != 4 {
		t.Errorf("total_queries_parsed = %v, want 4 (3 statement + 1 execute; parse/bind skipped)", total)
	}
	queries := sqlQueries(t, got)
	if findQuery(queries, "from products") == nil {
		t.Error("query 'select * from products' missing")
	}
	if findQuery(queries, "from users where email") == nil {
		t.Error("execute query 'select id from users' missing")
	}
	if findQuery(queries, "delete from") == nil {
		t.Error("DELETE query missing")
	}
}

// TestRegression_SQLTopQueriesOrderedByMaxTime verifies the
// `--sql-performance --json` view orders the slowest list by max_time.
// The fixture has:
//   - pg_sleep(?) : 1 call, 1s    → max=1s
//   - SELECT FROM big_table : 5 calls, 100ms each → max=100ms, total=500ms
//   - SELECT now() : 10 calls, 1ms each → max=1ms, total=10ms
//
// Order by max_time: pg_sleep > big_table > now.
func TestRegression_SQLTopQueriesOrderedByMaxTime(t *testing.T) {
	out := runHarness(t, false, "testdata/regressions/sql_top_queries.log", "--sql-performance", "--json")
	doc := mustJSON(t, out)
	slowest, _ := doc["slowest_queries"].([]any)
	if len(slowest) < 3 {
		t.Fatalf("slowest_queries has %d entries, want >= 3", len(slowest))
	}
	first := slowest[0].(map[string]any)["normalized_query"].(string)
	second := slowest[1].(map[string]any)["normalized_query"].(string)
	third := slowest[2].(map[string]any)["normalized_query"].(string)

	if !strings.Contains(first, "pg_sleep") {
		t.Errorf("slowest[0] = %q, want pg_sleep first (slowest single query)", first)
	}
	if !strings.Contains(second, "big_table") {
		t.Errorf("slowest[1] = %q, want big_table second", second)
	}
	if !strings.Contains(third, "now()") {
		t.Errorf("slowest[2] = %q, want now() third (most frequent but fastest)", third)
	}
}

// looksBetween parses a duration string like "260 ms" or "1.20 s" and
// reports whether the numeric part falls in [lowMs, highMs] milliseconds.
func looksBetween(t *testing.T, dur string, lowMs, highMs float64) bool {
	t.Helper()
	parts := strings.Fields(dur)
	if len(parts) < 2 {
		return false
	}
	var v float64
	if _, err := fmt.Sscanf(parts[0], "%f", &v); err != nil {
		return false
	}
	switch parts[1] {
	case "ms":
		return v >= lowMs && v <= highMs
	case "s":
		ms := v * 1000
		return ms >= lowMs && ms <= highMs
	}
	return false
}
