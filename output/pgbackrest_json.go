package output

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// pgbrRoundAge rounds an age to the second, the one rule age_seconds and
// the text/Markdown ages share. Kept here because the browser build
// compiles this file and not the text one.
func pgbrRoundAge(age time.Duration) time.Duration {
	return time.Duration(math.Round(age.Seconds())) * time.Second
}

// pgbrDate formats a timestamp like the rest of the JSON, or "" for a
// zero time so that omitempty drops it.
func pgbrDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

type pgBackRestBackupJSON struct {
	Label         string `json:"label,omitempty"`
	Type          string `json:"type,omitempty"`
	ChangedToFull bool   `json:"changed_to_full,omitempty"`
	RequestedType string `json:"requested_type,omitempty"`
	Repo          string `json:"repo,omitempty"`
	StartDate     string `json:"start_date,omitempty"`
	EndDate       string `json:"end_date,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	Size          string `json:"size,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	FileTotal     int    `json:"file_total,omitempty"`
	Succeeded     bool   `json:"succeeded"`
	// Crashed is true when this run never wrote its own "command end"
	// line (the process was killed or crashed) — Succeeded is then
	// always false too, and EndDate is absent: the end is unknown.
	// LastLineDate is when it, or a run in progress, wrote its last line.
	Crashed      bool   `json:"crashed,omitempty"`
	LastLineDate string `json:"last_line_date,omitempty"`
	// WaitingFor names the checkpoint a crashed run's last line said it
	// was waiting for ("next regular checkpoint").
	WaitingFor string `json:"waiting_for,omitempty"`
	// Signal names the signal the log says terminated a crashed run
	// ("SIGTERM"); absent when the log says nothing more.
	Signal string `json:"signal,omitempty"`
	// InProgress is true when the run had not ended by the --end bound.
	InProgress  bool     `json:"in_progress,omitempty"`
	ErrorCode   int      `json:"error_code,omitempty"`
	ErrorLine   string   `json:"error_line,omitempty"`
	ErrorDetail []string `json:"error_detail,omitempty"`
	// ErrorLabel is the failure's short cause, as the text report words it.
	ErrorLabel string `json:"error_label,omitempty"`
	ExecID     string `json:"exec_id,omitempty"`
}

type pgBackRestLastBackupJSON struct {
	Label      string `json:"label"`
	Repo       string `json:"repo,omitempty"`
	EndDate    string `json:"end_date"`
	AgeSeconds int64  `json:"age_seconds"`
}

// pgbrAgeSeconds is pgbrRoundAge in seconds.
func pgbrAgeSeconds(age time.Duration) int64 {
	return int64(pgbrRoundAge(age).Seconds())
}

type pgBackRestCommandStatJSON struct {
	Command         string `json:"command"`
	Runs            int    `json:"runs"`
	Succeeded       int    `json:"succeeded"`
	Failed          int    `json:"failed"`
	TotalDurationMs int64  `json:"total_duration_ms"`
	MaxDurationMs   int64  `json:"max_duration_ms"`
}

type pgBackRestFailureStretchJSON struct {
	Segment   string `json:"segment,omitempty"`
	Code      int    `json:"code,omitempty"`
	FirstLine string `json:"first_line,omitempty"`
	FirstDate string `json:"first_date,omitempty"`
	LastDate  string `json:"last_date,omitempty"`
	Attempts  int    `json:"attempts"`
	// Recovery and RecoveredBy: archive-push stretches only.
	Recovery    string              `json:"recovery,omitempty"`
	RecoveredBy *pgBackRestPushJSON `json:"recovered_by,omitempty"`
	// Cause is the stretch's short cause, as the text report words it.
	Cause string `json:"cause,omitempty"`
	// Path is an archive-get stretch's data directory (--pg1-path).
	Path string `json:"path,omitempty"`
}

type pgBackRestPathDatesJSON struct {
	Path  string   `json:"path"`
	Dates []string `json:"dates"`
}

