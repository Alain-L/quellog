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

func pgbrEntry(ts string, pid, message string) *parser.LogEntry {
	var t time.Time
	if ts != "" {
		var err error
		t, err = time.Parse("2006-01-02 15:04:05", ts)
		if err != nil {
			panic(err)
		}
	}
	return &parser.LogEntry{Timestamp: t, PID: pid, Message: message}
}

// feed processes P00 lines all dated ts.
func feed(a *PgBackRestAnalyzer, ts string, messages ...string) {
	for _, m := range messages {
		a.Process(pgbrEntry(ts, "P00", m))
	}
}

// byStanza finalizes a and indexes its reports by stanza.
func byStanza(a *PgBackRestAnalyzer) map[string]*PgBackRestMetrics {
	out := make(map[string]*PgBackRestMetrics)
	for _, m := range a.Finalize() {
		out[m.Stanza] = &m
	}
	return out
}

// stanza finalizes a and returns the report of the named stanza, failing
// the test when there is none.
func stanza(t *testing.T, a *PgBackRestAnalyzer, name string) *PgBackRestMetrics {
	t.Helper()
	m := byStanza(a)[name]
	if m == nil {
		t.Fatalf("no stanza %q", name)
	}
	return m
}

// sourceMarker builds the marker announcing an input file, or an archive
// member with member set.
func sourceMarker(name string, member bool) *parser.LogEntry {
	e := parser.PgBackRestSourceMarker(name, member)
	return &e
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

// pushRun feeds a synchronous archive-push run of seg ("" for none) on
// stanza, all dated ts, that pushes it or fails with [103].
func pushRun(a *PgBackRestAnalyzer, ts, stanza, seg string, pushed bool) {
	arg := ""
	if seg != "" {
		arg = "[pg_wal/" + seg + "] "
	}
	a.Process(pgbrEntry(ts, "P00", "INFO: archive-push command begin 2.60.0: "+arg+"--stanza="+stanza))
	if pushed {
		feed(a, ts, "INFO: pushed WAL file '"+seg+"' to the archive", "INFO: archive-push command end: completed successfully (10ms)")
	} else {
		feed(a, ts, "ERROR: [103]: unable to find a valid repository", "INFO: archive-push command end: aborted with exception [103]")
	}
}

// asyncBatch feeds an archive-push:async run of stanza begun at begin
// announcing files ("N WAL file(s) to archive: ..."), then lines at begin
// (a "P01 " prefix sets their process), ended successfully at end.
func asyncBatch(a *PgBackRestAnalyzer, stanza, begin, end, files string, lines ...string) {
	feed(a, begin, "INFO: archive-push:async command begin 2.60.0: [/pg_wal] --archive-async --stanza="+stanza, "INFO: push "+files)
	for _, l := range lines {
		pid := "P00"
		if rest, ok := strings.CutPrefix(l, "P01 "); ok {
			pid, l = "P01", rest
		}
		a.Process(pgbrEntry(begin, pid, l))
	}
	a.Process(pgbrEntry(end, "P00", "INFO: archive-push:async command end: completed successfully (200ms)"))
}

// couldNotPush is the async worker's warning for a segment it failed to push.
func couldNotPush(seg string) string {
	return "WARN: could not push WAL file '" + seg + "' to the archive (will be retried): [39] raised from local-1 shim protocol: unable to open file"
}

// pgArchiveFailure feeds PostgreSQL's failure line head and, unless
// command is "", the DETAIL naming that command.
func pgArchiveFailure(a *PgBackRestAnalyzer, ts time.Time, head, command string) {
	a.observePGArchiveFailure(pgLogEntry(ts, head, false))
	if command != "" {
		a.observePGArchiveFailure(pgLogEntry(ts, "The failed archive command was: "+command, true))
	}
}

// TestPgBackRestAnalyzer_ChangedToFullKeepsRequestedType pins a backup
// pgBackRest changed to full: type full, the type asked (--type=, else the
// WARN's word), label, size and file total, and the end line's duration.
func TestPgBackRestAnalyzer_ChangedToFullKeepsRequestedType(t *testing.T) {
	for _, tc := range []struct{ opt, warn, want string }{
		{" --type=incr", "incr", "incr"},
		{"", "diff", "diff"},
	} {
		a := NewPgBackRestAnalyzer()
		a.Process(pgbrEntry("2026-01-10 02:00:01", "P00", "INFO: backup command begin 2.60.0: --stanza=demo"+tc.opt))
		a.Process(pgbrEntry("2026-01-10 02:00:01", "P00", "WARN: no prior backup exists, "+tc.warn+" backup has been changed to full"))
		a.Process(pgbrEntry("2026-01-10 02:00:46", "P00", "INFO: new backup label = 20260110-020001F"))
		a.Process(pgbrEntry("2026-01-10 02:00:47", "P00", "INFO: full backup size = 22MB, file total = 963"))
		a.Process(pgbrEntry("2026-01-10 02:00:47", "P00", "INFO: backup command end: completed successfully (46383ms)"))
		m := stanza(t, a, "demo")
		if len(m.Backups) != 1 {
			t.Fatalf("got %+v, want 1 backup", m.Backups)
		}
		if b := m.Backups[0]; b.Type != "full" || !b.ChangedToFull || b.RequestedType != tc.want {
			t.Errorf("begin%q: type %q, changed %v, requested %q; want full, true, %q", tc.opt, b.Type, b.ChangedToFull, b.RequestedType, tc.want)
		}
		if b := m.Backups[0]; b.Label != "20260110-020001F" || b.SizeRaw != "22MB" || b.FileTotal != 963 || !b.Succeeded || b.Duration != 46383*time.Millisecond {
			t.Errorf("backup = %+v, want label, size, file total, success and the end line's 46383ms", b)
		}
	}
}

// TestPgBackRestAnalyzer_BeginEndLIFOPairing pins that an end line closes
// the most recently begun run of its command (a stack): each label keeps
// the duration of its own run.
func TestPgBackRestAnalyzer_BeginEndLIFOPairing(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --exec-id=AAA"))
	a.Process(pgbrEntry("2026-01-14 09:10:01", "P00", "INFO: new backup label = LABEL-A"))
	a.Process(pgbrEntry("2026-01-14 09:10:05", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --exec-id=BBB"))
	a.Process(pgbrEntry("2026-01-14 09:10:06", "P00", "INFO: new backup label = LABEL-B"))
	a.Process(pgbrEntry("2026-01-14 09:10:10", "P00", "INFO: backup command end: completed successfully (100ms)"))
	a.Process(pgbrEntry("2026-01-14 09:10:20", "P00", "INFO: backup command end: completed successfully (200ms)"))
	durByLabel := map[string]time.Duration{}
	for _, b := range stanza(t, a, "demo").Backups {
		durByLabel[b.Label] = b.Duration
	}
	if len(durByLabel) != 2 || durByLabel["LABEL-A"] != 200*time.Millisecond || durByLabel["LABEL-B"] != 100*time.Millisecond {
		t.Errorf("durations = %v, want LABEL-A 200ms and LABEL-B 100ms (LIFO)", durByLabel)
	}
}

// TestPgBackRestAnalyzer_LockConflictOverlapPairedCorrectly pins S3's
// overlap: run B begins inside run A and fails at once on [050]; the
// success stays A's, the failure B's.
func TestPgBackRestAnalyzer_LockConflictOverlapPairedCorrectly(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	feed(a, "2026-01-14 13:32:24",
		"INFO: backup command begin 2.59.3: --stanza=demo --exec-id=A --type=full",
		"INFO: backup command begin 2.59.3: --stanza=demo --exec-id=B --type=full",
		"ERROR: [050]: unable to acquire lock on file '/tmp/pgbackrest/demo-backup-1.lock': Resource temporarily unavailable",
		"INFO: backup command end: aborted with exception [050]")
	feed(a, "2026-01-14 13:32:43",
		"INFO: new backup label = 20260114-133224F",
		"INFO: full backup size = 74.3MB, file total = 1278",
		"INFO: backup command end: completed successfully (19011ms)")
	var got []string
	for _, b := range stanza(t, a, "demo").Backups {
		got = append(got, fmt.Sprintf("ok=%v %q %v [%d]", b.Succeeded, b.Label, b.Duration, b.ErrorCode))
	}
	slices.Sort(got)
	if want := []string{`ok=false "" 0s [50]`, `ok=true "20260114-133224F" 19.011s [0]`}; !slices.Equal(got, want) {
		t.Errorf("backups = %q, want %q", got, want)
	}
}

// TestPgBackRestAnalyzer_KilledRunReportedAsCrashed pins that a run with
// no end line is a failed, crashed backup dated by its last line.
func TestPgBackRestAnalyzer_KilledRunReportedAsCrashed(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	feed(a, "2026-01-14 09:05:53",
		"INFO: backup command begin 2.60.0: --stanza=demo --exec-id=KILLED --type=diff",
		"WARN: option 'repo1-retention-full' is not set for 'repo1-retention-full-type=count', the repository may run out of space")
	m := stanza(t, a, "demo")
	if len(m.Backups) != 1 {
		t.Fatalf("got %+v, want the killed run", m.Backups)
	}
	if b := m.Backups[0]; b.Succeeded || !b.Crashed || !b.LastLine.Equal(mustParse(t, "2026-01-14 09:05:53")) || !b.End.IsZero() {
		t.Errorf("backup = %+v, want crashed, not succeeded, last line 09:05:53, no end", b)
	}
}

// TestPgBackRestAnalyzer_ArchivePushStats pins the push/failed counters
// and the stretch of a segment that fails twice, then is pushed.
func TestPgBackRestAnalyzer_ArchivePushStats(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: archive-push command begin 2.60.0: --stanza=demo"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: pushed WAL file '000000010000000000000001' to the archive"))
	a.Process(pgbrEntry("2026-01-14 09:00:02", "P00", "INFO: archive-push command end: completed successfully (2000ms)"))
	for _, ts := range []string{"2026-01-14 09:10:00", "2026-01-14 09:11:00"} {
		feed(a, ts, "INFO: archive-push command begin 2.60.0: --stanza=demo",
			"ERROR: [103]: unable to find a valid repository: repo1: [FileMissingError] archive.info missing '000000010000000000000010'",
			"INFO: archive-push command end: aborted with exception [103]")
	}
	a.Process(pgbrEntry("2026-01-14 09:30:00", "P00", "INFO: archive-push command begin 2.60.0: --stanza=demo"))
	a.Process(pgbrEntry("2026-01-14 09:30:01", "P00", "INFO: pushed WAL file '000000010000000000000010' to the archive"))
	a.Process(pgbrEntry("2026-01-14 09:30:01", "P00", "INFO: archive-push command end: completed successfully (1000ms)"))
	ap := stanza(t, a, "demo").ArchivePush
	if ap.Pushed != 2 || ap.Failed != 2 || len(ap.Failures) != 1 {
		t.Fatalf("pushed=%d failed=%d stretches=%+v, want 2, 2 and one stretch", ap.Pushed, ap.Failed, ap.Failures)
	}
	if f := ap.Failures[0]; f.Attempts != 2 || f.Code != 103 || !f.First.Equal(mustParse(t, "2026-01-14 09:10:00")) || !f.Last.Equal(mustParse(t, "2026-01-14 09:11:00")) {
		t.Errorf("stretch = %+v, want 2 attempts of [103] from 09:10:00 to 09:11:00", f)
	}
}

// TestPgBackRestAnalyzer_IssueGrouping_NormalizesQuotedTokens pins that
// two warnings differing only in a quoted token are one group of 2.
func TestPgBackRestAnalyzer_IssueGrouping_NormalizesQuotedTokens(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "WARN: dropped WAL file '000000010000000000000001' because archive queue exceeded 16MB"))
	a.Process(pgbrEntry("2026-01-14 09:11:00", "P00", "WARN: dropped WAL file '000000010000000000000002' because archive queue exceeded 16MB"))
	m := stanza(t, a, pgbrUnknownStanza)
	if len(m.Warnings) != 1 || m.Warnings[0].Count != 2 || len(m.ArchivePush.DroppedEvents) != 2 {
		t.Errorf("warnings = %+v, dropped = %d; want one group of 2 and 2 drops", m.Warnings, len(m.ArchivePush.DroppedEvents))
	}
}

