package cmd

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/parser"
)

// partitionInputsByFormat splits the expanded input files into
// PostgreSQL files and pgBouncer files by per-file content detection.
// pgBouncer and PostgreSQL logs are always distinct files (separate
// daemons), so routing each file to the right analyzer lets a single
// invocation combine both — the PostgreSQL report plus a pgBouncer
// section — instead of silently feeding one analyzer foreign lines.
// stdin ("-") cannot be sampled by path and is treated as PostgreSQL.
func partitionInputsByFormat(files []string) (pgFiles, pgbFiles []string) {
	var undetected []string
	for _, f := range files {
		if f == "-" {
			pgFiles = append(pgFiles, f)
			continue
		}
		switch parser.DetectFileFormat(f) {
		case "pgbouncer":
			pgbFiles = append(pgbFiles, f)
		case "":
			undetected = append(undetected, f)
		default:
			pgFiles = append(pgFiles, f)
		}
	}
	// A file the detector rejected belongs to no track: it was already
	// reported on stderr, by name, by the detector. It joins the PostgreSQL
	// track, where the run-level "no log entries could be parsed" error
	// covers an input list holding nothing else — but only when no pgBouncer
	// file was recognized. Otherwise that track would fail on the rejected
	// file alone and take down a pooler report that did parse, turning one
	// unreadable input into an empty report and a non-zero exit for the
	// whole run.
	if len(pgbFiles) == 0 {
		pgFiles = append(pgFiles, undetected...)
	}
	return pgFiles, pgbFiles
}

// computePgBouncerMetrics runs a self-contained parse+filter+analyze
// pass over the pgBouncer files. It uses no progress bar (pgBouncer logs
// are small) and its own PgBouncerAnalyzer, and reports into the caller's
// parsedAny flag like the PostgreSQL track does.
//
// Only the time bounds are applied. The attribute filters (--dbname,
// --dbuser, --appname) select PostgreSQL backends by the db=/user=/app=
// tokens of a log_line_prefix; pgBouncer lines carry unrelated db=/user=
// tokens of their own (connection endpoints), so applying them here
// silently amputated the pooler report instead of filtering it.
func computePgBouncerMetrics(ctx context.Context, files []string, beginT, endT time.Time, parsedAny *atomic.Bool) analysis.PgBouncerMetrics {
	raw := make(chan []parser.LogEntry, 64)
	go parseFilesAsync(ctx, files, raw, parsedAny, nil)

	in := (<-chan []parser.LogEntry)(raw)
	filters := parser.LogFilters{BeginT: parser.WallClock(beginT), EndT: parser.WallClock(endT)}
	if !filters.IsEmpty() {
		filtered := make(chan []parser.LogEntry, 64)
		go parser.FilterStream(ctx, raw, filtered, filters)
		in = filtered
	}
	return analysis.AggregatePgBouncerMetrics(ctx, in)
}
