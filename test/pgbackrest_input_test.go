// test/pgbackrest_input_test.go
package quellog_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sagaFiles returns a saga's PostgreSQL and pgBackRest log files, sorted.
func sagaFiles(t *testing.T, saga string) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{"pg_logs*/*.log", "pgbackrest_logs/*.log"} {
		m, _ := filepath.Glob(filepath.Join("testdata/pgbackrest", saga, pattern))
		files = append(files, m...)
	}
	slices.Sort(files)
	if len(files) == 0 {
		t.Fatalf("no files for saga %s", saga)
	}
	return files
}

// pgbrWithoutFiles returns doc's "pgbackrest" list without the "files"
// key, which names the packaging, as JSON.
func pgbrWithoutFiles(t *testing.T, doc map[string]any) string {
	t.Helper()
	list, ok := doc["pgbackrest"].([]any)
	if !ok {
		t.Fatalf("missing 'pgbackrest' list: %v", doc)
	}
	for _, s := range list {
		delete(s.(map[string]any), "files")
	}
	return jsonOf(list)
}

// runJSONWithStdin runs the harness on stdin data with --json.
func runJSONWithStdin(t *testing.T, data []byte) map[string]any {
	t.Helper()
	out, _ := runWithStdin(t, data, "-", "--json")
	return mustJSON(t, out)
}

