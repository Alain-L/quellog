//go:build !js

package output

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alain-L/quellog/analysis"
)

// Text and Markdown read the JSON contract, so every format states the same.

func printPatroniSection(m *analysis.PatroniMetrics, detail bool, bold, reset string) {
	j := buildPatroniJSON(m)
	clock := patroniClock(j)
	fmt.Println(bold + "\nPATRONI\n" + reset)
	row := func(label, value string) { fmt.Printf("  %-25s : %s\n", label, value) }
	row("Start date", j.StartDate)
	row("End date", j.EndDate)
	if detail {
		row("Duration", j.Duration)
		row("Total entries", strconv.Itoa(j.TotalLogs))
	}
	row("Members", patroniMemberNames(j))
	if len(j.MissingLogs) > 0 {
		row("No log from", strings.Join(j.MissingLogs, ", ")+", "+patroniMissingNote)
	}
	row("Leader at end", patroniLeaderText(j, clock))
	row("Leader changes", patroniChangesText(j))
	row("Time without leader", j.LeaderlessTime)
	row("DCS incidents", patroniDCSText(j))
	if n := j.EventCounts["lag_refused"]; detail || n > 0 {
		row("Refused for lag", strconv.Itoa(n))
	}
	row("Errors", strconv.Itoa(j.ErrorCount))
	row("Warnings", strconv.Itoa(j.WarningCount))

	if !detail {
		if top := patroniTopErrors(j); len(top) > 0 {
			lw := 0
			for _, msg := range top {
				lw = max(lw, utf8.RuneCountInString(pgbrTrunc(msg.Message, patroniLabelWidth)))
			}
			fmt.Println("\n  Top errors:")
			for _, msg := range top {
				fmt.Printf("    %-*s  %4d  %s\n", lw, pgbrTrunc(msg.Message, patroniLabelWidth), msg.Count, patroniWhen(msg, clock))
			}
		}
		fmt.Println("\n  " + ansiMutedItalic + patroniDetailHint + reset)
		return
	}

	fmt.Println("\n  Members:")
	for _, mb := range j.Members {
		fmt.Printf("    %-10s %-8s %12s as leader %7d entries   %s → %s\n", mb.Name, mb.Role, mb.LeaderTime, mb.TotalLogs, clock(mb.FirstSeen), clock(mb.LastSeen))
	}
	if len(j.Changes) > 0 {
		fmt.Println("\n  Leader changes:")
		for _, c := range j.Changes {
			fmt.Printf("    %s  %-10s  %s → %s   %s without leader%s\n", clock(c.Timestamp), c.Kind, c.From, c.To, c.Leaderless, patroniSeenByOthers(j, c))
			for _, line := range patroniPgLines(c, clock) {
				fmt.Printf("    %s%*s  postgres, any node  %s%s\n", ansiMutedItalic, len(clock(c.Timestamp)), "", line, reset)
			}
		}
	}
	if len(j.DCSIncidents) > 0 {
		fmt.Println("\n  DCS incidents:")
		for _, in := range j.DCSIncidents {
			fmt.Printf("    %s → %s  %-10s %s\n", clock(in.Start), clock(in.End), in.Duration, strings.Join(in.Members, ", "))
		}
	}
	if len(j.Events) > 0 {
		fmt.Println("\n  Timeline:")
		for _, ev := range j.Events {
			fmt.Printf("    %s  %-10s %-18s %s\n", clock(ev.Timestamp), ev.Member, ev.Kind, ev.Detail)
		}
	}
	// The warnings and errors laid out like the EVENTS section, each with
	// its --event-detail ID.
	for _, sev := range patroniSeverities(j) {
		fmt.Printf("\n  %-25s : %d (%s)\n", sev.label, sev.total, pgbrPlural(len(sev.msgs), "kind"))
		lw := 0
		for _, msg := range sev.msgs {
			lw = max(lw, utf8.RuneCountInString(pgbrTrunc(msg.Message, patroniLabelWidth)))
		}
		for _, msg := range sev.msgs {
			fmt.Printf("    %s%s%s  %-*s  %4d  %6.2f%%  %s\n", ansiMutedItalic, msg.ID, reset, lw, pgbrTrunc(msg.Message, patroniLabelWidth),
				msg.Count, 100*float64(msg.Count)/float64(sev.total), patroniWhen(msg, clock))
		}
	}
}

// patroniDetailHint points the default view to the detailed one.
const patroniDetailHint = "Details: --patroni-detail"

// patroniLabelWidth caps a message in the text tables.
const patroniLabelWidth = 72

