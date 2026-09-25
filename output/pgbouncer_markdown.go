//go:build !js

package output

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBouncerMarkdown writes one pgBouncer report per pooler, each
// titled by the pooler's name when there are several.
func ExportPgBouncerMarkdown(w io.Writer, poolers []analysis.PgBouncerMetrics) {
	var b strings.Builder
	for i, m := range poolers {
		title := "# pgBouncer Report"
		if len(poolers) > 1 {
			title += " (" + escapeMDCell(m.Name) + ")"
		}
		if i > 0 {
			b.WriteString("\n")
		}
		writePgBouncerMarkdown(&b, m, title)
	}
	io.WriteString(w, b.String())
}

// writePgBouncerMarkdown writes one pooler's report as GitHub-flavoured
// Markdown: a summary table and its timeline, throughput/timing tables,
// the session-time histogram as a table, per-dimension breakdowns, and
// the errors. Tables go through mdTable, as in the PostgreSQL report.
func writePgBouncerMarkdown(b *strings.Builder, m analysis.PgBouncerMetrics, title string) {
	clock := pgbClock(m)
	section := func(heading string) { fmt.Fprintf(b, "\n%s\n\n", heading) }

	fmt.Fprintf(b, "%s\n", title)
	section("## Summary")
	summary := [][]string{{"Pooler", m.Name}}
	if m.Version != "" {
		summary = append(summary, []string{"Version", "PgBouncer " + m.Version})
	}
	summary = append(summary,
		[]string{"Files", strings.Join(m.Files, ", ")},
		[]string{"Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST")},
		[]string{"End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST")},
		[]string{"Duration", m.MaxTimestamp.Sub(m.MinTimestamp).String()},
		[]string{"Log lines read", fmt.Sprint(m.LinesRead)},
		[]string{"Log entries", fmt.Sprint(m.Lines)})
	if m.LinesUnrecognized > 0 {
		summary = append(summary, []string{"Unrecognized lines", fmt.Sprint(m.LinesUnrecognized)})
	}
	if n := m.LinesFiltered(); n > 0 {
		summary = append(summary, []string{"Filtered-out lines", fmt.Sprint(n)})
	}
	if m.DebugLines > 0 {
		summary = append(summary, []string{"Debug/noise entries", fmt.Sprint(m.DebugLines)})
	}
	summary = append(summary,
		[]string{"Stats snapshots", fmt.Sprint(m.StatsSampleCount)},
		[]string{"Login attempts", fmt.Sprint(m.LoginAttempts)},
		[]string{"Server connections opened", fmt.Sprint(m.ServerConnections)},
		[]string{"Completed sessions", fmt.Sprint(m.Sessions)})
	if m.ReservePoolActivations > 0 {
		summary = append(summary, []string{"Reserve pool activations", fmt.Sprint(m.ReservePoolActivations)})
	}
	summary = append(summary, []string{"Errors", fmt.Sprint(m.TotalErrors)})
	mdTable(b, []string{"Metric", "Value"}, "ll", summary)

	if len(m.Events) > 0 {
		section("### Timeline")
		rows := make([][]string, len(m.Events))
		for i, e := range m.Events {
			rows[i] = []string{e.Time.Format(clock), e.Kind, e.Detail}
		}
		mdTable(b, []string{"Time", "Event", "Detail"}, "lll", rows)
	}

	section("## Request Throughput")
	if m.StatsSampleCount == 0 {
		b.WriteString("(No data available)\n\n")
		writeNoteMD(b, pgbNoStatsNote)
	} else {
		if m.StatsUnderCovered() {
			writeNoteMD(b, pgbCoverageNote(m))
			b.WriteString("\n")
		}
		writeNoteMD(b, pgbRateNote[:])
		b.WriteString("\n")
		mdTable(b, []string{"Metric", "Average", "Max snapshot average", "At"}, "lrrr", [][]string{
			{"Transactions/s", fmt.Sprintf("%.1f", m.AvgXactsPerSec), fmt.Sprint(m.MaxXactsPerSec), pgbAtMD(m.MaxXactsTimestamp, clock)},
			{"Queries/s", fmt.Sprintf("%.1f", m.AvgQueriesPerSec), fmt.Sprint(m.MaxQueriesPerSec), pgbAtMD(m.MaxQueriesTimestamp, clock)},
			{"Network in", bytesPerSec(m.AvgInBytesPerSec), bytesPerSec(float64(m.MaxInBytesPerSec)), pgbAtMD(m.MaxInTimestamp, clock)},
			{"Network out", bytesPerSec(m.AvgOutBytesPerSec), bytesPerSec(float64(m.MaxOutBytesPerSec)), pgbAtMD(m.MaxOutTimestamp, clock)},
		})

		section("## Query Timing")
		writeNoteMD(b, pgbTimingNote[:])
		b.WriteString("\n")
		mdTable(b, []string{"Metric", "Weighted average", "Max snapshot average", "At"}, "lrrr", [][]string{
			{"Transaction time", micros(m.AvgXactMicros), microsI(m.MaxXactMicros), pgbAtMD(m.MaxXactTimestamp, clock)},
			{"Query time", micros(m.AvgQueryMicros), microsI(m.MaxQueryMicros), pgbAtMD(m.MaxQueryTimestamp, clock)},
		})

		section("## Pool Wait")
		if m.WaitIsQueueDepth() {
			writeNoteMD(b, pgbQueueDepthNote(m.Version))
			b.WriteString("\n")
			mdTable(b, []string{"Metric", "Average", "Max snapshot average", "At"}, "lrrr", [][]string{
				{"Clients queued", queueDepth(m.AvgWaitMicros), queueDepth(float64(m.MaxWaitMicros)), pgbAtMD(m.MaxWaitTimestamp, clock)},
			})
		} else {
			writeNoteMD(b, pgbWaitNote)
			b.WriteString("\n")
			mdTable(b, []string{"Metric", "Weighted average", "Max snapshot average", "At"}, "lrrr", [][]string{
				{"Pool wait time", micros(m.AvgWaitMicros), microsI(m.MaxWaitMicros), pgbAtMD(m.MaxWaitTimestamp, clock)},
			})
		}
		if len(m.WaitEpisodes) > 0 {
			section("### " + pgbEpisodeTitle(m))
			var rows [][]string
			for _, e := range headMD(m.WaitEpisodes) {
				rows = append(rows, []string{e.Start.Format(clock), e.End.Format(clock),
					formatSessionDuration(e.End.Sub(e.Start)), pgbWait(m, e.PeakUs), e.PeakTime.Format(clock)})
			}
			mdTable(b, []string{"Start", "End", "Duration", "Peak", "Peak at"}, "llrrl", rows)
			writeTailMD(b, len(m.WaitEpisodes), pgbListMax)
		}
	}

	// Sessions and Connections print their heading whether or not they
	// have anything under it, with the setting that governs them: an
	// absent section cannot tell an idle pooler from a silenced one.
	section("## Sessions")
	if m.Sessions == 0 {
		b.WriteString("(No data available)\n\n")
		writeNoteMD(b, pgbNoSessions.note(m.ClientLinesMissing()))
	} else {
		fmt.Fprintf(b, "- Completed sessions: **%d**\n", m.Sessions)
		fmt.Fprintf(b, "- Average session time: **%s**\n", formatSessionDuration(time.Duration(m.AvgSessionSeconds()*float64(time.Second))))
		if len(m.SessionReasons) > 0 {
			byClient := m.SessionsEndedByClient()
			fmt.Fprintf(b, "- Ended by the client: **%d**\n", byClient)
			fmt.Fprintf(b, "- Ended by the pooler: **%d**\n", m.Sessions-byClient)
		}
		b.WriteString("\n")
		var hist [][]string
		for _, bucket := range sessionBucketOrder {
			if c := m.SessionDistribution[bucket]; c > 0 {
				hist = append(hist, []string{bucket, fmt.Sprint(c)})
			}
		}
		mdTable(b, []string{"Session time", "Count"}, "lr", hist)
		if len(m.SessionReasons) > 0 {
			section("### Sessions per close reason")
			var rows [][]string
			for _, r := range headMD(m.SessionReasons) {
				avg := time.Duration(float64(r.TotalSeconds) / float64(r.Count) * float64(time.Second))
				rows = append(rows, []string{r.Reason, fmt.Sprint(r.Count), pct(r.Count, m.Sessions), formatSessionDuration(avg)})
			}
			mdTable(b, []string{"Reason", "Count", "%", "Average age"}, "lrrr", rows)
			writeTailMD(b, len(m.SessionReasons), pgbListMax)
		}
		writeBreakdownMD(b, "Sessions per database", m.SessionsPerDatabase, m.Sessions)
		writeBreakdownMD(b, "Sessions per user", m.SessionsPerUser, m.Sessions)
		writeBreakdownMD(b, "Sessions per host", m.SessionsPerHost, m.Sessions)
	}

	if m.LoginAttempts == 0 {
		section("## Connections")
		b.WriteString("(No data available)\n\n")
		writeNoteMD(b, pgbNoConnections.note(m.ClientLinesMissing()))
	} else {
		b.WriteString("\n## Connections\n") // each breakdown opens with its own blank line
		writeBreakdownMD(b, "Connections per database", m.ConnectionsPerDatabase, m.LoginAttempts)
		writeBreakdownMD(b, "Connections per user", m.ConnectionsPerUser, m.LoginAttempts)
		writeBreakdownMD(b, "Connections per host", m.ConnectionsPerHost, m.LoginAttempts)
	}

	if m.TotalErrors > 0 {
		section("## Most Frequent Errors")
		var rows [][]string
		for _, e := range headMD(m.Errors) {
			rows = append(rows, []string{e.Message, fmt.Sprint(e.Count), pct(e.Count, m.TotalErrors), e.First.Format(clock), e.Last.Format(clock)})
		}
		mdTable(b, []string{"Error", "Count", "%", "First", "Last"}, "lrrrr", rows)
		writeTailMD(b, len(m.Errors), pgbListMax)
	}
}

