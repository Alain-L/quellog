package analysis

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

func pgbrMarker(info parser.PgBackRestLineInfo) *parser.LogEntry {
	return &parser.LogEntry{FromPgBackRest: true, PgBackRest: &info}
}

func pgLogEntry(ts time.Time, body string, continuation bool) *parser.LogEntry {
	prefix := "LOG:  "
	if continuation {
		prefix = "DETAIL:  "
	}
	return &parser.LogEntry{Timestamp: ts, Message: prefix + body, IsContinuation: continuation, BodyOffset: int32(len(prefix))}
}

func keysOf(m map[string]*PgBackRestMetrics) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	slices.Sort(k)
	return k
}

// TestPgBackRestAnalyzer_SignalGoesToCurrentRunOfItsProcess pins that a
// signal line names only the run its process was writing: not every open
// run, and not a run an earlier process left open.
func TestPgBackRestAnalyzer_SignalGoesToCurrentRunOfItsProcess(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --exec-id=1-a --stanza=main --type=full"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:05:00", "P00", "INFO: backup command begin 2.60.0: --exec-id=2-b --stanza=main --type=incr"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGTERM"}))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGKILL"}))
	signals := map[string]string{}
	for _, b := range stanza(t, a, "main").Backups {
		signals[b.ExecID] = b.Signal
	}
	if signals["1-a"] != "" || signals["2-b"] != "SIGTERM" {
		t.Errorf("signals by run = %v, want only 2-b killed by SIGTERM (the SIGKILL process had no run)", signals)
	}
}

// TestPgBackRestAnalyzer_IssueGoesToCurrentRun pins that an ERROR names
// the command of the run being written, not a backup an earlier process
// left open, and a WARN of a process with no run names none.
func TestPgBackRestAnalyzer_IssueGoesToCurrentRun(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	feed(a, "2026-01-14 09:01:00", "INFO: stanza-create command begin 2.60.0: --stanza=main", "ERROR: [056]: unable to find primary cluster - cannot proceed")
	a.Process(pgbrEntry("2026-01-14 09:01:01", "P00", "INFO: stanza-create command end: aborted with exception [056]"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:02:00", "P00", "WARN: a warning from a process with no run"))
	m := stanza(t, a, "main")
	if len(m.Errors) != 1 || !slices.Equal(m.Errors[0].Commands, []string{"stanza-create"}) || len(m.Warnings) != 1 || len(m.Warnings[0].Commands) != 0 {
		t.Errorf("errors = %+v, warnings = %+v; want the [056] on stanza-create only, the WARN on no command", m.Errors, m.Warnings)
	}
}

// TestPgBackRestAnalyzer_BackupType pins a backup's type: the "backup
// size" line, else --type, else the label's letter or the "changed to
// full" WARN (which keeps the type asked), else incr, pgBackRest's default.
func TestPgBackRestAnalyzer_BackupType(t *testing.T) {
	for _, c := range []struct {
		opt, want, requested string
		lines                []string
	}{
		{"", "incr", "", []string{"ERROR: [038]: postmaster.pid exists", "INFO: backup command end: aborted with exception [038]"}},
		{"", "full", "", []string{"INFO: new backup label = 20260114-091000F", "INFO: backup command end: aborted with exception [082]"}},
		{"", "full", "incr", []string{"WARN: no prior backup exists, incr backup has been changed to full", "INFO: backup command end: aborted with exception [082]"}},
		{"", "diff", "", []string{"INFO: new backup label = 20260110-000000F_20260110-000100D", "INFO: backup command end: completed successfully (10ms)"}},
		{"", "incr", "", []string{"INFO: new backup label = 20260110-000000F_20260110-000200I", "INFO: backup command end: completed successfully (10ms)"}},
		{" --type=diff", "full", "", []string{"INFO: full backup size = 22MB, file total = 963", "INFO: backup command end: completed successfully (46383ms)"}},
	} {
		a := NewPgBackRestAnalyzer()
		feed(a, "2026-01-14 09:00:00", append([]string{"INFO: backup command begin 2.60.0: --stanza=main" + c.opt}, c.lines...)...)
		if bs := stanza(t, a, "main").Backups; len(bs) != 1 || bs[0].Type != c.want || bs[0].RequestedType != c.requested {
			t.Errorf("%q: backups = %+v, want type %q, requested %q", c.lines[0], bs, c.want, c.requested)
		}
	}
}

// TestPgBackRestAnalyzer_ExpireRecordsOutcome pins that an expire run
// keeps whether it succeeded and its error code.
func TestPgBackRestAnalyzer_ExpireRecordsOutcome(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: expire command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "ERROR: [050]: unable to acquire lock"))
	a.Process(pgbrEntry("2026-01-14 09:00:02", "P00", "INFO: expire command end: aborted with exception [050]"))
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: expire command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:10:01", "P00", "INFO: expire command end: completed successfully (5ms)"))
	runs := stanza(t, a, "main").ExpireRuns
	if len(runs) != 2 || runs[0].Succeeded || runs[0].ErrorCode != 50 || !runs[1].Succeeded || runs[1].ErrorCode != 0 {
		t.Errorf("expire runs = %+v, want a failed [050] run then a successful one", runs)
	}
}