// TestPgBackRestAnalyzer_observePGArchiveFailure pins PostgreSQL's
// LOG + DETAIL pair: stanza and segment from the archive_command, an exit
// code or a signal.
func TestPgBackRestAnalyzer_observePGArchiveFailure(t *testing.T) {
	for head, want := range map[string]string{
		"archive command failed with exit code 103":                       "exit=true/103 signal=false/0",
		"archive command was terminated by signal 11: Segmentation fault": "exit=false/0 signal=true/11",
	} {
		a := NewPgBackRestAnalyzer()
		pgArchiveFailure(a, mustParse(t, "2026-01-14 13:07:00"), head, "pgbackrest --stanza=demo  archive-push pg_wal/000000010000000000000010")
		m := stanza(t, a, "demo")
		if len(m.PGArchiveFailures) != 1 {
			t.Fatalf("%s: failures = %+v, want 1", head, m.PGArchiveFailures)
		}
		f := m.PGArchiveFailures[0]
		if got := fmt.Sprintf("exit=%v/%d signal=%v/%d", f.HasExit, f.ExitCode, f.IsSignal, f.Signal); got != want || f.Segment != "000000010000000000000010" {
			t.Errorf("%s: %s segment %q, want %s and the archive-push segment", head, got, f.Segment, want)
		}
	}
}

// TestPgBackRestAnalyzer_observePGArchiveFailure_MissingDetail pins that
// a failure with no DETAIL counts, with no stanza or segment, once the
// run shows pgBackRest, and not at all otherwise.
func TestPgBackRestAnalyzer_observePGArchiveFailure_MissingDetail(t *testing.T) {
	failure := func(a *PgBackRestAnalyzer) {
		pgArchiveFailure(a, mustParse(t, "2026-01-14 13:07:00"), "archive command failed with exit code 103", "")
		a.observePGArchiveFailure(pgLogEntry(mustParse(t, "2026-01-14 13:07:01"), "checkpoint starting: time", false))
	}
	alone := NewPgBackRestAnalyzer()
	failure(alone)
	if got := alone.Finalize(); len(got) != 0 {
		t.Errorf("without pgBackRest evidence the failure must not make a report, got %+v", got)
	}
	a := NewPgBackRestAnalyzer()
	a.Process(&parser.LogEntry{Timestamp: mustParse(t, "2026-01-14 13:07:00"), Message: "ERROR: [103]: unable to find a valid repository", FromPgBackRest: true, PID: "P00"})
	failure(a)
	if m := stanza(t, a, pgbrUnknownStanza); len(m.PGArchiveFailures) != 1 || m.PGArchiveFailures[0].Segment != "" {
		t.Fatalf("got %+v, want 1 failure with no segment", m.PGArchiveFailures)
	}
}

// TestPgBackRestAnalyzer_EmbeddedObserverFilesOnlyTouchedOnes pins that a
// stanza's Files lists only the input files that gave it a line.
func TestPgBackRestAnalyzer_EmbeddedObserverFilesOnlyTouchedOnes(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	for _, f := range []struct{ file, ts, stanza string }{
		{"fileA.log", "2026-01-14 09:00:00", "alpha"},
		{"fileB.log", "2026-01-14 09:05:00", "beta"},
	} {
		a.Process(sourceMarker(f.file, false))
		pgArchiveFailure(a, mustParse(t, f.ts), "archive command failed with exit code 103", "pgbackrest --stanza="+f.stanza+"  archive-push pg_wal/000000010000000000000001")
	}
	a.Process(sourceMarker("fileC.log", false))
	a.Process(pgLogEntry(mustParse(t, "2026-01-14 09:06:00"), "checkpoint starting: time", false))
	out := byStanza(a)
	if out["alpha"] == nil || out["beta"] == nil || !slices.Equal(out["alpha"].Files, []string{"fileA.log"}) || !slices.Equal(out["beta"].Files, []string{"fileB.log"}) {
		t.Errorf("got %v, want alpha from fileA.log only and beta from fileB.log only", out)
	}
}

// TestPgBackRestAnalyzer_LastFullAcrossFiles pins that the last full is
// the latest over every file of the stanza, named by the file names.
func TestPgBackRestAnalyzer_LastFullAcrossFiles(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	for _, f := range []struct{ file, day, label string }{
		{"demo-backup.log", "2026-01-10", "20260110-020001F"},
		{"demo-backup.log.1", "2026-01-12", "20260112-020001F"},
	} {
		a.Process(sourceMarker(f.file, false))
		feed(a, f.day+" 02:00:00", "INFO: backup command begin 2.60.0: --type=full", "INFO: new backup label = "+f.label,
			"INFO: backup command end: completed successfully (1000ms)")
	}
	result := a.Finalize()
	if len(result) != 1 || result[0].Stanza != "demo" || len(result[0].Backups) != 2 {
		t.Fatalf("got %+v, want one stanza 'demo' (from the file names) with 2 backups", result)
	}
	if b := result[0].Last["full"].Backup; b == nil || b.Label != "20260112-020001F" {
		t.Errorf("last full = %+v, want the later (01-12) backup", b)
	}
}

// TestPgBackRestAnalyzer_NoTimestampNoteNamesItsSource pins that the
// log-timestamp=n note survives a dated source and names the undated
// file, member, or "some lines"; a bare stderr line is not log-timestamp=n.
func TestPgBackRestAnalyzer_NoTimestampNoteNamesItsSource(t *testing.T) {
	for _, c := range []struct{ steps, want string }{
		// src/member: a source marker; D, U: dated, undated check run; B: a bare line.
		{"src:timed.log D src:notime.out U", "notime.out carries no timestamps"},
		{"src:s4.tar member:02/timed.out D member:02/notime.out U", "02/notime.out carries no timestamps"},
		{"src:all.log D U", "some lines of all.log carry no timestamps"},
		{"src:- D U", "some lines carry no timestamps"},
		{"D B", ""},
	} {
		a := NewPgBackRestAnalyzer()
		for _, step := range strings.Fields(c.steps) {
			kind, name, _ := strings.Cut(step, ":")
			switch kind {
			case "src", "member":
				a.Process(sourceMarker(name, kind == "member"))
			case "D", "U":
				ts := map[string]string{"D": "2026-01-10 02:00:00", "U": ""}[kind]
				feed(a, ts, "INFO: check command begin 2.59.3: --stanza=demo", "INFO: check command end: completed successfully")
			case "B":
				a.Process(pgbrEntry("", "", "ERROR: [103]: unable to find a valid repository"))
			}
		}
		m := stanza(t, a, "demo")
		note := ""
		for _, n := range m.MissingInfo {
			if strings.HasPrefix(n, "log-timestamp=n") {
				note = n
			}
		}
		if !m.HasTimestamps || (c.want == "") != (note == "") || !strings.Contains(note, c.want) {
			t.Errorf("%s: timestamps %v, note %q, want %q", c.steps, m.HasTimestamps, note, c.want)
		}
	}
}