type pgBackRestMissingWALJSON struct {
	Segment    string `json:"segment"`
	Path       string `json:"path,omitempty"`
	Date       string `json:"date,omitempty"`
	PushedDate string `json:"pushed_date,omitempty"`
}

type pgBackRestPushJSON struct {
	Segment string `json:"segment,omitempty"`
	Date    string `json:"date,omitempty"`
	Mode    string `json:"mode"`
}

func buildPushJSON(p *analysis.PgBackRestPush) *pgBackRestPushJSON {
	if p == nil {
		return nil
	}
	return &pgBackRestPushJSON{Segment: p.Segment, Date: pgbrDate(p.Time), Mode: p.Mode}
}

type pgBackRestDropJSON struct {
	Segment string `json:"segment,omitempty"`
	Date    string `json:"date,omitempty"`
}

// pgBackRestArchiveStatsJSON keeps sync and async-worker counters apart
// (see analysis.PgBackRestArchiveStats). Logged/AsyncLogged say whether
// the command's own begin/end lines were seen: without them its counters
// are not logged rather than zero.
type pgBackRestArchiveStatsJSON struct {
	Logged        bool                           `json:"logged"`
	AsyncLogged   bool                           `json:"async_logged"`
	Pushed        int                            `json:"pushed"`
	PushedAsync   int                            `json:"pushed_async"`
	Found         int                            `json:"found"`
	FoundAsync    int                            `json:"found_async"`
	NotFound      int                            `json:"not_found"`
	NotFoundAsync int                            `json:"not_found_async"`
	Dropped       int                            `json:"dropped"`
	DroppedWAL    []pgBackRestDropJSON           `json:"dropped_wal,omitempty"`
	Failed        int                            `json:"failed"`
	FailedAsync   int                            `json:"failed_async"`
	Timeouts      int                            `json:"timeouts"`
	Failures      []pgBackRestFailureStretchJSON `json:"failures,omitempty"`
	LastPush      *pgBackRestPushJSON            `json:"last_push,omitempty"`
	LastFound     string                         `json:"last_found,omitempty"`
}

type pgBackRestExpireRunJSON struct {
	Date           string   `json:"date,omitempty"`
	Repo           string   `json:"repo,omitempty"`
	Expired        []string `json:"expired,omitempty"`
	ArchiveRemoved int      `json:"archive_removed"`
	Warnings       []string `json:"warnings,omitempty"`
	Succeeded      bool     `json:"succeeded"`
	ErrorCode      int      `json:"error_code,omitempty"`
	ErrorLine      string   `json:"error_line,omitempty"`
	ErrorDetail    []string `json:"error_detail,omitempty"`
	SetLabel       string   `json:"set_label,omitempty"`
}

// pgBackRestWindowJSON is the --begin/--end window the report covers.
type pgBackRestWindowJSON struct {
	Begin     string `json:"begin,omitempty"`
	End       string `json:"end,omitempty"`
	CutBefore bool   `json:"cut_before"`
	CutAfter  bool   `json:"cut_after"`
}

type pgBackRestOperationJSON struct {
	Command    string `json:"command"`
	Date       string `json:"date,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Succeeded  bool   `json:"succeeded"`
	HasOutcome bool   `json:"has_outcome"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	ErrorCode  int    `json:"error_code,omitempty"`
	// Crashed is true when this run never wrote its own "command end"
	// line — see pgBackRestBackupJSON.Crashed.
	Crashed bool   `json:"crashed,omitempty"`
	Signal  string `json:"signal,omitempty"` // see pgBackRestBackupJSON.Signal
	// Detail is the summary line shortened, as the text report words it.
	Detail string `json:"detail,omitempty"`
	// Path is a restore's data directory (--pg1-path); Recovery what
	// stopped its recovery, as the text report words it.
	Path     string `json:"path,omitempty"`
	Recovery string `json:"recovery,omitempty"`
}

