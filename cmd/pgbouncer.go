package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

// partitionInputsByFormat splits the expanded input files into
// PostgreSQL files and pgBouncer files by per-file content detection.
// pgBouncer and PostgreSQL logs are always distinct files (separate
// daemons), so routing each file to the right analyzer lets a single
// invocation combine both — the PostgreSQL report plus a pgBouncer
// section — instead of silently feeding one analyzer foreign lines.
// stdin ("-") cannot be sampled by path and is treated as PostgreSQL, and
// so is a file the detector rejects: the PostgreSQL track reports it, and
// a pooler report that did parse survives that track coming out empty. An
// archive holding both kinds of log goes to both tracks.
//
// The detector logs what it finds and the parse that follows detects
// again, so the files are sorted in silence: each message prints once.
func partitionInputsByFormat(files []string) (pgFiles, pgbFiles []string) {
	for _, f := range files {
		var formats []string
		if f != "-" {
			quietly(func() { formats = parser.InputFormats(f) })
		}
		pooler := slices.Contains(formats, "pgbouncer")
		// A pooler log given twice, under any path, is one pooler, not two
		// at the same time.
		if pooler && !slices.ContainsFunc(pgbFiles, func(g string) bool { return sameFile(f, g) }) {
			pgbFiles = append(pgbFiles, f)
		}
		// An archive holding both kinds of log goes to both tracks, each
		// reading its own members (see parser.ArchiveTrack).
		if !pooler || len(formats) > 1 {
			pgFiles = append(pgFiles, f)
		}
	}
	return pgFiles, pgbFiles
}

// sameFile reports whether two paths name one file ("pgb.log" and
// "./pgb.log", a relative and an absolute path).
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// poolerMemberErrors passes on only the read errors of pooler members
// (parser.PoolerMemberError): the messages the PostgreSQL track prints on
// the same archive.
type poolerMemberErrors struct{ slog.Handler }

func (h poolerMemberErrors) Handle(ctx context.Context, r slog.Record) error {
	keep := false
	r.Attrs(func(a slog.Attr) bool {
		var pe parser.PoolerMemberError
		if err, ok := a.Value.Any().(error); ok && errors.As(err, &pe) {
			keep = true
		}
		return !keep
	})
	if !keep {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

// quietly runs fn with the default logger silenced. Detection logs what
// it finds, and a caller that only needs the verdict of a file the parse
// reports on its own would print every message twice.
func quietly(fn func()) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	defer slog.SetDefault(prev)
	fn()
}

// reportFormat is the format badge of the HTML report: the format of the
// first input that is a PostgreSQL log. Patroni and pooler logs have their
// own sections; the search is silent, the parse having reported every file.
func reportFormat(inputArgs []string) string {
	format := ""
	quietly(func() {
		for _, a := range inputArgs {
			if parser.IsPatroniLog(a) {
				continue
			}
			if f := parser.DetectFileFormat(a); f != "pgbouncer" {
				format = f
				return
			}
		}
	})
	return format
}

// computePgBouncerMetrics analyzes the pgBouncer files one at a time and
// returns one report per pooler (see analysis.PgBouncerInstances). It uses
// no progress bar (pooler logs are small) and reports into the caller's
// parsedAny flag like the PostgreSQL track does. Each input is read once,
// an archive member standing for a file (analysis.ConsumeInput).
//
// Only the time bounds are applied. The attribute filters (--dbname,
// --dbuser, --appname) select PostgreSQL backends by the db=/user=/app=
// tokens of a log_line_prefix; pgBouncer lines carry unrelated db=/user=
// tokens of their own (connection endpoints), so applying them here
// silently amputated the pooler report instead of filtering it.
//
// An archive the PostgreSQL track reads too (pgFiles) reports here only
// its pooler members' read errors: that track prints the rest, once.
func computePgBouncerMetrics(ctx context.Context, files, pgFiles []string, beginT, endT time.Time, parsedAny *atomic.Bool) []analysis.PgBouncerMetrics {
	bounds := parser.LogFilters{BeginT: parser.WallClock(beginT), EndT: parser.WallClock(endT)}
	var parts []*analysis.PgBouncerAnalyzer
	for _, f := range files {
		read := func() {
			parser.ResetPgBouncerLineCounts()
			raw := make(chan []parser.LogEntry, 64)
			go parseFilesAsync(ctx, []string{f}, raw, parsedAny, nil, parser.PoolerMembers)
			parts = append(parts, analysis.ConsumeInput(ctx, raw, bounds, f)...)
		}
		if !slices.Contains(pgFiles, f) {
			read()
			continue
		}
		prev := slog.Default()
		slog.SetDefault(slog.New(poolerMemberErrors{prev.Handler()}))
		read()
		slog.SetDefault(prev)
	}
	return analysis.PgBouncerInstances(parts)
}

// poolerEntries counts the entries the pooler reports hold.
func poolerEntries(pgb []analysis.PgBouncerMetrics) (n int) {
	for _, m := range pgb {
		n += m.Lines
	}
	return n
}

// distinctFiles lists the inputs of both tracks, each once: an archive
// holding both kinds of log is in both lists.
func distinctFiles(a, b []string) []string {
	out := slices.Clone(a)
	for _, f := range b {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// renderPgBouncerDetail writes the detailed pgBouncer report alone
// (--pgbouncer-detail).
func renderPgBouncerDetail(pgb []analysis.PgBouncerMetrics, startTime time.Time, size int64) error {
	if len(pgb) == 0 {
		return errNoPgBouncer
	}
	w, closer, err := createOutputWriter(outputFlag)
	if err != nil {
		return err
	}
	defer closer()
	if mdFlag {
		output.ExportPgBouncerMarkdown(w, pgb, true)
	} else {
		PrintProcessingSummary(poolerEntries(pgb), time.Since(startTime), size)
		output.PrintPgBouncer(pgb, true)
	}
	return closer()
}

// errNoPgBouncer is --pgbouncer-detail's error when no pooler entry is
// left to report: no pgBouncer log given, or none in the time window.
var errNoPgBouncer = fmt.Errorf("no pgBouncer log entries to report: give the pooler's log (e.g. /var/log/pgbouncer/pgbouncer.log), and check that the time filters match some entries")

// errNoPgBouncerEntries is a pooler-only run's error when no entry is
// left: the files could not be read as pooler logs, or the time window
// holds none of their entries.
var errNoPgBouncerEntries = fmt.Errorf("no pgBouncer log entries could be parsed: check that files are readable and in pgBouncer log format, and that the time filters match some entries")