// TestNormalizePgBackRestMessage pins the grouping placeholders: quoted
// tokens and backup labels (incr children too) replaced, paths shortened.
func TestNormalizePgBackRestMessage(t *testing.T) {
	for in, want := range map[string]string{
		"dropped WAL file '000000010000000000000001' because archive queue exceeded 16MB": "dropped WAL file '...' because archive queue exceeded 16MB",
		"unable to find backup '20260101-000000F_20260101-010000I' in the repository":     "unable to find backup '...' in the repository",
		"unable to find backup '20260102-000000F_20260102-020000I' in the repository":     "unable to find backup '...' in the repository",
		"unable to open /var/lib/pg/main/base/1/2: denied, see '/a/b/c/d'":                "unable to open .../1/2: denied, see '.../c/d'",
	} {
		if got := normalizePgBackRestMessage(in); got != want {
			t.Errorf("normalizePgBackRestMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPgBackRestAnalyzer_SyncAndAsyncGetOutcomesApart pins that sync and
// async archive-get finds and misses land in four different counters.
func TestPgBackRestAnalyzer_SyncAndAsyncGetOutcomesApart(t *testing.T) {
	for _, outcome := range []string{"found", "unable to find"} {
		a := NewPgBackRestAnalyzer()
		for i, cmd := range []string{"archive-get", "archive-get:async"} {
			feed(a, fmt.Sprintf("2026-01-14 09:0%d:00", i), "INFO: "+cmd+" command begin 2.60.0: --stanza=demo",
				fmt.Sprintf("INFO: %s 00000001000000000000000%d in the archive%s", outcome, i+1, []string{"", " asynchronously"}[i]),
				"INFO: "+cmd+" command end: completed successfully (100ms)")
		}
		ag := stanza(t, a, "demo").ArchiveGet
		if got := fmt.Sprintf("%d %d %d %d", ag.Found, ag.FoundAsync, ag.NotFound, ag.NotFoundAsync); got != map[string]string{"found": "1 1 0 0", "unable to find": "0 0 1 1"}[outcome] {
			t.Errorf("%s: found/async, not found/async = %s", outcome, got)
		}
	}
}

// TestPgBackRestAnalyzer_DroppedWALNotDoubleCountedAsPushedAsync pins
// that the "pushed ... asynchronously" line after a drop of the same
// segment is not a push, while another segment's push counts.
func TestPgBackRestAnalyzer_DroppedWALNotDoubleCountedAsPushedAsync(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: archive-push:async command begin 2.60.0: --stanza=demo"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "WARN: dropped WAL file '000000010000000000000001' because archive queue exceeded 16KB"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: pushed WAL file '000000010000000000000001' to the archive asynchronously"))
	a.Process(pgbrEntry("2026-01-14 09:00:02", "P00", "INFO: pushed WAL file '000000010000000000000002' to the archive asynchronously"))
	a.Process(pgbrEntry("2026-01-14 09:00:03", "P00", "INFO: archive-push:async command end: completed successfully (50ms)"))
	if ap := stanza(t, a, "demo").ArchivePush; len(ap.DroppedEvents) != 1 || ap.PushedAsync != 1 {
		t.Errorf("dropped = %d, pushed async = %d, want 1 and 1", len(ap.DroppedEvents), ap.PushedAsync)
	}
}

// TestDedupDropEvents_ToleranceBoundary pins the 5-minute edge between
// one drop seen twice and two drops (a literal, so a changed constant
// fails too).
func TestDedupDropEvents_ToleranceBoundary(t *testing.T) {
	t0 := mustParse(t, "2026-01-14 09:00:00")
	for gap, want := range map[time.Duration]int{5*time.Minute - time.Second: 1, 5*time.Minute + time.Second: 2} {
		events := []PgBackRestDropEvent{
			{Segment: "000000010000000000000001", Time: t0},
			{Segment: "000000010000000000000001", Time: t0.Add(gap)},
		}
		if got := dedupDropEvents(events); len(got) != want {
			t.Errorf("gap %v: %d events, want %d", gap, len(got), want)
		}
	}
}

// TestMergeFailureStretches pins the merge of one segment's stretches:
// overlapping or touching windows merge, Attempts the max of both
// readings (never the sum), and disjoint windows stay two outages.
func TestMergeFailureStretches(t *testing.T) {
	const seg = "000000010000000000000001"
	at := func(s string) time.Time { return mustParse(t, "2026-01-14 "+s) }
	st := func(first, last string, attempts int) []PgBackRestFailureStretch {
		return []PgBackRestFailureStretch{{Segment: seg, Code: 103, First: at(first), Last: at(last), Attempts: attempts}}
	}
	for _, c := range []struct {
		dst, src []PgBackRestFailureStretch
		want     string
	}{
		{st("09:00:00", "09:05:00", 5), st("09:00:00", "09:05:00", 3), "1: x5 until 09:05:00"},
		{st("09:00:00", "09:05:00", 3), st("09:00:00", "09:05:00", 5), "1: x5 until 09:05:00"},
		{st("09:00:00", "09:05:00", 4), st("09:05:00", "09:07:00", 2), "1: x4 until 09:07:00"},
		{st("09:00:00", "09:05:00", 4), st("09:30:00", "09:35:00", 2), "2: x4 until 09:05:00"},
	} {
		merged := mergeFailureStretches(c.dst, c.src)
		if got := fmt.Sprintf("%d: x%d until %s", len(merged), merged[0].Attempts, merged[0].Last.Format("15:04:05")); got != c.want {
			t.Errorf("merge %+v into %+v = %s, want %s", c.src, c.dst, got, c.want)
		}
	}
}

// TestAddArchiveCounts_FailedKeptSeparateFromFailedAsync pins that
// merging two stanzas' stats never adds Failed and FailedAsync together.
func TestAddArchiveCounts_FailedKeptSeparateFromFailedAsync(t *testing.T) {
	got := PgBackRestArchiveStats{Failed: 2, FailedAsync: 5}
	addArchiveCounts(&got, PgBackRestArchiveStats{Failed: 3, FailedAsync: 7, AsyncLogged: true})
	if got.Logged || !got.AsyncLogged || got.Failed != 5 || got.FailedAsync != 12 {
		t.Errorf("got %+v, want async logged only, Failed 5, FailedAsync 12", got)
	}
}

// TestPgBackRestAnalyzer_Classification pins how single lines and short
// runs are counted, one counter each.
func TestPgBackRestAnalyzer_Classification(t *testing.T) {
	for _, c := range []struct {
		name, stanza string
		lines        [][3]string // timestamp, process, message
		got          func(m *PgBackRestMetrics) string
		want         string
	}{
		{"[082] is a timeout, [050] is not", pgbrUnknownStanza, [][3]string{
			{"2026-01-14 09:00:00", "P00", "ERROR: [082]: WAL segment 000000010000000000000001 was not archived before the 60000ms timeout"},
			{"2026-01-14 09:01:00", "P00", "ERROR: [050]: unable to acquire lock on file '/tmp/pgbackrest/demo-backup-1.lock': Resource temporarily unavailable"},
		}, func(m *PgBackRestMetrics) string { return fmt.Sprint(m.ArchivePush.Timeouts) }, "1"},
		{"could not push is an async failure with a stretch", pgbrUnknownStanza, [][3]string{
			{"2026-01-14 09:00:00", "P00", couldNotPush("000000010000000000000001")},
		}, func(m *PgBackRestMetrics) string {
			s := fmt.Sprintf("%d %d", m.ArchivePush.FailedAsync, m.ArchivePush.Failed)
			for _, f := range m.ArchivePush.Failures {
				s += " " + f.Segment
			}
			return s
		}, "1 0 000000010000000000000001"},
		{"async get failure counted apart", "main", [][3]string{
			{"2026-01-14 09:00:00", "P00", "INFO: archive-get:async command begin 2.60.0: [000000010000000000000002] --stanza=main"},
			{"2026-01-14 09:00:01", "P00", "ERROR: [103]: unable to find a valid repository"},
			{"2026-01-14 09:00:01", "P00", "INFO: archive-get:async command end: aborted with exception [103]"},
		}, func(m *PgBackRestMetrics) string {
			return fmt.Sprintf("%d %d", m.ArchiveGet.FailedAsync, m.ArchiveGet.Failed)
		}, "1 0"},
		{"a remote worker's begin/end is no backup", "demo", [][3]string{
			{"2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"},
			{"2026-01-14 09:00:01", "P00", "INFO: new backup label = 20260114-090000F"},
			{"2026-01-14 09:00:02", "P01", "INFO: backup:remote command begin 2.60.0: --stanza=demo --process=1"},
			{"2026-01-14 09:00:02", "P01", "INFO: backup:remote command end: completed successfully (50ms)"},
			{"2026-01-14 09:00:03", "P00", "INFO: full backup size = 10MB, file total = 100"},
			{"2026-01-14 09:00:04", "P00", "INFO: backup command end: completed successfully (3000ms)"},
		}, func(m *PgBackRestMetrics) string { return fmt.Sprint(len(m.Backups)) }, "1"},
		{"another end wording is a failure", "main", [][3]string{
			{"2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"},
			{"2026-01-14 09:00:05", "P00", "INFO: backup command end: something unexpected"},
		}, func(m *PgBackRestMetrics) string {
			s := ""
			for _, b := range m.Backups {
				s += fmt.Sprintf("ok=%v crashed=%v running=%v; ", b.Succeeded, b.Crashed, b.InProgress)
			}
			for _, c := range m.Commands {
				s += fmt.Sprintf("%d failed", c.Failed)
			}
			return s
		}, "ok=false crashed=false running=false; 1 failed"},
		{"an undated issue's first date comes from a dated one", pgbrUnknownStanza, [][3]string{
			{"", "", "ERROR: [046]: unexpected control version = 1700 and catalog version = 202406281"},
			{"2026-01-14 09:10:00", "", "ERROR: [046]: unexpected control version = 1700 and catalog version = 202406281"},
		}, func(m *PgBackRestMetrics) string {
			s := ""
			for _, e := range m.Errors {
				s += fmt.Sprintf("x%d %s;", e.Count, e.First.Format(time.DateTime))
			}
			return s
		}, "x2 2026-01-14 09:10:00;"},
	} {
		a := NewPgBackRestAnalyzer()
		for _, l := range c.lines {
			a.Process(pgbrEntry(l[0], l[1], l[2]))
		}
		if got := c.got(stanza(t, a, c.stanza)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// TestPgBackRestAnalyzer_CouldNotRestoreFileCountsOnPGSide pins that
// PostgreSQL's "could not restore file" counts in PGRestoreFailures.
func TestPgBackRestAnalyzer_CouldNotRestoreFileCountsOnPGSide(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	ts := mustParse(t, "2026-01-14 09:00:00")
	a.Process(&parser.LogEntry{Timestamp: ts, Message: "INFO: archive-get command begin 2.60.0: [000000010000000000000001] --stanza=demo", FromPgBackRest: true, PID: "P00"})
	a.observePGArchiveFailure(pgLogEntry(ts, `could not restore file "000000010000000000000001" from archive`, false))
	if m := stanza(t, a, "demo"); m.PGRestoreFailures != 1 {
		t.Errorf("PGRestoreFailures = %d, want 1", m.PGRestoreFailures)
	}
}

// TestPgBackRestSegmentExtraction pins the segment read from a begin
// line's bracket (one file, not a batch) and the unquoted hex word of 16
// characters or more.
func TestPgBackRestSegmentExtraction(t *testing.T) {
	for _, c := range []struct {
		fn       func(string) string
		in, want string
	}{
		{extractBeginSegment, "[000000010000000000000001,000000010000000000000002] --stanza=demo", ""},
		{extractBeginSegment, "[pg_wal/000000010000000000000001] --stanza=demo", "000000010000000000000001"},
		{extractSegmentHint, "unable to push WAL file 000000010000000000000003: timeout", "000000010000000000000003"},
		{extractSegmentHint, "path 0000000100000000 is missing", "0000000100000000"},
		{extractSegmentHint, "value 000000010000000 is short", ""},
		{extractSegmentHint, "unable to push '000000010000000000000004'", "000000010000000000000004"},
	} {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPgBackRestAnalyzer_LinesGoToTheirStanza pins the per-stanza line
// count of one stream holding two stanzas: banners, signals and blank
// lines count for the invocation they belong to.
func TestPgBackRestAnalyzer_LinesGoToTheirStanza(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	banner := pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true})
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=alpha"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: backup command end: completed successfully (10ms)"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{BlankLines: 2}))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 09:01:00", "P00", "INFO: backup command begin 2.60.0: --stanza=beta"))
	a.Process(pgbrEntry("2026-01-14 09:01:01", "P00", "WARN: option 'repo1-retention-full' is not set\n       for 'repo1-retention-full-type=count', beware"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGTERM"}))
	out := byStanza(a)
	// alpha: banner, begin, end, 2 blank lines; beta: banner, begin, WARN and its continuation, signal.
	if len(out) != 2 || out["alpha"] == nil || out["beta"] == nil || out["alpha"].LinesRead != 5 || out["beta"].LinesRead != 5 {
		t.Fatalf("got %v, want alpha and beta with 5 lines each", out)
	}
}

// TestPgBackRestAnalyzer_ExpireRepoAndWarnings pins an expire run's
// repository (--repo=N, "all" for several configured, else repo1) and
// warnings (its WARNs and the retention-archive INFO).
func TestPgBackRestAnalyzer_ExpireRepoAndWarnings(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	feed(a, "2026-01-10 02:00:00", "INFO: expire command begin 2.59.3: --repo1-path=/repo1 --repo2-path=/repo2 --stanza=main",
		"INFO: repo1: expire adhoc backup set 20260110-000000F", "INFO: repo2: expire adhoc backup set 20260110-000001F")
	a.Process(pgbrEntry("2026-01-10 02:00:01", "P00", "INFO: expire command end: completed successfully (10ms)"))
	feed(a, "2026-01-10 02:01:00", "INFO: expire command begin 2.59.3: --repo=2 --repo1-path=/repo1 --repo2-path=/repo2 --set=20260110-000001F --stanza=main",
		"INFO: option 'repo2-retention-archive' is not set - archive logs will not be expired",
		"WARN: repo2: expiring latest backup 20260110-000001F - the ability to perform point-in-time-recovery (PITR) may be affected")
	a.Process(pgbrEntry("2026-01-10 02:01:01", "P00", "INFO: expire command end: completed successfully (10ms)"))
	a.Process(pgbrEntry("2026-01-10 02:02:00", "P00", "INFO: expire command begin 2.59.3: --repo1-path=/repo1 --stanza=main"))
	a.Process(pgbrEntry("2026-01-10 02:02:01", "P00", "INFO: expire command end: completed successfully (10ms)"))
	a.Process(pgbrEntry("2026-01-10 02:03:00", "P00", "INFO: backup command begin 2.59.3: --repo1-path=/repo1 --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-10 02:03:01", "P00", "WARN: a backup warning"))
	runs := stanza(t, a, "main").ExpireRuns
	if len(runs) != 3 {
		t.Fatalf("got %d expire runs, want 3: %+v", len(runs), runs)
	}
	if runs[0].Repo != "all" || runs[1].Repo != "repo2" || runs[2].Repo != "repo1" {
		t.Errorf("repos = %q, %q, %q, want all, repo2, repo1", runs[0].Repo, runs[1].Repo, runs[2].Repo)
	}
	want := []string{
		"option 'repo2-retention-archive' is not set - archive logs will not be expired",
		"repo2: expiring latest backup 20260110-000001F - the ability to perform point-in-time-recovery (PITR) may be affected",
	}
	if !slices.Equal(runs[1].Warnings, want) || runs[0].Warnings != nil || runs[2].Warnings != nil {
		t.Errorf("warnings = %q / %q / %q, want %q on the second run only", runs[0].Warnings, runs[1].Warnings, runs[2].Warnings, want)
	}
}

// TestPgBackRestAnalyzer_SingleAnalyzerOrdersOperationsAndIssueCommands
// pins that files read out of time order give chronological operations
// and expire runs, and sorted issue commands.
func TestPgBackRestAnalyzer_SingleAnalyzerOrdersOperationsAndIssueCommands(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	for _, run := range []struct{ begin, end, cmd, failure string }{
		{"2026-01-14 10:00:00", "2026-01-14 10:00:01", "expire", ""},
		{"2026-01-14 09:30:00", "2026-01-14 09:30:01", "restore", ""},
		{"2026-01-14 09:00:00", "2026-01-14 09:00:01", "stanza-create", "ERROR: [056]: unable to find primary cluster - cannot proceed"},
		{"2026-01-14 09:10:00", "2026-01-14 09:10:01", "backup --type=full", "ERROR: [056]: unable to find primary cluster - cannot proceed"},
		{"2026-01-14 09:20:00", "2026-01-14 09:20:01", "expire", ""},
	} {
		cmd, opt, _ := strings.Cut(run.cmd, " ")
		a.Process(pgbrEntry(run.begin, "P00", strings.TrimSpace("INFO: "+cmd+" command begin 2.60.0: --stanza=main "+opt)))
		end := "completed successfully (10ms)"
		if run.failure != "" {
			a.Process(pgbrEntry(run.begin, "P00", run.failure))
			end = "aborted with exception [056]"
		}
		a.Process(pgbrEntry(run.end, "P00", "INFO: "+cmd+" command end: "+end))
	}
	m := stanza(t, a, "main")
	if !slices.IsSortedFunc(m.Operations, func(x, y PgBackRestOperation) int { return x.Time.Compare(y.Time) }) {
		t.Errorf("operations not chronological: %+v", m.Operations)
	}
	if len(m.ExpireRuns) != 2 || !m.ExpireRuns[0].Time.Before(m.ExpireRuns[1].Time) {
		t.Errorf("expire runs not chronological: %+v", m.ExpireRuns)
	}
	if len(m.Errors) != 1 || strings.Join(m.Errors[0].Commands, ",") != "backup,stanza-create" {
		t.Errorf("errors = %+v, want one group naming backup,stanza-create (sorted)", m.Errors)
	}
}

// TestPgBackRestAnalyzer_SignalNamesWhyRunCrashed pins that a signal line
// seen while a run is open is kept on the crashed run; a run that just
// stops keeps no signal.
func TestPgBackRestAnalyzer_SignalNamesWhyRunCrashed(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=diff"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGTERM"}))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=incr"))
	var got []string
	for _, b := range stanza(t, a, "main").Backups {
		got = append(got, fmt.Sprintf("crashed=%v %q", b.Crashed, b.Signal))
	}
	if want := []string{`crashed=true "SIGTERM"`, `crashed=true ""`}; !slices.Equal(got, want) {
		t.Errorf("backups = %q, want %q", got, want)
	}
}

// TestPgBackRestAnalyzer_FileNameBeatsLastNamedStanza pins that a line
// naming no stanza goes to the stanza of its file name, not the last named.
func TestPgBackRestAnalyzer_FileNameBeatsLastNamedStanza(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	banner := pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true})
	a.Process(sourceMarker("beta-backup.log", false))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=alpha"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: backup command end: completed successfully (10ms)"))
	a.Process(banner)
	a.Process(pgbrEntry("2026-01-14 09:01:00", "P00", "WARN: a line that names no stanza"))
	out := byStanza(a)
	if out["beta"] == nil || len(out["beta"].Warnings) != 1 || (out["alpha"] != nil && len(out["alpha"].Warnings) != 0) {
		t.Errorf("got %v, want the WARN under beta, the file's stanza, alone", keysOf(out))
	}
}

