//go:build !js

package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBouncerMarkdown writes one pgBouncer report per pooler, each
// titled by the pooler's name when there are several.
func ExportPgBouncerMarkdown(w io.Writer, poolers []analysis.PgBouncerMetrics) {
	for i, m := range poolers {
		title := "# pgBouncer Report"
		if len(poolers) > 1 {
			title += " (" + escapeMDCell(m.Name) + ")"
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		exportPgBouncerMarkdown(w, m, title)
	}
}

// exportPgBouncerMarkdown writes one pooler's report as GitHub-flavoured
// Markdown: a summary table and its timeline, throughput/timing tables,
// the session-time histogram as a table, per-dimension breakdowns, and
// the errors.
func exportPgBouncerMarkdown(w io.Writer, m analysis.PgBouncerMetrics, title string) {
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)
	clock := pgbClock(m)

	fmt.Fprintln(w, title)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Metric | Value |")
	fmt.Fprintln(w, "|---|---|")
	fmt.Fprintf(w, "| Pooler | %s |\n", escapeMDCell(m.Name))
	if m.Version != "" {
		fmt.Fprintf(w, "| Version | PgBouncer %s |\n", escapeMDCell(m.Version))
	}
	fmt.Fprintf(w, "| Files | %s |\n", escapeMDCell(strings.Join(m.Files, ", ")))
	fmt.Fprintf(w, "| Start date | %s |\n", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "| End date | %s |\n", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "| Duration | %s |\n", duration)
	fmt.Fprintf(w, "| Log lines read | %d |\n", m.LinesRead)
	fmt.Fprintf(w, "| Log entries | %d |\n", m.Lines)
	if m.LinesUnrecognized > 0 {
		fmt.Fprintf(w, "| Unrecognized lines | %d |\n", m.LinesUnrecognized)
	}
	if n := m.LinesFiltered(); n > 0 {
		fmt.Fprintf(w, "| Filtered-out lines | %d |\n", n)
	}
	if m.DebugLines > 0 {
		fmt.Fprintf(w, "| Debug/noise entries | %d |\n", m.DebugLines)
	}
	fmt.Fprintf(w, "| Stats snapshots | %d |\n", m.StatsSampleCount)
	fmt.Fprintf(w, "| Login attempts | %d |\n", m.LoginAttempts)
	fmt.Fprintf(w, "| Server connections opened | %d |\n", m.ServerConnections)
	fmt.Fprintf(w, "| Completed sessions | %d |\n", m.Sessions)
	if m.ReservePoolActivations > 0 {
		fmt.Fprintf(w, "| Reserve pool activations | %d |\n", m.ReservePoolActivations)
	}
	fmt.Fprintf(w, "| Errors | %d |\n", m.TotalErrors)

	if len(m.Events) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "### Timeline")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Time | Event | Detail |")
		fmt.Fprintln(w, "|---|---|---|")
		for _, e := range m.Events {
			fmt.Fprintf(w, "| %s | %s | %s |\n", e.Time.Format(clock), e.Kind, escapeMDCell(e.Detail))
		}
	}

	if m.StatsSampleCount == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Request Throughput")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(No data available)")
		fmt.Fprintln(w)
		writeNoteMD(w, pgbNoStatsNote)
	}

	if m.StatsSampleCount > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Request Throughput")
		fmt.Fprintln(w)
		if m.StatsUnderCovered() {
			writeNoteMD(w, pgbCoverageNote(m))
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "*%s %s*\n", pgbRateNote[0], pgbRateNote[1])
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Metric | Average | Max snapshot average | At |")
		fmt.Fprintln(w, "|---|---|---|---|")
		fmt.Fprintf(w, "| Transactions/s | %.1f | %d | %s |\n", m.AvgXactsPerSec, m.MaxXactsPerSec, pgbAtMD(m.MaxXactsTimestamp, clock))
		fmt.Fprintf(w, "| Queries/s | %.1f | %d | %s |\n", m.AvgQueriesPerSec, m.MaxQueriesPerSec, pgbAtMD(m.MaxQueriesTimestamp, clock))
		fmt.Fprintf(w, "| Network in | %s | %s | %s |\n", bytesPerSec(m.AvgInBytesPerSec), bytesPerSec(float64(m.MaxInBytesPerSec)), pgbAtMD(m.MaxInTimestamp, clock))
		fmt.Fprintf(w, "| Network out | %s | %s | %s |\n", bytesPerSec(m.AvgOutBytesPerSec), bytesPerSec(float64(m.MaxOutBytesPerSec)), pgbAtMD(m.MaxOutTimestamp, clock))

		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Query Timing")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "*%s %s*\n", pgbTimingNote[0], pgbTimingNote[1])
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Metric | Weighted average | Max snapshot average | At |")
		fmt.Fprintln(w, "|---|---|---|---|")
		fmt.Fprintf(w, "| Transaction time | %s | %s | %s |\n", micros(m.AvgXactMicros), microsI(m.MaxXactMicros), pgbAtMD(m.MaxXactTimestamp, clock))
		fmt.Fprintf(w, "| Query time | %s | %s | %s |\n", micros(m.AvgQueryMicros), microsI(m.MaxQueryMicros), pgbAtMD(m.MaxQueryTimestamp, clock))

		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Pool Wait")
		fmt.Fprintln(w)
		if m.WaitIsQueueDepth() {
			writeNoteMD(w, pgbQueueDepthNote(m.Version))
			fmt.Fprintln(w)
			fmt.Fprintln(w, "| Metric | Average | Max snapshot average | At |")
			fmt.Fprintln(w, "|---|---|---|---|")
			fmt.Fprintf(w, "| Clients queued | %s | %s | %s |\n", queueDepth(m.AvgWaitMicros), queueDepth(float64(m.MaxWaitMicros)), pgbAtMD(m.MaxWaitTimestamp, clock))
		} else {
			writeNoteMD(w, pgbWaitNote)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "| Metric | Weighted average | Max snapshot average | At |")
			fmt.Fprintln(w, "|---|---|---|---|")
			fmt.Fprintf(w, "| Pool wait time | %s | %s | %s |\n", micros(m.AvgWaitMicros), microsI(m.MaxWaitMicros), pgbAtMD(m.MaxWaitTimestamp, clock))
		}
	}

	// Sessions and Connections below print their heading whether or not
	// they have anything under it: a pooler run with log_connections = 0
	// used to drop both from a document that went on reporting traffic
	// and errors, with nothing saying why.
	if m.Sessions == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Sessions")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(No data available)")
		fmt.Fprintln(w)
		writeNoteMD(w, pgbNoSessions.note(m.ClientLinesMissing()))
	}

	if m.Sessions > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Sessions")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "- Completed sessions: **%d**\n", m.Sessions)
		fmt.Fprintf(w, "- Average session time: **%s**\n", formatSessionDuration(time.Duration(m.AvgSessionSeconds()*float64(time.Second))))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Session time | Count |")
		fmt.Fprintln(w, "|---|---|")
		for _, b := range sessionBucketOrder {
			if c := m.SessionDistribution[b]; c > 0 {
				fmt.Fprintf(w, "| %s | %d |\n", escapeMDCell(b), c)
			}
		}
		if len(m.SessionReasons) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "### Sessions per close reason")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "| Reason | Count | % | Average age |")
			fmt.Fprintln(w, "|---|---|---|---|")
			for _, r := range headMD(m.SessionReasons) {
				avg := time.Duration(float64(r.TotalSeconds) / float64(r.Count) * float64(time.Second))
				fmt.Fprintf(w, "| %s | %d | %.1f%% | %s |\n", escapeMDCell(r.Reason), r.Count, float64(r.Count)/float64(m.Sessions)*100, formatSessionDuration(avg))
			}
			writeTailMD(w, len(m.SessionReasons), pgbListMax)
		}
		writeBreakdownMD(w, "Sessions per database", m.SessionsPerDatabase, m.Sessions)
		writeBreakdownMD(w, "Sessions per user", m.SessionsPerUser, m.Sessions)
		writeBreakdownMD(w, "Sessions per host", m.SessionsPerHost, m.Sessions)
	}

	if m.LoginAttempts == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Connections")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(No data available)")
		fmt.Fprintln(w)
		writeNoteMD(w, pgbNoConnections.note(m.ClientLinesMissing()))
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
		fmt.Fprintln(w, "| Error | Count | % | First | Last |")
		fmt.Fprintln(w, "|---|---|---|---|---|")
		for _, e := range headMD(m.Errors) {
			pct := float64(e.Count) / float64(m.TotalErrors) * 100
			fmt.Fprintf(w, "| %s | %d | %.1f%% | %s | %s |\n", escapeMDCell(e.Message), e.Count, pct, e.First.Format(clock), e.Last.Format(clock))
		}
		writeTailMD(w, len(m.Errors), pgbListMax)
	}
}

// writeNoteMD renders a note block as the single italic paragraph the
// document already uses for the throughput, timing and wait notes. The
// renderers hold the note as one line per printed line for the text
// report; Markdown reflows, so they are joined.
func writeNoteMD(w io.Writer, lines []string) {
	fmt.Fprintf(w, "*%s*\n", strings.Join(lines, " "))
}

// writeTailMD declares the rows a ranked table cut, in the form the
// PostgreSQL Markdown report already uses for its skipped-relation
// table. A title that names a whole dimension ("Connections per user")
// over a head of ten rows is a half-truth without it.
func writeTailMD(w io.Writer, total, shown int) {
	if total > shown {
		fmt.Fprintf(w, "\n_\u2026 and %d more_\n", total-shown)
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
		fmt.Fprintf(w, "| %s | %d | %.1f%% |\n", escapeMDCell(p.name), p.count, pct)
	}
	writeTailMD(w, len(m), 10)
}

// headMD keeps the first pgbListMax rows of a list.
func headMD[T any](rows []T) []T {
	return rows[:min(len(rows), pgbListMax)]
}

// pgbAtMD is the "At" cell of a maximum: its time, or a dash.
func pgbAtMD(t time.Time, clock string) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(clock)
}