// writeArchive packs names/contents into a tar, a tgz or a zip at path.
func writeArchive(t *testing.T, path string, names []string, contents [][]byte) {
	t.Helper()
	var buf bytes.Buffer
	var err error
	if strings.HasSuffix(path, ".zip") {
		zw := zip.NewWriter(&buf)
		for i, name := range names {
			w, _ := zw.Create(name)
			w.Write(contents[i])
		}
		err = zw.Close()
	} else {
		tw := tar.NewWriter(&buf)
		for i, name := range names {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents[i])), Typeflag: tar.TypeReg})
			tw.Write(contents[i])
		}
		err = tw.Close()
	}
	data := buf.Bytes()
	if strings.HasSuffix(path, ".tgz") {
		data = gzipBytes(t, data)
	}
	if err == nil {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestPgBackRest_SameReportWhateverThePackaging pins the one input path:
// a saga's logs give the same pgBackRest report, line counts included, as
// separate files, gzipped one by one, in a tar, a tgz or a zip,
// concatenated, or on stdin.
func TestPgBackRest_SameReportWhateverThePackaging(t *testing.T) {
	for _, saga := range []string{"S1", "S2", "S3", "S5"} {
		t.Run(saga, func(t *testing.T) {
			files := sagaFiles(t, saga)
			want := pgbrWithoutFiles(t, runJSON(t, files...))
			dir := t.TempDir()
			var names, gzFiles []string
			var contents [][]byte
			for _, f := range files {
				data := mustRead(t, f)
				name := strings.TrimPrefix(f, "testdata/pgbackrest/")
				names, contents = append(names, name), append(contents, data)
				gz := filepath.Join(dir, "gz", name+".gz")
				if err := os.MkdirAll(filepath.Dir(gz), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(gz, gzipBytes(t, data), 0o644); err != nil {
					t.Fatal(err)
				}
				gzFiles = append(gzFiles, gz)
			}
			concat := slices.Concat(contents...)
			variants := map[string]map[string]any{"gzip": runJSON(t, gzFiles...), "stdin": runJSONWithStdin(t, concat)}
			for _, ext := range []string{"tar", "tgz", "zip"} {
				p := filepath.Join(dir, saga+"."+ext)
				writeArchive(t, p, names, contents)
				variants[ext] = runFixtureJSON(t, p)
			}
			cat := filepath.Join(dir, "all.log")
			if err := os.WriteFile(cat, concat, 0o644); err != nil {
				t.Fatal(err)
			}
			variants["concatenated"] = runFixtureJSON(t, cat)
			for name, doc := range variants {
				if got := pgbrWithoutFiles(t, doc); got != want {
					t.Errorf("%s: pgBackRest report differs from the separate files'\nwant %s\ngot  %s", name, want, got)
				}
			}
		})
	}
}

// pgbackrestHead builds n pgBackRest console lines of archive-push runs
// starting at 2025-11-30 18:00, one run (three lines) per minute.
func pgbackrestHead(n int) []byte {
	var b bytes.Buffer
	for i := 0; i*3 < n; i++ {
		ts := fmt.Sprintf("2025-11-30 %02d:%02d:00", 18+i/60, i%60)
		seg := fmt.Sprintf("0000000100000000000000%02X", i%256)
		fmt.Fprintf(&b, "%s.100 P00   INFO: archive-push command begin 2.59.3: [pg_wal/%s] --stanza=main\n", ts, seg)
		fmt.Fprintf(&b, "%s.150 P00   INFO: pushed WAL file '%s' to the archive\n", ts, seg)
		fmt.Fprintf(&b, "%s.151 P00   INFO: archive-push command end: completed successfully (51ms)\n", ts)
	}
	return b.Bytes()
}

// TestPgBackRest_PgBackRestHeavyHeadKeepsPostgreSQLReport pins that a
// PostgreSQL log whose first 32 KB are almost all pgBackRest lines keeps
// its whole PostgreSQL report, plain, gzipped or archived.
func TestPgBackRest_PgBackRestHeavyHeadKeepsPostgreSQLReport(t *testing.T) {
	want := runFixtureJSON(t, "testdata/stderr.log")["summary"].(map[string]any)["total_logs"].(float64) + 1
	first := []byte("2025-11-30 17:59:00.000 UTC [55] 00000: db=,user=,app=,client= LOG:  checkpoint starting: time\n")
	data := slices.Concat(first, pgbackrestHead(600), mustRead(t, "testdata/stderr.log"))
	dir := t.TempDir()
	plain, gz := filepath.Join(dir, "postgresql.log"), filepath.Join(dir, "postgresql.log.gz")
	if err := os.WriteFile(plain, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gz, gzipBytes(t, data), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := []string{plain, gz}
	for _, ext := range []string{"tar", "zip"} {
		p := filepath.Join(dir, "logs."+ext)
		writeArchive(t, p, []string{"postgresql.log"}, [][]byte{data})
		inputs = append(inputs, p)
	}
	for _, in := range inputs {
		doc := runFixtureJSON(t, in)
		summary, _ := doc["summary"].(map[string]any)
		if summary == nil || summary["total_logs"] != want {
			t.Errorf("%s: summary %v, want total_logs %v (stderr.log's plus the first line)", filepath.Base(in), summary, want)
		}
		if main := pgbrStanzaByName(t, doc, "main"); main["lines_read"] != float64(600) {
			t.Errorf("%s: pgBackRest lines_read = %v, want 600", filepath.Base(in), main["lines_read"])
		}
	}
}

// TestPgBackRest_ScopeOptionsLeaveSectionOut pins that pgBackRest, with
// no database, user or application, leaves the report under an attribute
// filter or a section option, with the exit code of a run without it.
func TestPgBackRest_ScopeOptionsLeaveSectionOut(t *testing.T) {
	const pgLog = "testdata/pgbackrest/S1/pg_logs_main/postgresql.log"
	for _, args := range [][]string{{"--checkpoints"}, {"--sql-summary"}, {"--exclude-user", "nobody"}} {
		if doc := runJSON(t, append([]string{pgLog}, args...)...); doc["pgbackrest"] != nil {
			t.Errorf("%v: pgbackrest key present", args)
		}
	}
	if runJSON(t, pgLog)["pgbackrest"] == nil {
		t.Fatal("no pgbackrest key on the full report: the case is moot")
	}

	// With --dbname, the report is the one the PostgreSQL lines alone give.
	mixed := filepath.Join(t.TempDir(), "postgresql.log")
	if err := os.WriteFile(mixed, slices.Concat(pgbackrestHead(30), mustRead(t, "testdata/stderr.log")), 0o644); err != nil {
		t.Fatal(err)
	}
	want := runHarness(t, false, "testdata/stderr.log", "--dbname", "postgres", "--json")
	if got := runHarness(t, false, mixed, "--dbname", "postgres", "--json"); !bytes.Equal(got, want) {
		t.Errorf("--dbname: report differs from the PostgreSQL lines alone'\nwant %s\ngot  %s", want, got)
	}

	for _, args := range [][]string{
		{pgLog, "--dbname", "nosuchdb"},
		{"testdata/pgbackrest/S1/pgbackrest_logs", "--checkpoints"},
		{"testdata/pgbackrest/S1/pgbackrest_logs", "--sql-summary"},
		{"testdata/pgbackrest/S1/pgbackrest_logs", "--appname", "psql"},
	} {
		if _, stderr, code := runQuellog(t, harnessBinary, args...); code == 0 || !strings.Contains(stderr, "no PostgreSQL log entries to analyze") ||
			!strings.Contains(stderr, "pgBackRest lines read are reported only without") {
			t.Errorf("%v: exit %d stderr=%q, want a non-zero exit saying where the pgBackRest report went", args, code, stderr)
		}
	}
}

// TestPgBackRest_OnlyRunWritesEachExportFormat pins that several export
// formats on a pgBackRest-only run write one file per format.
func TestPgBackRest_OnlyRunWritesEachExportFormat(t *testing.T) {
	fixture, _ := filepath.Abs("testdata/pgbackrest/S5/pgbackrest_logs")
	cmd := exec.Command(harnessBinary, fixture, "--json", "--md")
	cmd.Dir = t.TempDir()
	if _, stderr, err := runCmd(cmd); err != nil {
		t.Fatalf("--json --md: %v\nstderr: %s", err, stderr)
	}
	js := mustRead(t, filepath.Join(cmd.Dir, "quellog-pgbackrest_logs.json"))
	if keys := slices.Sorted(maps.Keys(mustJSON(t, js))); !slices.Equal(keys, []string{"pgbackrest"}) {
		t.Errorf("JSON file is not the pgBackRest-only report: %v", keys)
	}
	if md := mustRead(t, filepath.Join(cmd.Dir, "quellog-pgbackrest_logs.md")); !bytes.Contains(md, []byte("## PGBACKREST")) {
		t.Error("Markdown file is not the pgBackRest report")
	}
}

// TestPgBackRest_NoTimestampNoteWhateverThePackaging pins that the
// log-timestamp=n note names the file or archive member, and says "some
// lines" when the source is not known.
func TestPgBackRest_NoTimestampNoteWhateverThePackaging(t *testing.T) {
	const dir = "testdata/pgbackrest/S4/02_timestamp/"
	files := []string{dir + "check_console_default.out", dir + "check_console_no_timestamp.out"}
	contents := [][]byte{mustRead(t, files[0]), mustRead(t, files[1])}
	tarPath := filepath.Join(t.TempDir(), "s4.tar")
	writeArchive(t, tarPath, []string{filepath.Base(files[0]), filepath.Base(files[1])}, contents)
	for name, c := range map[string]struct {
		doc  map[string]any
		want string
	}{
		"files": {runJSON(t, files...), "check_console_no_timestamp.out carries no timestamps"},
		"tar":   {runFixtureJSON(t, tarPath), "check_console_no_timestamp.out carries no timestamps"},
		"stdin": {runJSONWithStdin(t, slices.Concat(contents...)), "some lines carry no timestamps"},
	} {
		note := ""
		for _, n := range pgbrStanzaByName(t, c.doc, "main")["missing_info"].([]any) {
			if s := n.(string); strings.HasPrefix(s, "log-timestamp=n") {
				note = s
			}
		}
		if !strings.Contains(note, c.want) {
			t.Errorf("%s: note = %q, want %q", name, note, c.want)
		}
	}
}

// TestPgBackRest_StdinMatchesFile pins that a pgBackRest log piped on
// stdin gives the report the same file gives.
func TestPgBackRest_StdinMatchesFile(t *testing.T) {
	const fixture = "testdata/pgbackrest/S1/pgbackrest_logs/main-backup.log"
	if got, want := pgbrWithoutFiles(t, runJSONWithStdin(t, mustRead(t, fixture))), pgbrWithoutFiles(t, runFixtureJSON(t, fixture)); got != want {
		t.Errorf("stdin report differs from the file's\nwant %s\ngot  %s", want, got)
	}
}

// TestPgBackRest_SplitPeriodsArePostgreSQLOnly pins that pgBackRest lines
// open no --split period, and an input with nothing else ends in the
// error a run without them gives.
func TestPgBackRest_SplitPeriodsArePostgreSQLOnly(t *testing.T) {
	dir := t.TempDir()
	out, _, code := runQuellog(t, harnessBinary, "testdata/pgbackrest/S2/pg_logs_main/postgresql.log", "--split", "1m", "--html", "-o", filepath.Join(dir, "pg.html"))
	if code != 0 || !strings.Contains(out, "(8 periods)") {
		t.Errorf("PostgreSQL log with pgBackRest lines: exit %d out=%q, want the 8 periods of its PostgreSQL entries", code, out)
	}
	_, stderr, code := runQuellog(t, harnessBinary, "testdata/pgbackrest/S2/pgbackrest_logs", "--split", "1m", "--html", "-o", filepath.Join(dir, "pgbr.html"))
	if code == 0 || !strings.Contains(stderr, "no timestamped log entries to split into periods") {
		t.Errorf("pgBackRest logs only: exit %d stderr=%q, want the no-periods error", code, stderr)
	}
}

// TestPgBackRest_TimeWindow pins --begin/--end end to end: what was cut
// is seen (signals, files, lines inside the window), and a backup running
// at --end is listed as still running, neither succeeded nor failed.
func TestPgBackRest_TimeWindow(t *testing.T) {
	main := pgbrStanzaByName(t, runJSON(t, append(sagaFiles(t, "S1"), "--begin", "2026-10-10 13:00:00")...), "main")
	if w := main["window"].(map[string]any); w["cut_before"] != true || w["cut_after"] != false {
		t.Errorf("window = %v, want cut before only", w)
	}
	signals := 0
	for _, b := range main["backups"].([]any) {
		if b.(map[string]any)["signal"] == "SIGTERM" {
			signals++
		}
	}
	files := fmt.Sprint(main["files"])
	if signals != 2 || main["lines_read"] != float64(259) || !strings.Contains(files, "main-backup.log") || !strings.Contains(files, "postgresql.log") {
		t.Errorf("signals=%d lines_read=%v files=%s, want 2 SIGTERM, 259 lines and the backup and PostgreSQL logs", signals, main["lines_read"], files)
	}

	st := runJSON(t, "testdata/pgbackrest/S2/pgbackrest_logs/main-backup.log", "--begin", "2026-10-10 13:34:00", "--end", "2026-10-10 13:34:19")["pgbackrest"].([]any)[0].(map[string]any)
	if backups, _ := st["backups"].([]any); len(backups) != 1 || backups[0].(map[string]any)["in_progress"] != true {
		t.Errorf("backups = %v, want the 13:34:15 run, still running at --end", backups)
	}
	if got := fmt.Sprint(st["commands"]); got != "[map[command:backup failed:0 max_duration_ms:0 runs:1 succeeded:0 total_duration_ms:0]]" {
		t.Errorf("commands = %s, want one backup run, neither succeeded nor failed", got)
	}
}

// runInZones runs the harness with --json under each machine zone.
func runInZones(t *testing.T, args ...string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, zone := range []string{"UTC", "Europe/Paris", "America/New_York"} {
		cmd := exec.Command(harnessBinary, append(slices.Clone(args), "--json")...)
		cmd.Env = append(os.Environ(), "TZ="+zone)
		stdout, stderr, err := runCmd(cmd)
		if err != nil {
			t.Fatalf("TZ=%s: %v\n%s", zone, err, stderr)
		}
		out[zone] = mustJSON(t, []byte(stdout))
	}
	return out
}

// TestPgBackRest_SameReportWhateverTheMachineZone pins that a PostgreSQL
// log written in a named zone gives the same report next to pgBackRest's
// zoneless logs whatever the machine zone, ages included.
func TestPgBackRest_SameReportWhateverTheMachineZone(t *testing.T) {
	dir := t.TempDir()
	pgLog := filepath.Join(dir, "postgresql.log")
	src := mustRead(t, "testdata/pgbackrest/S2/pg_logs_main/postgresql.log")
	if err := os.WriteFile(pgLog, bytes.ReplaceAll(src, []byte(" UTC ["), []byte(" CEST [")), 0o644); err != nil {
		t.Fatal(err)
	}
	pgbr, _ := filepath.Glob("testdata/pgbackrest/S2/pgbackrest_logs/*.log")
	want := pgbrWithoutFiles(t, runInZones(t, sagaFiles(t, "S2")...)["UTC"])
	for zone, doc := range runInZones(t, append([]string{pgLog}, pgbr...)...) {
		if pgbrWithoutFiles(t, doc) != want {
			t.Errorf("TZ=%s: CEST PostgreSQL log gives a different pgBackRest report than the UTC one", zone)
		}
	}

	// The backup ends at 16:00, ten minutes before the PostgreSQL log.
	backup, pg := filepath.Join(dir, "main-backup.log"), filepath.Join(dir, "pg-cest.log")
	if err := os.WriteFile(backup, []byte("2026-10-10 15:55:00.000 P00   INFO: backup command begin 2.59.3: --stanza=main --type=full\n"+
		"2026-10-10 16:00:00.000 P00   INFO: new backup label = 20261010-155500F\n"+
		"2026-10-10 16:00:00.000 P00   INFO: backup command end: completed successfully (300000ms)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pg, []byte("2026-10-10 15:50:00.000 CEST [100] LOG:  database system is ready to accept connections\n"+
		"2026-10-10 16:10:00.000 CEST [101] LOG:  checkpoint starting: time\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for zone, doc := range runInZones(t, pg, backup) {
		if got := pgbrWithoutFiles(t, doc); !strings.Contains(got, `"age_seconds":600`) {
			t.Errorf("TZ=%s: last full = %s, want age_seconds 600", zone, got)
		}
	}
}

// TestPgBackRest_BareBlockDatedByZonedPostgreSQLLine pins that an undated
// pgBackRest block in a CEST PostgreSQL log takes the wall clock of the
// PostgreSQL line after it, whatever the machine zone.
func TestPgBackRest_BareBlockDatedByZonedPostgreSQLLine(t *testing.T) {
	pg := filepath.Join(t.TempDir(), "pg.log")
	if err := os.WriteFile(pg, []byte("2026-10-10 15:50:00.000 CEST [100] LOG:  database system is ready to accept connections\n"+
		"ERROR: [104]: archive-push command encountered error(s):\n"+
		"       repo1: [FileOpenError] unable to open file\n"+
		"2026-10-10 16:00:00.000 CEST [101] LOG:  archive command failed with exit code 104\n"+
		"2026-10-10 16:00:00.000 CEST [101] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000005\n"+
		"2026-10-10 16:10:00.000 CEST [101] LOG:  checkpoint starting: time\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for zone, doc := range runInZones(t, pg) {
		st := doc["pgbackrest"].([]any)[0].(map[string]any)
		errs, _ := st["errors"].([]any)
		if len(errs) != 1 || st["start_date"] != "2026-10-10 16:00:00" || errs[0].(map[string]any)["first_date"] != "2026-10-10 16:00:00" {
			t.Errorf("TZ=%s: start_date=%v errors=%v, want the [104] block at 2026-10-10 16:00:00", zone, st["start_date"], errs)
		}
	}
}
