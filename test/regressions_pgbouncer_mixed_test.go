package quellog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestPgBouncerRunSurvivesUnrecognizedInput locks the routing contract for a
// mixed invocation: one real pgBouncer log plus a file whose format the
// detector rejects. The unrecognized file is reported on stderr by the
// detector, file by file, and must not take the pooler report down with it —
// the run still exits 0 and emits exactly the report the pgBouncer log alone
// produces.
func TestPgBouncerRunSurvivesUnrecognizedInput(t *testing.T) {
	const fixture = "testdata/regressions/pgbouncer/pgbouncer.log"

	want, err := os.ReadFile(goldenPath(fixture, "json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	junk := filepath.Join(t.TempDir(), "notalog.txt")
	if err := os.WriteFile(junk, []byte("hello world\nnothing to see here\n"), 0o644); err != nil {
		t.Fatalf("write junk file: %v", err)
	}

	got := runHarness(t, false, fixture, junk, "--json")
	if !bytes.Equal(got, want) {
		t.Errorf("mixed run diverges from the pgBouncer-only golden\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
