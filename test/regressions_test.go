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

// TestRegression_VacuumContinuationsParsed pins the parsing of the
// "buffer usage / WAL usage / tuples / system usage" continuation lines
// that PostgreSQL emits after "automatic vacuum of table". The fixture
// has three vacuums (one on "orders" with mild numbers, two on "events"
// where "dead but not yet removable" climbs into the xmin-pressure
// range) plus one analyze.
//
// The test pins:
//   - The global totals (elapsed, tuples removed, xmin-blocked count,
//     buffer hits/misses/dirtied/written, WAL records/bytes) so the
//     extractors don't silently drift across PostgreSQL log shapes.
//   - The "top tables by elapsed" ordering puts the heaviest table
//     first.
//   - The xmin-blocked list surfaces "events" (not "orders"), capturing
//     the wraparound-precursor signal we promised in the audit.
//   - The "slowest single vacuum" sample points at "events" with the
//     45-second elapsed it actually had.
func TestRegression_VacuumContinuationsParsed(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/vacuum/vacuum_continuations.log")
	m, ok := got["maintenance"].(map[string]any)
	if !ok {
		t.Fatal("missing 'maintenance' section")
	}

	// Counts (existing, unchanged behaviour).
	if v, _ := m["vacuum_count"].(float64); v != 3 {
		t.Errorf("vacuum_count = %v, want 3", v)
	}
	if v, _ := m["analyze_count"].(float64); v != 1 {
		t.Errorf("analyze_count = %v, want 1", v)
	}

	// Global continuation aggregates.
	if v, _ := m["total_vacuum_elapsed_seconds"].(float64); v < 49.4 || v > 49.5 {
		t.Errorf("total_vacuum_elapsed_seconds = %v, want ~49.479 (1.234 + 45.789 + 2.456)", v)
	}
	if v, _ := m["total_tuples_removed"].(float64); v != 10350 {
		t.Errorf("total_tuples_removed = %v, want 10350 (250 + 10000 + 100)", v)
	}
	if v, _ := m["total_tuples_not_yet_removable"].(float64); v != 1_500_000 {
		t.Errorf("total_tuples_not_yet_removable = %v, want 1500000 (two events vacuums report 750000 each)", v)
	}
	if v, _ := m["total_buffer_hits"].(float64); v != 7100 {
		t.Errorf("total_buffer_hits = %v, want 7100", v)
	}
	if v, _ := m["total_buffer_misses"].(float64); v != 1555 {
		t.Errorf("total_buffer_misses = %v, want 1555", v)
	}
	if v, _ := m["total_buffer_dirtied"].(float64); v != 842 {
		t.Errorf("total_buffer_dirtied = %v, want 842", v)
	}
	if v, _ := m["total_buffer_written"].(float64); v != 424 {
		t.Errorf("total_buffer_written = %v, want 424", v)
	}
	if v, _ := m["total_wal_records"].(float64); v != 3430 {
		t.Errorf("total_wal_records = %v, want 3430", v)
	}
	if v, _ := m["total_wal_bytes"].(float64); v != 918479 {
		t.Errorf("total_wal_bytes = %v, want 918479", v)
	}

	// Top-N ordering on elapsed: the two-run "events" table dominates
	// "orders" since 45.789 + 2.456 > 1.234.
	top, _ := m["top_vacuum_tables"].([]any)
	if len(top) != 2 {
		t.Fatalf("top_vacuum_tables length = %d, want 2 (events, orders)", len(top))
	}
	if first, _ := top[0].(map[string]any); first["table"] != "appdb.public.events" {
		t.Errorf("top_vacuum_tables[0].table = %v, want appdb.public.events", first["table"])
	}

	// Xmin-blocked list only carries "events" (orders reports 0
	// not-yet-removable tuples and must be filtered out).
	xmin, _ := m["xmin_blocked_tables"].([]any)
	if len(xmin) != 1 {
		t.Fatalf("xmin_blocked_tables length = %d, want 1 (only events has not-yet-removable tuples)", len(xmin))
	}
	first, _ := xmin[0].(map[string]any)
	if first["table"] != "appdb.public.events" {
		t.Errorf("xmin_blocked_tables[0].table = %v, want appdb.public.events", first["table"])
	}

	// Slowest single vacuum: the 45.789s "events" run.
	sl, ok := m["slowest_vacuum"].(map[string]any)
	if !ok {
		t.Fatal("missing slowest_vacuum")
	}
	if sl["table"] != "appdb.public.events" {
		t.Errorf("slowest_vacuum.table = %v, want appdb.public.events", sl["table"])
	}
	if v, _ := sl["elapsed_seconds"].(float64); v < 45.7 || v > 45.9 {
		t.Errorf("slowest_vacuum.elapsed_seconds = %v, want ~45.789", v)
	}
}

