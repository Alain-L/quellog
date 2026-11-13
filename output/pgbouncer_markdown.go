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
// titled by the pooler's name when there are several. The default view is
// synthetic; detail (--pgbouncer-detail, --full) adds every breakdown.
func ExportPgBouncerMarkdown(w io.Writer, poolers []analysis.PgBouncerMetrics, detail bool) {
	var b strings.Builder
	for i, m := range poolers {
		title := "## PGBOUNCER"
		if len(poolers) > 1 {
			title += " " + escapeMDCell(m.Name)
		}
		if i > 0 {
			b.WriteString("\n")
		}
		writePgBouncerMarkdown(&b, m, title, detail)
	}
	io.WriteString(w, b.String())
}

// writePgBouncerMarkdown writes one pooler's report as the text report
// lays it out: the summary rows as a table, the warnings and errors, the
// activity table, and in the detailed view the highest snapshot averages
// and the breakdowns. Tables go through mdTable, as in the PostgreSQL
// report.
func writePgBouncerMarkdown(b *strings.Builder, m analysis.PgBouncerMetrics, title string, detail bool) {
	clock := pgbClock(m)
	section := func(heading string) { fmt.Fprintf(b, "\n%s\n\n", heading) }

	fmt.Fprintf(b, "%s\n\n", title)
	var rows [][]string
	for _, r := range pgbSummaryRows(m, detail) {
		rows = append(rows, []string{r[0], r[1]})
	}
	mdTable(b, []string{"Metric", "Value"}, "ll", rows)

	if detail && m.StatsSampleCount > 1 {
		section("### Max snapshot averages")
		rows = nil
		for _, r := range pgbMaxRows(m) {
			rows = append(rows, []string{r[0], r[1]})
		}
		mdTable(b, []string{"Metric", "Value"}, "ll", rows)
	}

	section("### " + pgbMessagesTitle(m))
	if list, n := pgbMessages(m, detail); n > 0 {
		rows = nil
		for _, e := range list[:n] {
			r := []string{e.Message, fmt.Sprint(e.Count), pgbWhen(e, clock)}
			if detail {
				r = append([]string{"`" + e.ID + "`"}, r...)
			}
			rows = append(rows, r)
		}
		if detail {
			mdTable(b, []string{"ID", "Message", "Count", "When"}, "llrl", rows)
		} else {
			mdTable(b, []string{"Message", "Count", "When"}, "lrl", rows)
		}
		writeTailMD(b, len(list), n)
	}

	max := pgbActivityRows
	if detail {
		max = pgbActivityDetailRows
	}
	if a, ok := pgbBuildActivity(m, max); ok {
		layout := a.clock(m)
		section("### Activity " + a.title())
		rows = nil
		for _, r := range a.Rows {
			switch {
			case !r.IdleUntil.IsZero():
				rows = append(rows, []string{r.At.Format(layout), "0/s", "", "", "no traffic until " + r.IdleUntil.Format(layout)})
			case r.Snapshots == 0:
				rows = append(rows, []string{r.At.Format(layout), "", "", "", pgbEventsShort(r.Events)})
			default:
				rows = append(rows, []string{r.At.Format(layout), pgbRate(r.Queries), pgbRowQuery(r.QueryUs), pgbRowWait(m, r.WaitUs), pgbEventsShort(r.Events)})
			}
		}
		wait := "Pool wait"
		if m.WaitIsQueueDepth() {
			wait = "Clients queued"
		}
		mdTable(b, []string{"Time", "Queries", "Query time", wait, "Events"}, "lrrrl", rows)
		fmt.Fprintf(b, "\n_Query time and %s: worst snapshot of each row._\n", strings.ToLower(wait))
	} else {
		if len(m.Events) > 0 {
			section("### Timeline")
			rows = nil
			for _, e := range m.Events {
				rows = append(rows, []string{e.Time.Format(clock), e.Kind, pgbEventDetail(e)})
			}
			mdTable(b, []string{"Time", "Event", "Detail"}, "lll", rows)
		}
		if detail && len(m.WaitEpisodes) > 0 {
			section("### " + pgbEpisodeTitle(m))
			rows = nil
			for _, e := range headMD(m.WaitEpisodes) {
				rows = append(rows, []string{e.Start.Format(clock), e.End.Format(clock),
					formatSessionDuration(e.End.Sub(e.Start)), pgbWait(m, e.PeakUs), e.PeakTime.Format(clock)})
			}
			mdTable(b, []string{"Start", "End", "Duration", "Peak", "Peak at"}, "llrrl", rows)
			writeTailMD(b, len(m.WaitEpisodes), pgbListMax)
		}
	}

	if !detail {
		b.WriteString("\n_" + pgbDetailHint + "_\n")
		return
	}
	if m.Sessions > 0 {
		section("### Session time")
		rows = nil
		for _, bucket := range sessionBucketOrder {
			if c := m.SessionDistribution[bucket]; c > 0 {
				rows = append(rows, []string{bucket, fmt.Sprint(c)})
			}
		}
		mdTable(b, []string{"Session time", "Count"}, "lr", rows)
		if len(m.SessionReasons) > 0 {
			section("### Sessions per close reason")
			rows = nil
			for _, r := range headMD(m.SessionReasons) {
				avg := time.Duration(float64(r.TotalSeconds) / float64(r.Count) * float64(time.Second))
				rows = append(rows, []string{r.Reason, fmt.Sprint(r.Count), pct(r.Count, m.Sessions), formatSessionDuration(avg)})
			}
			mdTable(b, []string{"Reason", "Count", "%", "Average age"}, "lrrr", rows)
			writeTailMD(b, len(m.SessionReasons), pgbListMax)
		}
	}
	if m.LoginAttempts > 0 {
		writeBreakdownMD(b, "Connections per database", m.ConnectionsPerDatabase, m.LoginAttempts)
		writeBreakdownMD(b, "Connections per user", m.ConnectionsPerUser, m.LoginAttempts)
		writeBreakdownMD(b, "Connections per host", m.ConnectionsPerHost, m.LoginAttempts)
	}
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

// writeBreakdownMD renders the heaviest names of a breakdown, cut as the
// text report cuts them (rankedKept).
func writeBreakdownMD(b *strings.Builder, title string, counts map[string]int, total int) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### %s\n\n", title)
	sorted := analysis.SortByCount(counts)
	values := make([]int, len(sorted))
	for i, e := range sorted {
		values[i] = e.Count
	}
	kept := rankedKept(values, total)
	var rows [][]string
	for _, e := range sorted[:kept] {
		rows = append(rows, []string{e.Name, fmt.Sprint(e.Count), pct(e.Count, total)})
	}
	mdTable(b, []string{"Name", "Count", "%"}, "lrr", rows)
	writeTailMD(b, len(counts), kept)
}

// pct renders a share of a total with one decimal.
func pct(n, total int) string {
	return fmt.Sprintf("%.1f%%", float64(n)/float64(total)*100)
}

// headMD keeps the first pgbListMax rows of a list.
func headMD[T any](rows []T) []T {
	return rows[:min(len(rows), pgbListMax)]
}
