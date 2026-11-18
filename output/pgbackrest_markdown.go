//go:build !js

package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBackRestMarkdown writes the PGBACKREST section in markdown,
// the same view as the text report (see buildPgBackRestView). No-op for
// an empty pbr.
func ExportPgBackRestMarkdown(w io.Writer, pbr []analysis.PgBackRestMetrics, detail bool) {
	if len(pbr) == 0 {
		return
	}
	v := buildPgBackRestView(pbr, detail)
	var b strings.Builder
	b.WriteString("## PGBACKREST\n\n")
	for _, kv := range v.Head {
		fmt.Fprintf(&b, "- **%s**: %s\n", kv.Label, kv.Value)
	}
	for _, s := range v.Stanzas {
		fmt.Fprintf(&b, "\n### %s\n\n", s.Name)
		for _, kv := range s.Lines {
			fmt.Fprintf(&b, "- **%s**: %s\n", kv.Label, kv.Value)
		}
	}
	oneStanza := len(v.Stanzas) == 1

	if len(v.TopErrors) > 0 {
		b.WriteString("\n### Top errors\n\n")
		headers, align := []string{"Code", "Error", "Stanza", "Count", "When"}, "lllrl"
		if oneStanza {
			headers, align = []string{"Code", "Error", "Count", "When"}, "llrl"
		}
		var rows [][]string
		for _, e := range v.TopErrors {
			row := []string{e.Code, e.Label}
			if !oneStanza {
				row = append(row, e.Stanza)
			}
			rows = append(rows, append(row, fmt.Sprint(e.Count), e.When))
		}
		mdTable(&b, headers, align, rows)
		if v.MoreErrors > 0 {
			fmt.Fprintf(&b, "\n_and %d more_\n", v.MoreErrors)
		}
	}
	if !v.Detail {
		fmt.Fprintf(&b, "\n_%s_\n", strings.Replace(pgbrDetailHint, "--pgbackrest-detail[=stanza]", "`--pgbackrest-detail[=stanza]`", 1))
		io.WriteString(w, b.String())
		return
	}

	if len(v.Timeline) > 0 {
		b.WriteString("\n### Timeline\n\n")
		headers := []string{"Time"}
		if v.TimelineStanza {
			headers = append(headers, "Stanza")
		}
		headers = append(headers, "Event", "Duration")
		if v.TimelineSize {
			headers = append(headers, "Size")
		}
		headers = append(headers, "Result", "Detail")
		var align strings.Builder
		for _, h := range headers {
			if h == "Duration" || h == "Size" {
				align.WriteByte('r')
			} else {
				align.WriteByte('l')
			}
		}
		var rows [][]string
		for _, e := range v.Timeline {
			row := []string{v.at(e.At)}
			if v.TimelineStanza {
				row = append(row, e.Stanza)
			}
			row = append(row, e.Event, e.Duration)
			if v.TimelineSize {
				row = append(row, e.Size)
			}
			rows = append(rows, append(row, e.Result, e.Detail))
		}
		mdTable(&b, headers, align.String(), rows)
	}
	if len(v.Severities) > 0 {
		b.WriteString("\n### Errors and warnings\n\n")
	}
	for _, sev := range v.Severities {
		fmt.Fprintf(&b, "- **%s**: %d (%s)\n", sev.Label, sev.Total, pgbrPlural(sev.Kinds, "kind"))
		for _, g := range sev.Groups {
			indent := "  "
			if g.Header != "" {
				fmt.Fprintf(&b, "  - %s\n", g.Header)
				indent = "    "
			}
			for _, r := range g.Rows {
				stanza := ""
				if !oneStanza {
					stanza = " " + r.Stanza
				}
				fmt.Fprintf(&b, "%s- *%s* `%s`%s (%d) [%.2f%%] %s\n", indent, r.ID, r.Label, stanza, r.Count, r.Pct, r.When)
			}
		}
	}
	io.WriteString(w, b.String())
}
