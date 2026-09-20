//go:build !js

package output

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBouncerMarkdown writes the pgBouncer report as GitHub-flavoured
// Markdown: a summary table, throughput/timing tables, the session-time
// histogram as a table, per-dimension breakdowns, and the error ranking.
func ExportPgBouncerMarkdown(w io.Writer, m analysis.PgBouncerMetrics) {
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)

	fmt.Fprintln(w, "# pgBouncer Report")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Metric | Value |")
	fmt.Fprintln(w, "|---|---|")
	fmt.Fprintf(w, "| Start date | %s |\n", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "| End date | %s |\n", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "| Duration | %s |\n", duration)
	fmt.Fprintf(w, "| Log lines read | %d |\n", m.LinesRead)
	fmt.Fprintf(w, "| Log entries | %d |\n", m.Lines)
	if m.LinesUnrecognized > 0 {
		fmt.Fprintf(w, "| Unrecognized lines | %d |\n", m.LinesUnrecognized)
	}
	if m.DebugLines > 0 {
		fmt.Fprintf(w, "| Debug/noise entries | %d |\n", m.DebugLines)
	}
	fmt.Fprintf(w, "| Stats snapshots | %d |\n", m.StatsSampleCount)
	fmt.Fprintf(w, "| Login attempts | %d |\n", m.LoginAttempts)
	fmt.Fprintf(w, "| Server connections | %d |\n", m.ServerConnections)
	fmt.Fprintf(w, "| Completed sessions | %d |\n", m.Sessions)
	if m.ReservePoolActivations > 0 {
		fmt.Fprintf(w, "| Reserve pool activations | %d |\n", m.ReservePoolActivations)
	}
	fmt.Fprintf(w, "| Errors | %d |\n", m.TotalErrors)

	if m.StatsSampleCount > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Request Throughput")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Metric | Average | Peak |")
		fmt.Fprintln(w, "|---|---|---|")
		fmt.Fprintf(w, "| Transactions/s | %.1f | %d |\n", m.AvgXactsPerSec, m.MaxXactsPerSec)
		fmt.Fprintf(w, "| Queries/s | %.1f | %d |\n", m.AvgQueriesPerSec, m.MaxQueriesPerSec)
		fmt.Fprintf(w, "| Network in | %s | %s |\n", bytesPerSecMD(m.AvgInBytesPerSec), bytesPerSecMD(float64(m.MaxInBytesPerSec)))
		fmt.Fprintf(w, "| Network out | %s | %s |\n", bytesPerSecMD(m.AvgOutBytesPerSec), bytesPerSecMD(float64(m.MaxOutBytesPerSec)))

		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Query Timing")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "*%s %s*\n", pgbTimingNote[0], pgbTimingNote[1])
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Metric | Weighted average | Max snapshot average |")
		fmt.Fprintln(w, "|---|---|---|")
		fmt.Fprintf(w, "| Transaction time | %s | %s |\n", microsMD(m.AvgXactMicros), microsMD(float64(m.MaxXactMicros)))
		fmt.Fprintf(w, "| Query time | %s | %s |\n", microsMD(m.AvgQueryMicros), microsMD(float64(m.MaxQueryMicros)))
		fmt.Fprintf(w, "| Wait time | %s | %s |\n", microsMD(m.AvgWaitMicros), microsMD(float64(m.MaxWaitMicros)))
	}

	if m.Sessions > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Sessions")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "- Completed sessions: **%d**\n", m.Sessions)
		fmt.Fprintf(w, "- Average session time: **%s**\n", formatSessionDurationMD(m.AvgSessionSeconds()))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Session time | Count |")
		fmt.Fprintln(w, "|---|---|")
		for _, b := range sessionBucketOrder {
			if c := m.SessionDistribution[b]; c > 0 {
				fmt.Fprintf(w, "| %s | %d |\n", b, c)
			}
		}
		writeBreakdownMD(w, "Sessions per database", m.SessionsPerDatabase, m.Sessions)
		writeBreakdownMD(w, "Sessions per user", m.SessionsPerUser, m.Sessions)
		writeBreakdownMD(w, "Sessions per host", m.SessionsPerHost, m.Sessions)
	}

	if m.LoginAttempts > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Connections")
		writeBreakdownMD(w, "Connections per database", m.ConnectionsPerDatabase, m.LoginAttempts)
		writeBreakdownMD(w, "Connections per user", m.ConnectionsPerUser, m.LoginAttempts)
		writeBreakdownMD(w, "Connections per host", m.ConnectionsPerHost, m.LoginAttempts)
	}

	if m.TotalErrors > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Most Frequent Errors")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Error | Count | % |")
		fmt.Fprintln(w, "|---|---|---|")
		for _, p := range topPairsMD(m.Errors, 10) {
			pct := float64(p.count) / float64(m.TotalErrors) * 100
			fmt.Fprintf(w, "| %s | %d | %.1f%% |\n", p.name, p.count, pct)
		}
	}
}

type mdPair struct {
	name  string
	count int
}

func topPairsMD(m map[string]int, limit int) []mdPair {
	pairs := make([]mdPair, 0, len(m))
	for n, c := range m {
		pairs = append(pairs, mdPair{n, c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return pairs
}

func writeBreakdownMD(w io.Writer, title string, m map[string]int, total int) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "### %s\n", title)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Name | Count | % |")
	fmt.Fprintln(w, "|---|---|---|")
	for _, p := range topPairsMD(m, 10) {
		pct := float64(p.count) / float64(total) * 100
		fmt.Fprintf(w, "| %s | %d | %.1f%% |\n", p.name, p.count, pct)
	}
}

func bytesPerSecMD(v float64) string {
	return FormatBytes(int64(v+0.5)) + "/s"
}

func microsMD(us float64) string {
	switch {
	case us < 1000:
		return fmt.Sprintf("%.0f µs", us)
	case us < 1_000_000:
		return fmt.Sprintf("%.1f ms", us/1000)
	default:
		return fmt.Sprintf("%.2f s", us/1_000_000)
	}
}

// formatSessionDurationMD renders a whole-second session mean. Kept
// local (not shared with the text renderer's formatSessionDuration,
// which lives behind //go:build !js) so this file stays WASM-safe.
func formatSessionDurationMD(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d < time.Hour:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
}
