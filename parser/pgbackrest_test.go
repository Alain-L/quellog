package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLooksLikePgBackRest pins the content detector: pgBackRest's header
// shapes (timestamped, log-timestamp=n, banner) pass; PostgreSQL's
// stderr shape, its severities and text mentioning "INFO:" or "P00" do
// not; the vote needs two lines and 9 shaped lines in 10.
func TestLooksLikePgBackRest(t *testing.T) {
	const header = "2026-01-14 09:00:00.000 P00   INFO: pushed WAL file\n"
	const plain = "an ordinary line with no pgBackRest shape at all\n"
	for name, c := range map[string]struct {
		sample string
		want   bool
	}{
		"timestamped": {"2026-01-10 02:00:01.123 P00   INFO: backup command begin 2.59.3: --stanza=demo\n" +
			"2026-01-10 02:00:47.506 P00   INFO: backup command end: completed successfully (46383ms)\n", true},
		"no-timestamp": {"P00   INFO: backup command begin 2.59.3: --stanza=demo\n" +
			"P00   INFO: backup command end: completed successfully\n", true},
		"banner-settles-it": {pgBackRestBanner + "\n" +
			"2026-01-10 02:00:01.123 P00   INFO: backup command begin 2.59.3: --stanza=demo\n", true},
		"plain-postgres": {"2026-01-14 08:59:55.000 UTC  [1200]  LOG:  database system is ready to accept connections\n" +
			"2026-01-14 09:00:00.000 UTC  [1300]  LOG:  checkpoint starting: time\n", false},
		"postgres-error-detail": {"2026-01-14 09:00:00.000 UTC  [1300]  ERROR:  relation \"t\" does not exist\n" +
			"2026-01-14 09:00:00.000 UTC  [1300]  DETAIL:  nothing to see here\n", false},
		"mentions-info-and-p00-in-text": {"2026-01-14 09:00:00.000 UTC  [1300]  LOG:  statement: SELECT 'P00', 'INFO: test'\n", false},
		"9 of 10 shaped":                {strings.Repeat(header, 9) + plain, true},
		"8 of 10 shaped":                {strings.Repeat(header, 8) + plain + plain, false},
		"one header":                    {header, false},
		"two headers":                   {header + header, true},
	} {
		if got := looksLikePgBackRest(c.sample); got != c.want {
			t.Errorf("%s: looksLikePgBackRest = %v, want %v", name, got, c.want)
		}
	}
}

// TestLooksLikePgBackRest_FixturesNotMisdetected pins that no PostgreSQL
// .log fixture of the repo (pgBackRest directories aside) is detected as
// pgBackRest.
func TestLooksLikePgBackRest_FixturesNotMisdetected(t *testing.T) {
	checked := 0
	err := filepath.WalkDir("../test/testdata", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "pgbackrest" {
			return filepath.SkipDir
		}
		if filepath.Ext(path) != ".log" || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if looksLikePgBackRest(string(data)) {
			t.Errorf("%s detected as pgBackRest", path)
		}
		checked++
		return nil
	})
	if err != nil || checked == 0 {
		t.Fatalf("walk: %v, %d fixtures checked", err, checked)
	}
}

// TestParsePgBackRestHeader pins the byte-level header decoder: pid and
// message with any level padding, the optional timestamp (pgBackRest's
// own logTest.c literal "P44  ERROR: [026]: message" has none), and the
// rejected forms, among them a process marker of 1 or 4 digits.
func TestParsePgBackRestHeader(t *testing.T) {
	for _, c := range []struct {
		line, pid, msg string
		dated          bool
	}{
		{"P44  ERROR: [026]: message", "P44", "ERROR: [026]: message", false},
		{"2026-01-10 02:00:01.123 P00   INFO: pushed WAL file '000000010000000000000002' to the archive", "P00", "INFO: pushed WAL file '000000010000000000000002' to the archive", true},
		{"2026-01-14 09:10:00.400 P01  ERROR: [103]: unable to find a valid repository:", "P01", "ERROR: [103]: unable to find a valid repository:", true},
		{"2026-01-10 02:00:01.456 P00 DETAIL: no prior backup exists", "P00", "DETAIL: no prior backup exists", true},
		{"2026-01-14 09:00:00.000 P123   INFO: text", "P123", "INFO: text", true},
		{"2026-01-14 09:00:00.000 P1234 INFO: text", "", "", false},
		{"2026-01-14 09:00:00.000 P1   INFO: text", "", "", false},
		{"2026-01-10 02:00:01.123 P00 NOTICE: not a pgBackRest level", "", "", false},
		{"2026-01-10 02:00:01.123 P00   INFO no colon", "", "", false},
		{"P00INFO: no space", "", "", false},
	} {
		h, ok := parsePgBackRestHeader(c.line)
		if ok != (c.pid != "") || (ok && (h.pid != c.pid || h.msg != c.msg || h.ts.IsZero() == c.dated)) {
			t.Errorf("parsePgBackRestHeader(%q) = %+v, %v; want pid %q msg %q dated %v", c.line, h, ok, c.pid, c.msg, c.dated)
		}
	}
}