type pgBackRestIssueJSON struct {
	ID string `json:"id"`
	// Label is the issue's short label, as the text report words it.
	Label     string   `json:"label"`
	Code      int      `json:"code,omitempty"`
	Message   string   `json:"message"`
	Example   string   `json:"example,omitempty"`
	RepoLine  string   `json:"repo_line,omitempty"`
	Detail    []string `json:"detail,omitempty"`
	Count     int      `json:"count"`
	FirstDate string   `json:"first_date,omitempty"`
	LastDate  string   `json:"last_date,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	// Dates holds each dated occurrence, for the --event-detail
	// histogram.
	Dates []string `json:"dates,omitempty"`
}

type pgBackRestPGFailureGroupJSON struct {
	Segment   string `json:"segment,omitempty"`
	Cause     string `json:"cause"`
	Count     int    `json:"count"`
	FirstDate string `json:"first_date,omitempty"`
	LastDate  string `json:"last_date,omitempty"`
}

type pgBackRestRetentionWarningJSON struct {
	Text      string   `json:"text"`
	Count     int      `json:"count"`
	Repos     []string `json:"repos"`
	FirstDate string   `json:"first_date,omitempty"`
	LastDate  string   `json:"last_date,omitempty"`
}

type pgBackRestPGArchiveFailureJSON struct {
	Segment  string `json:"segment,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Signal   int    `json:"signal,omitempty"`
	Date     string `json:"date,omitempty"`
}

// pgBackRestJSON is the per-stanza view serialized under the top-level
// "pgbackrest" JSON key. snake_case keys; counters are always emitted,
// lists use omitempty, per docs/web-data-contract.md.
type pgBackRestJSON struct {
	Stanza        string   `json:"stanza"`
	Files         []string `json:"files,omitempty"`
	LinesRead     int      `json:"lines_read"`
	Versions      []string `json:"versions,omitempty"`
	StartDate     string   `json:"start_date,omitempty"`
	EndDate       string   `json:"end_date,omitempty"`
	HasTimestamps bool     `json:"has_timestamps"`
	SawBeginEnd   bool     `json:"saw_begin_end"`
	BackupsLogged bool     `json:"backups_logged"`

	Window *pgBackRestWindowJSON `json:"window,omitempty"`

	Backups  []pgBackRestBackupJSON    `json:"backups,omitempty"`
	LastFull *pgBackRestLastBackupJSON `json:"last_full,omitempty"`
	LastDiff *pgBackRestLastBackupJSON `json:"last_diff,omitempty"`
	LastIncr *pgBackRestLastBackupJSON `json:"last_incr,omitempty"`
	// LastFullExpired/LastDiffExpired/LastIncrExpired carry the label of
	// the most recent successful backup of that type when this report's
	// own expire run(s) removed it since — see analysis.PgBackRestMetrics.
	LastFullExpired string `json:"last_full_expired,omitempty"`
	LastDiffExpired string `json:"last_diff_expired,omitempty"`
	LastIncrExpired string `json:"last_incr_expired,omitempty"`

	Commands []pgBackRestCommandStatJSON `json:"commands,omitempty"`

	ArchivePush pgBackRestArchiveStatsJSON `json:"archive_push"`
	ArchiveGet  pgBackRestArchiveStatsJSON `json:"archive_get"`

	ExpireRuns        []pgBackRestExpireRunJSON        `json:"expire_runs,omitempty"`
	RetentionWarnings []pgBackRestRetentionWarningJSON `json:"retention_warnings,omitempty"`

	Operations []pgBackRestOperationJSON `json:"operations,omitempty"`

	Errors   []pgBackRestIssueJSON `json:"errors,omitempty"`
	Warnings []pgBackRestIssueJSON `json:"warnings,omitempty"`

	PGArchiveFailures      []pgBackRestPGArchiveFailureJSON `json:"pg_archive_failures,omitempty"`
	PGArchiveFailureGroups []pgBackRestPGFailureGroupJSON   `json:"pg_archive_failure_groups,omitempty"`
	PGRetryWarnings        int                              `json:"pg_retry_warnings"`
	PGRestoreFailures      int                              `json:"pg_restore_failures"`

	MissingInfo []string `json:"missing_info,omitempty"`

	MissingWAL []pgBackRestMissingWALJSON `json:"missing_wal,omitempty"`
	// RestoreDates dates each segment archive-get found, per data
	// directory, in order of first find.
	RestoreDates []pgBackRestPathDatesJSON `json:"restore_dates,omitempty"`
	// PGRetryDates dates each of pg_retry_warnings.
	PGRetryDates []string `json:"pg_retry_dates,omitempty"`

	// PushDates dates each segment archive_push.pushed and pushed_async
	// count, in time order (an async batch's segments at its end).
	PushDates []string `json:"push_dates,omitempty"`
}

