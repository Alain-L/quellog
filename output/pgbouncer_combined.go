//go:build !js

package output

import (
	"io"

	"github.com/Alain-L/quellog/analysis"
)

// PrintMetricsCombined renders the PostgreSQL text report and, when
// pgBouncer files were part of the same run, appends the pgBouncer
// section after it. With pgb nil it is identical to PrintMetrics.
func PrintMetricsCombined(m analysis.AggregatedMetrics, pgb *analysis.PgBouncerMetrics, sections []string, full bool) {
	PrintMetrics(m, sections, full)
	if pgb != nil && pgb.Lines > 0 {
		PrintPgBouncer(*pgb)
	}
}

// ExportMarkdownCombined renders the PostgreSQL Markdown report and
// appends the pgBouncer report when present. With pgb nil it is
// identical to ExportMarkdown.
func ExportMarkdownCombined(w io.Writer, m analysis.AggregatedMetrics, pgb *analysis.PgBouncerMetrics, sections []string, full bool) {
	ExportMarkdown(w, m, sections, full)
	if pgb != nil && pgb.Lines > 0 {
		io.WriteString(w, "\n")
		ExportPgBouncerMarkdown(w, *pgb)
	}
}