// TestPgBackRestAnalyzer_WindowBeginInclusive pins that a line dated
// exactly --begin is inside the window.
func TestPgBackRestAnalyzer_WindowBeginInclusive(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.SetTimeWindow(mustParse(t, "2026-01-14 09:00:00"), time.Time{})
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:05", "P00", "INFO: backup command end: completed successfully (5000ms)"))
	if m := stanza(t, a, "main"); len(m.Backups) != 1 || !m.Backups[0].Succeeded || m.Window.CutBefore {
		t.Errorf("got %+v, want the backup that begins at --begin, nothing cut before", m)
	}
}

// TestPgBackRestAnalyzer_AsyncWorkerPushes pins the async pushes: the
// worker's successful batches without what it dropped, a segment the
// sync call also reported counted once, one only it reported counted too.
func TestPgBackRestAnalyzer_AsyncWorkerPushes(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(sourceMarker("main-archive-push-async.log", false))
	asyncBatch(a, "main", "2026-01-14 09:00:00", "2026-01-14 09:00:01", "1 WAL file(s) to archive: 000000010000000000000013",
		"WARN: dropped WAL file '000000010000000000000013' because archive queue exceeded 16KB")
	feed(a, "2026-01-14 09:01:00", "INFO: archive-push:async command begin 2.60.0: [/pg/pg_wal] --stanza=main",
		"INFO: push 2 WAL file(s) to archive: 000000010000000000000014...000000010000000000000015",
		"ERROR: [103]: unable to find a valid repository", "INFO: archive-push:async command end: aborted with exception [103]")
	asyncBatch(a, "main", "2026-01-14 09:02:00", "2026-01-14 09:02:01", "3 WAL file(s) to archive: 000000010000000000000014...000000010000000000000016")
	if got := stanza(t, a, "main").ArchivePush.PushedAsync; got != 3 {
		t.Errorf("worker log alone: PushedAsync = %d, want 3", got)
	}

	b := NewPgBackRestAnalyzer()
	b.Process(sourceMarker("main-archive-push-async.log", false))
	asyncBatch(b, "main", "2026-01-14 09:02:00", "2026-01-14 09:02:01", "2 WAL file(s) to archive: 000000010000000000000014...000000010000000000000015")
	b.Process(sourceMarker("postgresql.log", false))
	for _, seg := range []string{"000000010000000000000014", "000000010000000000000015", "000000010000000000000017"} {
		feed(b, "2026-01-14 09:02:01", "INFO: archive-push command begin 2.60.0: [pg_wal/"+seg+"] --stanza=main",
			"INFO: pushed WAL file '"+seg+"' to the archive asynchronously", "INFO: archive-push command end: completed successfully")
	}
	if got := stanza(t, b, "main").ArchivePush.PushedAsync; got != 3 {
		t.Errorf("worker and synchronous call: PushedAsync = %d, want 3 (14 and 15 once, 17)", got)
	}
}

