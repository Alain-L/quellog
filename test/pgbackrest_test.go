// test/pgbackrest_test.go
package quellog_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// elapsedRe matches the wall-clock parse time of the text header.
var elapsedRe = regexp.MustCompile(`processed in [0-9.]+ s`)

// TestPgBackRest_Corpus golden-compares the text, --json and --md output
// of each saga's pgBackRest log directory alone, and the text output of
// the embedded fixture, whose JSON and Markdown TestRegressionCorpus pins.
func TestPgBackRest_Corpus(t *testing.T) {
	const embedded = "testdata/regressions/pgbackrest/postgresql-pgbackrest.log"
	for _, f := range []string{
		"testdata/pgbackrest/S1/pgbackrest_logs",
		"testdata/pgbackrest/S2/pgbackrest_logs",
		"testdata/pgbackrest/S3/pgbackrest_logs",
		"testdata/pgbackrest/S5/pgbackrest_logs",
		"testdata/pgbackrest/S7/pgbackrest_logs",
		embedded,
	} {
		t.Run(f+"/text", func(t *testing.T) {
			got := elapsedRe.ReplaceAll(runHarness(t, false, f), []byte("processed in X s"))
			compareOrUpdateGolden(t, got, goldenPath(f, "txt"))
		})
		if f == embedded {
			continue
		}
		for _, format := range []string{"json", "md"} {
			t.Run(f+"/"+format, func(t *testing.T) {
				compareOrUpdateGolden(t, runHarness(t, false, f, "--"+format), goldenPath(f, format))
			})
		}
	}
}

// pgbrStanzaByName returns the named stanza object from a pgbackrest
// JSON list, failing the test if it is missing.
func pgbrStanzaByName(t *testing.T, got map[string]any, name string) map[string]any {
	t.Helper()
	list, ok := got["pgbackrest"].([]any)
	if !ok {
		t.Fatalf("missing 'pgbackrest' list: %v", got)
	}
	for _, s := range list {
		if stanza, _ := s.(map[string]any); stanza["stanza"] == name {
			return stanza
		}
	}
	t.Fatalf("no stanza %q in %v", name, list)
	return nil
}

// runJSON runs the harness on args with --json.
func runJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	return mustJSON(t, runHarness(t, false, append(slices.Clone(args), "--json")...))
}