// TestRegression_ServerSectionCapturesLifecycle pins the server-section
// markers parser: starts, SIGHUP reloads, parameter changes, fast
// shutdowns, backend signal crashes and crash-recovery announcements.
//
// The fixture mixes (in order):
//   - 1 "not properly shut down" + 1 start (crash-recovery dance)
//   - 1 SIGHUP that carries 2 "parameter X changed to Y" lines
//   - 1 "server process terminated by signal 11" (backend crash)
//   - 1 fast shutdown + 1 shutdown completed line
//
// The test pins the counts and the parameter-change extraction so a
// future refactor of the prefix-stripping or the body matchers does
// not silently lose a marker (each one represents real grep work a
// DBA would otherwise redo manually during a post-mortem).
func TestRegression_ServerSectionCapturesLifecycle(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/server/server.log")
	srv, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatal("missing 'server' section in JSON output")
	}

	// Counters.
	if v, _ := srv["starts"].(float64); v != 1 {
		t.Errorf("starts = %v, want 1", v)
	}
	if v, _ := srv["reloads"].(float64); v != 1 {
		t.Errorf("reloads = %v, want 1", v)
	}
	if v, _ := srv["shutdowns_fast"].(float64); v != 1 {
		t.Errorf("shutdowns_fast = %v, want 1", v)
	}
	if v, _ := srv["crash_recoveries"].(float64); v != 1 {
		t.Errorf("crash_recoveries = %v, want 1", v)
	}
	if v, _ := srv["backend_crashes"].(float64); v != 1 {
		t.Errorf("backend_crashes = %v, want 1", v)
	}
	if v, _ := srv["shutdown_completed"].(float64); v != 1 {
		t.Errorf("shutdown_completed = %v, want 1", v)
	}

	// Signal breakdown — signal 11 (SIGSEGV) is the test crash.
	sigs, _ := srv["signal_counts"].(map[string]any)
	if v, _ := sigs["11"].(float64); v != 1 {
		t.Errorf("signal_counts[\"11\"] = %v, want 1", v)
	}

	// Parameter changes: 2 entries from the SIGHUP, "work_mem" first.
	params, _ := srv["parameter_changes"].([]any)
	if len(params) != 2 {
		t.Fatalf("parameter_changes length = %d, want 2", len(params))
	}
	first, _ := params[0].(map[string]any)
	if first["parameter"] != "work_mem" {
		t.Errorf("parameter_changes[0].parameter = %v, want work_mem", first["parameter"])
	}
	if first["new"] != "16MB" {
		t.Errorf("parameter_changes[0].new = %v, want 16MB", first["new"])
	}
	second, _ := params[1].(map[string]any)
	if second["parameter"] != "log_min_duration_statement" {
		t.Errorf("parameter_changes[1].parameter = %v, want log_min_duration_statement", second["parameter"])
	}

	// Timeline carries one entry per lifecycle event, in order.
	timeline, _ := srv["timeline"].([]any)
	if len(timeline) != 5 {
		t.Fatalf("timeline length = %d, want 5 (recovery, start, sighup, crash, shutdown)", len(timeline))
	}
	kinds := []string{"recovery", "start", "sighup", "crash", "shutdown"}
	for i, want := range kinds {
		ev, _ := timeline[i].(map[string]any)
		if ev["kind"] != want {
			t.Errorf("timeline[%d].kind = %v, want %v", i, ev["kind"], want)
		}
	}
}