// TestPgBackRestAnalyzer_StanzaFromContentFileNameFallback pins that the
// stanza named in the content wins over the file name, with no phantom,
// and that the name serves only when no line names one, file or member.
func TestPgBackRestAnalyzer_StanzaFromContentFileNameFallback(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(sourceMarker("db-prod-backup.log", false))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=db-prod --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: backup command end: completed successfully (5ms)"))
	if got := byStanza(a); len(got) != 1 || got["db-prod"] == nil || got["db-prod"].LinesRead != 3 {
		t.Errorf("stanzas = %v, want db-prod alone with the file's 3 lines", keysOf(got))
	}
	for _, member := range []bool{false, true} {
		b := NewPgBackRestAnalyzer()
		if member {
			b.Process(sourceMarker("logs.tar", false))
		}
		b.Process(sourceMarker("demo-backup.log", member))
		b.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
		b.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "WARN: a warning without any command framing"))
		b.Process(sourceMarker("other-backup.log", member))
		b.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
		b.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: backup command begin 2.60.0: --stanza=other --type=full"))
		if got := byStanza(b); len(got) != 2 || got["demo"] == nil || got["demo"].LinesRead != 2 || got["other"] == nil {
			t.Errorf("member=%v: stanzas = %v, want demo (from its name, 2 lines) and other", member, keysOf(got))
		}
	}
}

// TestPgBackRestAnalyzer_UnnamedInvocationStaysWithItsFile pins that an
// invocation whose begin line is hidden, in a file whose name gives no
// stanza, goes to the stanza the file named before, not the next file's.
func TestPgBackRestAnalyzer_UnnamedInvocationStaysWithItsFile(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	banner := pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true})
	a.Process(sourceMarker("main-backup.combined.log", false))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: backup command end: completed successfully (5ms)"))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 10:00:00", "P00", "WARN: a warning without any command framing"))
	a.Process(sourceMarker("other-backup.log", false))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 11:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=other --type=full"))
	if got := byStanza(a); len(got) != 2 || got["main"] == nil || got["main"].LinesRead != 5 || len(got["main"].Warnings) != 1 {
		t.Errorf("got %v (main %+v), want main with its 5 lines and the warning, and other", keysOf(got), got["main"])
	}
}

