package quellog_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// These tests pin specific bug fixes by asserting concrete values in the
// JSON output. They give clearer failure messages than the corpus
// goldens ("locks.total_events should be 1, got 2" vs "the golden
// diverges somewhere on line 247").
//
// One test per bug class. Add a new test here when you fix a bug that
// would otherwise only be guarded by a golden diff.

// runFixtureJSON is a small helper that runs the harness on a fixture
// with --json and returns the unmarshalled top-level object.
func runFixtureJSON(t *testing.T, fixturePath string) map[string]any {
	t.Helper()
	return mustJSON(t, runHarness(t, false, fixturePath, "--json"))
}

// mustJSON unmarshals raw output as a top-level JSON object, failing the
// test on parse error.
func mustJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, raw)
	}
	return got
}

// TestRegression_DeadlockNotDoubleCounted pins the b913788 fix: a
// `still waiting` LOG followed by a `deadlock detected` ERROR for the
// same PID must produce exactly ONE lock event (the wait, promoted to
// deadlock state), not two.
func TestRegression_DeadlockNotDoubleCounted(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/locks/deadlock_basic.log")
	locks, ok := got["locks"].(map[string]any)
	if !ok {
		t.Fatal("missing 'locks' section in JSON output")
	}
	if total, _ := locks["total_events"].(float64); total != 1 {
		t.Errorf("locks.total_events = %v, want 1 (deadlock should not be counted on top of waiting)", total)
	}
	if dl, _ := locks["deadlock_events"].(float64); dl != 1 {
		t.Errorf("locks.deadlock_events = %v, want 1", dl)
	}
	if w, _ := locks["waiting_events"].(float64); w != 1 {
		t.Errorf("locks.waiting_events = %v, want 1", w)
	}
}

// TestRegression_LockDedupSameWait pins the 4f4e698 fix: when a
// `still waiting` LOG is followed by an `acquired` LOG for the same
// PID/lock, this is one event (the wait that completed), not two.
func TestRegression_LockDedupSameWait(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/locks/lock_dedup_waiting_acquired.log")
	locks, ok := got["locks"].(map[string]any)
	if !ok {
		t.Fatal("missing 'locks' section in JSON output")
	}
	if total, _ := locks["total_events"].(float64); total != 1 {
		t.Errorf("locks.total_events = %v, want 1 (same wait counted twice)", total)
	}
	if acq, _ := locks["acquired_events"].(float64); acq != 1 {
		t.Errorf("locks.acquired_events = %v, want 1", acq)
	}
	if w, _ := locks["waiting_events"].(float64); w != 0 {
		t.Errorf("locks.waiting_events = %v, want 0 (the wait completed, became acquired)", w)
	}
}

// TestRegression_LockQueryAssociation pins the 4b9655a fix: a STATEMENT
// continuation line following a lock event must let the analyzer
// resolve a query_id on the lock event itself.
func TestRegression_LockQueryAssociation(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/locks/lock_with_statement.log")
	locks, ok := got["locks"].(map[string]any)
	if !ok {
		t.Fatal("missing 'locks' section")
	}
	events, ok := locks["events"].([]any)
	if !ok || len(events) == 0 {
		t.Fatalf("locks.events is empty or missing: %v", locks["events"])
	}
	first, _ := events[0].(map[string]any)
	queryID, _ := first["query_id"].(string)
	if queryID == "" {
		t.Errorf("locks.events[0].query_id is empty; expected a non-empty ID resolved from the STATEMENT line")
	}
}

// TestRegression_RelationExtractionFromContext pins the 0494e4b fix:
// CONTEXT lines like `while updating tuple (X,Y) in relation "name"` are
// the source for `locks.relation_stats`. Three lock events on `orders`
// (×2) and `customers` (×1) should yield those exact counts.
func TestRegression_RelationExtractionFromContext(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/locks/lock_relation_extraction.log")
	locks, ok := got["locks"].(map[string]any)
	if !ok {
		t.Fatal("missing 'locks' section")
	}
	stats, ok := locks["relation_stats"].(map[string]any)
	if !ok {
		t.Fatalf("missing locks.relation_stats: %v", locks)
	}
	if v, _ := stats["orders"].(float64); v != 2 {
		t.Errorf("relation_stats[\"orders\"] = %v, want 2", v)
	}
	if v, _ := stats["customers"].(float64); v != 1 {
		t.Errorf("relation_stats[\"customers\"] = %v, want 1", v)
	}
}

