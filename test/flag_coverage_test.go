package quellog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlagCoverage exercises CLI flags that the corpus tests don't touch
// directly: time/attribute filtering, section selection, alternate output
// formats, and the SQL sub-views. Each subtest is small and focused.
//
// Most subtests use multi_db.log (3 databases, 4 users) so filtering is
// observable; some use other corpus fixtures.
func TestFlagCoverage(t *testing.T) {
	const multiDB = "testdata/regressions/connections/multi_db.log"
	const ddlk = "testdata/regressions/locks/deadlock_basic.log"
	const sqlNorm = "testdata/regressions/sql/sql_normalization.log"

	t.Run("DBNameFilter", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--dbname", "appdb", "--json")
		doc := mustJSON(t, out)
		dbs := doc["databases"].([]any)
		if len(dbs) != 1 {
			t.Fatalf("expected 1 database after --dbname appdb, got %d: %v", len(dbs), dbs)
		}
		if name := dbs[0].(map[string]any)["name"].(string); name != "appdb" {
			t.Errorf("filtered db = %q, want appdb", name)
		}
	})

	t.Run("DBUserFilter", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--dbuser", "admin", "--json")
		doc := mustJSON(t, out)
		users := doc["users"].([]any)
		if len(users) != 1 {
			t.Fatalf("expected 1 user after --dbuser admin, got %d", len(users))
		}
		if name := users[0].(map[string]any)["name"].(string); name != "admin" {
			t.Errorf("filtered user = %q, want admin", name)
		}
	})

	t.Run("ExcludeUser", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--exclude-user", "app", "--json")
		doc := mustJSON(t, out)
		users := doc["users"].([]any)
		for _, u := range users {
			if name := u.(map[string]any)["name"].(string); name == "app" {
				t.Errorf("user 'app' present despite --exclude-user app")
			}
		}
		if len(users) == 0 {
			t.Error("all users excluded — expected analyst/auditor/admin to remain")
		}
	})

	t.Run("AppNameFilter", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--appname", "tableau", "--json")
		doc := mustJSON(t, out)
		apps := doc["apps"].([]any)
		if len(apps) != 1 {
			t.Fatalf("expected 1 app, got %d", len(apps))
		}
		if name := apps[0].(map[string]any)["name"].(string); name != "tableau" {
			t.Errorf("filtered app = %q, want tableau", name)
		}
	})

	t.Run("TimeRangeBegin", func(t *testing.T) {
		// Keep entries from 10:00:04 onward (3 entries: 04, 05, 06)
		out := runHarness(t, false, multiDB, "--begin", "2026-04-20 10:00:04", "--json")
		doc := mustJSON(t, out)
		total := doc["summary"].(map[string]any)["total_logs"].(float64)
		if total < 3 || total > 4 {
			t.Errorf("--begin filter: total_logs = %v, want 3-4", total)
		}
	})

	t.Run("TimeRangeEnd", func(t *testing.T) {
		// Keep entries up to 10:00:02 (3 entries: 00, 01, 02)
		out := runHarness(t, false, multiDB, "--end", "2026-04-20 10:00:02", "--json")
		doc := mustJSON(t, out)
		total := doc["summary"].(map[string]any)["total_logs"].(float64)
		if total < 2 || total > 3 {
			t.Errorf("--end filter: total_logs = %v, want 2-3", total)
		}
	})

	t.Run("SectionFlagSummaryOnly", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--summary", "--json")
		doc := mustJSON(t, out)
		if _, ok := doc["summary"]; !ok {
			t.Error("--summary --json: missing summary section")
		}
		// Most other top-level sections must be absent.
		for _, k := range []string{"sql_performance", "checkpoints", "events", "connections"} {
			if _, ok := doc[k]; ok {
				t.Errorf("--summary --json: unexpected section %q present", k)
			}
		}
	})

	t.Run("SectionFlagLocksOnly", func(t *testing.T) {
		out := runHarness(t, false, ddlk, "--locks", "--json")
		doc := mustJSON(t, out)
		if _, ok := doc["locks"]; !ok {
			t.Error("--locks --json: missing locks section")
		}
	})

	t.Run("SQLPerformanceFlag", func(t *testing.T) {
		out := runHarness(t, false, sqlNorm, "--sql-performance", "--json")
		doc := mustJSON(t, out)
		// --sql-performance --json puts SQL data at the top level
		// (no 'sql_performance' wrapper), with 'slowest_queries' etc.
		if _, ok := doc["slowest_queries"]; !ok {
			t.Error("--sql-performance --json: missing slowest_queries top-level field")
		}
	})

	t.Run("SQLOverviewFlag", func(t *testing.T) {
		out := runHarness(t, false, sqlNorm, "--sql-overview", "--json")
		doc := mustJSON(t, out)
		if len(doc) == 0 {
			t.Error("--sql-overview --json: empty output")
		}
	})

	t.Run("SQLDetailFlag", func(t *testing.T) {
		// First, get a known query ID from the default JSON.
		base := runHarness(t, false, sqlNorm, "--json")
		bdoc := mustJSON(t, base)
		queries := bdoc["sql_performance"].(map[string]any)["queries"].([]any)
		if len(queries) == 0 {
			t.Skip("sql_normalization produced no queries — cannot pick a query ID")
		}
		qid := queries[0].(map[string]any)["id"].(string)

		out := runHarness(t, false, sqlNorm, "--sql-detail", qid, "--json")
		var doc any
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("--sql-detail %s --json: invalid JSON: %v\n%s", qid, err, out)
		}
		// Verify the picked ID appears somewhere in the rendered JSON.
		if !strings.Contains(string(out), qid) {
			t.Errorf("--sql-detail %s output does not mention the query ID", qid)
		}
	})

	t.Run("FullFlag", func(t *testing.T) {
		// multi_db has both successful SELECT/INSERT (sql_performance)
		// and an ERROR (events). It does not exercise the locks section
		// — that's covered by SectionFlagLocksOnly above.
		out := runHarness(t, false, multiDB, "--full", "--json")
		doc := mustJSON(t, out)
		for _, k := range []string{"sql_performance", "summary", "events"} {
			if _, ok := doc[k]; !ok {
				t.Errorf("--full --json: missing section %q", k)
			}
		}
	})

	t.Run("OutputToFile", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "out.json")
		out := runHarness(t, false, multiDB, "--json", "-o", path)
		// stdout should be empty when -o is used.
		if len(strings.TrimSpace(string(out))) != 0 {
			t.Errorf("--json -o: stdout should be empty, got %d bytes", len(out))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read output file: %v", err)
		}
		if _, err := mustJSONNoFail(data); err != nil {
			t.Errorf("output file contents are not valid JSON: %v", err)
		}
	})

	t.Run("YAMLFormat", func(t *testing.T) {
		out := runHarness(t, false, multiDB, "--yaml")
		text := string(out)
		if len(strings.TrimSpace(text)) == 0 {
			t.Error("--yaml produced empty output")
		}
		// YAML smoke check: first non-empty line should look key-like.
		if !strings.Contains(text, "summary:") {
			snippet := text
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			t.Errorf("--yaml output does not contain a 'summary:' key:\n%s", snippet)
		}
	})

	t.Run("HTMLFormat", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "report.html")
		runHarness(t, false, multiDB, "--html", "-o", path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read HTML output: %v", err)
		}
		// HTML smoke checks.
		text := string(data)
		if !strings.Contains(strings.ToLower(text), "<html") {
			t.Error("--html output does not contain '<html'")
		}
		if !strings.Contains(strings.ToLower(text), "</html>") {
			t.Error("--html output is missing closing </html> tag")
		}
	})
}

// mustJSONNoFail unmarshals raw as a top-level value, returning the
// error rather than failing the test. Used where we want to assert on
// the error itself.
func mustJSONNoFail(raw []byte) (any, error) {
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}