func buildPgBackRestJSON(m analysis.PgBackRestMetrics) pgBackRestJSON {
	j := pgBackRestJSON{
		Stanza:            m.Stanza,
		Files:             m.Files,
		LinesRead:         m.LinesRead,
		Versions:          m.Versions,
		StartDate:         pgbrDate(m.StartTime),
		EndDate:           pgbrDate(m.EndTime),
		HasTimestamps:     m.HasTimestamps,
		SawBeginEnd:       m.SawBeginEnd,
		BackupsLogged:     m.BackupsLogged,
		PGRetryWarnings:   m.PGRetryWarnings,
		PGRestoreFailures: m.PGRestoreFailures,
		MissingInfo:       m.MissingInfo,
	}
	if w := m.Window; w != nil {
		j.Window = &pgBackRestWindowJSON{Begin: pgbrDate(w.Begin), End: pgbrDate(w.End), CutBefore: w.CutBefore, CutAfter: w.CutAfter}
	}
	for _, b := range m.Backups {
		bj := pgBackRestBackupJSON{
			Label: b.Label, Type: b.Type, ChangedToFull: b.ChangedToFull, RequestedType: b.RequestedType, Repo: b.Repo,
			StartDate: pgbrDate(b.Start), EndDate: pgbrDate(b.End), LastLineDate: pgbrDate(b.LastLine),
			DurationMs: b.Duration.Milliseconds(), Size: b.SizeRaw, FileTotal: b.FileTotal,
			Succeeded: b.Succeeded, Crashed: b.Crashed, WaitingFor: b.WaitingFor, Signal: b.Signal, InProgress: b.InProgress, ErrorCode: b.ErrorCode, ErrorLine: b.ErrorLine, ErrorDetail: b.ErrorDetail, ExecID: b.ExecID,
		}
		if !b.Succeeded && !b.Crashed && !b.InProgress && (b.ErrorLine != "" || b.ErrorCode != 0) {
			bj.ErrorLabel = pgbrErrorLabel(b.ErrorCode, b.ErrorCode != 0, b.ErrorLine, "", b.ErrorDetail)
		}
		if b.HasSize {
			bj.SizeBytes = b.SizeBytes
		}
		j.Backups = append(j.Backups, bj)
	}
	lastJSON := func(typ string) *pgBackRestLastBackupJSON {
		last, age, ok := m.LastOf(typ)
		if !ok {
			return nil
		}
		return &pgBackRestLastBackupJSON{Label: last.Label, Repo: last.Repo, EndDate: pgbrDate(last.End), AgeSeconds: pgbrAgeSeconds(age)}
	}
	j.LastFull, j.LastDiff, j.LastIncr = lastJSON("full"), lastJSON("diff"), lastJSON("incr")
	j.LastFullExpired = m.Last["full"].Expired
	j.LastDiffExpired = m.Last["diff"].Expired
	j.LastIncrExpired = m.Last["incr"].Expired
	for _, cs := range m.Commands {
		j.Commands = append(j.Commands, pgBackRestCommandStatJSON{
			Command: cs.Command, Runs: cs.Runs, Succeeded: cs.Succeeded, Failed: cs.Failed,
			TotalDurationMs: cs.TotalDuration.Milliseconds(), MaxDurationMs: cs.MaxDuration.Milliseconds(),
		})
	}
	j.ArchivePush = buildArchiveStatsJSON(m.ArchivePush)
	j.ArchiveGet = buildArchiveStatsJSON(m.ArchiveGet)
	for _, er := range m.ExpireRuns {
		j.ExpireRuns = append(j.ExpireRuns, pgBackRestExpireRunJSON{
			Date: pgbrDate(er.Time), Repo: er.Repo, Expired: er.Expired,
			ArchiveRemoved: er.ArchiveRemoved, Warnings: er.Warnings,
			Succeeded: er.Succeeded, ErrorCode: er.ErrorCode, ErrorLine: er.ErrorLine, ErrorDetail: er.ErrorDetail, SetLabel: er.SetLabel,
		})
	}
	for _, w := range m.RetentionWarnings {
		j.RetentionWarnings = append(j.RetentionWarnings, pgBackRestRetentionWarningJSON{
			Text: w.Text, Count: w.Count, Repos: w.Repos, FirstDate: pgbrDate(w.First), LastDate: pgbrDate(w.Last),
		})
	}
	for i, op := range m.Operations {
		j.Operations = append(j.Operations, pgBackRestOperationJSON{
			Command: op.Command, Date: pgbrDate(op.Time), Summary: op.Summary, Succeeded: op.Succeeded,
			HasOutcome: op.HasOutcome, DurationMs: op.Duration.Milliseconds(), ErrorCode: op.ErrorCode,
			Crashed: op.Crashed, Signal: op.Signal, Detail: pgbrOperationDetail(op),
			Path: op.Path, Recovery: pgbrRestoreRecovery(m, i),
		})
	}
	errLabels, warnLabels := pgbrIssueLabels(m)
	for i, is := range m.Errors {
		ij := buildIssueJSON(is)
		ij.Label = errLabels[i]
		j.Errors = append(j.Errors, ij)
	}
	for i, is := range m.Warnings {
		ij := buildIssueJSON(is)
		ij.Label = warnLabels[i]
		j.Warnings = append(j.Warnings, ij)
	}
	for i, f := range m.ArchivePush.Failures {
		j.ArchivePush.Failures[i].Cause = pgbrOutageCause(m, f, "archive-push")
	}
	for i, f := range m.ArchiveGet.Failures {
		j.ArchiveGet.Failures[i].Cause = pgbrOutageCause(m, f, "archive-get")
	}
	for _, p := range m.RestoreTimes {
		pd := pgBackRestPathDatesJSON{Path: p.Path}
		for _, t := range p.Times {
			pd.Dates = append(pd.Dates, pgbrDate(t))
		}
		j.RestoreDates = append(j.RestoreDates, pd)
	}
	for _, t := range m.PGRetryTimes {
		j.PGRetryDates = append(j.PGRetryDates, pgbrDate(t))
	}
	for _, w := range m.MissingWAL {
		j.MissingWAL = append(j.MissingWAL, pgBackRestMissingWALJSON{Segment: w.Segment, Path: w.Path, Date: pgbrDate(w.Time), PushedDate: pgbrDate(w.PushedAt)})
	}
	for _, t := range m.PushTimes {
		j.PushDates = append(j.PushDates, pgbrDate(t))
	}
	for _, f := range m.PGArchiveFailures {
		pf := pgBackRestPGArchiveFailureJSON{Segment: f.Segment, Date: pgbrDate(f.Time)}
		if f.HasExit {
			pf.ExitCode = f.ExitCode
		}
		if f.IsSignal {
			pf.Signal = f.Signal
		}
		j.PGArchiveFailures = append(j.PGArchiveFailures, pf)
	}
	for _, g := range m.PGArchiveFailureGroups {
		j.PGArchiveFailureGroups = append(j.PGArchiveFailureGroups, pgBackRestPGFailureGroupJSON{
			Segment: g.Segment, Cause: g.Cause, Count: g.Count, FirstDate: pgbrDate(g.First), LastDate: pgbrDate(g.Last),
		})
	}
	return j
}

