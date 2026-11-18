package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

// pgbrAllStanzas is --pgbackrest-detail's value when given no stanza.
const pgbrAllStanzas = "*"

// pgBackRestInScope applies analysis.PgBackRestInScope to the command
// line; --full keeps every section.
func pgBackRestInScope() bool {
	sections := []string{"all"}
	if !fullFlag {
		sections = buildSectionList()
	}
	return analysis.PgBackRestInScope(buildLogFilters(time.Time{}, time.Time{}), sections)
}

// renderPgBackRestOnlyReport writes the report of a run with no
// PostgreSQL entry: the pgBackRest report alone, through the same output
// flags as the full report.
func renderPgBackRestOnlyReport(pbr []analysis.PgBackRestMetrics, inputArgs []string, totalFileSize int64, processingDuration time.Duration) error {
	rw := reportWriters{
		json:     func(w io.Writer) { output.ExportPgBackRestOnlyJSON(w, pbr, jsonCompactFlag) },
		yaml:     func(w io.Writer) { output.ExportPgBackRestOnlyYAML(w, pbr) },
		markdown: func(w io.Writer) { output.ExportPgBackRestMarkdown(w, pbr, fullFlag) },
		html: func(w io.Writer, info output.HTMLReportInfo) error {
			return output.ExportHTMLCombined(w, analysis.AggregatedMetrics{}, pbr, nil, info, nil, true, false)
		},
		text: func() {
			PrintProcessingSummary(analysis.PgBackRestLinesRead(pbr), processingDuration, totalFileSize)
			output.PrintPgBackRestReport(pbr, fullFlag)
		},
	}
	if exportFormatCount() > 1 {
		return renderMultipleFormats(rw, inputArgs, totalFileSize, processingDuration)
	}
	return dispatchFormattedOutput(rw, inputArgs, totalFileSize, processingDuration)
}

// renderPgBackRestDetail writes the detailed pgBackRest report alone, of
// every stanza or of the one --pgbackrest-detail names.
func renderPgBackRestDetail(ctx context.Context, filteredLogs <-chan []parser.LogEntry, startTime time.Time, totalFileSize int64, workers int, pb *progressBar, pgbr *analysis.PgBackRestAnalyzer) error {
	analysis.AggregateMetricsObserved(ctx, filteredLogs, workers, pgbr)
	pb.Finish()
	processingDuration := time.Since(startTime)
	var pbr []analysis.PgBackRestMetrics
	if pgbr != nil {
		pbr = pgbr.Finalize()
	}
	if len(pbr) == 0 {
		return errNoPgBackRest
	}
	if !pgBackRestInScope() {
		return errPgBackRestOutOfScope
	}
	if pgbackrestDetailFlag != pgbrAllStanzas {
		sel, err := selectPgBackRestStanza(pbr, pgbackrestDetailFlag)
		if err != nil {
			return err
		}
		pbr = sel
	}
	w, closer, err := createOutputWriter(outputFlag)
	if err != nil {
		return err
	}
	defer closer()
	switch {
	case jsonFlag || jsonCompactFlag:
		output.ExportPgBackRestOnlyJSON(w, pbr, jsonCompactFlag)
	case yamlFlag:
		output.ExportPgBackRestOnlyYAML(w, pbr)
	case mdFlag:
		output.ExportPgBackRestMarkdown(w, pbr, true)
	default:
		PrintProcessingSummary(analysis.PgBackRestLinesRead(pbr), processingDuration, totalFileSize)
		output.PrintPgBackRestReport(pbr, true)
	}
	return closer()
}

// errNoPgBackRest is --pgbackrest-detail's error when the input holds no
// pgBackRest line.
var errNoPgBackRest = fmt.Errorf("no pgBackRest lines in the input: give the pgBackRest log files (e.g. /var/log/pgbackrest/*.log), the PostgreSQL log, or both")

// selectPgBackRestStanza keeps the report of stanza.
func selectPgBackRestStanza(pbr []analysis.PgBackRestMetrics, stanza string) ([]analysis.PgBackRestMetrics, error) {
	names := make([]string, len(pbr))
	for i, m := range pbr {
		names[i] = analysis.PgBackRestStanzaName(m.Stanza)
		if m.Stanza == stanza || names[i] == stanza {
			return []analysis.PgBackRestMetrics{m}, nil
		}
	}
	return nil, fmt.Errorf("stanza %q not found in the pgBackRest lines read (stanzas: %s)", stanza, strings.Join(names, ", "))
}
