//go:build !js

package output

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/web"
)

// pgbTemplateValues are the fields injected into web/pgbouncer.tmpl.
type pgbTemplateValues struct {
	CSS       template.CSS
	Body      template.HTML
	Meta      string
	Version   string
	UplotJS   template.JS
	ChartsJS  template.JS
	ChartData template.JS
}

// ExportPgBouncerHTML writes a self-contained HTML pgBouncer report.
// Tables and cards are rendered server-side with the same CSS classes as
// the PostgreSQL report (shared web/styles.css), and three uPlot
// time-series charts are drawn client-side from the embedded telemetry.
func ExportPgBouncerHTML(w io.Writer, m analysis.PgBouncerMetrics, info HTMLReportInfo) error {
	tmpl, err := template.New("pgbouncer").Parse(web.PgBouncerTmpl)
	if err != nil {
		return fmt.Errorf("failed to parse pgBouncer template: %w", err)
	}

	vals := pgbTemplateValues{
		CSS:       template.CSS(minifyCSS(web.StylesCSS + pgbExtraCSS)),
		Body:      template.HTML(buildPgBouncerBody(m)),
		Meta:      buildPgBouncerMeta(m, info),
		Version:   info.Version,
		UplotJS:   template.JS(web.UplotJS),
		ChartsJS:  template.JS(web.PgBouncerChartsJS),
		ChartData: template.JS(buildPgBouncerChartData(m)),
	}
	if err := tmpl.Execute(w, vals); err != nil {
		return fmt.Errorf("failed to render pgBouncer report: %w", err)
	}
	return nil
}

// pgbExtraCSS layers a few pgBouncer-only rules on top of the shared
// stylesheet (chart sizing, the summary meta line).
const pgbExtraCSS = `
.report{max-width:1120px;margin:0 auto;padding:0 1.5rem 2rem}
.report-header{padding:1.1rem 0 0.9rem;border-bottom:2px solid var(--border);margin-bottom:1.25rem}
.report-title{font-size:1.4rem;font-weight:600;color:var(--text)}
.report-meta{font-size:0.8rem;color:var(--text-muted);margin-top:0.3rem}
.report-footer{margin-top:2rem;padding-top:1rem;border-top:1px solid var(--border);font-size:0.75rem;color:var(--text-muted);text-align:center}
.pgb-chart{width:100%;height:220px}
.pgb-charts{display:grid;grid-template-columns:1fr;gap:1rem}
.pgb-charts>div{margin-bottom:2.75rem}
.pgb-charts>div:last-child{margin-bottom:0}
.pgb-tables{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:1rem 1.5rem}
.pgb-bar{display:inline-block;height:8px;min-width:2px;background:var(--chart-bar,var(--accent));border-radius:2px;vertical-align:middle}
`

func buildPgBouncerMeta(m analysis.PgBouncerMetrics, info HTMLReportInfo) string {
	dur := m.MaxTimestamp.Sub(m.MinTimestamp)
	parts := []string{
		html.EscapeString(info.Filename),
		FormatBytes(info.FileSize),
		fmt.Sprintf("%s – %s (%s)",
			m.MinTimestamp.Format("2006-01-02 15:04"),
			m.MaxTimestamp.Format("2006-01-02 15:04"),
			dur.Round(time.Second)),
		fmt.Sprintf("%d log lines in %.0f ms", m.Lines, info.ProcessTime),
	}
	return strings.Join(parts, "  ·  ")
}