// TestPgBackRestAnalyzer_FailedRunDuration pins that a failed run is timed
// by its begin and end lines and counts in its command's total and max;
// a killed run has neither an end nor a duration.
func TestPgBackRestAnalyzer_FailedRunDuration(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"))
	feed(a, "2026-01-14 09:01:00", "ERROR: [082]: WAL segment 000000010000000000000003 was not archived before the 60000ms timeout",
		"INFO: backup command end: aborted with exception [082]")
	a.Process(pgbrEntry("2026-01-14 09:02:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:02:10", "P00", "INFO: backup command end: completed successfully (10000ms)"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:03:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=full"))
	m := stanza(t, a, "demo")
	if len(m.Backups) != 3 {
		t.Fatalf("got %+v, want 3 backups", m.Backups)
	}
	if b := m.Backups[0]; !b.HasDuration || b.Duration != time.Minute {
		t.Errorf("failed run: duration %v (known %v), want 1m from its begin and end lines", b.Duration, b.HasDuration)
	}
	if b := m.Backups[2]; b.HasDuration || !b.End.IsZero() {
		t.Errorf("killed run: duration %v (known %v), end %v; want neither", b.Duration, b.HasDuration, b.End)
	}
	if cs := m.Commands[0]; cs.TotalDuration != 70*time.Second || cs.MaxDuration != time.Minute {
		t.Errorf("backup total %v, max %v; want 1m10s and 1m", cs.TotalDuration, cs.MaxDuration)
	}
}

// TestPgBackRestAnalyzer_KilledWhileWaitingForCheckpoint pins that a
// killed backup whose last line waits for a checkpoint names it, and
// that a later line clears it.
func TestPgBackRestAnalyzer_KilledWhileWaitingForCheckpoint(t *testing.T) {
	const next, immediate = "INFO: execute backup start: backup begins after the next regular checkpoint completes",
		"INFO: execute backup start: backup begins after the requested immediate checkpoint completes"
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{[]string{next}, "next regular checkpoint"},
		{[]string{immediate}, "requested immediate checkpoint"},
		{[]string{immediate, "INFO: check archive for prior segment 000000010000000000000011"}, ""},
	} {
		a := NewPgBackRestAnalyzer()
		a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=demo --type=diff"))
		feed(a, "2026-01-14 09:00:01", tc.lines...)
		a.Process(pgbrMarker(parser.PgBackRestLineInfo{Signal: "SIGTERM"}))
		if m := stanza(t, a, "demo"); len(m.Backups) != 1 || !m.Backups[0].Crashed || m.Backups[0].WaitingFor != tc.want {
			t.Errorf("last line %q: got %+v, want a killed backup waiting for %q", tc.lines[len(tc.lines)-1], m.Backups, tc.want)
		}
	}
}

// TestPgBackRestAnalyzer_IssuesGroupedByCause pins that two errors with
// one code and message but different causes stay apart, in one source
// and across merged sources, while the same cause groups.
func TestPgBackRestAnalyzer_IssuesGroupedByCause(t *testing.T) {
	const open = "ERROR: [103]: unable to find a valid repository:\nrepo1: [FileOpenError] unable to load info file '/var/lib/pgbackrest/repo1/archive/main/archive.info'"
	const missing = "ERROR: [103]: unable to find a valid repository:\nrepo1: [FileMissingError] unable to load info file '/doesnotexist/archive/main/archive.info'"
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: archive-push command begin 2.60.0: [pg_wal/000000010000000000000001] --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", open))
	a.Process(pgbrEntry("2026-01-14 09:00:02", "P00", open))
	a.Process(pgbrEntry("2026-01-14 09:00:03", "P00", missing))
	m := stanza(t, a, "main")
	if len(m.Errors) != 2 || m.Errors[0].Count != 2 || m.Errors[1].Count != 1 || !strings.Contains(m.Errors[1].RepoLine, "FileMissingError") {
		t.Fatalf("got %+v, want FileOpenError x2 and FileMissingError x1 apart", m.Errors)
	}
	merged := mergeIssues(m.Errors[:1], []PgBackRestIssue{m.Errors[1], m.Errors[0]})
	if len(merged) != 2 || merged[0].Count != 4 || merged[1].Count != 1 {
		t.Errorf("merged %+v, want the same cause summed and the other kept apart", merged)
	}
}

// TestRecomputeLastBackups_StartOrderLastByEnd pins that backups are
// listed by start, while the last backup of a type is the one that ended
// last.
func TestRecomputeLastBackups_StartOrderLastByEnd(t *testing.T) {
	at := func(s string) time.Time { return mustParse(t, "2026-01-14 "+s) }
	m := PgBackRestMetrics{Backups: []PgBackRestBackup{
		{Label: "B", Type: "full", Start: at("09:10:00"), End: at("09:15:00"), Succeeded: true},
		{Label: "A", Type: "full", Start: at("09:00:00"), End: at("09:30:00"), Succeeded: true},
		{Label: "C", Type: "incr", Start: at("09:10:00"), End: at("09:11:00"), Succeeded: false},
	}}
	recomputeLastBackups(&m)
	var order []string
	for _, b := range m.Backups {
		order = append(order, b.Label)
	}
	if b := m.Last["full"].Backup; strings.Join(order, ",") != "A,B,C" || b == nil || b.Label != "A" {
		t.Errorf("order %v, last full %+v; want A,B,C (by start, stable) and A, the one that ended last", order, b)
	}
}

// TestPgBackRestAnalyzer_RetryWarningAloneMakesNoReport pins that
// PostgreSQL's "failed too many times" alone makes no pgBackRest report.
func TestPgBackRestAnalyzer_RetryWarningAloneMakesNoReport(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.observePGArchiveFailure(pgLogEntry(mustParse(t, "2026-01-14 09:00:00"), `archiving write-ahead log file "000000010000000000000001" failed too many times, will try again later`, false))
	if got := a.Finalize(); len(got) != 0 {
		t.Errorf("got %+v, want no report", got)
	}
}

// TestPgBackRestAnalyzer_BannersOnlyStanzaIsKept pins that a file of
// banners alone keeps its stanza, with a note: log-level-file hides all.
func TestPgBackRestAnalyzer_BannersOnlyStanzaIsKept(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	banner := pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true})
	a.Process(sourceMarker("main-backup.log", false))
	a.Process(banner)
	a.Process(banner)
	if m := stanza(t, a, "main"); m.LinesRead != 2 || len(m.MissingInfo) == 0 {
		t.Errorf("got %+v, want stanza main with its 2 banner lines and a note", m)
	}
}

