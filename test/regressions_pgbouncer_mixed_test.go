package quellog_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
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

// TestMixedRunHeaderCountsBothTracks: the run header counts the PostgreSQL
// entries and the pooler records, and the bytes of both files -- P1's
// server log holds 124 entries in 14996 bytes, its pooler log 513 records
// in 73496 bytes.
func TestMixedRunHeaderCountsBothTracks(t *testing.T) {
	out := string(runHarness(t, false, "testdata/pgbouncer/P1/p1.postgresql.log", "testdata/pgbouncer/P1/p1.pgbouncer.log"))
	header, _, _ := strings.Cut(out, "\n")
	for _, want := range []string{"637 entries processed", "(86.42 KB)"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q does not read %q", header, want)
		}
	}
}

// TestPoolerSectionFollowsTheSectionFlags: beside a PostgreSQL report, a
// section flag leaves the pooler section out of text and JSON, as it
// does pgBackRest's and Patroni's, while its entries stay in the header
// count; a pooler-only run keeps its report.
func TestPoolerSectionFollowsTheSectionFlags(t *testing.T) {
	const pg, pooler = "testdata/pgbouncer/P1/p1.postgresql.log", "testdata/pgbouncer/P1/p1.pgbouncer.log"
	has := func(args ...string) bool {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(runHarness(t, false, args...), &doc); err != nil {
			t.Fatalf("%v: invalid JSON: %v", args, err)
		}
		_, ok := doc["pgbouncer"]
		return ok
	}
	if has(pg, pooler, "--summary", "--json") {
		t.Error("--summary beside a PostgreSQL report keeps the pooler section in JSON")
	}
	if !has(pg, pooler, "--json") {
		t.Error("the full report lost its pooler section")
	}
	if !has(pooler, "--summary", "--json") {
		t.Error("--summary on a pooler-only run drops its report")
	}
	out := string(runHarness(t, false, pg, pooler, "--summary"))
	if strings.Contains(out, "PGBOUNCER") {
		t.Error("--summary beside a PostgreSQL report keeps the pooler section in text")
	}
	if header, _, _ := strings.Cut(out, "\n"); !strings.Contains(header, "637 entries processed") {
		t.Errorf("header %q no longer counts the pooler entries", header)
	}
}

// TestPoolerInputsCountOnce: the header counts every input once -- a
// pooler log given twice, an archive read by both tracks -- and a
// pooler-only run states the size of its files.
func TestPoolerInputsCountOnce(t *testing.T) {
	const pg, pooler = "testdata/pgbouncer/P1/p1.postgresql.log", "testdata/pgbouncer/P1/p1.pgbouncer.log"
	header := func(args ...string) string {
		h, _, _ := strings.Cut(string(runHarness(t, false, args...)), "\n")
		return h
	}
	for _, again := range []string{pooler, "./" + pooler} {
		if h := header(pooler, again); !strings.Contains(h, "513 entries processed") || !strings.Contains(h, "(71.77 KB)") {
			t.Errorf("a pooler log given twice (%s): header %q, want 513 entries and 71.77 KB", again, h)
		}
	}
	if h := header(pooler, "--pgbouncer-detail"); !strings.Contains(h, "(71.77 KB)") {
		t.Errorf("--pgbouncer-detail: header %q, want 71.77 KB", h)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []string{pg, pooler} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tw.WriteHeader(&tar.Header{Name: filepath.Base(f), Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write(b)
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "both.tgz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("(%.2f KB)", float64(buf.Len())/1024)
	if h := header(path); !strings.Contains(h, "637 entries processed") || !strings.Contains(h, want) {
		t.Errorf("an archive read by both tracks: header %q, want 637 entries and %s", h, want)
	}
}

// TestPoolerEmptyWindowNamesTheFilters: a time window that leaves no
// pooler entry says the filters may be why, with or without
// --pgbouncer-detail, rather than blaming the file's format.
func TestPoolerEmptyWindowNamesTheFilters(t *testing.T) {
	for _, args := range [][]string{
		{"testdata/pgbouncer/P1/p1.pgbouncer.log", "--begin", "2030-01-01 00:00:00"},
		{"testdata/pgbouncer/P1/p1.pgbouncer.log", "--begin", "2030-01-01 00:00:00", "--pgbouncer-detail"},
	} {
		cmd := exec.Command(harnessBinary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Errorf("%v: exit code 0 for an empty window", args)
		}
		if !strings.Contains(stderr.String(), "time filters match some entries") {
			t.Errorf("%v: the error does not name the time filters:\n%s", args, stderr.String())
		}
	}
}

// TestPoolerMemberErrorIsReported: a pooler member cut short in an
// archive the PostgreSQL track reads too is said once; that track never
// reads the member, and silencing the pooler track lost the error.
func TestPoolerMemberErrorIsReported(t *testing.T) {
	b, err := os.ReadFile("testdata/pgbouncer/P1/p1.pgbouncer.log")
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(b)
	zw.Close()
	cut := gz.Bytes()[:gz.Len()/2]
	for _, members := range [][]string{{"p1.postgresql.log", "cut.log.gz"}, {"cut.log.gz"}} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, name := range members {
			data := cut
			if name != "cut.log.gz" {
				if data, err = os.ReadFile("testdata/pgbouncer/P1/" + name); err != nil {
					t.Fatal(err)
				}
			}
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
			tw.Write(data)
		}
		tw.Close()
		path := filepath.Join(t.TempDir(), "cut.tar")
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(harnessBinary, path)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		cmd.Run()
		if n := strings.Count(stderr.String(), "entry=cut.log.gz"); n != 1 {
			t.Errorf("%v: the cut member is reported %d times, want once:\n%s", members, n, stderr.String())
		}
	}
}
