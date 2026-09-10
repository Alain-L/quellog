package cmd

import (
	"context"
	"fmt"
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
// stdin ("-") cannot be sampled by path and is treated as PostgreSQL.
func partitionInputsByFormat(files []string) (pgFiles, pgbFiles []string) {
	for _, f := range files {
		if f != "-" && parser.DetectFileFormat(f) == "pgbouncer" {
			pgbFiles = append(pgbFiles, f)
			continue
		}
		pgFiles = append(pgFiles, f)
	}
	return pgFiles, pgbFiles
}

// computePgBouncerMetrics runs a self-contained parse+filter+analyze
// pass over the pgBouncer files. It mirrors the main pipeline's filter
// wiring but uses no progress bar (pgBouncer logs are small) and its own
// PgBouncerAnalyzer.
func computePgBouncerMetrics(ctx context.Context, files []string, beginT, endT time.Time) analysis.PgBouncerMetrics {
	raw := make(chan []parser.LogEntry, 64)
	var parsedAny atomic.Bool
	go parseFilesAsync(ctx, files, raw, &parsedAny, nil)

	in := (<-chan []parser.LogEntry)(raw)
	filters := buildLogFilters(beginT, endT)
	if !filters.IsEmpty() {
		filtered := make(chan []parser.LogEntry, 64)
		go parser.FilterStream(ctx, raw, filtered, filters)
		in = filtered
	}
	return analysis.AggregatePgBouncerMetrics(ctx, in)
}

// renderPgBouncerStandalone renders the pooler report for a pure
// pgBouncer run (no PostgreSQL files) from already-computed metrics.
// pgBouncer logs have no SQL/query-detail concept, so the SQL-specific
// flags (--sql-summary, --sql-detail, --split, …) do not apply; they are
// silently ignored in favour of the pooler report.
func renderPgBouncerStandalone(m analysis.PgBouncerMetrics, startTime time.Time, totalFileSize int64, inputArgs []string) error {
	processingDuration := time.Since(startTime)

	if m.Lines == 0 {
		return fmt.Errorf("no pgBouncer log entries could be parsed: check that files are readable and in pgBouncer log format")
	}

	// HTML goes through the shared web app in pgBouncer-only mode: an
	// empty PostgreSQL payload plus the pgbouncer key, with the app
	// suppressing the empty PostgreSQL sections. This keeps the standalone
	// pooler report visually identical to the PostgreSQL report.
	if htmlFlag {
		outputName := outputFlag
		if outputName == "" {
			outputName = generateHTMLFilename(inputArgs)
		}
		w, closer, err := createOutputWriter(outputName)
		if err != nil {
			return err
		}
		reportInfo := output.HTMLReportInfo{
			Filename:    generateInputDescription(inputArgs),
			FileSize:    totalFileSize,
			ProcessTime: float64(processingDuration.Milliseconds()),
			Format:      "pgbouncer",
			Version:     version,
		}
		if err := output.ExportHTMLCombined(w, analysis.AggregatedMetrics{}, &m, reportInfo, []string{"all"}, true); err != nil {
			closer()
			return fmt.Errorf("failed to write HTML report: %w", err)
		}
		if err := closer(); err != nil {
			return err
		}
		if !followFlag {
			fmt.Printf("Report saved to %s\n", outputName)
		}
		if openFlag {
			openInBrowser(outputName)
		}
		return nil
	}

	w, closer, err := createOutputWriter(outputFlag)
	if err != nil {
		return err
	}
	defer closer()

	switch {
	case jsonFlag || jsonCompactFlag:
		output.ExportPgBouncerJSON(w, m, jsonCompactFlag)
	case mdFlag:
		output.ExportPgBouncerMarkdown(w, m)
	default:
		PrintProcessingSummary(m.Lines, processingDuration, totalFileSize)
		output.PrintPgBouncer(m)
	}
	return closer()
}