func buildArchiveStatsJSON(s analysis.PgBackRestArchiveStats) pgBackRestArchiveStatsJSON {
	out := pgBackRestArchiveStatsJSON{
		Logged: s.Logged, AsyncLogged: s.AsyncLogged,
		Pushed: s.Pushed, PushedAsync: s.PushedAsync, Found: s.Found, FoundAsync: s.FoundAsync,
		NotFound: s.NotFound, NotFoundAsync: s.NotFoundAsync, Dropped: len(s.DroppedEvents),
		Failed: s.Failed, FailedAsync: s.FailedAsync, Timeouts: s.Timeouts,
		LastPush: buildPushJSON(s.LastPush), LastFound: pgbrDate(s.LastFound),
	}
	for _, d := range s.DroppedEvents {
		out.DroppedWAL = append(out.DroppedWAL, pgBackRestDropJSON{Segment: d.Segment, Date: pgbrDate(d.Time)})
	}
	for _, f := range s.Failures {
		out.Failures = append(out.Failures, pgBackRestFailureStretchJSON{
			Segment: f.Segment, Code: f.Code, FirstLine: f.FirstLine,
			FirstDate: pgbrDate(f.First), LastDate: pgbrDate(f.Last), Attempts: f.Attempts,
			Recovery: f.Recovery, RecoveredBy: buildPushJSON(f.RecoveredBy), Path: f.Path,
		})
	}
	return out
}