// buildPgBouncerChartData serializes the stats time-series into parallel
// arrays for uPlot (x is unix seconds).
func buildPgBouncerChartData(m analysis.PgBouncerMetrics) string {
	n := len(m.StatsSeries)
	tms := make([]int64, n)
	xacts := make([]int, n)
	queries := make([]int, n)
	in := make([]int, n)
	out := make([]int, n)
	xactUs := make([]int64, n)
	queryUs := make([]int64, n)
	waitUs := make([]int64, n)
	for i, p := range m.StatsSeries {
		tms[i] = p.Time.Unix()
		xacts[i] = p.Xacts
		queries[i] = p.Queries
		in[i] = p.InBytes
		out[i] = p.OutBytes
		xactUs[i] = p.XactMicros
		queryUs[i] = p.QueryMicros
		waitUs[i] = p.WaitMicros
	}
	data := map[string]interface{}{
		"t": tms, "xacts": xacts, "queries": queries,
		"in": in, "out": out,
		"xact_us": xactUs, "query_us": queryUs, "wait_us": waitUs,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// buildPgBouncerBody renders every report section as HTML using the
// shared card/table classes.
func buildPgBouncerBody(m analysis.PgBouncerMetrics) string {
	var b strings.Builder

	// Summary cards.
	b.WriteString(`<div class="section" id="summary"><div class="section-header">Summary</div><div class="section-body"><div class="stat-grid">`)
	card(&b, fmt.Sprintf("%.1f/s", m.AvgQueriesPerSec), "Avg queries")
	card(&b, fmt.Sprintf("%d", m.MaxQueriesPerSec), "Peak queries/s")
	card(&b, fmt.Sprintf("%d", m.LoginAttempts), "Login attempts")
	card(&b, fmt.Sprintf("%d", m.Sessions), "Sessions")
	card(&b, fmt.Sprintf("%d", m.ServerConnections), "Server conns")
	if m.ReservePoolActivations > 0 {
		card(&b, fmt.Sprintf("%d", m.ReservePoolActivations), "Reserve pool")
	}
	card(&b, fmt.Sprintf("%d", m.TotalErrors), "Errors")
	b.WriteString(`</div></div></div>`)

	// Charts.
	if m.StatsSampleCount > 0 {
		b.WriteString(`<div class="section"><div class="section-header">Throughput &amp; Timing</div><div class="section-body"><div class="pgb-charts">`)
		b.WriteString(`<div><div class="subsection-title">Request throughput (per second)</div><div id="chart-throughput" class="pgb-chart"></div></div>`)
		b.WriteString(`<div><div class="subsection-title">Network I/O (bytes per second)</div><div id="chart-io" class="pgb-chart"></div></div>`)
		b.WriteString(`<div><div class="subsection-title">Transaction / query / wait time</div><div id="chart-timing" class="pgb-chart"></div></div>`)
		b.WriteString(`</div>`)
		// Numeric summary of averages/peaks.
		b.WriteString(`<div class="pgb-tables" style="margin-top:1rem">`)
		avgPeakTable(&b, "Throughput", [][3]string{
			{"Transactions/s", fmt.Sprintf("%.1f", m.AvgXactsPerSec), fmt.Sprintf("%d", m.MaxXactsPerSec)},
			{"Queries/s", fmt.Sprintf("%.1f", m.AvgQueriesPerSec), fmt.Sprintf("%d", m.MaxQueriesPerSec)},
			{"Network in", bytesPerSecMD(m.AvgInBytesPerSec), bytesPerSecMD(float64(m.MaxInBytesPerSec))},
			{"Network out", bytesPerSecMD(m.AvgOutBytesPerSec), bytesPerSecMD(float64(m.MaxOutBytesPerSec))},
		})
		avgPeakTable(&b, "Timing", [][3]string{
			{"Transaction", microsMD(m.AvgXactMicros), microsMD(float64(m.MaxXactMicros))},
			{"Query", microsMD(m.AvgQueryMicros), microsMD(float64(m.MaxQueryMicros))},
			{"Wait", microsMD(m.AvgWaitMicros), microsMD(float64(m.MaxWaitMicros))},
		})
		b.WriteString(`</div></div></div>`)
	}

	// Sessions.
	if m.Sessions > 0 {
		b.WriteString(`<div class="section" id="sessions"><div class="section-header">Sessions</div><div class="section-body">`)
		b.WriteString(`<div class="stat-grid">`)
		card(&b, fmt.Sprintf("%d", m.Sessions), "Completed")
		card(&b, formatSessionDurationMD(m.AvgSessionSeconds()), "Avg session")
		b.WriteString(`</div>`)
		histogramTable(&b, "Session time", m.SessionDistribution, sessionBucketOrder, m.Sessions)
		b.WriteString(`<div class="pgb-tables">`)
		breakdownTable(&b, "Sessions per database", m.SessionsPerDatabase, m.Sessions)
		breakdownTable(&b, "Sessions per user", m.SessionsPerUser, m.Sessions)
		breakdownTable(&b, "Sessions per host", m.SessionsPerHost, m.Sessions)
		b.WriteString(`</div></div></div>`)
	}

	// Connections.
	if m.LoginAttempts > 0 {
		b.WriteString(`<div class="section" id="connections"><div class="section-header">Connections</div><div class="section-body"><div class="pgb-tables">`)
		breakdownTable(&b, "Connections per database", m.ConnectionsPerDatabase, m.LoginAttempts)
		breakdownTable(&b, "Connections per user", m.ConnectionsPerUser, m.LoginAttempts)
		breakdownTable(&b, "Connections per host", m.ConnectionsPerHost, m.LoginAttempts)
		b.WriteString(`</div></div></div>`)
	}

	// Errors.
	if m.TotalErrors > 0 {
		b.WriteString(`<div class="section" id="errors"><div class="section-header">Most Frequent Errors</div><div class="section-body">`)
		breakdownTable(&b, "", m.Errors, m.TotalErrors)
		b.WriteString(`</div></div>`)
	}

	return b.String()
}

func card(b *strings.Builder, value, label string) {
	fmt.Fprintf(b, `<div class="stat-card"><div class="stat-value">%s</div><div class="stat-label">%s</div></div>`,
		html.EscapeString(value), html.EscapeString(label))
}

func avgPeakTable(b *strings.Builder, title string, rows [][3]string) {
	b.WriteString(`<div><table class="data-table"><thead><tr><th>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</th><th>Average</th><th>Peak</th></tr></thead><tbody>`)
	for _, r := range rows {
		fmt.Fprintf(b, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`,
			html.EscapeString(r[0]), html.EscapeString(r[1]), html.EscapeString(r[2]))
	}
	b.WriteString(`</tbody></table></div>`)
}

func histogramTable(b *strings.Builder, title string, dist map[string]int, order []string, total int) {
	if total == 0 {
		return
	}
	max := 0
	for _, c := range dist {
		if c > max {
			max = c
		}
	}
	b.WriteString(`<div class="subsection"><div class="subsection-title">`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</div><table class="data-table"><tbody>`)
	for _, label := range order {
		c := dist[label]
		if c == 0 {
			continue
		}
		w := 0
		if max > 0 {
			w = c * 120 / max
		}
		pct := float64(c) / float64(total) * 100
		fmt.Fprintf(b, `<tr><td>%s</td><td><span class="pgb-bar" style="width:%dpx"></span></td><td>%d</td><td>%.1f%%</td></tr>`,
			html.EscapeString(label), w, c, pct)
	}
	b.WriteString(`</tbody></table></div>`)
}

func breakdownTable(b *strings.Builder, title string, m map[string]int, total int) {
	if len(m) == 0 {
		return
	}
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(m))
	for n, c := range m {
		pairs = append(pairs, pair{n, c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > 10 {
		pairs = pairs[:10]
	}
	b.WriteString(`<div>`)
	if title != "" {
		b.WriteString(`<div class="subsection-title">`)
		b.WriteString(html.EscapeString(title))
		b.WriteString(`</div>`)
	}
	b.WriteString(`<table class="data-table"><tbody>`)
	for _, p := range pairs {
		pct := float64(p.count) / float64(total) * 100
		fmt.Fprintf(b, `<tr><td>%s</td><td>%d</td><td>%.1f%%</td></tr>`,
			html.EscapeString(p.name), p.count, pct)
	}
	b.WriteString(`</tbody></table></div>`)
}