// TestPgBackRestSetSummary pins how an operation's pending summary
// evolves: the latest line wins, lines naming two different repos are
// both kept, and a line naming no repo replaces one that names a repo.
func TestPgBackRestSetSummary(t *testing.T) {
	const on1, on2, bare = "stanza-create for stanza 'main' on repo1", "stanza-create for stanza 'main' on repo2", "stanza-create for stanza 'main'"
	const exists = "stanza 'main' already exists on repo1 and is valid"
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{[]string{on1, on2}, on1 + "; " + on2},
		{[]string{on1, exists}, exists},
		{[]string{on1, bare}, bare},
		{[]string{bare, on1}, on1},
	} {
		st := newPgbrStanza("main")
		for _, l := range tc.lines {
			st.setSummary("stanza-create", l)
		}
		if got := st.pendingSummary["stanza-create"]; got != tc.want {
			t.Errorf("%q: summary %q, want %q", tc.lines, got, tc.want)
		}
	}
}

// TestPgBackRestOperationWithoutEndGetsNoSummary pins that an operation
// killed before its end, or running at --end, does not take the success
// summary its lines left pending; the next run that ends does.
func TestPgBackRestOperationWithoutEndGetsNoSummary(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: stanza-create command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: stanza-create for stanza 'main' on repo1"))
	a.Process(pgbrMarker(parser.PgBackRestLineInfo{ProcessStart: true}))
	a.Process(pgbrEntry("2026-01-14 09:01:00", "P00", "INFO: check command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:01:01", "P00", "INFO: check command end: completed successfully (5ms)"))
	m := stanza(t, a, "main")
	if len(m.Operations) != 2 || m.Operations[0].Command != "stanza-create" || !m.Operations[0].Crashed || m.Operations[0].Summary != "" {
		t.Errorf("operations = %+v, want the killed stanza-create with no summary, and the check", m.Operations)
	}

	a = NewPgBackRestAnalyzer()
	a.SetTimeWindow(time.Time{}, mustParse(t, "2026-01-14 09:05:00"))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: stanza-create command begin 2.60.0: --stanza=main"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: stanza-create for stanza 'main' on repo1"))
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: stanza-create command end: completed successfully (5ms)"))
	if m := stanza(t, a, "main"); len(m.Operations) != 1 || m.Operations[0].HasOutcome || m.Operations[0].Crashed || m.Operations[0].Summary != "" {
		t.Errorf("operations = %+v, want one stanza-create still running with no summary", m.Operations)
	}
}

// TestPgBackRestExpireRemovedLabels pins that "remove expired backup X"
// lines name the backups an expire run removed, and are ignored outside
// one.
func TestPgBackRestExpireRemovedLabels(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 08:59:00", "P00", "INFO: repo1: remove expired backup 20260101-000000F"))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: expire command begin 2.60.0: --stanza=main"))
	feed(a, "2026-01-14 09:00:01", "INFO: repo1: remove expired backup 20260110-000000F_20260111-000000I", "INFO: repo1: remove expired backup 20260110-000000F")
	a.Process(pgbrEntry("2026-01-14 09:00:02", "P00", "INFO: expire command end: completed successfully (9ms)"))
	want := []string{"20260110-000000F_20260111-000000I", "20260110-000000F"}
	if m := stanza(t, a, "main"); len(m.ExpireRuns) != 1 || !slices.Equal(m.ExpireRuns[0].Expired, want) {
		t.Errorf("expire runs = %+v, want one that removed %q", m.ExpireRuns, want)
	}
}

// TestRetentionWarnings pins the grouping of the expire runs' warnings:
// one group per text, counted over every run, distinct repos, widened span,
// in order of first run.
func TestRetentionWarnings(t *testing.T) {
	at := func(s string) time.Time { ts, _ := time.Parse("15:04:05", s); return ts }
	a, b := "option 'repo1-retention-archive' is not set", "option 'repo2-retention-archive' is not set"
	got := retentionWarnings([]PgBackRestExpireRun{
		{Time: at("09:00:00"), Repo: "repo1", Warnings: []string{a}},
		{Time: at("09:10:00"), Repo: "repo2", Warnings: []string{b, a}},
		{Time: at("09:20:00"), Repo: "repo1", Warnings: []string{a}},
		{Time: at("09:30:00"), Repo: "repo1"},
	})
	want := []PgBackRestRetentionWarning{
		{Text: a, Count: 3, Repos: []string{"repo1", "repo2"}, First: at("09:00:00"), Last: at("09:20:00")},
		{Text: b, Count: 1, Repos: []string{"repo2"}, First: at("09:10:00"), Last: at("09:10:00")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("retentionWarnings = %+v, want %+v", got, want)
	}
}

// TestPgFailureGroups pins the grouping of PostgreSQL's archive_command
// failures: by segment and cause, spans widened, groups in time order.
func TestPgFailureGroups(t *testing.T) {
	at := func(s string) time.Time { ts, _ := time.Parse("15:04:05", s); return ts }
	got := pgFailureGroups([]PgBackRestPGArchiveFailure{
		{Segment: "A", HasExit: true, ExitCode: 1, Time: at("10:00:02")},
		{Segment: "B", HasExit: true, ExitCode: 1, Time: at("10:00:05")},
		{Segment: "B", HasExit: true, ExitCode: 1, Time: at("10:00:01")},
		{Segment: "B", IsSignal: true, Signal: 9, Time: at("10:00:03")},
		{Segment: "A", Time: at("10:00:04")},
	})
	want := []PgBackRestPGFailureGroup{
		{Segment: "B", Cause: "exit 1", Count: 2, First: at("10:00:01"), Last: at("10:00:05")},
		{Segment: "A", Cause: "exit 1", Count: 1, First: at("10:00:02"), Last: at("10:00:02")},
		{Segment: "B", Cause: "signal 9", Count: 1, First: at("10:00:03"), Last: at("10:00:03")},
		{Segment: "A", Cause: "exit unknown", Count: 1, First: at("10:00:04"), Last: at("10:00:04")},
	}
	if !slices.Equal(got, want) {
		t.Errorf("groups = %+v\nwant %+v", got, want)
	}
}

// TestPgBackRestUnquotedTokensGroup pins that the same warning on two
// backups is one row: unquoted backup labels are replaced.
func TestPgBackRestUnquotedTokensGroup(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "WARN: resumable backup 20260114-090000F of same type exists -- invalid files will be removed then the backup will resume"))
	a.Process(pgbrEntry("2026-01-14 10:00:00", "P00", "WARN: resumable backup 20260114-100000F_20260114-100500D of same type exists -- invalid files will be removed then the backup will resume"))
	a.Process(pgbrEntry("2026-01-14 10:01:00", "P00", "WARN: invalid page checksum found in file /var/lib/pg/main/base/16384/16411 at page 5"))
	if m := stanza(t, a, ""); len(m.Warnings) != 2 || m.Warnings[0].Count != 2 || m.Warnings[0].Message != "resumable backup ... of same type exists -- invalid files will be removed then the backup will resume" {
		t.Errorf("warnings = %+v, want the two resumable-backup lines as one row", m.Warnings)
	}
}

