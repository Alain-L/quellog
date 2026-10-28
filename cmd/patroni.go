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

func patroniEntries(m analysis.AggregatedMetrics) int {
	if m.Patroni == nil {
		return 0
	}
	return m.Patroni.Entries
}

// firstPostgresArg returns the first argument that is not a Patroni log:
// the report's format label describes the PostgreSQL logs.
func firstPostgresArg(args []string) string {
	for _, a := range args {
		if !parser.IsPatroniLog(a) {
			return a
		}
	}
	return args[0]
}

// splitPatroniInputs separates Patroni logs from the rest by content
// detection. Patroni and PostgreSQL write distinct files, so each goes to
// its own analysis instead of feeding foreign lines to the other.
func splitPatroniInputs(files []string) (pgFiles, patroniFiles []string) {
	for _, f := range files {
		if f != "-" && parser.IsPatroniLog(f) {
			patroniFiles = append(patroniFiles, f)
		} else {
			pgFiles = append(pgFiles, f)
		}
	}
	return pgFiles, patroniFiles
}

// computePatroniMetrics analyzes each Patroni file as one member's log,
// then merges them.
func computePatroniMetrics(ctx context.Context, files []string, beginT, endT time.Time) *analysis.PatroniMetrics {
	feeds := make([]func(chan<- []parser.LogEntry), len(files))
	for i, f := range files {
		feeds[i] = func(out chan<- []parser.LogEntry) {
			var parsed atomic.Bool
			parseFilesAsync(ctx, []string{f}, out, &parsed, nil)
		}
	}
	window := parser.LogFilters{BeginT: parser.WallClock(beginT), EndT: parser.WallClock(endT)}
	return analysis.AnalyzePatroniMembers(ctx, feeds, window)
}

// renderPatroniDetail writes the detailed Patroni report alone
// (--patroni-detail), each leader change with what the PostgreSQL logs
// given logged around it.
func renderPatroniDetail(ctx context.Context, filteredLogs <-chan []parser.LogEntry, startTime time.Time, totalFileSize int64, workers int, pb *progressBar, pgbr *analysis.PgBackRestAnalyzer, patroniReport *analysis.PatroniMetrics) error {
	metrics := analysis.AggregateMetricsObserved(ctx, filteredLogs, workers, pgbr)
	pb.Finish()
	if patroniReport == nil {
		return errNoPatroni
	}
	patroniReport.CorrelatePostgres(metrics.Server.Timeline)
	w, closer, err := createOutputWriter(outputFlag)
	if err != nil {
		return err
	}
	defer closer()
	if mdFlag {
		output.ExportPatroniMarkdown(w, patroniReport, true)
	} else {
		PrintProcessingSummary(patroniReport.Entries, time.Since(startTime), totalFileSize)
		output.PrintPatroniReport(patroniReport, true)
	}
	return closer()
}

// errNoPatroni is --patroni-detail's error when no Patroni log was given.
var errNoPatroni = fmt.Errorf("no Patroni log in the input: give one Patroni log per member (e.g. /var/log/patroni/patroni.log)")
