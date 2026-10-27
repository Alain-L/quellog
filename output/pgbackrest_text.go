//go:build !js

package output

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Alain-L/quellog/analysis"
)

// PrintPgBackRestReport prints the PGBACKREST section in the house
// style of text.go: the synthetic view, or the detailed one (see
// buildPgBackRestView). No-op for an empty pbr.
func PrintPgBackRestReport(pbr []analysis.PgBackRestMetrics, detail bool) {
	if len(pbr) == 0 {
		return
	}
	for _, l := range pgbrTextLines(buildPgBackRestView(pbr, detail)) {
		fmt.Println(l)
	}
}

// pgbrTextLines renders v, one string per line.
func pgbrTextLines(v pgbrView) []string {
	bold, reset := ansiBold, ansiReset
	out := []string{"", bold + "PGBACKREST" + reset, ""}
	for _, kv := range v.Head {
		out = append(out, fmt.Sprintf("  %-25s : %s", kv.Label, kv.Value))
	}
	for _, s := range v.Stanzas {
		out = append(out, "", "  "+bold+s.Name+reset)
		for _, kv := range s.Lines {
			out = append(out, fmt.Sprintf("    %-23s : %s", kv.Label, kv.Value))
		}
	}
	oneStanza := len(v.Stanzas) == 1

	if len(v.TopErrors) > 0 {
		lw, sw := 0, 0
		for _, e := range v.TopErrors {
			lw, sw = max(lw, utf8.RuneCountInString(e.Label)), max(sw, utf8.RuneCountInString(e.Stanza))
		}
		out = append(out, "", "  Top errors:")
		for _, e := range v.TopErrors {
			code := e.Code
			if code == "" {
				code = "     "
			}
			row := fmt.Sprintf("    %s %-*s  ", code, lw, e.Label)
			if !oneStanza {
				row += fmt.Sprintf("%-*s  ", sw, e.Stanza)
			}
			out = append(out, strings.TrimRight(row+fmt.Sprintf("%4d  %s", e.Count, e.When), " "))
		}
		if v.MoreErrors > 0 {
			out = append(out, fmt.Sprintf("    %sand %d more%s", ansiMutedItalic, v.MoreErrors, reset))
		}
	}
	if !v.Detail {
		return append(out, "", "  "+ansiMutedItalic+pgbrDetailHint+reset, "")
	}

	if len(v.Timeline) > 0 {
		out = append(out, "", "  Timeline:")
		out = append(out, pgbrTimelineTable(v)...)
	}
	if len(v.Severities) > 0 {
		out = append(out, "")
	}
	for _, sev := range v.Severities {
		out = append(out, fmt.Sprintf("  %-25s : %d (%s)", sev.Label, sev.Total, pgbrPlural(sev.Kinds, "kind")))
		lw, sw := 0, 0
		for _, g := range sev.Groups {
			for _, r := range g.Rows {
				lw, sw = max(lw, utf8.RuneCountInString(r.Label)), max(sw, utf8.RuneCountInString(r.Stanza))
			}
		}
		for _, g := range sev.Groups {
			if g.Header != "" {
				out = append(out, "    "+g.Header)
			}
			for _, r := range g.Rows {
				row := fmt.Sprintf("    %s%s%s  %-*s  ", ansiMutedItalic, r.ID, reset, lw, r.Label)
				if !oneStanza {
					row += fmt.Sprintf("%-*s  ", sw, r.Stanza)
				}
				out = append(out, strings.TrimRight(row+fmt.Sprintf("%4d  %6.2f%%  %s", r.Count, r.Pct, r.When), " "))
			}
		}
	}
	return append(out, "")
}

// pgbrTimelineTable renders the timeline under a header row, Duration
// and Size right-aligned.
func pgbrTimelineTable(v pgbrView) []string {
	head := []string{"Time", "Stanza", "Event", "Duration", "Size", "Result", "Detail"}
	keep := []bool{true, v.TimelineStanza, true, true, v.TimelineSize, true, true}
	right := []bool{false, false, false, true, true, false, false}
	rows := [][]string{head}
	for _, e := range v.Timeline {
		rows = append(rows, []string{v.at(e.At), e.Stanza, e.Event, e.Duration, e.Size, e.Result, e.Detail})
	}
	width := make([]int, len(head))
	for _, r := range rows {
		for i, c := range r {
			width[i] = max(width[i], utf8.RuneCountInString(c))
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var cells []string
		for i, c := range r {
			if !keep[i] {
				continue
			}
			switch {
			case i == len(r)-1:
				cells = append(cells, c)
			case right[i]:
				cells = append(cells, fmt.Sprintf("%*s", width[i], c))
			default:
				cells = append(cells, fmt.Sprintf("%-*s", width[i], c))
			}
		}
		out = append(out, strings.TrimRight("    "+strings.Join(cells, "  "), " "))
	}
	return out
}