// TestPgBackRestBackupsLogged pins when backups count as logged: a begin
// or end line of a command other than archive-push/archive-get, which
// only pgBackRest's own logs hold.
func TestPgBackRestBackupsLogged(t *testing.T) {
	for cmd, want := range map[string]bool{"stanza-create": true, "backup": true, "archive-push": false, "archive-get:async": false} {
		a := NewPgBackRestAnalyzer()
		a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: "+cmd+" command begin 2.60.0: --stanza=main"))
		a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: "+cmd+" command end: completed successfully (5ms)"))
		if got := stanza(t, a, "main").BackupsLogged; got != want {
			t.Errorf("%s: BackupsLogged = %v, want %v", cmd, got, want)
		}
	}
}

// TestPgBackRestAgeAtInputEnd pins that a backup's age is measured to the
// last timestamp of the whole input, PostgreSQL lines included, and that
// a report of lines naming no stanza has Stanza "".
func TestPgBackRestAgeAtInputEnd(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	a.Process(pgbrEntry("2026-01-14 08:00:00", "P00", "WARN: [104]: before any stanza"))
	a.Process(pgbrEntry("2026-01-14 09:00:00", "P00", "INFO: backup command begin 2.60.0: --stanza=main --type=full"))
	a.Process(pgbrEntry("2026-01-14 09:00:01", "P00", "INFO: new backup label = 20260114-090000F"))
	a.Process(pgbrEntry("2026-01-14 09:10:00", "P00", "INFO: backup command end: completed successfully (5ms)"))
	a.noteInputTime(mustParse(t, "2026-01-14 12:10:00"))
	if _, age, ok := stanza(t, a, "main").LastOf("full"); !ok || age != 3*time.Hour {
		t.Errorf("last full age %v (%v), want 3h, to the end of the input", age, ok)
	}
	b := NewPgBackRestAnalyzer()
	b.Process(pgbrEntry("2026-01-14 08:00:00", "", "ERROR: [103]: unable to find a valid repository"))
	if r := b.Finalize(); len(r) != 1 || r[0].Stanza != "" || PgBackRestStanzaName(r[0].Stanza) != PgBackRestUnnamedStanza {
		t.Errorf("reports = %+v, want one with Stanza \"\"", r)
	}
}

// TestPgbrStanzaAsyncPushTimesCoverage pins which synchronously reported
// segments the worker batches cover: overlapping, adjacent, nested and
// inverted batches, bounds, gaps, other name lengths, an empty name.
func TestPgbrStanzaAsyncPushTimesCoverage(t *testing.T) {
	seg := func(i int) string { return fmt.Sprintf("00000001%016X", i) }
	for _, c := range []struct {
		ranges []pgbrPushRange
		segs   []string
		want   int
	}{
		{[]pgbrPushRange{
			{first: seg(10), last: seg(14), n: 5},
			{first: seg(12), last: seg(20), n: 9}, // overlaps the first
			{first: seg(21), last: seg(22), n: 2}, // adjacent to the second
			{first: seg(30), last: seg(30), n: 1},
			{first: seg(40), last: seg(35), n: 0}, // inverted: covers nothing
		}, []string{
			seg(10), seg(17), seg(20), seg(21), seg(22), seg(30), // covered
			seg(9), seg(25), seg(31), seg(37), seg(50), // uncovered
			seg(15)[1:], "", // other length, empty
		}, 17 + 7},
		// Nested in the first: the merged span keeps the outer end.
		{[]pgbrPushRange{{first: seg(10), last: seg(20), n: 11}, {first: seg(12), last: seg(14), n: 3}}, []string{seg(17)}, 14},
		{[]pgbrPushRange{{first: seg(10), last: seg(20), n: 11}}, []string{seg(15) + ".partial"}, 12},
	} {
		st := &pgbrStanza{asyncPushRanges: c.ranges}
		for _, sg := range c.segs {
			st.asyncPushedSegs = append(st.asyncPushedSegs, pgbrAsyncSeg{seg: sg})
		}
		if got := len(st.asyncPushTimes()); got != c.want {
			t.Errorf("segments %q: async pushes = %d, want %d", c.segs, got, c.want)
		}
	}
}

// TestPgBackRestAnalyzer_PushRecovery pins LastPush and each push
// failure stretch's recovery: by the first later successful push of the
// same or a later segment, sync or through the async worker's batch.
func TestPgBackRestAnalyzer_PushRecovery(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	pushRun(a, "2026-01-14 09:10:00", "demo", "000000010000000000000010", false)
	pushRun(a, "2026-01-14 09:11:00", "demo", "000000010000000000000010", false)
	pushRun(a, "2026-01-14 09:30:01", "demo", "000000010000000000000010", true)
	pushRun(a, "2026-01-14 09:40:00", "demo", "000000010000000000000011", false)
	asyncBatch(a, "demo", "2026-01-14 09:45:00", "2026-01-14 09:45:02", "3 WAL file(s) to archive: 000000010000000000000011...000000010000000000000013")
	pushRun(a, "2026-01-14 09:50:00", "demo", "000000010000000000000014", false)

	ap := stanza(t, a, "demo").ArchivePush
	if ap.LastPush == nil || ap.LastPush.Segment != "000000010000000000000013" || ap.LastPush.Mode != "async" {
		t.Fatalf("LastPush = %+v, want the async batch ending at ...13", ap.LastPush)
	}
	var got []string
	for _, f := range ap.Failures {
		by := ""
		if f.RecoveredBy != nil {
			by = f.RecoveredBy.Segment + " " + f.RecoveredBy.Time.Format("15:04:05")
		}
		got = append(got, f.Recovery+" "+by)
	}
	want := []string{PgBackRestRecovered + " 000000010000000000000010 09:30:01", PgBackRestRecovered + " 000000010000000000000013 09:45:02", PgBackRestStillFailing + " "}
	if !slices.Equal(got, want) {
		t.Errorf("stretches = %q, want %q", got, want)
	}
}

// TestPgBackRestAnalyzer_NoLaterPush pins a stretch with no later push
// for its stanza: no_later_push only when another stanza pushed its
// segment after its last attempt and the verdict is known (no --end cut).
func TestPgBackRestAnalyzer_NoLaterPush(t *testing.T) {
	for _, c := range []struct {
		end    string
		pushes [][2]string // time, segment pushed by main
		want   string
	}{
		{"", [][2]string{{"2026-01-14 10:05:00", "000000010000000000000010"}}, PgBackRestNoLaterPush},
		{"", [][2]string{{"2026-01-14 10:05:00", "000000010000000000000011"}}, PgBackRestStillFailing},
		{"", [][2]string{{"2026-01-14 09:55:00", "000000010000000000000010"}}, PgBackRestStillFailing},
		{"2026-01-14 10:10:00", [][2]string{{"2026-01-14 10:05:00", "000000010000000000000010"}, {"2026-01-14 10:20:00", "000000010000000000000010"}}, PgBackRestRecoveryUnknown},
	} {
		a := NewPgBackRestAnalyzer()
		if c.end != "" {
			a.SetTimeWindow(time.Time{}, mustParse(t, c.end))
		}
		pushRun(a, "2026-01-14 10:00:00", "ghost", "000000010000000000000010", false)
		for _, p := range c.pushes {
			pushRun(a, p[0], "main", p[1], true)
		}
		if fs := stanza(t, a, "ghost").ArchivePush.Failures; len(fs) != 1 || fs[0].Recovery != c.want {
			t.Errorf("end %q, main pushed %v: ghost stretches = %+v, want one %s", c.end, c.pushes, fs, c.want)
		}
	}
}

// TestPgBackRestAnalyzer_AsyncCouldNotPush pins the async worker's
// "could not push": its code kept, counted as async, its stretch not
// recovered by its own batch (nor a later segment of it), only by a later
// push of that segment; LastPush names the batch's last pushed segment.
func TestPgBackRestAnalyzer_AsyncCouldNotPush(t *testing.T) {
	run := func(retry bool) PgBackRestArchiveStats {
		a := NewPgBackRestAnalyzer()
		asyncBatch(a, "main", "2026-10-10 13:37:13", "2026-10-10 13:37:14", "2 WAL file(s) to archive: 000000010000000000000014...000000010000000000000015",
			couldNotPush("000000010000000000000015"))
		if retry {
			asyncBatch(a, "main", "2026-10-10 13:37:20", "2026-10-10 13:37:21", "1 WAL file(s) to archive: 000000010000000000000015")
		}
		return stanza(t, a, "main").ArchivePush
	}
	ap := run(false)
	if ap.Failed != 0 || ap.FailedAsync != 1 || ap.PushedAsync != 1 {
		t.Errorf("failed=%d failed_async=%d pushed_async=%d, want 0 1 1", ap.Failed, ap.FailedAsync, ap.PushedAsync)
	}
	if len(ap.Failures) != 1 || ap.Failures[0].Code != 39 || ap.Failures[0].Recovery != PgBackRestStillFailing {
		t.Fatalf("stretches = %+v, want one [39] still failing", ap.Failures)
	}
	if ap.LastPush == nil || ap.LastPush.Segment != "000000010000000000000014" {
		t.Errorf("LastPush = %+v, want ...14, the batch's last pushed segment", ap.LastPush)
	}
	ap = run(true)
	if f := ap.Failures[0]; ap.PushedAsync != 2 || f.Recovery != PgBackRestRecovered || f.RecoveredBy.Segment != "000000010000000000000015" || f.RecoveredBy.Time.Format("15:04:05") != "13:37:21" {
		t.Errorf("pushed_async=%d stretch = %+v by %+v, want 2 and recovered by ...15 at 13:37:21", ap.PushedAsync, f, f.RecoveredBy)
	}

	for failed, wantLast := range map[string]string{"000000010000000000000022": "000000010000000000000023", "000000010000000000000023": "000000010000000000000022"} {
		a := NewPgBackRestAnalyzer()
		asyncBatch(a, "main", "2026-10-10 14:00:00", "2026-10-10 14:00:01", "3 WAL file(s) to archive: 000000010000000000000021...000000010000000000000023", "P01 "+couldNotPush(failed))
		ap := stanza(t, a, "main").ArchivePush
		if len(ap.Failures) != 1 || ap.Failures[0].Recovery != PgBackRestStillFailing || ap.LastPush == nil || ap.LastPush.Segment != wantLast {
			t.Errorf("failed %s: stretches = %+v, LastPush = %+v; want one still failing, last push %s", failed, ap.Failures, ap.LastPush, wantLast)
		}
	}
}

