package quellog_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ansiEscapeRe strips terminal escape sequences so the text-smoke check
// is robust whether the harness binary detected a TTY or not.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

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

	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("read regressions dir: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isFixtureFile(name) {
			continue
		}
		fixturePath := filepath.Join(fixturesDir, name)

		t.Run(name+"/json", func(t *testing.T) {
			got := runHarness(t, false, fixturePath, "--json")
			compareOrUpdateGolden(t, got, goldenPath(fixturePath, "json"))
		})

		t.Run(name+"/md", func(t *testing.T) {
			got := runHarness(t, false, fixturePath, "--md")
			compareOrUpdateGolden(t, got, goldenPath(fixturePath, "md"))
		})

		t.Run(name+"/text-smoke", func(t *testing.T) {
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