// writeNoteMD renders a note block as the single italic paragraph the
// document already uses for the throughput, timing and wait notes. The
// renderers hold the note as one line per printed line for the text
// report; Markdown reflows, so they are joined.
func writeNoteMD(b *strings.Builder, lines []string) {
	fmt.Fprintf(b, "*%s*\n", strings.Join(lines, " "))
}

// writeTailMD declares the rows a ranked table cut, in the form the
// PostgreSQL Markdown report already uses for its skipped-relation
// table. A title that names a whole dimension ("Connections per user")
// over a head of ten rows is a half-truth without it.
func writeTailMD(b *strings.Builder, total, shown int) {
	if total > shown {
		fmt.Fprintf(b, "\n_\u2026 and %d more_\n", total-shown)
	}
}

// writeBreakdownMD renders the ten heaviest names of a breakdown.
func writeBreakdownMD(b *strings.Builder, title string, counts map[string]int, total int) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### %s\n\n", title)
	var rows [][]string
	for _, e := range analysis.SortByCount(counts)[:min(len(counts), 10)] {
		rows = append(rows, []string{e.Name, fmt.Sprint(e.Count), pct(e.Count, total)})
	}
	mdTable(b, []string{"Name", "Count", "%"}, "lrr", rows)
	writeTailMD(b, len(counts), 10)
}

// pct renders a share of a total with one decimal.
func pct(n, total int) string {
	return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
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