// TestPgBackRestAnalyzer_ArchiveFailureNeedsPgBackRest pins that a failed
// archive_command counts, with its retry warning, only when it runs
// pgBackRest (whatever the case of its name).
func TestPgBackRestAnalyzer_ArchiveFailureNeedsPgBackRest(t *testing.T) {
	ts := mustParse(t, "2026-01-14 09:00:00")
	for command, want := range map[string]string{
		"cp pg_wal/000000010000000000000001 /mnt/archive/000000010000000000000001":       "",
		"pgbackrest --stanza=demo archive-push pg_wal/000000010000000000000001":          "demo 1 1",
		"/usr/bin/pgBackRest --stanza=main archive-push pg_wal/000000010000000000000001": "main 1 1",
	} {
		a := NewPgBackRestAnalyzer()
		pgArchiveFailure(a, ts, "archive command failed with exit code 1", command)
		a.observePGArchiveFailure(pgLogEntry(ts, `archiving write-ahead log file "000000010000000000000001" failed too many times, will try again later`, false))
		got := ""
		for _, m := range a.Finalize() {
			got += fmt.Sprintf("%s %d %d", m.Stanza, len(m.PGArchiveFailures), m.PGRetryWarnings)
		}
		if got != want {
			t.Errorf("%s: got %q, want %q (stanza, failures, retry warnings)", command, got, want)
		}
	}
}

// TestPgBackRestAnalyzer_OtherProgramDetailDropsFollowingFailure pins
// that once a DETAIL showed another program, a later failure without
// DETAIL and the retry warning are dropped too, even with a backup log.
func TestPgBackRestAnalyzer_OtherProgramDetailDropsFollowingFailure(t *testing.T) {
	ts := mustParse(t, "2026-01-14 09:00:00")
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 08:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"))
	a.Process(pgbrEntry("2026-01-14 08:00:01", "P00", "INFO: backup command end: completed successfully (5ms)"))
	pgArchiveFailure(a, ts, "archive command failed with exit code 1", "cp pg_wal/000000010000000000000001 /mnt/archive")
	pgArchiveFailure(a, ts.Add(time.Minute), "archive command failed with exit code 1", "")
	a.observePGArchiveFailure(pgLogEntry(ts.Add(time.Minute), "checkpoint starting: time", false))
	a.observePGArchiveFailure(pgLogEntry(ts.Add(2*time.Minute), `archiving write-ahead log file "000000010000000000000001" failed too many times, will try again later`, false))
	if m := stanza(t, a, "demo"); len(m.PGArchiveFailures) != 0 || m.PGRetryWarnings != 0 {
		t.Errorf("got %+v, want demo with no PostgreSQL-side failure or retry warning", m)
	}
}

// TestPgBackRestAnalyzer_EndLineDoesNotProlongRunBelow pins that an end
// line dates only the run it closes: a backup left open underneath keeps
// its own last line.
func TestPgBackRestAnalyzer_EndLineDoesNotProlongRunBelow(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:05", "P00", "INFO: execute non-exclusive backup start"))
	a.Process(pgbrEntry("2026-01-14 10:00:00", "P00", "INFO: check command begin 2.60.0: --stanza=demo"))
	a.Process(pgbrEntry("2026-01-14 10:00:09", "P00", "INFO: check command end: completed successfully (9000ms)"))
	bs := stanza(t, a, "demo").Backups
	if len(bs) != 1 || !bs[0].Crashed || !bs[0].LastLine.Equal(mustParse(t, "2026-01-14 09:00:05")) || !bs[0].End.IsZero() {
		t.Errorf("backups = %+v, want one crashed at its own last line, 09:00:05, with no end", bs)
	}
}

