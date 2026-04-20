package quellog_test

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ansiEscapeRe strips terminal escape sequences so the text-smoke check
// is robust whether the harness binary detected a TTY or not.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// markdownNonDeterministic used to hold fixtures whose Markdown output
// varied across runs due to a missing tiebreaker in the histogram
// sort. The sort now falls back to the full label string when two
// buckets share the same start minute, so the map is empty. Kept here
// in case the issue resurfaces.
var markdownNonDeterministic = map[string]bool{}

// TestRegressionCorpus iterates over every fixture in
// testdata/regressions/ and exercises three formats:
//
//   - JSON: golden-compared (regenerable via -update)
//   - Markdown: golden-compared (regenerable via -update)
//   - Text (default): smoke-tested only — the "processed in X.XX s" line
//     in the text output is non-deterministic, so a strict golden does
//     not work; we instead assert exit 0, non-empty output, presence of
//     a section header, and absence of panic/runtime-error markers.
//
// Adding a new fixture is "drop a file in testdata/regressions/" plus
// `go test ./test/ -run TestRegressionCorpus -update` to seed its
// goldens — no Go code change required.
func TestRegressionCorpus(t *testing.T) {
	const fixturesDir = "testdata/regressions"

	// Walk recursively: fixtures live in themed subdirectories
	// (locks/, sql/, parsers/, ...) under testdata/regressions/.
	err := filepath.WalkDir(fixturesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !isFixtureFile(name) {
			return nil
		}
		// Sub-test name uses the path relative to fixturesDir so it
		// reads naturally: "locks/deadlock_basic.log/json".
		rel, _ := filepath.Rel(fixturesDir, path)
		fixturePath := path

		t.Run(rel+"/json", func(t *testing.T) {
			got := runHarness(t, false, fixturePath, "--json")
			compareOrUpdateGolden(t, got, goldenPath(fixturePath, "json"))
		})

		t.Run(rel+"/md", func(t *testing.T) {
			if markdownNonDeterministic[name] {
				t.Skipf("markdown output is non-deterministic for %s (see markdownNonDeterministic)", name)
			}
			got := runHarness(t, false, fixturePath, "--md")
			compareOrUpdateGolden(t, got, goldenPath(fixturePath, "md"))
		})

		t.Run(rel+"/text-smoke", func(t *testing.T) {
			raw := runHarness(t, false, fixturePath)
			text := ansiEscapeRe.ReplaceAllString(string(raw), "")
			if len(strings.TrimSpace(text)) == 0 {
				t.Fatal("text output is empty")
			}
			for _, marker := range []string{"panic:", "runtime error", "fatal error:"} {
				if strings.Contains(text, marker) {
					t.Fatalf("text output contains %q:\n%s", marker, text)
				}
			}
			if !strings.Contains(text, "SUMMARY") {
				t.Fatalf("text output missing SUMMARY section:\n%s", text)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk regressions dir: %v", err)
	}
}

// isFixtureFile reports whether name is a corpus fixture (not a golden,
// not the README, not anything else we drop alongside).
func isFixtureFile(name string) bool {
	if name == "README.md" {
		return false
	}
	if strings.HasSuffix(name, ".golden") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".log", ".csv", ".json":
		return true
	}
	return false
}
