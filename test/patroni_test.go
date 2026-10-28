package quellog_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPatroniGoldens runs each lab capture under testdata/patroni (one
// directory per scenario, Patroni and PostgreSQL logs of every member) and
// pins the JSON and Markdown reports, and the text report of the Patroni
// logs alone and of --patroni-detail.
func TestPatroniGoldens(t *testing.T) {
	manifests, _ := filepath.Glob("testdata/patroni/*/MANIFEST.md")
	if len(manifests) == 0 {
		t.Fatal("no Patroni fixtures")
	}
	for _, manifest := range manifests {
		dir := filepath.Dir(manifest)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			logs, _ := filepath.Glob(filepath.Join(dir, "*.patroni.log"))
			pg, _ := filepath.Glob(filepath.Join(dir, "*.postgresql.log"))
			args := append(logs, pg...)
			for _, format := range []string{"json", "md"} {
				got := runHarness(t, false, append(args, "--"+format)...)
				compareOrUpdateGolden(t, got, goldenPath(dir, format))
			}
			got := runHarness(t, false, append(args, "--md", "--patroni-detail")...)
			compareOrUpdateGolden(t, got, goldenPath(dir, "detail.md"))
			got = elapsedRe.ReplaceAll(runHarness(t, false, logs...), []byte("processed in X s"))
			compareOrUpdateGolden(t, got, goldenPath(dir, "txt"))
			got = elapsedRe.ReplaceAll(runHarness(t, false, append(args, "--patroni-detail")...), []byte("processed in X s"))
			compareOrUpdateGolden(t, got, goldenPath(dir, "detail.txt"))
		})
	}
}

// TestPatroniEventDetail pins --event-detail on a Patroni error: its ID
// from the JSON report finds its occurrences, dates and members.
func TestPatroniEventDetail(t *testing.T) {
	logs, _ := filepath.Glob("testdata/patroni/A-p4-text-async/*.patroni.log")
	var id string
	for _, m := range mustJSON(t, runHarness(t, false, append(logs, "--json")...))["patroni"].(map[string]any)["messages"].([]any) {
		if msg := m.(map[string]any); msg["message"] == "Error communicating with DCS" {
			id = msg["id"].(string)
		}
	}
	out := string(runHarness(t, false, append(logs, "-E", id)...))
	for _, want := range []string{"Id                   : " + id, "Source               : Patroni, pg1, pg2, pg3", "Count                : 15", "First Seen           : 2026-09-24 13:54:08"} {
		if !strings.Contains(out, want) {
			t.Errorf("--event-detail %s: missing %q in\n%s", id, want, out)
		}
	}
}