// TestPgBackRestAnalyzer_LastPushSkipsBackupFile pins LastPush on the
// last WAL segment pushed, not the .backup history file pushed after it.
func TestPgBackRestAnalyzer_LastPushSkipsBackupFile(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	for _, seg := range []string{"00000001000000000000000F", "00000001000000000000000E.00000028.backup"} {
		pushRun(a, "2026-01-14 14:04:30", "main", seg, true)
	}
	if lp := stanza(t, a, "main").ArchivePush.LastPush; lp == nil || lp.Segment != "00000001000000000000000F" {
		t.Errorf("LastPush = %+v, want 00000001000000000000000F", lp)
	}
}

// TestPgBackRestAnalyzer_PushRecoveryEdges pins the edges of a push
// failure stretch's recovery: a history file is not a later segment, a
// push in the last attempt's millisecond is not after it, a stretch with
// no segment is recovered by any later push, a stretch cut by --end says
// nothing, and the async worker's lines alone call it still failing.
func TestPgBackRestAnalyzer_PushRecoveryEdges(t *testing.T) {
	only := func(a *PgBackRestAnalyzer) PgBackRestFailureStretch {
		t.Helper()
		fs := stanza(t, a, "demo").ArchivePush.Failures
		if len(fs) != 1 {
			t.Fatalf("stretches = %+v, want 1", fs)
		}
		return fs[0]
	}
	for _, c := range []struct{ name, at, seg, want string }{
		{"history file", "2026-01-14 09:20:00", "00000002.history", PgBackRestStillFailing},
		{"next timeline", "2026-01-14 09:20:00", "000000020000000000000010", PgBackRestRecovered},
		{"same millisecond", "2026-01-14 09:10:00.250", "000000010000000000000011", PgBackRestStillFailing},
		{"next millisecond", "2026-01-14 09:10:00.251", "000000010000000000000011", PgBackRestRecovered},
	} {
		a := NewPgBackRestAnalyzer()
		pushRun(a, "2026-01-14 09:00:00", "demo", "000000010000000000000009", true)
		pushRun(a, "2026-01-14 09:10:00.250", "demo", "000000010000000000000010", false)
		pushRun(a, c.at, "demo", c.seg, true)
		if got := only(a).Recovery; got != c.want {
			t.Errorf("%s: pushed %s at %s, recovery = %s, want %s", c.name, c.seg, c.at, got, c.want)
		}
	}

	a := NewPgBackRestAnalyzer()
	pushRun(a, "2026-01-14 09:10:00", "demo", "", false)
	pushRun(a, "2026-01-14 09:20:00", "demo", "000000010000000000000011", true)
	if f := only(a); f.Segment != "" || f.Recovery != PgBackRestRecovered {
		t.Errorf("unnamed stretch = %+v, want recovered", f)
	}

	a = NewPgBackRestAnalyzer()
	a.SetTimeWindow(time.Time{}, mustParse(t, "2026-01-14 09:15:00"))
	pushRun(a, "2026-01-14 09:00:00", "demo", "000000010000000000000009", true)
	pushRun(a, "2026-01-14 09:10:00", "demo", "000000010000000000000010", false)
	pushRun(a, "2026-01-14 09:20:00", "demo", "000000010000000000000010", true)
	if f := only(a); f.Recovery != PgBackRestRecoveryUnknown || f.RecoveredBy != nil {
		t.Errorf("stretch cut by --end = %+v, want unknown", f)
	}

	a = NewPgBackRestAnalyzer()
	asyncBatch(a, "demo", "2026-01-14 09:10:00", "2026-01-14 09:10:01", "1 WAL file(s) to archive: 000000010000000000000010", "P01 "+couldNotPush("000000010000000000000010"))
	if ap := stanza(t, a, "demo").ArchivePush; ap.Logged || !ap.AsyncLogged || ap.LastPush != nil || len(ap.Failures) != 1 || ap.Failures[0].Recovery != PgBackRestStillFailing {
		t.Errorf("logged=%v async_logged=%v last_push=%+v stretches=%+v, want only the worker logged, no push, still failing", ap.Logged, ap.AsyncLogged, ap.LastPush, ap.Failures)
	}
}

// TestPgBackRestAnalyzer_AsyncFailureIsItsOwnStretch pins that the async
// worker's "could not push" opens a stretch apart from an earlier,
// non-overlapping synchronous one of the same segment.
func TestPgBackRestAnalyzer_AsyncFailureIsItsOwnStretch(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	pushRun(a, "2026-10-10 10:00:00", "main", "000000010000000000000015", false)
	asyncBatch(a, "main", "2026-10-10 11:00:00", "2026-10-10 11:00:01", "1 WAL file(s) to archive: 000000010000000000000015", "P01 "+couldNotPush("000000010000000000000015"))
	fs := stanza(t, a, "main").ArchivePush.Failures
	if len(fs) != 2 || fs[0].Code != 103 || fs[0].Attempts != 1 || fs[1].Code != 39 || fs[1].Attempts != 1 {
		t.Errorf("stretches = %+v, want [103] at 10:00 and [39] at 11:00, one attempt each", fs)
	}
}

// TestPgBackRestAnalyzer_AsyncBatchWithDropNamesNoPush pins that a batch
// whose last segment failed and which dropped one names no pushed
// segment: the one before the failure may be the dropped one.
func TestPgBackRestAnalyzer_AsyncBatchWithDropNamesNoPush(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	asyncBatch(a, "main", "2026-10-10 14:00:00", "2026-10-10 14:00:01", "3 WAL file(s) to archive: 000000010000000000000021...000000010000000000000023",
		"WARN: dropped WAL file '000000010000000000000022' because archive queue exceeded 16KB", "P01 "+couldNotPush("000000010000000000000023"))
	if lp := stanza(t, a, "main").ArchivePush.LastPush; lp == nil || lp.Segment != "" || lp.Mode != "async" {
		t.Errorf("LastPush = %+v, want the async batch with no segment named", lp)
	}
}

// TestPgBackRestAnalyzer_RelayedAsyncErrorCountedOnce pins that the
// synchronous call's relay of an async worker ERROR is one error, while
// the failure counters stay apart and an unrelayed worker ERROR counts.
func TestPgBackRestAnalyzer_RelayedAsyncErrorCountedOnce(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	for _, r := range [][2]string{{"2026-10-10 10:00:00", "archive-push:async"}, {"2026-10-10 10:00:00", "archive-push"}, {"2026-10-10 10:00:05", "archive-push"}, {"2026-10-10 11:00:00", "archive-push:async"}} {
		arg := "[/pg_wal]"
		if r[1] == "archive-push" {
			arg = "[pg_wal/000000010000000000000015]"
		}
		feed(a, r[0], "INFO: "+r[1]+" command begin 2.59.3: "+arg+" --archive-async --stanza=main",
			"ERROR: [103]: unable to find a valid repository", "INFO: "+r[1]+" command end: aborted with exception [103]")
	}
	m := stanza(t, a, "main")
	if len(m.Errors) != 1 || m.Errors[0].Count != 3 || m.ArchivePush.Failed != 2 || m.ArchivePush.FailedAsync != 2 {
		t.Errorf("errors = %+v, failed = %d, failed_async = %d; want one [103] counted 3 times, 2 and 2", m.Errors, m.ArchivePush.Failed, m.ArchivePush.FailedAsync)
	}
}

// A pushed .history file archive-get later cannot find is missing: the
// recovery then forks a timeline already taken (lab, pgBackRest 2.60.0).
// The probe for a timeline never pushed is not.
func TestPgBackRestMissingHistoryFile(t *testing.T) {
	a := NewPgBackRestAnalyzer()
	pushRun(a, "2026-10-26 16:18:44", "main", "00000002.history", true)
	for _, h := range []string{"00000002.history", "00000003.history"} {
		feed(a, "2026-10-26 16:18:55", "INFO: archive-get command begin 2.60.0: ["+h+", pg_wal/RECOVERYHISTORY] --pg1-path=/var/lib/pg/r2 --stanza=main",
			"INFO: unable to find "+h+" in the archive", "INFO: archive-get command end: completed successfully (1ms)")
	}
	if got := stanza(t, a, "main").MissingWAL; len(got) != 1 || got[0].Segment != "00000002.history" || got[0].Path != "/var/lib/pg/r2" {
		t.Errorf("missing WAL: %+v", got)
	}
}