// patroniChangesText is "3, 1 switchover, 2 failovers, 1 retaken".
func patroniChangesText(j PatroniJSON) string {
	plural := func(n int, word string) string {
		if n > 1 {
			return word + "s"
		}
		return word
	}
	s := fmt.Sprintf("%d, %d %s, %d %s", j.LeaderChanges, j.Switchovers, plural(j.Switchovers, "switchover"), j.Failovers, plural(j.Failovers, "failover"))
	if j.LockRetaken > 0 {
		s += fmt.Sprintf(", %d retaken", j.LockRetaken)
	}
	return s
}

// patroniDCSText is the incident count and the longest one.
func patroniDCSText(j PatroniJSON) string {
	if len(j.DCSIncidents) == 0 {
		return "0"
	}
	longest := j.DCSIncidents[0]
	for _, in := range j.DCSIncidents[1:] {
		if d1, e1 := time.ParseDuration(in.Duration); e1 == nil {
			if d0, e0 := time.ParseDuration(longest.Duration); e0 != nil || d1 > d0 {
				longest = in
			}
		}
	}
	return fmt.Sprintf("%d, longest %s", len(j.DCSIncidents), longest.Duration)
}

// patroniTopErrors is the five most frequent errors.
func patroniTopErrors(j PatroniJSON) []PatroniMessageJSON {
	var top []PatroniMessageJSON
	for _, msg := range j.Messages { // most frequent first
		if msg.Level != "WARNING" && len(top) < 5 {
			top = append(top, msg)
		}
	}
	return top
}

// patroniWhen is a message's first and last occurrence.
func patroniWhen(msg PatroniMessageJSON, clock func(string) string) string {
	switch {
	case msg.FirstDate == "":
		return ""
	case msg.FirstDate == msg.LastDate:
		return clock(msg.FirstDate)
	}
	return clock(msg.FirstDate) + " → " + clock(msg.LastDate)
}

// patroniSeverity is one level of the warnings and errors, most frequent
// first.
type patroniSeverity struct {
	label string
	total int
	msgs  []PatroniMessageJSON
}

// patroniSeverities groups the messages as the EVENTS section does: the
// errors (CRITICAL included), then the warnings.
func patroniSeverities(j PatroniJSON) []patroniSeverity {
	errs, warns := patroniSeverity{label: "ERROR"}, patroniSeverity{label: "WARNING"}
	for _, msg := range j.Messages {
		sev := &errs
		if msg.Level == "WARNING" {
			sev = &warns
		}
		sev.total += msg.Count
		sev.msgs = append(sev.msgs, msg)
	}
	var out []patroniSeverity
	for _, sev := range []patroniSeverity{errs, warns} {
		if len(sev.msgs) > 0 {
			out = append(out, sev)
		}
	}
	return out
}

const patroniMissingNote = "seen only by the members whose log was given"

// patroniSeenByOthers flags a change involving a member whose log was not
// given: its time without leader is then seen by the others only.
func patroniSeenByOthers(j PatroniJSON, c PatroniChangeJSON) string {
	if slices.Contains(j.MissingLogs, c.From) || slices.Contains(j.MissingLogs, c.To) {
		return ", seen by the others"
	}
	return ""
}

// patroniClock drops the date when the logs fit in one day; timestamps are
// "YYYY-MM-DD HH:MM:SS", so the time starts at byte 11.
func patroniClock(j PatroniJSON) func(string) string {
	oneDay := len(j.StartDate) >= 10 && j.StartDate[:10] == j.EndDate[:10]
	return func(ts string) string {
		if oneDay && len(ts) > 11 {
			return ts[11:]
		}
		return ts
	}
}

// patroniPgLines renders the PostgreSQL events of a change, one line per
// distinct event: nodes are not told apart, so the same event logged by two
// of them reads "×2" rather than as a repeat.
func patroniPgLines(c PatroniChangeJSON, clock func(string) string) []string {
	var lines []string
	count := map[string]int{}
	for _, p := range c.Postgres {
		line := clock(p.Timestamp) + "  " + p.Detail
		if count[line]++; count[line] == 1 {
			lines = append(lines, line)
		}
	}
	for i, line := range lines {
		if n := count[line]; n > 1 {
			lines[i] = fmt.Sprintf("%s ×%d", line, n)
		}
	}
	return lines
}

func patroniMemberNames(j PatroniJSON) string {
	names := make([]string, len(j.Members))
	for i, mb := range j.Members {
		names[i] = mb.Name
	}
	return strings.Join(names, ", ")
}

func patroniLeaderText(j PatroniJSON, clock func(string) string) string {
	if j.Leader == "" {
		return "none"
	}
	s := j.Leader + " since " + clock(j.LeaderSince)
	if slices.Contains(j.MissingLogs, j.Leader) {
		s += ", as seen by the others"
	}
	return s
}