// TestPgBackRestAnalyzer_WindowLeavesEdgesOut pins that a time window
// does not turn its cut edges into facts: lines outside are left out and
// recorded as cut, an end without its begin is neither a backup nor a
// run, a run open at --end is still running, no begin/end note is
// issued, and a stanza left with no line is not reported.
func TestPgBackRestAnalyzer_WindowLeavesEdgesOut(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(sourceMarker("main-backup.log", false))
	begin, end := mustParse(t, "2026-01-14 09:00:00"), mustParse(t, "2026-01-14 10:00:00")
	a.SetTimeWindow(begin, end)
	a.Process(pgbrEntry("2026-01-14 08:59:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:05", "P00", "WARN: inside the window --stanza is unknown here"))
	a.Process(pgbrEntry("2026-01-14 09:00:10", "P00", "INFO: backup command end: completed successfully (5000ms)"))
	a.Process(pgbrEntry("2026-01-14 09:50:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 10:00:01", "P00", "INFO: backup command end: completed successfully (600000ms)"))
	got := byStanza(a)
	m := got["main"]
	if m == nil || len(got) != 1 {
		t.Fatalf("stanzas = %v, want main alone", keysOf(got))
	}
	if len(m.Backups) != 1 || !m.Backups[0].InProgress || m.Backups[0].Crashed || m.Backups[0].Succeeded {
		t.Errorf("backups = %+v, want the 09:50 run alone, still running at --end", m.Backups)
	}
	if len(m.Commands) != 1 || m.Commands[0] != (PgBackRestCommandStat{Command: "backup", Runs: 1}) {
		t.Errorf("commands = %+v, want one backup run, neither succeeded nor failed", m.Commands)
	}
	if w := m.Window; len(m.MissingInfo) != 0 || m.LinesRead != 3 || w == nil || !w.Begin.Equal(begin) || !w.End.Equal(end) || !w.CutBefore || !w.CutAfter {
		t.Errorf("MissingInfo %q, LinesRead %d, Window %+v; want no note, the 3 lines inside, the bounds cut on both sides", m.MissingInfo, m.LinesRead, w)
	}

	// The --end bound is inclusive; a run open at the end of an input not
	// cut after the window was killed, and so was one whose process
	// logged its signal, cut or not.
	for _, c := range []struct {
		name                string
		lines               [][2]string
		inProgress, crashed bool
		signal              string
	}{
		{"end line at --end", [][2]string{{"10:00:00", "INFO: backup command end: completed successfully (600000ms)"}}, false, false, ""},
		{"input ends inside the window", nil, false, true, ""},
		{"signal, then cut", [][2]string{{"09:55:00", "-"}, {"10:00:01", "INFO: unrelated line"}}, false, true, "SIGTERM"},
	} {
		w := NewPgBackRestAnalyzer()
		w.SetTimeWindow(begin, end)
		w.Process(pgbrEntry("2026-01-14 09:50:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
		for _, l := range c.lines {
			if l[1] == "-" {
				w.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGTERM"}))
				continue
			}
			w.Process(pgbrEntry("2026-01-14 "+l[0], "P00", l[1]))
		}
		bs := stanza(t, w, "main").Backups
		if len(bs) != 1 || bs[0].InProgress != c.inProgress || bs[0].Crashed != c.crashed || bs[0].Signal != c.signal ||
			bs[0].Succeeded != (!c.inProgress && !c.crashed) {
			t.Errorf("%s: backups = %+v, want in progress %v, crashed %v, signal %q", c.name, bs, c.inProgress, c.crashed, c.signal)
		}
	}

	empty := NewPgBackRestAnalyzer()
	empty.Process(sourceMarker("main-backup.log", false))
	empty.SetTimeWindow(mustParse(t, "2030-01-01 00:00:00"), time.Time{})
	empty.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	empty.Process(pgbrEntry("2026-01-14 09:00:05", "P00", "INFO: backup command begin 2.60.0: --stanza=main"))
	if got := byStanza(empty); len(got) != 0 {
		t.Errorf("a window keeping no line still reports %v", keysOf(got))
	}
}

// TestPgBackRestAnalyzer_ArchiveOutcomesLoggedOrNot pins the Logged
// flags: sync figures are read only from the sync command's begin/end
// lines; bare ERROR lines or an async worker's log alone say nothing.
func TestPgBackRestAnalyzer_ArchiveOutcomesLoggedOrNot(t *testing.T) {
	ts := mustParse(t, "2026-01-14 09:00:00")
	bare := NewPgBackRestAnalyzer()
	bare.Process(&parser.LogEntry{Timestamp: ts, Message: "ERROR: [103]: unable to find a valid repository"})
	pgArchiveFailure(bare, ts, "archive command failed with exit code 103", "pgbackrest --stanza=main archive-push pg_wal/000000010000000000000001")
	if m := stanza(t, bare, "main"); m.ArchivePush.Logged || m.ArchivePush.Failed != 0 || len(m.PGArchiveFailures) != 1 {
		t.Errorf("default console level: got %+v, want push not logged, PostgreSQL's 1 failure", m)
	}

	framed := NewPgBackRestAnalyzer()
	pushRun(framed, "2026-01-14 09:00:00", "main", "000000010000000000000001", false)
	if m := stanza(t, framed, "main"); !m.ArchivePush.Logged || m.ArchivePush.Failed != 1 || m.ArchiveGet.Logged {
		t.Errorf("info console level: got %+v, want push logged with 1 failure, get not logged", m)
	}

	async := NewPgBackRestAnalyzer()
	feed(async, "2026-01-14 09:00:00", "INFO: archive-get:async command begin 2.60.0: [000000010000000000000002] --stanza=main",
		"INFO: found 000000010000000000000002 in the archive asynchronously")
	async.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: archive-get:async command end: completed successfully"))
	if m := stanza(t, async, "main"); m.ArchiveGet.Logged || !m.ArchiveGet.AsyncLogged || m.ArchiveGet.FoundAsync != 1 {
		t.Errorf("archive-get async alone: got %+v, want async logged, sync not, 1 found", m)
	}

	pushAsync := NewPgBackRestAnalyzer()
	pushAsync.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: archive-push:async command begin 2.60.0: [/var/lib/pg/pg_wal] --stanza=main"))
	pushAsync.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: archive-push:async command end: completed successfully"))
	if m := stanza(t, pushAsync, "main"); m.ArchivePush.Logged || !m.ArchivePush.AsyncLogged {
		t.Errorf("archive-push async alone: got %+v, want async logged, sync not", m)
	}
}

// TestPgBackRestAnalyzer_ManualExpireAndBackupSize pins the --set= label
// of a manual expire, and a backup's size in bytes, parsed once with
// pgBackRest's 1024-based units.
func TestPgBackRestAnalyzer_ManualExpireAndBackupSize(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:04", "P00", "INFO: full backup size = 29.4MB, file total = 1270"))
	a.Process(pgbrEntry("2026-01-14 09:00:05", "P00", "INFO: backup command end: completed successfully (5000ms)"))
	for _, set := range []string{"--set=20260114-090000F ", ""} {
		a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: expire command begin 2.60.0: "+set+"--stanza=main"))
		a.Process(pgbrEntry("2026-01-14 09:10:01", "P00", "INFO: expire command end: completed successfully (10ms)"))
	}
	m := stanza(t, a, "main")
	if len(m.Backups) != 1 || len(m.ExpireRuns) != 2 {
		t.Fatalf("got %+v", m)
	}
	if b := m.Backups[0]; !b.HasSize || b.SizeBytes != 30828134 || b.SizeRaw != "29.4MB" {
		t.Errorf("size = %q / %d (has %v), want 29.4MB / 30828134", b.SizeRaw, b.SizeBytes, b.HasSize)
	}
	if m.ExpireRuns[0].SetLabel != "20260114-090000F" || m.ExpireRuns[1].SetLabel != "" {
		t.Errorf("SetLabel = %q, %q, want the --set= label, then none", m.ExpireRuns[0].SetLabel, m.ExpireRuns[1].SetLabel)
	}
	for raw, want := range map[string]int64{"8.3KB": 8499, "512B": 512, "1GB": 1 << 30, "105MB": 105 << 20, "": -1, "MB": -1, "12XB": -1, "1.2.3MB": -1} {
		if got, ok := parsePgBackRestSize(raw); ok != (want >= 0) || (ok && got != want) {
			t.Errorf("parsePgBackRestSize(%q) = %d, %v, want %d", raw, got, ok, want)
		}
	}
}

// analyzeFiles runs files through the parser and feeds the analyzer the
// way ProcessBatch does.
func analyzeFiles(t *testing.T, files ...string) []PgBackRestMetrics {
	t.Helper()
	a := NewPgBackRestAnalyzer()
	for _, f := range files {
		out := make(chan []parser.LogEntry, 64)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for batch := range out {
				for i := range batch {
					e := &batch[i]
					e.BodyOffset = messageBodyOffset(e.Message)
					if e.FromPgBackRest {
						a.Process(e)
					} else {
						a.observePGArchiveFailure(e)
					}
				}
			}
		}()
		out <- []parser.LogEntry{parser.PgBackRestSourceMarker(f, false)}
		if err := parser.ParseFile(f, out); err != nil {
			t.Fatal(err)
		}
		close(out)
		<-done
	}
	return a.Finalize()
}

// TestPgBackRestAnalyzer_DefaultConsoleLevelArchiveFailures pins the
// default settings (log-level-console=warn): bare ERROR lines and
// PostgreSQL's failures, no begin/end; sync push figures not logged.
func TestPgBackRestAnalyzer_DefaultConsoleLevelArchiveFailures(t *testing.T) {
	for dir, wantErrors := range map[string]int{"03_stderr/off": 0, "03_stderr/warn": 3, "06_oldversion/old_2.48": 6, "06_oldversion/current_2.59.3": 3} {
		got := analyzeFiles(t, "../test/testdata/pgbackrest/S4/"+dir+"/pg_log_excerpt.log")
		if len(got) != 1 {
			t.Fatalf("%s: got %d stanzas, want 1", dir, len(got))
		}
		m := got[0]
		errors := 0
		for _, is := range m.Errors {
			errors += is.Count
		}
		if m.ArchivePush.Logged || m.ArchivePush.Failed != 0 || len(m.PGArchiveFailures) == 0 || errors != wantErrors {
			t.Errorf("%s: push logged=%v failed=%d, PostgreSQL failures %d, errors %d; want not logged, PostgreSQL's failures, %d errors",
				dir, m.ArchivePush.Logged, m.ArchivePush.Failed, len(m.PGArchiveFailures), errors, wantErrors)
		}
	}
}

// TestPgBackRestAnalyzer_LineDatesOnlyItsProcessRun pins that a line
// keeps alive only the run its own process opened: a run an earlier
// process left open ends at its own last line.
func TestPgBackRestAnalyzer_LineDatesOnlyItsProcessRun(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:01:00", "P00", "INFO: execute backup start: backup begins after the next regular checkpoint completes"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:30:00", "P00", "INFO: check command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:30:01", "P00", "INFO: check command end: completed successfully (1000ms)"))
	a.Process(pgbrEntry("2026-01-14 09:31:00", "P00", "WARN: the later process writes on"))
	m := stanza(t, a, "main")
	if bs := m.Backups; len(bs) != 1 || !bs[0].Crashed || !bs[0].LastLine.Equal(mustParse(t, "2026-01-14 09:01:00")) {
		t.Errorf("backups = %+v, want one killed run whose last line is at 09:01:00", bs)
	}
	if len(m.Warnings) != 1 || m.Warnings[0].Commands != nil {
		t.Errorf("warnings = %+v, want one naming no command", m.Warnings)
	}
}

