package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pgbouncerFixture is the reference real-world pgBouncer log shipped with the
// regression corpus. Detection tests build their samples out of its lines so
// no hand-written pgBouncer dialect ever enters the test suite.
const pgbouncerFixture = "../test/testdata/regressions/pgbouncer/pgbouncer.log"

// pgbouncerVerboseFixture is the verbose=2 capture: DEBUG/NOISE chatter, most
// of it without any socket marker.
const pgbouncerVerboseFixture = "../test/testdata/regressions/pgbouncer/pgbouncer_verbose.log"

// fixtureLines returns the non-empty lines of a fixture, optionally keeping
// only those that do not carry a C-0x/S-0x socket marker.
func fixtureLines(t *testing.T, path string, dropSocketLines bool) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		if dropSocketLines && strings.Contains(line, "-0x") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		t.Fatalf("fixture %s yielded no usable line", path)
	}
	return kept
}

// writeSample writes content to a .log file in a temp dir and returns its path.
func writeSample(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return path
}

// TestDetectPgBouncerWithoutSocketMarkers covers the operational captures that
// carry no "login attempt"/"closing because" socket line and no "stats:" line
// inside the detection sample: a restart, a freshly rotated file, a monitoring
// snapshot, and a file whose head is nothing but verbose DEBUG chatter. All of
// them are genuine pgBouncer logs and must not be rejected.
func TestDetectPgBouncerWithoutSocketMarkers(t *testing.T) {
	service := fixtureLines(t, pgbouncerFixture, true)
	debug := fixtureLines(t, pgbouncerVerboseFixture, true)

	// Startup banner alone: the first lines pgBouncer writes on restart.
	banner := strings.Join(service[:4], "\n") + "\n"

	// A single repeated banner line, as a supervision snapshot would hold.
	var repeated strings.Builder
	for i := 0; i < 20; i++ {
		repeated.WriteString(service[3])
		repeated.WriteString("\n")
	}

	// More than one detection sample (32 KB) of marker-free DEBUG/NOISE
	// chatter ahead of ordinary traffic.
	var noisy strings.Builder
	for noisy.Len() <= sampleBufferSize {
		for _, line := range debug {
			noisy.WriteString(line)
			noisy.WriteString("\n")
		}
	}
	traffic, err := os.ReadFile(pgbouncerFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	noisy.Write(traffic)

	cases := []struct {
		name    string
		content string
	}{
		{"startup banner", banner},
		{"repeated process up banner", repeated.String()},
		{"debug chatter ahead of traffic", noisy.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSample(t, "pgbouncer.log", tc.content)
			if got := DetectFileFormat(path); got != "pgbouncer" {
				t.Fatalf("DetectFileFormat = %q, want \"pgbouncer\"", got)
			}
		})
	}
}

// TestDetectPgBouncerWithoutTrailingNewline covers a tiny pgBouncer log whose
// last line is not newline-terminated — a file still being written, or a
// two-line excerpt. The sampler used to drop everything after the last
// newline, which on such a file discarded half the content.
func TestDetectPgBouncerWithoutTrailingNewline(t *testing.T) {
	lines := fixtureLines(t, pgbouncerFixture, true)
	content := lines[0] + "\n" + lines[3]
	if strings.HasSuffix(content, "\n") {
		t.Fatal("sample must not end with a newline")
	}
	path := writeSample(t, "pgbouncer.log", content)
	if got := DetectFileFormat(path); got != "pgbouncer" {
		t.Fatalf("DetectFileFormat = %q, want \"pgbouncer\"", got)
	}
}

// TestDetectNoPgBouncerFalsePositive is the counterweight to the two tests
// above: widening pgBouncer detection must never capture a PostgreSQL log.
// Every non-pgBouncer fixture in the corpus is re-detected here, and its
// format is logged so an intentional change is visible in -v output.
func TestDetectNoPgBouncerFalsePositive(t *testing.T) {
	root := "../test/testdata"
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "pgbouncer" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".log", ".csv", ".json", ".jsonl":
		default:
			return nil
		}
		if strings.Contains(path, ".golden") {
			return nil
		}
		format := DetectFileFormat(path)
		t.Logf("%-70s -> %q", path, format)
		if format == "pgbouncer" {
			t.Errorf("%s detected as pgBouncer (false positive)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestDetectPgBouncerSignatureMustBeOnItsOwnLine is a refutation test.
// The signature used to be matched against the whole sample, so a
// PostgreSQL line that merely quotes a pooler line -- a logged statement
// under log_statement = all, a support bundle where someone pasted an
// excerpt -- carried the entire file onto the pooler track. Matching it
// per line, on a line that is itself prefix-shaped, is the rule in
// force; matching it anywhere in the sample is the rule it replaces.
// The two agree on every real capture and disagree here.
func TestDetectPgBouncerSignatureMustBeOnItsOwnLine(t *testing.T) {
	dir := t.TempDir()
	quoted := filepath.Join(dir, "quoted.log")
	lines := "2026-01-05 10:00:00.123 UTC [4242] LOG:  statement: SELECT '[1] LOG stats: x';\n" +
		"2026-01-05 10:00:01.123 UTC [4242] LOG:  duration: 5.000 ms\n"
	if err := os.WriteFile(quoted, []byte(lines), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	if got := DetectFileFormat(quoted); got != "stderr" {
		t.Errorf("DetectFileFormat = %q, want \"stderr\": a PostgreSQL line quoting a pooler line is still a PostgreSQL line", got)
	}
}