// TestRegression_AutoExplainJSONNoPanic pins the fec50b4 fix:
// `strings.Repeat("  ", -1)` used to panic when the JSON plan parser
// went past the bottom of its indent stack. The fixture uses a
// 4-level-deep nested plan that previously triggered the panic.
func TestRegression_AutoExplainJSONNoPanic(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/autoexplain/autoexplain_json_negindent.log")
	// If we got here, exit was 0 and JSON unmarshalled successfully.
	// A loose sanity check: the summary should report at least one log.
	summary, ok := got["summary"].(map[string]any)
	if !ok {
		t.Fatal("missing 'summary' section")
	}
	if total, _ := summary["total_logs"].(float64); total < 1 {
		t.Errorf("summary.total_logs = %v, want >= 1", total)
	}
}

// TestRegression_EventContinuationsTriggeringQueries pins the Pareto
// triggering-queries aggregation built from STATEMENT continuation
// pairing.
//
// The fixture stages three "invalid input syntax" errors whose STATEMENT
// lines share the same normalised SQL signature but differ in their
// literals ('foo' / 'bar' / 'bar'). After normalisation they collapse
// into a single triggering_queries row with count 3.
//
// A second pattern (relation does not exist) carries one PID with a
// single STATEMENT — count 1. A third (WARNING "there is no transaction
// in progress") has no STATEMENT continuation at all, so its
// triggering_queries field is absent (omitempty).
func TestRegression_EventContinuationsTriggeringQueries(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/events/event_continuations.log")

	tops, _ := got["top_events"].([]any)
	if len(tops) != 3 {
		t.Fatalf("top_events length = %d, want 3 (invalid input + relation does not exist + no transaction in progress)", len(tops))
	}

	byMsg := map[string]map[string]any{}
	for _, raw := range tops {
		ev := raw.(map[string]any)
		byMsg[ev["message"].(string)] = ev
	}

	invalid := pickEvent(t, byMsg, "invalid input syntax")
	if v, _ := invalid["count"].(float64); v != 3 {
		t.Errorf("invalid input count = %v, want 3", v)
	}

	// The three INSERTs collapse to one normalised signature with count 3.
	invalidTQ, _ := invalid["triggering_queries"].([]any)
	if len(invalidTQ) != 1 {
		t.Fatalf("invalid triggering_queries = %d, want 1 (the three INSERTs collapse on normalisation)", len(invalidTQ))
	}
	tqRow := invalidTQ[0].(map[string]any)
	if c, _ := tqRow["count"].(float64); c != 3 {
		t.Errorf("invalid triggering_queries[0].count = %v, want 3 (foo + bar + bar)", c)
	}
	if nq, _ := tqRow["normalized_query"].(string); !strings.Contains(nq, "insert into orders") {
		t.Errorf("invalid triggering_queries[0].normalized_query unexpected: %q", nq)
	}
	if _, ok := tqRow["id"].(string); !ok {
		t.Errorf("invalid triggering_queries[0].id missing")
	}

	// relation does not exist: one PID, one STATEMENT, count 1.
	rel := pickEvent(t, byMsg, "relation ? does not exist")
	relTQ, _ := rel["triggering_queries"].([]any)
	if len(relTQ) != 1 {
		t.Fatalf("relation triggering_queries = %d, want 1", len(relTQ))
	}
	relRow := relTQ[0].(map[string]any)
	if c, _ := relRow["count"].(float64); c != 1 {
		t.Errorf("relation triggering_queries[0].count = %v, want 1", c)
	}
	if nq, _ := relRow["normalized_query"].(string); !strings.Contains(nq, "from users") {
		t.Errorf("relation triggering_query normalised form: %q", nq)
	}

	// WARNING with no STATEMENT continuation must have no triggering_queries.
	warn := pickEvent(t, byMsg, "there is no transaction in progress")
	if _, present := warn["triggering_queries"]; present {
		t.Errorf("WARNING triggering_queries should be omitted, got %v", warn["triggering_queries"])
	}
}

// pickEvent fails the test if no entry's message contains the substring.
func pickEvent(t *testing.T, byMsg map[string]map[string]any, sub string) map[string]any {
	t.Helper()
	for msg, ev := range byMsg {
		if strings.Contains(msg, sub) {
			return ev
		}
	}
	keys := make([]string, 0, len(byMsg))
	for k := range byMsg {
		keys = append(keys, k)
	}
	t.Fatalf("no top_events message contains %q (have: %v)", sub, keys)
	return nil
}

// TestRegression_MultilineStatementAssembly verifies that a multi-line
// STATEMENT (or duration log with embedded newlines) is assembled into
// a single LogEntry, not split into many continuation fragments. The
// fixture has a 17-line ERROR/STATEMENT block plus a CTE that span
// many lines but should produce 2 entries total.
func TestRegression_MultilineStatementAssembly(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/misc/multiline_statement.log")
	summary, ok := got["summary"].(map[string]any)
	if !ok {
		t.Fatal("missing 'summary' section")
	}
	if total, _ := summary["total_logs"].(float64); total != 2 {
		t.Errorf("summary.total_logs = %v, want 2 (continuation lines should fold)", total)
	}
}
