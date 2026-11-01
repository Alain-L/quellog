package parser

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// bothPaths are the two stderr parse paths: parseReader (CLI) and
// parseFromBytes (WASM).
var bothPaths = []string{"reader", "bytes"}

// parseWith parses data with p through parseReader ("reader") or
// parseFromBytes ("bytes") and returns every entry.
func parseWith(t *testing.T, p *StderrParser, path, data string) []LogEntry {
	t.Helper()
	out := make(chan []LogEntry, 64)
	var err error
	if path == "reader" {
		err = p.parseReader(strings.NewReader(data), out)
	} else {
		err = p.parseFromBytes([]byte(data), out)
	}
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	return drainEntries(out)
}

// parseBothWays is parseWith for a default stderr parser.
func parseBothWays(t *testing.T, path, data string) []LogEntry {
	t.Helper()
	return parseWith(t, &StderrParser{}, path, data)
}

// drainEntries collects every LogEntry sent on out until it is closed.
func drainEntries(out chan []LogEntry) []LogEntry {
	var entries []LogEntry
	for batch := range out {
		entries = append(entries, batch...)
	}
	return entries
}

// TestStderrParser_PgBackRestCarveOut pins that embedded console and
// bare-stderr lines become their own entries, the bare block one entry
// without a process marker, and the PostgreSQL entries around stay whole.
func TestStderrParser_PgBackRestCarveOut(t *testing.T) {
	input := "2026-01-14 08:59:55.000 UTC  [1200]  LOG:  database system is ready to accept connections\n" +
		"2026-01-14 09:00:05.321 P00   INFO: archive-push command begin 2.59.3: --stanza=demo\n" +
		"2026-01-14 09:00:05.900 P00   INFO: pushed WAL file '000000010000000000000009' to the archive\n" +
		"2026-01-14 09:00:05.901 P00   INFO: archive-push command end: completed successfully (580ms)\n" +
		"ERROR: [103]: unable to find a valid repository:\n" +
		"       repo1: unable to open archive.info\n" +
		"2026-01-14 13:07:00.664 UTC  [29455]  LOG:  archive command failed with exit code 103\n"
	var pg int
	var pgbr []string
	for _, e := range parseBothWays(t, "reader", input) {
		if !e.FromPgBackRest {
			pg++
			continue
		}
		pgbr = append(pgbr, e.PID+" "+e.Message)
	}
	want := []string{
		"P00 INFO: archive-push command begin 2.59.3: --stanza=demo",
		"P00 INFO: pushed WAL file '000000010000000000000009' to the archive",
		"P00 INFO: archive-push command end: completed successfully (580ms)",
		" ERROR: [103]: unable to find a valid repository:\nrepo1: unable to open archive.info",
	}
	if pg != 2 || !slices.Equal(pgbr, want) {
		t.Errorf("PostgreSQL entries %d, pgBackRest entries %q; want 2 and %q", pg, pgbr, want)
	}
}

// TestStderrParser_NothingCarved pins inputs where no line is
// pgBackRest's: PostgreSQL's own severities, a signal line after a
// PostgreSQL line, and held lines a DETAIL gives to another program,
// signal lines or a "terminated by signal" report notwithstanding.
func TestStderrParser_NothingCarved(t *testing.T) {
	const pg = "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n"
	const custom = "ERROR: [042]: custom archive script could not reach NFS share\n"
	const fail = "2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 42\n"
	const detail = "2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: /usr/local/bin/my-archiver pg_wal/000000010000000000000003\n"
	for _, c := range []struct {
		name, input, first string
		n                  int // entries, when not 0
	}{
		{"PostgreSQL severities", "2026-01-14 09:00:00.000 UTC  [1300]  ERROR:  relation \"t\" does not exist\n" +
			"2026-01-14 09:00:00.000 UTC  [1300]  DETAIL:  nothing to see here\n" +
			"2026-01-14 09:00:01.000 UTC  [1301]  WARNING:  some warning\n" +
			"2026-01-14 09:00:02.000 UTC  [1302]  INFO:  some info\n", "", 0},
		{"signal line after a PostgreSQL line", "2026-10-10 13:00:00.000 UTC [100] LOG:  statement: SELECT 1\n" +
			"terminated on signal SIGTERM\n" + pg, "SELECT 1 terminated on signal SIGTERM", 2},
		{"signal line after held text", pg + custom + "some text the script wrote\nterminated on signal SIGTERM\n" + fail + detail, "", 0},
		{"signal line before a foreign DETAIL", pg + custom + fail + "terminated on signal SIGTERM\n" + detail, "ERROR: [042]: custom archive script", 0},
		{"terminated by signal", pg + custom + "2026-10-10 13:00:01.000 UTC [90] LOG:  archive command was terminated by signal 9: Killed\n" + detail, "", 0},
	} {
		for _, path := range bothPaths {
			entries := parseBothWays(t, path, c.input)
			for _, e := range entries {
				if e.FromPgBackRest {
					t.Errorf("%s/%s: carved %+v", c.name, path, e)
				}
			}
			if (c.first != "" && (len(entries) == 0 || !strings.Contains(entries[0].Message, c.first))) || (c.n != 0 && len(entries) != c.n) {
				t.Errorf("%s/%s: got %+v, want %q kept in the first PostgreSQL entry", c.name, path, entries, c.first)
			}
		}
	}
}

