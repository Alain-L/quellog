package quellog_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestPgBouncerRunSurvivesEmptyPostgreSQLTrack is a refutation test. The
// pooler track already survives an input the detector rejects, but it
// used to die on a second path that no test covered: a filter that
// leaves the PostgreSQL side with nothing. The run aborted on "no log
// entries could be parsed" — a message that was false twice over, since
// the files were readable, in a supported format, and the pooler side
// had parsed every one of its lines.
//
// The two rules — abort when the PostgreSQL track is empty, versus
// abort only when neither track has anything — agree on every input
// where PostgreSQL has entries, which is every other test in this file.
// They separate here.
func TestPgBouncerRunSurvivesEmptyPostgreSQLTrack(t *testing.T) {
	const pgbFixture = "testdata/regressions/pgbouncer/pgbouncer.log"
	const pgFixture = "testdata/stderr.log"

	want, err := os.ReadFile(goldenPath(pgbFixture, "json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	// --dbname matches no PostgreSQL backend in the fixture, so that
	// track aggregates nothing. The pooler track ignores attribute
	// filters by design and is unaffected.
	got := runHarness(t, false, pgFixture, pgbFixture, "--dbname", "nosuchdatabase", "--json")
	if !bytes.Equal(got, want) {
		t.Errorf("a filter that empties the PostgreSQL track must not take the pooler report down\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestRejectedInputReportedOnce: sorting the inputs into tracks runs the
// detector ahead of the parse, which runs it again. A file both passes
// reject was named twice on stderr, where main names it once.
func TestRejectedInputReportedOnce(t *testing.T) {
	junk := filepath.Join(t.TempDir(), "notalog.txt")
	if err := os.WriteFile(junk, []byte("hello world\nnothing to see here\n"), 0o644); err != nil {
		t.Fatalf("write junk file: %v", err)
	}
	_, stderr, _ := runQuellog(t, harnessBinary, "testdata/stderr.log", junk, "--json")
	if n := strings.Count(stderr, "notalog.txt"); n != 1 {
		t.Errorf("rejected file named %d times on stderr, want 1:\n%s", n, stderr)
	}
}

// TestPooledStdinIsNamed: a pooler log piped in cannot reach its analyzer,
// and the run must say that rather than call the format unknown.
func TestPooledStdinIsNamed(t *testing.T) {
	log, err := os.Open("testdata/pgbouncer/P1/p1.pgbouncer.log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(harnessBinary, "-")
	cmd.Stdin = log
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() == nil {
		t.Fatal("a pooler log on stdin must fail the run")
	}
	if !strings.Contains(stderr.String(), "pgBouncer logs cannot be read from standard input") {
		t.Errorf("stderr does not name the pooler log:\n%s", stderr.String())
	}
}
