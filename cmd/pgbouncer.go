package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

// processPgBouncerOutput runs the pgBouncer analysis pipeline and renders
// the requested format. pgBouncer logs have no SQL/query-detail concept,
// so the SQL-specific flags (--sql-summary, --sql-detail, --split, …) do
// not apply; they are silently ignored in favour of the pooler report.
func processPgBouncerOutput(ctx context.Context, in <-chan []parser.LogEntry, startTime time.Time, totalFileSize int64, inputArgs []string, pb *progressBar) error {
	m := analysis.AggregatePgBouncerMetrics(ctx, in)
	pb.Finish()
	processingDuration := time.Since(startTime)

	if m.Lines == 0 {
		return fmt.Errorf("no pgBouncer log entries could be parsed: check that files are readable and in pgBouncer log format")
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
	case htmlFlag:
		reportInfo := output.HTMLReportInfo{
			Filename:    generateInputDescription(inputArgs),
			FileSize:    totalFileSize,
			ProcessTime: float64(processingDuration.Milliseconds()),
			Format:      "pgbouncer",
			Version:     version,
		}
		if err := output.ExportPgBouncerHTML(w, m, reportInfo); err != nil {
			return err
		}
	default:
		PrintProcessingSummary(m.Lines, processingDuration, totalFileSize)
		output.PrintPgBouncer(m)
	}
	return closer()
}
