//go:build !js

package output

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Alain-L/quellog/analysis"
)

func writePatroniMarkdown(b *strings.Builder, m *analysis.PatroniMetrics, detail bool) {
	j := buildPatroniJSON(m)
	clock := patroniClock(j)
	b.WriteString("## PATRONI\n\n")
	rows := [][]string{{"Start date", j.StartDate}, {"End date", j.EndDate}}
	if detail {
		rows = append(rows, []string{"Duration", j.Duration}, []string{"Total entries", strconv.Itoa(j.TotalLogs)})
	}
	rows = append(rows, []string{"Members", patroniMemberNames(j)})
	if len(j.MissingLogs) > 0 {
		rows = append(rows, []string{"No log from", strings.Join(j.MissingLogs, ", ") + ", " + patroniMissingNote})
	}
	rows = append(rows,
		[]string{"Leader at end", patroniLeaderText(j, clock)},
		[]string{"Leader changes", patroniChangesText(j)},
		[]string{"Time without leader", j.LeaderlessTime},
		[]string{"DCS incidents", patroniDCSText(j)})
	if n := j.EventCounts["lag_refused"]; detail || n > 0 {
		rows = append(rows, []string{"Refused for lag", strconv.Itoa(n)})
	}
	rows = append(rows, []string{"Errors", strconv.Itoa(j.ErrorCount)}, []string{"Warnings", strconv.Itoa(j.WarningCount)})
	mdTable(b, []string{"Metric", "Value"}, "lr", rows)
	if !detail {
		if top := patroniTopErrors(j); len(top) > 0 {
			b.WriteString("\n### Top errors\n\n")
			rows = nil
			for _, msg := range top {
				rows = append(rows, []string{msg.Message, strconv.Itoa(msg.Count), patroniWhen(msg, clock)})
			}
			mdTable(b, []string{"Error", "Count", "When"}, "lrl", rows)
		}
		b.WriteString("\n_" + patroniDetailHint + "_\n\n")
		return
	}
	b.WriteString("\n### Members\n\n")
	rows = nil
	for _, mb := range j.Members {
		rows = append(rows, []string{mb.Name, mb.Role, mb.LeaderTime, strconv.Itoa(mb.TotalLogs), mb.FirstSeen, mb.LastSeen})
	}
	mdTable(b, []string{"Member", "Role", "Leader time", "Entries", "First seen", "Last seen"}, "llrrll", rows)
	if len(j.Changes) > 0 {
		b.WriteString("\n### Leader changes\n\n")
		rows = nil
		headers, align := []string{"Time", "Kind", "Change", "Without leader"}, "lllr"
		pgColumn := false
		for _, c := range j.Changes {
			pgColumn = pgColumn || len(c.Postgres) > 0
		}
		if pgColumn {
			headers, align = append(headers, "PostgreSQL around it, any node"), align+"l"
		}
		for _, c := range j.Changes {
			row := []string{c.Timestamp, c.Kind, c.From + " → " + c.To, c.Leaderless + patroniSeenByOthers(j, c)}
			if pgColumn {
				row = append(row, strings.Join(patroniPgLines(c, clock), "; "))
			}
			rows = append(rows, row)
		}
		mdTable(b, headers, align, rows)
	}
	if len(j.DCSIncidents) > 0 {
		b.WriteString("\n### DCS incidents\n\n")
		rows = nil
		for _, in := range j.DCSIncidents {
			rows = append(rows, []string{in.Start, in.End, in.Duration, strings.Join(in.Members, ", ")})
		}
		mdTable(b, []string{"Start", "End", "Duration", "Members"}, "llrl", rows)
	}
	if len(j.Events) > 0 {
		b.WriteString("\n### Timeline\n\n")
		rows = nil
		for _, ev := range j.Events {
			rows = append(rows, []string{ev.Timestamp, ev.Member, ev.Kind, ev.Detail})
		}
		mdTable(b, []string{"Time", "Member", "Event", "Detail"}, "llll", rows)
	}
	for _, sev := range patroniSeverities(j) {
		fmt.Fprintf(b, "\n### %s: %d, %s\n\n", sev.label, sev.total, pgbrPlural(len(sev.msgs), "kind"))
		rows = nil
		for _, msg := range sev.msgs {
			rows = append(rows, []string{"`" + msg.ID + "`", msg.Message, strconv.Itoa(msg.Count),
				fmt.Sprintf("%.2f%%", 100*float64(msg.Count)/float64(sev.total)), patroniWhen(msg, clock)})
		}
		mdTable(b, []string{"ID", "Message", "Count", "Share", "When"}, "llrrl", rows)
	}
	b.WriteString("\n")
}
