//go:build !js

package output

import (
	"fmt"
	"io"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBouncerHTML writes a self-contained HTML pgBouncer report.
//
// TODO(pgbouncer): full dataviz report (throughput time-series, session
// histogram, breakdown tables) wired to web/js/sections/pgbouncer.js.
// This stub keeps the --html flag honest while the web layer is built.
func ExportPgBouncerHTML(w io.Writer, m analysis.PgBouncerMetrics, info HTMLReportInfo) error {
	return fmt.Errorf("pgBouncer HTML report is not implemented yet")
}
