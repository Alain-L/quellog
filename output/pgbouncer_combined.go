//go:build !js

package output

import (
	"io"

	"github.com/Alain-L/quellog/analysis"
)

// PrintMetricsCombined renders the PostgreSQL text report and, when
// pgBouncer files were part of the same run, appends the pgBouncer
// section after it. With pgb empty it is identical to PrintMetrics.
// pgbOnly marks a pure pgBouncer run: m is then the zero value, so only
// the pooler section is printed.
func PrintMetricsCombined(m analysis.AggregatedMetrics, pgb []analysis.PgBouncerMetrics, sections []string, full, pgbOnly bool) {
	if !pgbOnly {
		PrintMetrics(m, sections, full)
	}
	if len(pgb) > 0 {
		PrintPgBouncer(pgb)
	}
}

// ExportMarkdownCombined renders the PostgreSQL Markdown report and
// appends the pgBouncer report when present. With pgb empty it is
// identical to ExportMarkdown. pgbOnly marks a pure pgBouncer run: the
// pooler report is then the whole document.
func ExportMarkdownCombined(w io.Writer, m analysis.AggregatedMetrics, pgb []analysis.PgBouncerMetrics, sections []string, full, pgbOnly bool) {
	if pgbOnly {
		ExportPgBouncerMarkdown(w, pgb)
		return
	}
	ExportMarkdown(w, m, sections, full)
	if len(pgb) > 0 {
		io.WriteString(w, "\n")
		ExportPgBouncerMarkdown(w, pgb)
	}
}