// TestRegression_ReplicationSectionCapturesMarkers pins the parsing of
// the small but operationally-critical replication marker set (stream
// reconnects, WAL receive failures, replication terminations, recovery
// conflicts, walsender timeouts). The fixture mixes the most common
// real-world markers we see on Dalibo customer logs and asserts:
//
//   - per-marker counts in the markers map,
//   - the rolled-up headline counters (stream_reconnects, terminations,
//     conflicts) match the per-marker totals,
//   - last_termination points at the latest of the four termination
//     markers (here: the 12:01 walsender timeout),
//   - peak_hour_label is set when two reconnects fall in the same hour,
//   - conflict_queries resolve to a SQLID via the STATEMENT continuation
//     line that follows a recovery-conflict event for the same PID.
func TestRegression_ReplicationSectionCapturesMarkers(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/replication/replication.log")
	r, ok := got["replication"].(map[string]any)
	if !ok {
		t.Fatal("missing 'replication' section")
	}

	// Per-marker counts.
	markers, _ := r["markers"].(map[string]any)
	if v, _ := markers["stream_started"].(float64); v != 3 {
		t.Errorf("markers.stream_started = %v, want 3", v)
	}
	if v, _ := markers["wal_receive_failed"].(float64); v != 1 {
		t.Errorf("markers.wal_receive_failed = %v, want 1", v)
	}
	if v, _ := markers["replication_term"].(float64); v != 1 {
		t.Errorf("markers.replication_term = %v, want 1", v)
	}
	if v, _ := markers["walsender_timeout"].(float64); v != 1 {
		t.Errorf("markers.walsender_timeout = %v, want 1", v)
	}
	if v, _ := markers["conflict_cancel"].(float64); v != 1 {
		t.Errorf("markers.conflict_cancel = %v, want 1", v)
	}
	if v, _ := markers["conflict_terminate"].(float64); v != 1 {
		t.Errorf("markers.conflict_terminate = %v, want 1", v)
	}

	// Rolled-up headlines.
	if v, _ := r["stream_reconnects"].(float64); v != 3 {
		t.Errorf("stream_reconnects = %v, want 3", v)
	}
	if v, _ := r["conflicts_with_recovery"].(float64); v != 2 {
		t.Errorf("conflicts_with_recovery = %v, want 2 (cancel + terminate)", v)
	}
	if v, _ := r["replication_terminations"].(float64); v != 3 {
		t.Errorf("replication_terminations = %v, want 3 (wal_receive_failed + replication_term + walsender_timeout)", v)
	}
	if v, _ := r["total_events"].(float64); v != 8 {
		t.Errorf("total_events = %v, want 8", v)
	}

	// Most recent termination is the 12:01 walsender timeout.
	if v, _ := r["last_termination"].(string); v != "2026-04-20 12:01:05" {
		t.Errorf("last_termination = %v, want 2026-04-20 12:01:05", v)
	}

	// Two reconnects fall in the same hour (04:16 + 04:32) → peak hour.
	if v, _ := r["peak_hour_label"].(string); v != "04:00-05:00" {
		t.Errorf("peak_hour_label = %v, want 04:00-05:00", v)
	}
	if v, _ := r["peak_hour_count"].(float64); v != 2 {
		t.Errorf("peak_hour_count = %v, want 2", v)
	}

	// Two unique conflict queries; each resolved via STATEMENT continuation.
	cq, _ := r["conflict_queries"].([]any)
	if len(cq) != 2 {
		t.Fatalf("conflict_queries length = %d, want 2", len(cq))
	}
	for i, qAny := range cq {
		q, _ := qAny.(map[string]any)
		id, _ := q["id"].(string)
		if id == "" {
			t.Errorf("conflict_queries[%d].id is empty (STATEMENT continuation not resolved)", i)
		}
		if c, _ := q["count"].(float64); c != 1 {
			t.Errorf("conflict_queries[%d].count = %v, want 1", i, c)
		}
	}
}