// TestPgBackRestBareHeaderMatch_Forms pins the bare header grammar:
// "LEVEL: [NNN]: " for every level pgBackRest codes, nothing looser; the
// codeless form is PostgreSQL text.
func TestPgBackRestBareHeaderMatch_Forms(t *testing.T) {
	for line, want := range map[string]bool{
		"ERROR: [082]: text":                  true,
		"WARN: [104]: text":                   true,
		"INFO: [001]: text":                   true,
		"DETAIL: [123]: text":                 true,
		"ERROR: [082] text":                   false,
		"ERROR: [082]:text":                   false,
		"ERROR: [82]: text":                   false,
		"DEBUG: [123]: text":                  false,
		`INFO: syntax error at or near "foo"`: false,
		`ERROR: relation "t" does not exist`:  false,
		`WARN: deprecated option used`:        false,
		`DETAIL: nothing to see here`:         false,
	} {
		if got := pgbackrestBareHeaderMatch([]byte(line)); got != want {
			t.Errorf("pgbackrestBareHeaderMatch(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestStderrParser_BareEntryDating pins the date of an undated pgBackRest
// entry: the next dated line (PostgreSQL's or a pgBackRest header), else
// the previous one, else none; a console header keeps its own.
func TestStderrParser_BareEntryDating(t *testing.T) {
	const bare046 = "ERROR: [046]: unexpected control version = 1700 and catalog version = 202406281\n"
	for _, c := range []struct {
		name, input, prefix string
		want                time.Time
		entries, index      int // when not 0 / -1
	}{
		{"previous pgBackRest header", "2026-01-14 09:00:05.321 P00   INFO: archive-push command begin 2.59.3: --stanza=demo\n" +
			"ERROR: [103]: unable to find a valid repository:\n", "ERROR: [103]", time.Date(2026, 1, 14, 9, 0, 5, 321e6, time.UTC), 0, -1},
		{"nothing dated", "ERROR: [103]: unable to find a valid repository:\n", "ERROR: [103]", time.Time{}, 1, -1},
		{"previous PostgreSQL line", "2026-01-14 09:00:05.321 UTC [1200] LOG:  archive command failed with exit code 46\n" +
			"P00   INFO: archive-push command begin 2.48: [pg_wal/000000010000000000000016] --stanza=main\n" + bare046 +
			"       HINT: is this version of PostgreSQL supported?\n", "ERROR: [046]", time.Date(2026, 1, 14, 9, 0, 5, 321e6, time.UTC), 0, -1},
		{"nothing dated, with a continuation", bare046 + "       HINT: is this version of PostgreSQL supported?\n", "ERROR: [046]", time.Time{}, 1, -1},
		{"next PostgreSQL line", "2026-01-14 09:00:01.000 UTC [1200] LOG:  checkpoint starting: time\n" + bare046 +
			"2026-01-14 09:05:00.000 UTC [1200] LOG:  archive command failed with exit code 46\n", "ERROR: [046]", time.Date(2026, 1, 14, 9, 5, 0, 0, time.UTC), 0, -1},
		{"console header keeps its own", "2026-01-14 09:00:01.000 P00   INFO: archive-push command begin 2.59.3: --stanza=demo\n" +
			"2026-01-14 09:05:00.000 UTC [1200] LOG:  checkpoint starting: time\n", "INFO: archive-push", time.Date(2026, 1, 14, 9, 0, 1, 0, time.UTC), 0, -1},
		{"next pgBackRest header", "2026-01-14 09:00:01.000 UTC [1200] LOG:  checkpoint starting: time\n" +
			"ERROR: [103]: unable to find a valid repository\n" +
			"2026-01-14 09:05:00.000 P00   INFO: archive-push command end: aborted with exception [103]\n", "ERROR: [103]", time.Date(2026, 1, 14, 9, 5, 0, 0, time.UTC), 3, 1},
	} {
		for _, path := range bothPaths {
			entries := parseBothWays(t, path, c.input)
			i := slices.IndexFunc(entries, func(e LogEntry) bool { return e.FromPgBackRest && strings.HasPrefix(e.Message, c.prefix) })
			if i < 0 || (c.entries != 0 && len(entries) != c.entries) || (c.index >= 0 && i != c.index) {
				t.Errorf("%s/%s: got %+v, want the %q pgBackRest entry", c.name, path, entries, c.prefix)
			} else if !entries[i].Timestamp.Equal(c.want) {
				t.Errorf("%s/%s: dated %v, want %v", c.name, path, entries[i].Timestamp, c.want)
			}
		}
	}
}

// TestStderrParser_PgBackRestCarveOut_FileBytesParity pins that both
// parse paths carve the same 12 entries out of the embedded fixture.
func TestStderrParser_PgBackRestCarveOut_FileBytesParity(t *testing.T) {
	data, err := os.ReadFile("../test/testdata/regressions/pgbackrest/postgresql-pgbackrest.log")
	if err != nil {
		t.Fatal(err)
	}
	fileEntries, bytesEntries := parseBothWays(t, "reader", string(data)), parseBothWays(t, "bytes", string(data))
	sameEntries(t, "bytes path", fileEntries, bytesEntries)
	pgbr := 0
	for _, e := range fileEntries {
		if e.FromPgBackRest {
			pgbr++
		}
	}
	// begin/pushed/end of 3 archive-push runs, plus the two bare [103] blocks.
	if pgbr != 12 {
		t.Errorf("pgBackRest entries = %d, want 12", pgbr)
	}
}

// TestPgBackRestConsoleHeaderMatch_RejectsPostgreSQLLinesWithoutAllocating
// pins the byte gate: a PostgreSQL line whose zone starts with "P" is
// rejected without allocating; a real console header matches.
func TestPgBackRestConsoleHeaderMatch_RejectsPostgreSQLLinesWithoutAllocating(t *testing.T) {
	for _, line := range []string{
		"2026-10-10 13:05:16.739 PST [4242] LOG:  checkpoint starting: time",
		"2026-10-10 13:05:16.739 PDT [4242] ERROR:  relation \"t\" does not exist",
		"2026-10-10 13:05:16 PST [4242] LOG:  checkpoint starting: time",
		"2026-10-10 13:05:16.739 UTC [4242] LOG:  checkpoint starting: time",
		"2026-10-10 13:05:16.739 P0 [4242] LOG:  short",
	} {
		b := []byte(line)
		if _, ok := pgbackrestConsoleHeaderMatch(b); ok {
			t.Errorf("PostgreSQL line matched as a pgBackRest header: %q", line)
		}
		if allocs := testing.AllocsPerRun(100, func() { pgbackrestConsoleHeaderMatch(b) }); allocs != 0 {
			t.Errorf("%q: %v allocations per call, want 0", line, allocs)
		}
	}
	for _, line := range []string{
		"2026-10-10 13:05:16.739 P00   INFO: backup command begin 2.59.3: --stanza=main",
		"2026-10-10 13:05:16.739 P123 ERROR: [103]: unable to find a valid repository",
	} {
		if _, ok := pgbackrestConsoleHeaderMatch([]byte(line)); !ok {
			t.Errorf("pgBackRest header not matched: %q", line)
		}
	}
}

// TestStderrParser_CarvesConcatenatedPgBackRestLog pins that a pgBackRest
// log after a PostgreSQL log in one stream is carved whole (banner,
// signal, log-timestamp=n header, blank line), every line accounted for.
func TestStderrParser_CarvesConcatenatedPgBackRestLog(t *testing.T) {
	data := "2026-10-10 13:00:00.000 UTC [100] LOG:  database system is ready to accept connections\n" +
		pgBackRestBanner + "\n" +
		"2026-10-10 13:00:01.000 P00   INFO: backup command begin 2.59.3: --stanza=main --type=full\n" +
		"terminated on signal SIGTERM\n" +
		"\n" +
		pgBackRestBanner + "\n" +
		"P00   INFO: check command begin 2.59.3: --stanza=main\n" +
		"P00   INFO: check command end: completed successfully\n"
	for _, path := range bothPaths {
		var pg, banners, signals, blanks, lines int
		var messages []string
		for _, e := range parseBothWays(t, path, data) {
			switch {
			case !e.FromPgBackRest:
				pg++
			case e.PgBackRest == nil:
				messages = append(messages, e.Message)
				lines += 1 + strings.Count(e.Message, "\n")
				if strings.HasPrefix(e.Message, "INFO: check") && (!e.Timestamp.IsZero() || e.PID != "P00") {
					t.Errorf("%s: log-timestamp=n line: got timestamp %v, PID %q", path, e.Timestamp, e.PID)
				}
			case e.PgBackRest.ProcessStart:
				banners++
				lines++
			case e.PgBackRest.Signal == "SIGTERM":
				signals++
				lines++
			case e.PgBackRest.BlankLines > 0:
				blanks += e.PgBackRest.BlankLines
				lines += e.PgBackRest.BlankLines
			}
		}
		if pg != 1 || banners != 2 || signals != 1 || blanks != 1 || len(messages) != 3 || lines != 7 {
			t.Errorf("%s: pg=%d banners=%d signals=%d blanks=%d messages=%q lines=%d, want 1, 2, 1, 1, 3 messages, 7 lines", path, pg, banners, signals, blanks, messages, lines)
		}
	}
}

// TestStderrParser_BareLinesOfAnotherArchiver pins that bare lines stay
// in the PostgreSQL entry they follow when the failure's DETAIL names
// another program, and are pgBackRest's, dated by the failure line, when
// it names pgbackrest.
func TestStderrParser_BareLinesOfAnotherArchiver(t *testing.T) {
	stream := func(cmd string) string {
		return "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n" +
			"ERROR: [042]: custom archive script could not reach NFS share\n" +
			"       HINT: check the mount\n" +
			"2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 42\n" +
			"2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: " + cmd + "\n" +
			"2026-10-10 13:00:02.000 UTC [100] LOG:  checkpoint complete\n"
	}
	for _, path := range bothPaths {
		other := parseBothWays(t, path, stream("/usr/local/bin/my-archiver pg_wal/000000010000000000000003"))
		if len(other) != 4 || slices.ContainsFunc(other, func(e LogEntry) bool { return e.FromPgBackRest }) ||
			!strings.HasSuffix(other[0].Message, "checkpoint starting: time ERROR: [042]: custom archive script could not reach NFS share HINT: check the mount") {
			t.Errorf("%s: another archiver: got %+v, want 4 PostgreSQL entries, the held lines back in the first", path, other)
		}
		mine := parseBothWays(t, path, stream("pgbackrest --stanza=main archive-push pg_wal/000000010000000000000003"))
		if len(mine) != 5 || !strings.HasSuffix(mine[0].Message, "checkpoint starting: time") || !mine[1].FromPgBackRest ||
			mine[1].Message != "ERROR: [042]: custom archive script could not reach NFS share\nHINT: check the mount" ||
			!mine[1].Timestamp.Equal(time.Date(2026, 10, 10, 13, 0, 1, 0, time.UTC)) ||
			slices.ContainsFunc(mine[2:], func(e LogEntry) bool { return e.FromPgBackRest }) {
			t.Errorf("%s: pgBackRest archiver: got %+v, want the PostgreSQL entry, then the block dated by the failure line", path, mine)
		}
	}
}

// TestStderrParser_BareWarnLine pins that the bare form is read for WARN
// as for ERROR, and that without evidence (here a command begin line)
// the block stays in the PostgreSQL entry it follows.
func TestStderrParser_BareWarnLine(t *testing.T) {
	const pg = "2026-01-14 09:00:01.000 UTC [1200] LOG:  checkpoint starting: time\n"
	const block = "WARN: [104]: archive-push command encountered error(s):\n" +
		"       repo2: [FileOpenError] unable to open file\n" +
		"2026-01-14 09:00:02.000 UTC [90] LOG:  checkpoint complete\n"
	const begin = "P00   INFO: archive-push command begin 2.59.3: [pg_wal/000000010000000000000003] --stanza=main\n"
	for _, path := range bothPaths {
		entries := parseBothWays(t, path, pg+begin+block)
		if len(entries) != 4 || !entries[2].FromPgBackRest ||
			entries[2].Message != "WARN: [104]: archive-push command encountered error(s):\nrepo2: [FileOpenError] unable to open file" {
			t.Errorf("%s: got %+v, want the WARN block carved out after the begin line", path, entries)
		}
		entries = parseBothWays(t, path, pg+block)
		if len(entries) != 2 || entries[0].FromPgBackRest ||
			!strings.HasSuffix(entries[0].Message, "time WARN: [104]: archive-push command encountered error(s): repo2: [FileOpenError] unable to open file") {
			t.Errorf("%s: got %+v, want the unconfirmed block glued to the PostgreSQL entry", path, entries)
		}
	}
}

// TestStderrParser_SQLLookalikeLinesStayPostgreSQL pins that undated
// pgBackRest-shaped lines followed by a raw line continuing the pending
// PostgreSQL entry were that entry's text, glued back to it.
func TestStderrParser_SQLLookalikeLinesStayPostgreSQL(t *testing.T) {
	const head = "2025-01-01 10:00:00.000 UTC [100] LOG:  duration: 5.000 ms  statement: "
	const next = "2025-01-01 10:00:01.000 UTC [100] LOG:  duration: 7.000 ms  statement: SELECT 1\n"
	for name, tc := range map[string]struct{ in, want string }{
		"single quotes": {
			"INSERT INTO msgs VALUES ('line one\nERROR: [082]: not really pgbackrest\nP00   INFO: backup command begin 2.59.3: --stanza=main\n')\n",
			"INSERT INTO msgs VALUES ('line one ERROR: [082]: not really pgbackrest P00   INFO: backup command begin 2.59.3: --stanza=main ')",
		},
		"banner in a string": {
			"INSERT INTO logs VALUES ('line one\n" + pgBackRestBanner + "\nline three')\n",
			"INSERT INTO logs VALUES ('line one " + pgBackRestBanner + " line three')",
		},
		"dollar quotes":        {"SELECT $$\nP00   WARN: hello\n$$\n", "SELECT $$ P00   WARN: hello $$"},
		"tagged dollar quotes": {"SELECT $q$\nP00   INFO: backup command begin 2.59.3: --stanza=main\n$q$\n", "SELECT $q$ P00   INFO: backup command begin 2.59.3: --stanza=main $q$"},
		"block comment":        {"SELECT 1 /*\nP00   INFO: backup command begin 2.59.3: --stanza=main\n*/\n", "SELECT 1 /* P00   INFO: backup command begin 2.59.3: --stanza=main */"},
		"indented lines and a signal line": {
			"SELECT $$\nERROR: [103]: unable to find a valid repository:\n       repo1: missing\nterminated on signal SIGTERM\n$$\n",
			"SELECT $$ ERROR: [103]: unable to find a valid repository: repo1: missing terminated on signal SIGTERM $$",
		},
	} {
		for _, path := range bothPaths {
			entries := parseBothWays(t, path, head+tc.in+next)
			if len(entries) != 2 || entries[0].FromPgBackRest || entries[1].FromPgBackRest || !strings.HasSuffix(entries[0].Message, tc.want) {
				t.Errorf("%s/%s: got %+v, want the whole statement %q in the first entry", name, path, entries, tc.want)
			}
		}
	}
}

// countCarved counts the PostgreSQL entries, banners, headers starting
// with info and entries equal to bare among entries.
func countCarved(entries []LogEntry, info, bare string) (pg, banners, infos, bares int) {
	for _, e := range entries {
		switch {
		case !e.FromPgBackRest:
			pg++
		case e.PgBackRest != nil && e.PgBackRest.ProcessStart:
			banners++
		case strings.HasPrefix(e.Message, info):
			infos++
		case e.Message == bare:
			bares++
		}
	}
	return
}

// TestStderrParser_UndatedBlockBeforeDatedLineIsCarved pins that undated
// pgBackRest lines after a PostgreSQL entry, then a dated line or the
// end of input, are pgBackRest's, whatever quotes the entry holds; and so
// behind a literal line prefix.
func TestStderrParser_UndatedBlockBeforeDatedLineIsCarved(t *testing.T) {
	const bare = "ERROR: [103]: unable to find a valid repository:\nrepo1: [FileOpenError] unable to load info file"
	const pg = "2026-10-10 13:00:00.500 UTC [99] ERROR:  unterminated quoted string at or near \"'abc\" at character 8\n"
	block := pgBackRestBanner + "\n" +
		"P00   INFO: archive-get command begin 2.59.3: [000000010000000000000004, pg_wal/RECOVERYXLOG] --stanza=main\n" +
		"ERROR: [103]: unable to find a valid repository:\n" +
		"       repo1: [FileOpenError] unable to load info file\n"
	for name, tail := range map[string]string{
		"dated line":   "2026-10-10 13:00:01.000 UTC [100] LOG:  checkpoint starting: time\n",
		"end of input": "",
	} {
		for _, path := range bothPaths {
			entries := parseBothWays(t, path, pg+block+tail)
			_, banners, infos, bares := countCarved(entries[1:], "INFO: archive-get command begin", bare)
			if !strings.HasSuffix(entries[0].Message, "at character 8") || entries[0].FromPgBackRest || banners != 1 || infos != 1 || bares != 1 {
				t.Errorf("%s/%s: got %+v, want the PostgreSQL entry apart, then the banner, the header and the bare error carved", name, path, entries)
			}
		}
	}

	const pfx = "pod-0 | "
	data := pfx + "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n" +
		pfx + pgBackRestBanner + "\n" +
		pfx + "P00   INFO: archive-push command begin 2.59.3: [pg_wal/000000010000000000000003] --stanza=main\n" +
		pfx + "ERROR: [103]: unable to find a valid repository:\n" +
		pfx + "       repo1: [FileOpenError] unable to load info file\n" +
		pfx + "2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 103\n" +
		pfx + "2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000003\n"
	for _, path := range bothPaths {
		pg, banners, infos, bares := countCarved(parseWith(t, &StderrParser{prefixLen: len(pfx)}, path, data), "INFO: archive-push command begin", bare)
		if banners != 1 || infos != 1 || bares != 1 || pg != 3 {
			t.Errorf("prefixed/%s: banner %d, header %d, bare error %d, PostgreSQL %d; want 1, 1, 1, 3", path, banners, infos, bares, pg)
		}
	}
}

// TestStderrParser_HeldLinesWithoutDetail pins that held bare lines are
// pgBackRest's when the archiver's failure line has no DETAIL after it.
func TestStderrParser_HeldLinesWithoutDetail(t *testing.T) {
	data := "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n" +
		"ERROR: [103]: unable to find a valid repository\n" +
		"2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 103\n" +
		"2026-10-10 13:00:02.000 UTC [100] LOG:  checkpoint complete\n"
	for _, path := range bothPaths {
		n := 0
		for _, e := range parseBothWays(t, path, data) {
			if e.FromPgBackRest && strings.HasPrefix(e.Message, "ERROR: [103]") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d pgBackRest ERROR entries, want 1", path, n)
		}
	}
}

// TestStderrParser_UnconfirmedLinesParseLikeDev pins that undated
// pgBackRest-shaped lines nothing confirms parse as without pgBackRest
// support: like the same input with every undated line indented.
func TestStderrParser_UnconfirmedLinesParseLikeDev(t *testing.T) {
	const stmt = "2026-01-01 10:00:01.000 UTC [101] LOG:  duration: 12.000 ms  statement: "
	const next = "2026-01-01 10:00:04.000 UTC [100] LOG:  checkpoint starting: time\n"
	cases := map[string]string{
		"indented continuation": stmt + "SELECT 1 /*\nP00   WARN: hello\n\tAND 2 */\n" + next,
		"last line, P00":        stmt + "SELECT 1 -- note\nP00   WARN: hello\n" + next,
		"last line, banner":     stmt + "SELECT 1 -- note\n" + pgBackRestBanner + "\n" + next,
		"last line, bare":       stmt + "SELECT 1 -- note\nERROR: [082]: look-alike\n" + next,
		"last line of input":    stmt + "SELECT 1 -- note\nP00   WARN: hello\n",
		"in a DETAIL value": "2026-01-01 10:00:01.000 UTC [101] ERROR:  duplicate key value violates unique constraint \"t_pkey\"\n" +
			"2026-01-01 10:00:01.000 UTC [101] DETAIL:  Key (msg)=(line one\nP00   WARN: hello\nline three) already exists.\n" + next,
		"after a CONTEXT": "2026-01-01 10:00:01.000 UTC [101] ERROR:  boom\n" +
			"2026-01-01 10:00:01.000 UTC [101] CONTEXT:  PL/pgSQL function f() line 3 at RAISE\n" +
			"ERROR: [103]: unable to find a valid repository:\n" +
			"2026-01-01 10:00:01.000 UTC [101] STATEMENT:  SELECT f()\n" + next,
		"banner and indented tail in a DETAIL": "2026-01-01 10:00:01.000 UTC [101] ERROR:  boom\n" +
			"2026-01-01 10:00:01.000 UTC [101] DETAIL:  text from the user\n" + pgBackRestBanner + "\n\tindented tail\n" +
			"2026-01-01 10:00:01.000 UTC [101] STATEMENT:  SELECT f()\n" + next,
		"tagged dollar quote":   stmt + "CREATE FUNCTION g() RETURNS text AS $body$\nSELECT $q$\nP00   INFO: this is function text\nERROR: [123]: also function text\n$q$\n$body$ LANGUAGE sql\n" + next,
		"comment then indented": "2026-01-01 10:00:03.000 UTC [103] STATEMENT:  SELECT 1 -- comment\nP01 WARN: inside comment block\n\tAND 2\n" + next,
		"quoted identifier":     stmt + "SELECT \"col\nINFO: [001]: quoted ident\n\" FROM t\n" + next,
		"banner after a quote":  stmt + "SELECT 'it''s'\n" + pgBackRestBanner + "\n" + next,
		"bare then blank line":  stmt + "SELECT 1\nDETAIL: [999]: free text in statement continuation\n\n" + next,
		"other archiver": stmt + "SELECT 1\nERROR: [042]: custom script\n" +
			"2026-01-01 10:00:02.000 UTC [90] LOG:  archive command failed with exit code 42\n" +
			"2026-01-01 10:00:02.000 UTC [90] DETAIL:  The failed archive command was: cp pg_wal/1 /mnt/1\n" + next,
	}
	indent := func(in string) string {
		lines := strings.SplitAfter(in, "\n")
		for i, l := range lines {
			if l != "" && l != "\n" && !hasTimestampBytes([]byte(l)) {
				lines[i] = "\t" + l
			}
		}
		return strings.Join(lines, "")
	}
	for name, in := range cases {
		for _, path := range bothPaths {
			sameEntries(t, name+"/"+path, parseBothWays(t, path, indent(in)), parseBothWays(t, path, in))
		}
	}
}

// TestStderrParser_ConfirmedUndatedBlocks pins what confirms undated
// lines held after a PostgreSQL entry: a command begin or end line among
// them, an archiver failure whose DETAIL names pgBackRest (or that has no
// DETAIL), or a dated pgBackRest line right after them.
func TestStderrParser_ConfirmedUndatedBlocks(t *testing.T) {
	const pg = "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n"
	const warn = "P00   WARN: unable to reach repo2\n"
	const fail = "2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 104\n"
	for name, in := range map[string]string{
		"command end":      pg + warn + "P00   INFO: archive-push command end: completed successfully\n",
		"banner and begin": pg + pgBackRestBanner + "\nP00   INFO: archive-push:async command begin 2.59.3: [pg_wal/1] --stanza=main\n" + warn,
		"DETAIL names pgBackRest, any case": pg + warn + fail +
			"2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: /usr/bin/pgBackRest --stanza=main archive-push pg_wal/1\n",
		"failure without DETAIL": pg + warn + fail + pg,
		"dated pgBackRest line":  pg + warn + "2026-10-10 13:00:02.000 P00   INFO: archive-push command end: completed successfully\n",
	} {
		for _, path := range bothPaths {
			found := false
			for _, e := range parseBothWays(t, path, in) {
				found = found || (e.FromPgBackRest && e.Message == "WARN: unable to reach repo2")
				if !e.FromPgBackRest && strings.Contains(e.Message, "repo2") {
					t.Errorf("%s/%s: held line left in %+v", name, path, e)
				}
			}
			if !found {
				t.Errorf("%s/%s: the WARN line was not carved", name, path)
			}
		}
	}
}

// TestStderrParser_UndatedLinesWithNothingPendingAreCarved pins that
// undated lines with no dated PostgreSQL entry before them are carved at
// once, even when a failure naming another program follows.
func TestStderrParser_UndatedLinesWithNothingPendingAreCarved(t *testing.T) {
	data := "P00   INFO: archive-push command begin 2.59.3: [pg_wal/1] --stanza=main\n" +
		"2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 1\n" +
		"2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: cp pg_wal/1 /mnt/1\n"
	for _, path := range bothPaths {
		if entries := parseBothWays(t, path, data); len(entries) != 3 || !entries[0].FromPgBackRest {
			t.Errorf("%s: got %+v, want the begin line carved", path, entries)
		}
	}
}

// TestStderrParser_SignalLineAfterHeldPgBackRestLineStaysHeld pins that a
// signal line right after a held pgBackRest line joins the hold, so a
// DETAIL naming pgbackrest still confirms the lines.
func TestStderrParser_SignalLineAfterHeldPgBackRestLineStaysHeld(t *testing.T) {
	data := "2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time\n" +
		"ERROR: [042]: could not reach repo\n" +
		"terminated on signal SIGTERM\n" +
		"2026-10-10 13:00:01.000 UTC [90] LOG:  archive command failed with exit code 42\n" +
		"2026-10-10 13:00:01.000 UTC [90] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000003\n"
	for _, path := range bothPaths {
		var errLine, signal bool
		for _, e := range parseBothWays(t, path, data) {
			switch {
			case !e.FromPgBackRest:
				if strings.Contains(e.Message, "[042]") || strings.Contains(e.Message, "SIGTERM") {
					t.Errorf("%s: PostgreSQL entry %q holds pgBackRest lines", path, e.Message)
				}
			case e.PgBackRest != nil && e.PgBackRest.Signal == "SIGTERM":
				signal = true
			case strings.HasPrefix(e.Message, "ERROR: [042]"):
				errLine = true
			}
		}
		if !errLine || !signal {
			t.Errorf("%s: pgBackRest error %v, signal %v; want both", path, errLine, signal)
		}
	}
}

// TestStderrParser_BlankLineInsideHeldBlock pins that a blank line inside
// an undated block confirmed by its begin/end lines stays in the block,
// and the PostgreSQL entries keep their own text.
func TestStderrParser_BlankLineInsideHeldBlock(t *testing.T) {
	data := "2025-01-01 10:00:00.000 UTC [100] LOG:  checkpoint starting: time\n" +
		"P00   INFO: backup command begin 2.59.3: --stanza=main --type=full\n" +
		"P00   INFO: new backup label = 20250101-100000F\n" +
		"\n" +
		"P00   INFO: full backup size = 29.4MB, file total = 1000\n" +
		"P00   INFO: backup command end: completed successfully\n" +
		"2025-01-01 10:05:00.000 UTC [100] LOG:  checkpoint complete: wrote 3 buffers\n"
	for _, path := range bothPaths {
		var pg []string
		pgbr := 0
		for _, e := range parseBothWays(t, path, data) {
			if !e.FromPgBackRest {
				pg = append(pg, e.Message)
			} else if strings.Contains(e.Message, "backup command") {
				pgbr++
			}
		}
		want := []string{"[100] LOG:  checkpoint starting: time", "[100] LOG:  checkpoint complete: wrote 3 buffers"}
		if !slices.Equal(pg, want) || pgbr != 2 {
			t.Errorf("%s: PostgreSQL %q and %d backup begin/end lines; want %q and 2", path, pg, pgbr, want)
		}
	}
}
