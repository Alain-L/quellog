// test/counters_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestCounters regression-locks a batch of counter/calculation fixes made on
// the fix/counter-calculations branch. Each case is a hand-crafted stderr
// fixture that isolates one arithmetic or classification bug; the assertion
// pins the corrected JSON value so the bug cannot silently return.
//
// One fixture per bug, values independently recomputed from the fixture and
// (where a comparable definition exists) corroborated by pgBadger:
//
//	H1  SET must be UTILITY, not SELECT. The prefix table mapped "SET" to
//	    "se-", colliding with SELECT's "se-", so every SET counted as a SELECT.
//	H3  Lock average wait = total wait / ACQUIRED events, not / (waiting +
//	    acquired): only acquired events contribute a measured wait.
//	H4  Average session time = total session time / SESSION count, not /
//	    disconnection count (bare disconnects carry no session time).
//	H6  A genuine 0 ms minimum must survive: min was pre-seeded to 0, so any
//	    log made the reported minimum 0 even when the true minimum was higher;
//	    here the true minimum IS 0 and must still be reported as such.
//	M6  Temp-file average size = total size / files WITH a size, not / all
//	    temp-file messages (path-only lines have no size).
//	M8  A message pattern's SQLSTATE class must back-fill from a later
//	    occurrence when the first occurrence carried none.
//
// (M11 in the same branch was a documentation-only fix — the session-duration
// comment in output/json.go — and has no observable value to assert here.)
func TestCounters(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	cases := []struct {
		name    string
		fixture string
		flags   []string
		check   func(t *testing.T, root map[string]any)
	}{
		{
			name:    "H1_set_is_utility",
			fixture: "testdata/counter_h1.log",
			flags:   []string{"--full", "--json"},
			check: func(t *testing.T, root map[string]any) {
				qt := mustMap(t, root, "sql_overview")
				cats := asCountByKey(t, qt["categories"], "category")
				wantCount(t, cats, "DML", 5)
				wantCount(t, cats, "UTILITY", 4)
				types := asFieldByKey(t, qt["types"], "type", "category")
				if got := types["SET"]; got != "UTILITY" {
					t.Errorf("SET category = %q, want %q", got, "UTILITY")
				}
			},
		},
		{
			name:    "H3_lock_avg_wait_over_acquired",
			fixture: "testdata/counter_h3.log",
			flags:   []string{"--locks", "--json"},
			check: func(t *testing.T, root map[string]any) {
				locks := mustMap(t, root, "locks")
				wantString(t, locks, "avg_wait_time", "2.00 s")
				wantNumber(t, locks, "acquired_events", 3)
				wantNumber(t, locks, "waiting_events", 2)
			},
		},
		{
			name:    "H4_avg_session_over_session_count",
			fixture: "testdata/counter_h4.log",
			flags:   []string{"--connections", "--json"},
			check: func(t *testing.T, root map[string]any) {
				conn := mustMap(t, root, "connections")
				wantString(t, conn, "avg_session_time", "20s")
				wantNumber(t, mustMap(t, conn, "session_stats"), "count", 4)
			},
		},
		{
			name:    "H6_genuine_zero_minimum",
			fixture: "testdata/counter_h6.log",
			flags:   []string{"--sql-summary", "--json"},
			check: func(t *testing.T, root map[string]any) {
				wantString(t, mustMap(t, root, "sql_performance"), "query_min_duration", "0 ms")
			},
		},
		{
			name:    "M6_temp_avg_over_sized_files",
			fixture: "testdata/counter_m6.log",
			flags:   []string{"--tempfiles", "--json"},
			check: func(t *testing.T, root map[string]any) {
				tf := mustMap(t, root, "temp_files")
				wantString(t, tf, "avg_size", "2.00 MB")
				wantNumber(t, tf, "total_messages", 5)
			},
		},
		{
			name:    "M8_sqlstate_backfill",
			fixture: "testdata/counter_m8.log",
			flags:   []string{"--full", "--json"},
			check: func(t *testing.T, root map[string]any) {
				top := asList(t, root["top_events"])
				if len(top) == 0 {
					t.Fatal("top_events is empty")
				}
				first := mustCast(t, top[0])
				wantString(t, first, "sql_state_class", "22")
				wantNumber(t, first, "count", 3)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{tc.fixture}, tc.flags...)
			cmd := exec.Command("../quellog_test", args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run failed: %v\nstderr: %s", err, stderr.String())
			}
			var root map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &root); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
			}
			tc.check(t, root)
		})
	}
}

// --- JSON navigation helpers ------------------------------------------------

func mustMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q", key)
	}
	return mustCast(t, v)
}

func mustCast(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want object", v)
	}
	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("value is %T, want array", v)
	}
	return l
}

func wantString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok {
		t.Fatalf("key %q is %T, want string", key, m[key])
	}
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func wantNumber(t *testing.T, m map[string]any, key string, want float64) {
	t.Helper()
	got, ok := m[key].(float64)
	if !ok {
		t.Fatalf("key %q is %T, want number", key, m[key])
	}
	if got != want {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}

// asCountByKey turns a list of objects into keyField -> count(float64).
func asCountByKey(t *testing.T, v any, keyField string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, e := range asList(t, v) {
		m := mustCast(t, e)
		out[m[keyField].(string)] = m["count"].(float64)
	}
	return out
}

// asFieldByKey turns a list of objects into keyField -> valField(string).
func asFieldByKey(t *testing.T, v any, keyField, valField string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range asList(t, v) {
		m := mustCast(t, e)
		out[m[keyField].(string)] = m[valField].(string)
	}
	return out
}

func wantCount(t *testing.T, m map[string]float64, key string, want float64) {
	t.Helper()
	if got := m[key]; got != want {
		t.Errorf("%s count = %v, want %v", key, got, want)
	}
}