// TestStderrParser_PgBackRestOnlyStream pins a pgBackRest log read alone:
// the banner gives its marker only, a header and its indented lines one
// entry joined by "\n", and no PostgreSQL entry comes out.
func TestStderrParser_PgBackRestOnlyStream(t *testing.T) {
	input := pgBackRestBanner + "\n" +
		"2026-01-14 09:10:00.400 P00  ERROR: [103]: unable to find a valid repository:\n" +
		"                                    repo1: unable to open archive.info\n" +
		"2026-01-14 09:10:00.450 P00   INFO: archive-push command end: aborted with exception [103]\n"
	for _, path := range bothPaths {
		entries := parseBothWays(t, path, input)
		if len(entries) != 3 {
			t.Fatalf("%s: got %d entries, want 3 (the banner marker, then the two lines): %+v", path, len(entries), entries)
		}
		if entries[0].PgBackRest == nil || !entries[0].PgBackRest.ProcessStart || entries[0].Message != "" {
			t.Errorf("%s: entries[0] = %+v, want the banner's marker", path, entries[0])
		}
		want := "ERROR: [103]: unable to find a valid repository:\nrepo1: unable to open archive.info"
		if !entries[1].FromPgBackRest || entries[1].Message != want || entries[1].PID != "P00" ||
			entries[2].Message != "INFO: archive-push command end: aborted with exception [103]" {
			t.Errorf("%s: entries = %+v, want %q from P00, then the end line", path, entries[1:], want)
		}
	}
}

// TestDetection_PgBackRestOnlyContentAccepted pins that detection routes
// a pgBackRest sample, which the PostgreSQL patterns reject, to the
// stderr parser by extension, by content and on stdin.
func TestDetection_PgBackRestOnlyContentAccepted(t *testing.T) {
	pgbr := pgBackRestBanner + "\n" +
		"2026-01-14 09:10:00.400 P00   INFO: backup command begin 2.59.3: --stanza=main\n" +
		"\n" +
		"2026-01-14 09:10:00.450 P00   INFO: backup command end: completed successfully\n"
	noTimestamp := "P00   INFO: check command begin 2.59.3: --stanza=main\n" +
		"P00   INFO: check command end: completed successfully\n"
	for name, sample := range map[string]string{"banner": pgbr, "log-timestamp=n": noTimestamp} {
		if isLogContent(sample) {
			t.Fatalf("%s: sample already matches the PostgreSQL patterns, the case is moot", name)
		}
		_, ext := detectByExtension("main-backup.log", "log", sample).(*StderrParser)
		_, content := detectByContent("main-backup.out", sample).(*StderrParser)
		_, stdin := detectFormatFromSample(sample).(*StderrParser)
		if !ext || !content || !stdin {
			t.Errorf("%s: routed to the stderr parser by extension %v, content %v, stdin %v; want all", name, ext, content, stdin)
		}
	}
	if detectByExtension("notes.log", "log", "just some text\nmore text\n") != nil {
		t.Error("plain text accepted as a log")
	}
}

// TestStanzaHintFromFilename pins pgBackRest's own file name grammar,
// "{stanza|all}-{command}[-{role}-{NNN}].log": the stanza is what precedes
// a known command suffix, so a stanza holding "-" survives.
func TestStanzaHintFromFilename(t *testing.T) {
	cases := map[string]string{
		"/var/log/pgbackrest/demo-backup.log":              "demo",
		"/var/log/pgbackrest/demo-archive-push-async.log":  "demo",
		"/var/log/pgbackrest/all-info.log":                 "",
		"/var/log/pgbackrest/demo-backup-local-001.log.gz": "demo",
		"/var/log/pgbackrest/demo-archive-push.log.zst":    "demo",
		"db-prod-backup.log":                               "db-prod",
		"db-prod-archive-get-async.log":                    "db-prod",
		"my-db-stanza-create.log":                          "my-db",
		"main-backup.log.1":                                "main",
		"main-backup.log-20261010":                         "main",
		"main-expire.log.2.gz":                             "main",
		"db-prod-restore-remote-002.log":                   "db-prod",
		"pgbackrest-all.log":                               "",
		"backups-2026-10.log":                              "",
		"backup.log":                                       "",
		"nodash.log":                                       "",
	}
	for in, want := range cases {
		if got := StanzaHintFromFilename(in); got != want {
			t.Errorf("StanzaHintFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
