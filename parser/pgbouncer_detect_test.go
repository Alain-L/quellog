//go:build !js

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

// TestDetectFormatFromContentNamesPgBouncer pins the content-only
// detector the browser build uses. It had no pooler branch, so it
// returned "" and its caller fell back to "stderr": a pooler log dropped
// on the demo page was parsed as a PostgreSQL one and produced a
// plausible, wrong report rather than a refusal.
func TestDetectFormatFromContentNamesPgBouncer(t *testing.T) {
	sample := "2026-09-20 13:58:12.345 CEST [1] LOG stats: 3 xacts/s, 21 queries/s, " +
		"0 client parses/s, 0 server parses/s, 0 binds/s, in 1291 B/s, out 565 B/s, " +
		"xact 1990 us, query 261 us, wait 331 us\n" +
		"2026-09-20 13:58:13.000 CEST [1] LOG C-0x1: app/bob@10.0.0.1:5432 login attempt: db=app user=bob\n"
	if got := DetectFormatFromContent(sample); got != "pgbouncer" {
		t.Errorf("DetectFormatFromContent = %q, want \"pgbouncer\"", got)
	}
	pg := "2026-09-20 13:58:12.345 CEST [1] LOG:  duration: 1.000 ms  statement: SELECT 1;\n"
	if got := DetectFormatFromContent(pg); got != "stderr" {
		t.Errorf("DetectFormatFromContent = %q, want \"stderr\"", got)
	}
}

// TestSampleFormatOrderIsShared pins the one order of discriminators on a
// sample that satisfies both the pooler and the stderr detectors: a
// pooler stats line among PostgreSQL lines, as a log both daemons write
// to gives. The PostgreSQL lines outnumber the pooler one, so it is a
// PostgreSQL log on every path -- by extension, by content, and for an
// archive member. A marker settling it whatever the rest of the sample
// says is the rule this replaces: it carried the whole PostgreSQL log,
// and its report, onto the pooler track.
func TestSampleFormatOrderIsShared(t *testing.T) {
	sample := "2026-09-25 09:00:00.000 UTC [1] LOG stats: 3 xacts/s, 21 queries/s, in 1 B/s, out 1 B/s, xact 1 us, query 1 us, wait 1 us\n"
	for i := 0; i < 5; i++ {
		sample += "2026-09-25 09:00:01.000 UTC [42] LOG:  checkpoint starting: time\n"
	}
	if looksLikePgBouncer(sample) || !isLogContent(sample) {
		t.Fatalf("the sample must be a PostgreSQL log (pooler %v, stderr %v)", looksLikePgBouncer(sample), isLogContent(sample))
	}
	if got := sampleFormat(sample); got != "stderr" {
		t.Errorf("sampleFormat = %q, want stderr", got)
	}
	if _, ok := detectByContent("mixed", sample).(*StderrParser); !ok {
		t.Error("detectByContent does not read the mixed sample as a PostgreSQL log")
	}
	if _, ok := detectByExtension("mixed.log", "log", sample).(*StderrParser); !ok {
		t.Error("detectByExtension does not read the mixed sample as a PostgreSQL log")
	}
	if got := archiveMemberFormat("mixed.log", strings.NewReader(sample)); got != "stderr" {
		t.Errorf("archiveMemberFormat = %q, want stderr", got)
	}
}

// TestDetectStrayPoolerLineInOtherLogs: one pooler line in a log of any
// other shape -- a log_line_prefix that does not open on the timestamp,
// a literal in front, syslog, JSON, the indented lines of a multi-line
// statement or plan -- leaves it the log it is, and its report is not
// lost to the pooler track. A tie goes to the other log; pooler lines in
// the majority make a pooler log.
func TestDetectStrayPoolerLineInOtherLogs(t *testing.T) {
	stray := "2026-09-25 09:00:00.000 UTC [1] LOG stats: 3 xacts/s, 21 queries/s, in 1 B/s, out 1 B/s, xact 1 us, query 1 us, wait 1 us\n"
	for _, c := range []struct{ name, line string }{
		{"angle-bracket prefix", "< 2026-09-25 09:00:01.000 UTC > LOG:  checkpoint starting: time"},
		{"literal prefix", "dbhost: 2026-09-25 09:00:01.000 UTC [42] LOG:  checkpoint starting: time"},
		{"syslog rfc5424", "<134>1 2026-09-25T09:00:01+00:00 host postgres 42 - - [1-1] checkpoint starting: time"},
		{"syslog bsd", "Sep 25 09:00:01 host postgres[42]: [1-1] checkpoint starting: time"},
		{"iso timestamp", "2026-09-25T09:00:01.000Z [42] checkpoint starting: time"},
		{"json", `{"timestamp":"2026-09-25 09:00:01.000 UTC","error_severity":"LOG","message":"checkpoint starting: time"}`},
		{"plan line", "\t  ->  Seq Scan on t  (cost=0.00..35.50 rows=2550 width=4)"},
	} {
		if looksLikePgBouncer(stray + c.line + "\n") {
			t.Errorf("%s: one pooler line against one of this log reads as a pooler log", c.name)
		}
		if !looksLikePgBouncer(stray + stray + c.line + "\n") {
			t.Errorf("%s: two pooler lines against one of this log no longer make a pooler log", c.name)
		}
	}
}

// TestDetectPgBouncerByStatsLineAlone: in a sample too mixed for the shape
// vote, a stats line is a pooler marker on its own, as a socket id is.
func TestDetectPgBouncerByStatsLineAlone(t *testing.T) {
	stats := "2026-09-25 09:00:00.000 UTC [1] LOG stats: 3 xacts/s, 21 queries/s, in 1 B/s, out 1 B/s, xact 1 us, query 1 us, wait 1 us\n"
	sample := stats + "free text\nmore free text\nstill free text\n" + stats
	if !looksLikePgBouncer(sample) {
		t.Error("a stats line alone does not mark the sample as a pooler log")
	}
}

// TestDetectPgBouncerShapeVoteEdges pins the shape vote a marker-free
// sample falls back on: nine prefix-shaped lines in ten carry it, eight in
// ten do not, and a single shaped line is never enough on its own.
func TestDetectPgBouncerShapeVoteEdges(t *testing.T) {
	shaped := "2026-09-25 09:00:00.000 UTC [1] LOG process up: PgBouncer 1.25.2\n"
	sample := func(shapedLines, freeLines int) string {
		return strings.Repeat(shaped, shapedLines) + strings.Repeat("free text\n", freeLines)
	}
	for _, c := range []struct {
		name string
		s    string
		want bool
	}{
		{"nine shaped in ten", sample(9, 1), true},
		{"eight shaped in ten", sample(8, 2), false},
		{"one shaped line alone", sample(1, 0), false},
		{"two shaped lines alone", sample(2, 0), true},
	} {
		if got := looksLikePgBouncer(c.s); got != c.want {
			t.Errorf("%s: looksLikePgBouncer = %v, want %v", c.name, got, c.want)
		}
	}
}
