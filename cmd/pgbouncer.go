package cmd

import (
	"context"
	"log/slog"
	"slices"
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
// stdin ("-") cannot be sampled by path and is treated as PostgreSQL, and
// so is a file the detector rejects: the PostgreSQL track reports it, and
// a pooler report that did parse survives that track coming out empty. An
// archive holding both kinds of log goes to both tracks.
//
// The detector logs what it finds and the parse that follows detects
// again, so the files are sorted in silence: each message prints once.
func partitionInputsByFormat(files []string) (pgFiles, pgbFiles []string) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	defer slog.SetDefault(prev)
	for _, f := range files {
		var formats []string
		if f != "-" {
			formats = parser.InputFormats(f)
		}
		pooler := slices.Contains(formats, "pgbouncer")
		if pooler {
			pgbFiles = append(pgbFiles, f)
		}
		// An archive holding both kinds of log goes to both tracks, each
		// reading its own members (see parser.SetArchiveTrack).
		if !pooler || len(formats) > 1 {
			pgFiles = append(pgFiles, f)
		}
	}
	return pgFiles, pgbFiles
}

// computePgBouncerMetrics analyzes the pgBouncer files one at a time and
// returns one report per pooler (see analysis.PgBouncerInstances). It uses
// no progress bar (pooler logs are small) and reports into the caller's
// parsedAny flag like the PostgreSQL track does.
//
// Only the time bounds are applied. The attribute filters (--dbname,
// --dbuser, --appname) select PostgreSQL backends by the db=/user=/app=
// tokens of a log_line_prefix; pgBouncer lines carry unrelated db=/user=
// tokens of their own (connection endpoints), so applying them here
// silently amputated the pooler report instead of filtering it.
func computePgBouncerMetrics(ctx context.Context, files []string, beginT, endT time.Time, parsedAny *atomic.Bool) []analysis.PgBouncerMetrics {
	bounds := parser.LogFilters{BeginT: parser.WallClock(beginT), EndT: parser.WallClock(endT)}
	parser.SetArchiveTrack(parser.PoolerMembers)
	defer parser.SetArchiveTrack(parser.AllMembers)
	parts := make([]*analysis.PgBouncerAnalyzer, 0, len(files))
	for _, f := range files {
		parser.ResetPgBouncerLineCounts()
		raw := make(chan []parser.LogEntry, 64)
		go parseFilesAsync(ctx, []string{f}, raw, parsedAny, nil)
		a := analysis.NewPgBouncerAnalyzer()
		a.Consume(ctx, raw, bounds)
		// What the parser scanned and what it threw away: the analyzer
		// only sees the records that survived, so it cannot report a loss.
		read, unrecognized := parser.PgBouncerLineCounts()
		a.SetSource(f, int(read), int(unrecognized))
		parts = append(parts, a)
	}
	return analysis.PgBouncerInstances(parts)
}