// mustRead reads a file, failing the test on error.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// jsonOf marshals v for comparison.
func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// runCmd runs cmd and returns its stdout and stderr.
func runCmd(cmd *exec.Cmd) (stdout, stderr string, err error) {
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

// tarOf packs names/contents into a tar in a temp directory.
func tarOf(t *testing.T, names []string, contents ...[]byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.tar")
	writeArchive(t, p, names, contents)
	return p
}

// TestPgBackRest_S2_PostgreSQLAndPgBackRestLogsTogether pins S2's raw
// counts (MANIFEST.md): the sync failures match PostgreSQL's 27 on main,
// the async worker's 6 stay apart, ghost keeps its 3, one drop seen twice
// counts once.
func TestPgBackRest_S2_PostgreSQLAndPgBackRestLogsTogether(t *testing.T) {
	doc := runJSON(t, "testdata/pgbackrest/S2/pgbackrest_logs", "testdata/pgbackrest/S2/pg_logs_main/postgresql.log")
	main := pgbrStanzaByName(t, doc, "main")
	ap := main["archive_push"].(map[string]any)
	ghost := pgbrStanzaByName(t, doc, "ghost")["archive_push"].(map[string]any)
	got := fmt.Sprintf("failed=%v failed_async=%v pg_failures=%d dropped=%v ghost_failed=%v",
		ap["failed"], ap["failed_async"], len(main["pg_archive_failures"].([]any)), ap["dropped"], ghost["failed"])
	if want := "failed=27 failed_async=6 pg_failures=27 dropped=1 ghost_failed=3"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestPgBackRest_S2_WarningOrderSameConcatenatedOrSeparate pins that
// warnings read in the same order whether S2's two files are given apart
// or concatenated into one.
func TestPgBackRest_S2_WarningOrderSameConcatenatedOrSeparate(t *testing.T) {
	const dir = "testdata/pgbackrest/S2/pgbackrest_logs/"
	files := []string{dir + "main-archive-push-async.log", dir + "main-backup.log"}
	concat := filepath.Join(t.TempDir(), "concat.log")
	if err := os.WriteFile(concat, slices.Concat(mustRead(t, files[0]), mustRead(t, files[1])), 0o644); err != nil {
		t.Fatal(err)
	}
	messages := func(doc map[string]any) []string {
		var out []string
		for _, w := range pgbrStanzaByName(t, doc, "main")["warnings"].([]any) {
			out = append(out, w.(map[string]any)["message"].(string))
		}
		return out
	}
	want, got := messages(runJSON(t, files...)), messages(runJSON(t, concat))
	if len(want) == 0 || !slices.Equal(want, got) {
		t.Errorf("warning order differs:\nseparate:     %v\nconcatenated: %v", want, got)
	}
}

// TestPgBackRest_S1_ChangedToFullNamesRequestedType pins that the
// detailed text report of a backup changed to full names the type asked.
func TestPgBackRest_S1_ChangedToFullNamesRequestedType(t *testing.T) {
	if out := string(runHarness(t, false, "testdata/pgbackrest/S1/pgbackrest_logs", "--pgbackrest-detail=main2")); !strings.Contains(out, "requested incr") {
		t.Errorf("detailed report does not show 'requested incr':\n%s", out)
	}
}

// TestPgBackRest_S4_LogLevelFileWarnLeavesNoBeginEnd pins S4 incident 1:
// an invocation under log-level-file=warn leaves only its banner and
// WARN, and the report says its outcome is missing.
func TestPgBackRest_S4_LogLevelFileWarnLeavesNoBeginEnd(t *testing.T) {
	const f = "testdata/pgbackrest/S4/01_logfile_file/main-backup.combined.log"
	main := pgbrStanzaByName(t, runJSON(t, f), "main")
	backups, _ := main["backups"].([]any)
	warnings, _ := main["warnings"].([]any)
	note := fmt.Sprint(main["missing_info"])
	if len(backups) != 1 || len(warnings) == 0 || !strings.Contains(note, "1 invocation logged") || !strings.Contains(note, "no command begin/end") {
		t.Errorf("backups=%d warnings=%d missing_info=%s; want 1 backup, the retention WARN, the missing begin/end", len(backups), len(warnings), note)
	}
	for _, args := range [][]string{{f}, {f, "--md"}} {
		if out := string(runHarness(t, false, args...)); !strings.Contains(out, "1 run outcome (log-level-file)") {
			t.Errorf("%v: output does not say a run outcome is not in the log", args)
		}
	}
}

// TestPgBackRest_S4_WorkerLinesMakeNoBackup pins S4 incidents 4 and 5:
// per-worker DETAIL lines and a backup:local worker's own begin/end add
// no backup.
func TestPgBackRest_S4_WorkerLinesMakeNoBackup(t *testing.T) {
	const dir = "testdata/pgbackrest/S4/"
	for _, c := range []struct {
		files []string
		want  int
	}{
		{[]string{dir + "04_process_max/main-backup.detail.log"}, 2},
		{[]string{dir + "05_subprocess/local_logs/main-backup.log", dir + "05_subprocess/local_logs/main-backup-local-001.log"}, 3},
	} {
		if backups, _ := pgbrStanzaByName(t, runJSON(t, c.files...), "main")["backups"].([]any); len(backups) != c.want {
			t.Errorf("%v: %d backups, want %d", c.files, len(backups), c.want)
		}
	}
}

// TestPgBackRest_S4_OldVersionBareStderr pins S4 incident 6 (2.48): the
// six bare [046] blocks count, each dated by the PostgreSQL line after it.
func TestPgBackRest_S4_OldVersionBareStderr(t *testing.T) {
	var got []string
	for _, s := range runJSON(t, "testdata/pgbackrest/S4/06_oldversion/old_2.48/pg_log_excerpt.log")["pgbackrest"].([]any) {
		for _, e := range s.(map[string]any)["errors"].([]any) {
			if e := e.(map[string]any); e["code"] == float64(46) {
				got = append(got, fmt.Sprintf("x%v %v..%v", e["count"], e["first_date"], e["last_date"]))
			}
		}
	}
	if want := []string{"x6 2026-10-10 14:01:01..2026-10-10 14:01:37"}; !slices.Equal(got, want) {
		t.Errorf("[046] errors = %q, want %q", got, want)
	}
}

// TestPgBackRest_MultiFileMerge pins that two files of one stanza (S1's
// backup and expire logs) make one stanza holding both.
func TestPgBackRest_MultiFileMerge(t *testing.T) {
	list := runJSON(t, "testdata/pgbackrest/S1/pgbackrest_logs/main-backup.log", "testdata/pgbackrest/S1/pgbackrest_logs/main-expire.log")["pgbackrest"].([]any)
	if len(list) != 1 {
		t.Fatalf("got %d stanzas, want 1: %v", len(list), list)
	}
	st := list[0].(map[string]any)
	backups, _ := st["backups"].([]any)
	expires, _ := st["expire_runs"].([]any)
	if st["stanza"] != "main" || len(backups) == 0 || len(expires) == 0 {
		t.Errorf("stanza %v with %d backups and %d expire runs, want main with both", st["stanza"], len(backups), len(expires))
	}
}

// TestPgBackRest_PostgreSQLSectionsUnaffectedByAddingPgBackRestFiles pins
// that adding pgBackRest files to S3's five PostgreSQL logs leaves the
// PostgreSQL sections byte-identical.
func TestPgBackRest_PostgreSQLSectionsUnaffectedByAddingPgBackRestFiles(t *testing.T) {
	pg, _ := filepath.Glob("testdata/pgbackrest/S3/pg_logs_*/postgresql.log")
	without := runJSON(t, pg...)
	with := runJSON(t, append(slices.Clone(pg), "testdata/pgbackrest/S3/pgbackrest_logs")...)
	for _, key := range []string{"checkpoints", "events", "server", "summary", "top_events"} {
		if a, b := jsonOf(without[key]), jsonOf(with[key]); a != b {
			t.Errorf("%q differs once pgBackRest files are added:\nwithout: %s\nwith:    %s", key, a, b)
		}
	}
}

// TestPgBackRest_MixedTarOrderSymmetric pins that a tar mixing a
// PostgreSQL log and a pgBackRest log, plain or gzip, gives both reports
// in either member order, with no warning.
func TestPgBackRest_MixedTarOrderSymmetric(t *testing.T) {
	pg := mustRead(t, "testdata/pgbackrest/S1/pg_logs_main/postgresql.log")
	pbr := mustRead(t, "testdata/pgbackrest/S1/pgbackrest_logs/main-backup.log")
	for _, tc := range []struct {
		member  string
		content []byte
	}{{"main-backup.log", pbr}, {"main-backup.log.gz", gzipBytes(t, pbr)}} {
		t.Run(tc.member, func(t *testing.T) {
			var mains []string
			for _, tarPath := range []string{
				tarOf(t, []string{"postgresql.log", tc.member}, pg, tc.content),
				tarOf(t, []string{tc.member, "postgresql.log"}, tc.content, pg),
			} {
				stdout, stderr, err := runCmd(exec.Command(harnessBinary, tarPath, "--json"))
				if err != nil || strings.Contains(stderr, "WARN") {
					t.Fatalf("err=%v stderr=%s, want a clean run", err, stderr)
				}
				got := mustJSON(t, []byte(stdout))
				main := pgbrStanzaByName(t, got, "main")
				if backups, _ := main["backups"].([]any); got["summary"] == nil || len(backups) == 0 {
					t.Errorf("summary=%v, %d backups: a member was dropped", got["summary"] != nil, len(backups))
				}
				delete(main, "files")
				mains = append(mains, jsonOf(main))
			}
			if mains[0] != mains[1] {
				t.Errorf("'main' stanza differs by member order:\npg-first:  %s\npbr-first: %s", mains[0], mains[1])
			}
		})
	}
}

// TestPgBackRest_MultiRepo pins S5's two-repository run, PostgreSQL and
// pgBackRest logs together: the [104] row names the failing repo2 and its
// --event-detail the repo line, and "Last full" names its repo, in text,
// Markdown and JSON.
func TestPgBackRest_MultiRepo(t *testing.T) {
	args := []string{"testdata/pgbackrest/S5/pg_logs", "testdata/pgbackrest/S5/pgbackrest_logs"}
	for _, format := range [][]string{nil, {"--md"}} {
		out := string(runHarness(t, false, append(slices.Clone(args), format...)...))
		if !strings.Contains(out, "[104]") || !strings.Contains(out, "repo2 archive.info permission denied") {
			t.Errorf("%v: the [104] error does not name repo2:\n%s", format, out)
		}
		i := strings.Index(out, "Last full")
		if i < 0 || !strings.Contains(out[i:i+strings.Index(out[i:], "\n")], "(repo") {
			t.Errorf("%v: no 'Last full' line naming a repo", format)
		}
	}
	main := pgbrStanzaByName(t, runJSON(t, args...), "main")
	if lf, _ := main["last_full"].(map[string]any); lf == nil || lf["repo"] == nil || lf["repo"] == "" {
		t.Errorf("JSON last_full has no repo: %v", lf)
	}
	id := ""
	for _, e := range main["errors"].([]any) {
		if e := e.(map[string]any); e["code"] == float64(104) {
			id, _ = e["id"].(string)
		}
	}
	out := string(runHarness(t, false, append(slices.Clone(args), "--event-detail", id)...))
	for _, want := range []string{"Error code           : 104 - CommandError", "archive-push command encountered error(s):", "repo2: [FileOpenError]"} {
		if !strings.Contains(out, want) {
			t.Errorf("--event-detail %s lacks %q:\n%s", id, want, out)
		}
	}
}

// TestPgBackRest_ExistingFixturesNotMisdetected pins that ordinary
// PostgreSQL fixtures get no pgBackRest key or section.
func TestPgBackRest_ExistingFixturesNotMisdetected(t *testing.T) {
	for _, f := range []string{"testdata/stderr.log", "testdata/comprehensive/stderr.log"} {
		if _, ok := runFixtureJSON(t, f)["pgbackrest"]; ok {
			t.Errorf("%s: unexpected 'pgbackrest' key", f)
		}
		if strings.Contains(string(runHarness(t, false, f)), "PGBACKREST") {
			t.Errorf("%s: unexpected PGBACKREST section", f)
		}
	}
}

// TestPgBackRest_DetectionUnderRecognizedExtensionIsQuiet pins that a
// pgBackRest .log file is detected without a word on stderr.
func TestPgBackRest_DetectionUnderRecognizedExtensionIsQuiet(t *testing.T) {
	if _, stderr, code := runQuellog(t, harnessBinary, "testdata/pgbackrest/S1/pgbackrest_logs/main-backup.log", "--json"); code != 0 || stderr != "" {
		t.Errorf("exit %d, stderr %q; want a quiet success", code, stderr)
	}
}

// gzipBytes gzip-compresses data.
func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zstdBytes zstd-compresses data.
func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll(data, nil)
}

// normalizePgBackRestFiles returns doc's "pgbackrest" list, sorted by
// stanza, each "files" list reduced to sorted basenames without
// compression suffix.
func normalizePgBackRestFiles(t *testing.T, doc map[string]any) string {
	t.Helper()
	list, ok := doc["pgbackrest"].([]any)
	if !ok {
		t.Fatalf("missing 'pgbackrest' list: %v", doc)
	}
	for _, s := range list {
		st := s.(map[string]any)
		var names []string
		for _, f := range st["files"].([]any) {
			name := filepath.Base(f.(string))
			for _, suf := range []string{".gz", ".zst", ".zstd"} {
				name = strings.TrimSuffix(name, suf)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		st["files"] = names
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].(map[string]any)["stanza"].(string) < list[j].(map[string]any)["stanza"].(string)
	})
	return jsonOf(list)
}

// TestPgBackRest_CompressedStandaloneMatchesPlain pins that S1's
// pgBackRest files compressed one by one (gzip, zstd) give the plain
// directory's report, file names aside.
func TestPgBackRest_CompressedStandaloneMatchesPlain(t *testing.T) {
	const plainDir = "testdata/pgbackrest/S1/pgbackrest_logs"
	entries, err := os.ReadDir(plainDir)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizePgBackRestFiles(t, runFixtureJSON(t, plainDir))
	for codec, compress := range map[string]func(*testing.T, []byte) []byte{"gz": gzipBytes, "zst": zstdBytes} {
		dir := t.TempDir()
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data := compress(t, mustRead(t, filepath.Join(plainDir, e.Name())))
			if err := os.WriteFile(filepath.Join(dir, e.Name()+"."+codec), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := normalizePgBackRestFiles(t, runFixtureJSON(t, dir)); got != want {
			t.Errorf("%s output differs from the plain directory's:\nplain: %s\n%s:   %s", codec, want, codec, got)
		}
	}
}

// TestPgBackRest_TarMemberBoundaryClosesAbandonedRuns pins that S3's
// backup log (with abandoned runs) and expire log give a shared warning
// the same commands packed in a tar as given flat.
func TestPgBackRest_TarMemberBoundaryClosesAbandonedRuns(t *testing.T) {
	const dir = "testdata/pgbackrest/S3/pgbackrest_logs/"
	commands := func(doc map[string]any) []string {
		for _, w := range pgbrStanzaByName(t, doc, "main")["warnings"].([]any) {
			if w := w.(map[string]any); strings.Contains(w["message"].(string), "repo1-retention-full") {
				var out []string
				for _, c := range w["commands"].([]any) {
					out = append(out, c.(string))
				}
				sort.Strings(out)
				return out
			}
		}
		t.Fatal("no 'repo1-retention-full' warning found")
		return nil
	}
	want := commands(runJSON(t, dir+"main-backup.log", dir+"main-expire.log"))
	got := commands(runFixtureJSON(t, tarOf(t, []string{"main-backup.log", "main-expire.log"}, mustRead(t, dir+"main-backup.log"), mustRead(t, dir+"main-expire.log"))))
	if len(want) < 2 || !slices.Equal(want, got) {
		t.Errorf("retention warning's commands: flat %v, tar %v; want the same, at least backup and expire", want, got)
	}
}

// runWithStdin runs the harness binary on stdin data and returns stdout
// and stderr.
func runWithStdin(t *testing.T, stdin []byte, args ...string) (stdout, stderr []byte) {
	t.Helper()
	cmd := exec.Command(harnessBinary, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, errb, err := runCmd(cmd)
	if err != nil {
		t.Fatalf("quellog %v failed: %v\nstderr: %s", args, err, errb)
	}
	return []byte(out), []byte(errb)
}

// TestPgBackRest_DetectionMessagesKeptForPostgreSQLFiles pins that the
// pgBackRest sniff leaves a PostgreSQL file its own detection messages.
func TestPgBackRest_DetectionMessagesKeptForPostgreSQLFiles(t *testing.T) {
	var prefixed bytes.Buffer
	for _, line := range bytes.SplitAfter(mustRead(t, "testdata/stderr.log"), []byte("\n")) {
		if len(line) > 0 {
			prefixed.WriteString("pfx| ")
			prefixed.Write(line)
		}
	}
	p := filepath.Join(t.TempDir(), "prefixed.log")
	if err := os.WriteFile(p, prefixed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runQuellog(t, harnessBinary, p, "--json"); code != 0 || !strings.Contains(stderr, "literal prefix before timestamp detected and stripped") {
		t.Errorf("exit %d, the literal-prefix warning is missing:\n%s", code, stderr)
	}
}

// TestPgBackRest_MixedStdinGivesBothReports pins that a stream starting
// with a pgBackRest log keeps the PostgreSQL report of the log after it.
func TestPgBackRest_MixedStdinGivesBothReports(t *testing.T) {
	pbr := mustRead(t, "testdata/pgbackrest/S1/pgbackrest_logs/main-backup.log")
	pg := mustRead(t, "testdata/stderr.log")
	want, got := runJSONWithStdin(t, pg), runJSONWithStdin(t, slices.Concat(pbr, pg))
	if a, b := jsonOf(want["summary"]), jsonOf(got["summary"]); a != b {
		t.Errorf("PostgreSQL summary changed by the leading pgBackRest log:\nwant %s\ngot  %s", a, b)
	}
	if backups, _ := pgbrStanzaByName(t, got, "main")["backups"].([]any); len(backups) != 8 {
		t.Errorf("main backups = %d, want 8", len(backups))
	}
}

// TestPgBackRest_StanzaWithDashNoPhantom pins that a stanza holding a "-"
// yields that stanza alone, from a file as from a tar.
func TestPgBackRest_StanzaWithDashNoPhantom(t *testing.T) {
	data := bytes.ReplaceAll(mustRead(t, "testdata/pgbackrest/S5/pgbackrest_logs/main-backup.log"), []byte("--stanza=main"), []byte("--stanza=db-prod"))
	plain := filepath.Join(t.TempDir(), "db-prod-backup.log")
	if err := os.WriteFile(plain, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{plain, tarOf(t, []string{"db-prod-backup.log"}, data)} {
		var names []string
		for _, s := range runFixtureJSON(t, in)["pgbackrest"].([]any) {
			names = append(names, s.(map[string]any)["stanza"].(string))
		}
		if !slices.Equal(names, []string{"db-prod"}) {
			t.Errorf("%s: stanzas = %v, want [db-prod]", filepath.Base(in), names)
		}
	}
}

// TestPgBackRest_OtherArchiveCommandNoSection pins that a PostgreSQL log
// whose archive_command is not pgBackRest gets no pgBackRest section.
func TestPgBackRest_OtherArchiveCommandNoSection(t *testing.T) {
	log := "2026-10-10 13:00:00.000 UTC [100] LOG:  database system is ready to accept connections\n" +
		"2026-10-10 13:00:05.000 UTC [101] LOG:  archive command failed with exit code 1\n" +
		"2026-10-10 13:00:05.000 UTC [101] DETAIL:  The failed archive command was: cp pg_wal/000000010000000000000001 /mnt/archive/000000010000000000000001\n" +
		"2026-10-10 13:00:06.000 UTC [101] WARNING:  archiving write-ahead log file \"000000010000000000000001\" failed too many times, will try again later\n"
	p := filepath.Join(t.TempDir(), "postgresql.log")
	if err := os.WriteFile(p, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := runFixtureJSON(t, p)["pgbackrest"]; ok {
		t.Error("a cp archive_command produced a 'pgbackrest' key")
	}
}

// TestPgBackRest_ArchivePushLoggedTwiceCountsOnce pins S6: each
// archive-push run is in its own log file and in the PostgreSQL log; read
// together, in either order, they count as the file alone.
func TestPgBackRest_ArchivePushLoggedTwiceCountsOnce(t *testing.T) {
	pg := "testdata/pgbackrest/S6/pg_logs/postgresql.log"
	file := "testdata/pgbackrest/S6/pgbackrest_logs/main-archive-push.log"
	counts := func(m map[string]any) string {
		st := pgbrStanzaByName(t, m, "main")
		ap := st["archive_push"].(map[string]any)
		errs, runs := 0.0, 0.0
		for _, e := range st["errors"].([]any) {
			errs += e.(map[string]any)["count"].(float64)
		}
		for _, c := range st["commands"].([]any) {
			if c.(map[string]any)["command"] == "archive-push" {
				runs = c.(map[string]any)["runs"].(float64)
			}
		}
		return fmt.Sprintf("pushed=%v failed=%v errors=%v runs=%v stretches=%d",
			ap["pushed"], ap["failed"], errs, runs, len(ap["failures"].([]any)))
	}
	const want = "pushed=21 failed=12 errors=12 runs=33 stretches=2"
	for _, args := range [][]string{{file}, {pg, file}, {file, pg}} {
		if got := counts(runJSON(t, args...)); got != want {
			t.Errorf("%v: %s, want %s", args, got, want)
		}
	}
}

// TestPgBackRest_InputOrderDoesNotMatter pins S7 read in both orders: the
// pgBackRest files read after a PostgreSQL log complete its lines.
func TestPgBackRest_InputOrderDoesNotMatter(t *testing.T) {
	for _, run := range []string{"S6", "S7"} {
		files, _ := filepath.Glob("testdata/pgbackrest/" + run + "/pgbackrest_logs/*.log")
		pg, _ := filepath.Glob("testdata/pgbackrest/" + run + "/pg_logs*/postgresql.log")
		if jsonOf(runJSON(t, slices.Concat(files, pg)...)["pgbackrest"]) != jsonOf(runJSON(t, slices.Concat(pg, files)...)["pgbackrest"]) {
			t.Errorf("%s: the pgBackRest report depends on the order of the inputs", run)
		}
	}
}

// TestPgBackRest_FactsTheReportStatesOnce pins three facts each stated
// against the log elsewhere in the same report: PostgreSQL's failures of
// runs pgBackRest did not log (S4), WAL removed by an expire run although
// retention-archive is not set (S1), the shell's "not found" after the
// DETAIL that names the command (S6).
func TestPgBackRest_FactsTheReportStatesOnce(t *testing.T) {
	if out := string(runHarness(t, false, "testdata/pgbackrest/S4/postgresql.full.log")); !strings.Contains(out, "18 pushed, 0 failed, last at 13:57:05 (PostgreSQL: 18 archive_command failures)") {
		t.Errorf("S4: PostgreSQL's failures missing from the WAL archiving line:\n%s", out)
	}
	if out := string(runHarness(t, false, "testdata/pgbackrest/S1/pgbackrest_logs", "--pgbackrest-detail")); strings.Contains(out, "WAL never expired") {
		t.Errorf("S1: an expire run removed WAL, yet the report says it never expired")
	}
	pg := pgbrStanzaByName(t, runJSON(t, "testdata/pgbackrest/S6/pg_logs/postgresql.log"), "main")
	for _, f := range pg["pg_archive_failures"].([]any) {
		if seg := f.(map[string]any)["segment"]; seg == "found" {
			t.Errorf("S6: the shell's message read as a segment: %v", f)
		}
	}
}