// TestProcessBatch_RestoreFailureReachesObserver pins the byte gate of
// ProcessBatch: a PostgreSQL "could not restore file" line reaches the
// pgBackRest observer.
func TestProcessBatch_RestoreFailureReachesObserver(t *testing.T) {
	ts := mustParse(t, "2026-01-14 09:00:00")
	obs := NewPgBackRestAnalyzer()
	sa := NewStreamingAnalyzer()
	sa.pgbr = obs
	sa.ProcessBatch([]parser.LogEntry{
		{Timestamp: ts, Message: "INFO: archive-get command begin 2.60.0: [000000010000000000000001, pg_wal/RECOVERYXLOG] --stanza=demo", PID: "P00", FromPgBackRest: true},
		parser.NewLogEntry(ts, `LOG:  could not restore file "000000010000000000000001" from archive: child process exited with exit code 1`, false),
	})
	sa.Finalize()
	if m := stanza(t, obs, "demo"); m.PGRestoreFailures != 1 {
		t.Fatalf("got %+v, want one restore failure under demo", m)
	}
}

// TestPgBackRestAnalyzer_FailureKeepsItsCause pins that the cause under
// a failed run's ERROR stays with the run and with the error group, and
// that its HINT lines are left out.
func TestPgBackRestAnalyzer_FailureKeepsItsCause(t *testing.T) {
	detail := []string{"FileMissingError: unable to open missing file '/repo/backup/ghost/backup.info' for read"}
	errLine := "ERROR: [055]: unable to load info file '/repo/backup/ghost/backup.info':\n" + strings.Join(append(slices.Clone(detail),
		"HINT: backup.info cannot be opened and is required to perform a backup.", "HINT: has a stanza-create been performed?"), "\n")
	a := NewPgBackRestAnalyzer()
	for i, cmd := range []string{"backup", "expire"} {
		feed(a, fmt.Sprintf("2026-01-14 09:0%d:00", i), "INFO: "+cmd+" command begin 2.60.0: --stanza=ghost", errLine, "INFO: "+cmd+" command end: aborted with exception [055]")
	}
	m := stanza(t, a, "ghost")
	if len(m.Backups) != 1 || !slices.Equal(m.Backups[0].ErrorDetail, detail) || len(m.ExpireRuns) != 1 || !slices.Equal(m.ExpireRuns[0].ErrorDetail, detail) {
		t.Errorf("backups = %+v, expire runs = %+v; want the lines under their ERROR", m.Backups, m.ExpireRuns)
	}
	if len(m.Errors) != 1 || !slices.Equal(m.Errors[0].Detail, detail) {
		t.Errorf("errors = %+v, want the lines under the first occurrence", m.Errors)
	}
}