func buildIssueJSON(is analysis.PgBackRestIssue) pgBackRestIssueJSON {
	j := pgBackRestIssueJSON{
		ID: is.ID, Message: is.Message, Example: is.Example, RepoLine: is.RepoLine, Detail: is.Detail, Count: is.Count,
		FirstDate: pgbrDate(is.First), LastDate: pgbrDate(is.Last), Commands: is.Commands,
	}
	if is.HasCode {
		j.Code = is.Code
	}
	for _, t := range is.Times {
		j.Dates = append(j.Dates, pgbrDate(t))
	}
	return j
}

// pgBackRestJSONList builds the "pgbackrest" JSON array from the final
// per-stanza report.
func pgBackRestJSONList(pbr []analysis.PgBackRestMetrics) []pgBackRestJSON {
	out := make([]pgBackRestJSON, 0, len(pbr))
	for _, m := range pbr {
		out = append(out, buildPgBackRestJSON(m))
	}
	return out
}

// ExportPgBackRestOnlyJSON writes a document holding only the
// "pgbackrest" key, for a run with no PostgreSQL files at all.
func ExportPgBackRestOnlyJSON(w io.Writer, pbr []analysis.PgBackRestMetrics, compact bool) {
	data := map[string]interface{}{"pgbackrest": pgBackRestJSONList(pbr)}
	if err := encodeMapStreaming(w, data, compact); err != nil {
		fmt.Fprintf(w, "[ERROR] Failed to export JSON: %v\n", err)
	}
}

// ExportPgBackRestOnlyJSONStringWithMeta is the browser's pgBackRest-only
// shape: "pgbackrest", "meta" and a "pgbackrest_only" flag that tells the
// page to render only the pgBackRest sections.
func ExportPgBackRestOnlyJSONStringWithMeta(pbr []analysis.PgBackRestMetrics, meta *MetaInfo) (string, error) {
	data := map[string]interface{}{
		"pgbackrest_only": true,
		"pgbackrest":      pgBackRestJSONList(pbr),
	}
	if meta != nil {
		data["meta"] = meta
	}
	var buf bytes.Buffer
	if err := encodeMapStreaming(&buf, data, true); err != nil {
		return "", err
	}
	return buf.String(), nil
}
