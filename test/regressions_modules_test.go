package quellog_test

import (
	"strings"
	"testing"
)

// Module-targeted regression tests for areas that previously had only
// indirect coverage: temp files, connections, errors, checkpoints.

// TestRegression_TempFileQueryAssociation pins that a `temporary file:`
// LOG followed by a STATEMENT continuation correctly associates the
// temp file to a query_id, and that multiple temp files for the same
// query share the same id.
func TestRegression_TempFileQueryAssociation(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/misc/tempfile_with_query.log")
	tf, ok := got["temp_files"].(map[string]any)
	if !ok {
		t.Fatal("missing 'temp_files' section")
	}
	if total, _ := tf["total_messages"].(float64); total != 4 {
		t.Errorf("temp_files.total_messages = %v, want 4 (1 small + 3 sharedfileset)", total)
	}
	events, _ := tf["events"].([]any)
	if len(events) != 4 {
		t.Fatalf("expected 4 temp file events, got %d", len(events))
	}
	// Each event should have a non-empty query_id.
	for i, e := range events {
		m := e.(map[string]any)
		qid, _ := m["query_id"].(string)
		if qid == "" {
			t.Errorf("temp_files.events[%d] has empty query_id", i)
		}
	}
	// The last 3 events (sharedfileset) should share the same query_id.
	id1 := events[1].(map[string]any)["query_id"].(string)
	id2 := events[2].(map[string]any)["query_id"].(string)
	id3 := events[3].(map[string]any)["query_id"].(string)
	if id1 != id2 || id2 != id3 {
		t.Errorf("sharedfileset temp files should share query_id, got %q %q %q", id1, id2, id3)
	}
}

// TestRegression_ConnectionsAuthFailures pins that auth failures are
// counted as FATAL events and that successful connections produce
// session metrics. The fixture has 4 auth failures (md5, pg_hba,
// peer, LDAP) plus 2 successful sessions (one IPv6, one local).
func TestRegression_ConnectionsAuthFailures(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/connections/connections_auth_failures.log")

	// 4 FATAL events (the 4 auth failures)
	events, _ := got["events"].([]any)
	fatal := 0
	for _, e := range events {
		m := e.(map[string]any)
		if m["type"].(string) == "FATAL" {
			fatal = int(m["count"].(float64))
		}
	}
	if fatal != 4 {
		t.Errorf("events.FATAL.count = %d, want 4 (md5+pg_hba+peer+LDAP failures)", fatal)
	}

	// Successful sessions: 2 (only the ones that authorized + disconnected)
	conn, ok := got["connections"].(map[string]any)
	if !ok {
		t.Fatal("missing 'connections' section")
	}
	if c, _ := conn["connection_count"].(float64); c != 2 {
		t.Errorf("connections.connection_count = %v, want 2 (auth failures don't count as connections)", c)
	}
	if d, _ := conn["disconnection_count"].(float64); d != 2 {
		t.Errorf("connections.disconnection_count = %v, want 2", d)
	}
}

// TestRegression_ErrorClassesParsing verifies that the events
// classifier recognizes a variety of PostgreSQL SQLSTATE classes
// (08, 22, 23, 25, 28, 42, 53, 57). The fixture mixes 12 errors
// across these classes; the analyzer should expose them all in the
// text section "EVENTS" with proper class labels.
//
// Found while writing this test: `--errors --json` produces an empty
// object {} while `--errors` (text) prints the structured breakdown
// correctly. The `errors` JSON key is also missing in the default
// JSON output. Also, `summary.error_count` and `summary.fatal_count`
// stay at 0 while the events list correctly counts 10 ERROR and 2
// FATAL. Both gaps are documented in the README and the roadmap.
func TestRegression_ErrorClassesParsing(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/errors/errors_sqlstate_classes.log")

	events, _ := got["events"].([]any)
	counts := map[string]int{}
	for _, e := range events {
		m := e.(map[string]any)
		counts[m["type"].(string)] = int(m["count"].(float64))
	}
	if counts["ERROR"] != 10 {
		t.Errorf("events.ERROR.count = %d, want 10", counts["ERROR"])
	}
	if counts["FATAL"] != 2 {
		t.Errorf("events.FATAL.count = %d, want 2", counts["FATAL"])
	}

	// Smoke check on the text output: the SQLSTATE class labels must
	// appear (08, 22, 23, 25, 28, 42, 53, 57). Use --errors to scope.
	out := runHarness(t, false, "testdata/regressions/errors/errors_sqlstate_classes.log", "--errors")
	text := ansiEscapeRe.ReplaceAllString(string(out), "")
	for _, class := range []string{"08 - Connection", "22 - Data", "23 - Integrity", "25 -", "28 - Invalid", "42 - Syntax", "53 - Insufficient Resources", "57 - Operator"} {
		if !strings.Contains(text, class) {
			t.Errorf("--errors text output missing class label %q", class)
		}
	}
}

// TestRegression_CheckpointMetrics verifies that the checkpoint
// analyzer extracts WAL distance, buffer counts, type breakdown
// (time vs xlog) and warning counts.
func TestRegression_CheckpointMetrics(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/checkpoints/checkpoints_full.log")
	chk, ok := got["checkpoints"].(map[string]any)
	if !ok {
		t.Fatal("missing 'checkpoints' section")
	}
	if total, _ := chk["total_checkpoints"].(float64); total != 4 {
		t.Errorf("total_checkpoints = %v, want 4 (3 time + 1 xlog)", total)
	}
	if warn, _ := chk["warning_count"].(float64); warn != 1 {
		t.Errorf("warning_count = %v, want 1 (one 'too frequently' warning)", warn)
	}

	types, _ := chk["types"].(map[string]any)
	timeT, _ := types["time"].(map[string]any)
	xlogT, _ := types["xlog"].(map[string]any)
	if c, _ := timeT["count"].(float64); c != 3 {
		t.Errorf("types.time.count = %v, want 3", c)
	}
	if c, _ := xlogT["count"].(float64); c != 1 {
		t.Errorf("types.xlog.count = %v, want 1", c)
	}

	// Total buffers across the 4 completes: 1234+6047+1318+38069 = 46668
	if buf, _ := chk["total_buffers_written"].(float64); buf != 46668 {
		t.Errorf("total_buffers_written = %v, want 46668", buf)
	}

	// WAL distances must include the 4 completes.
	wd, _ := chk["wal_distances"].([]any)
	if len(wd) != 4 {
		t.Errorf("wal_distances has %d entries, want 4", len(wd))
	}
}