// TestPgbrStanzaAbsorbCoversEveryField fails when a field is added to a
// stanza's state without a decision in absorb (and addArchiveCounts):
// merge it there, or leave it to Finalize, then list it here.
func TestPgbrStanzaAbsorbCoversEveryField(t *testing.T) {
	known := map[reflect.Type]string{
		reflect.TypeFor[pgbrStanza](): "PgBackRestMetrics openRuns runStack cmdStat errIdx warnIdx pushFail getFail curExpire versions touchedFiles " +
			"lastDroppedSegment asyncPushedSegs asyncPushRanges syncPushAt getMisses getFoundAt getFoundSeg getFoundTimes archiveErrTimes pushes pendingSummary lastErrorFirstLine lastErrorDetail invocationHasActivity " +
			"invocationHasBeginEnd missingBeginEndRuns hasContent sawBanner undatedSources datedSources deferredFailures deferredRetries deferredRestores deferredArchived",
		// Files, InputEnd, Window, Last, Commands, RetentionWarnings,
		// PGArchiveFailureGroups, MissingInfo, MissingWAL and PushTimes are set by
		// Finalize.
		reflect.TypeFor[PgBackRestMetrics](): "Stanza Files LinesRead Versions StartTime EndTime HasTimestamps SawBeginEnd BackupsLogged InputEnd Window Backups " +
			"Last Commands ArchivePush ArchiveGet ExpireRuns RetentionWarnings Operations " +
			"Errors Warnings PGArchiveFailures PGArchiveFailureGroups PGRetryWarnings PGRetryTimes PGRestoreFailures MissingInfo RestoreTimes MissingWAL PushTimes",
		// Failures and LastPush are set by Finalize.
		reflect.TypeFor[PgBackRestArchiveStats](): "Pushed PushedAsync Found FoundAsync NotFound NotFoundAsync DroppedEvents Failed FailedAsync " +
			"Timeouts Failures LastFound LastPush Logged AsyncLogged",
		reflect.TypeFor[PgBackRestCommandStat](): "Command Runs Succeeded Failed TotalDuration MaxDuration",
	}
	for typ, names := range known {
		var got []string
		for i := range typ.NumField() {
			got = append(got, typ.Field(i).Name)
		}
		if strings.Join(got, " ") != names {
			t.Errorf("%s fields changed: %v; decide how absorb merges them, then update this list", typ.Name(), got)
		}
	}
}
