//go:build !js

package output

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// pgbrIssueDetail is a pgBackRest error or warning found by its
// --event-detail ID.
type pgbrIssueDetail struct {
	Stanza   string
	Severity string // ERROR, WARN
	Issue    analysis.PgBackRestIssue
}

// findPgBackRestIssueByID returns the error or warning of pbr with ID
// id, or nil.
func findPgBackRestIssueByID(pbr []analysis.PgBackRestMetrics, id string) *pgbrIssueDetail {
	for _, m := range pbr {
		for _, l := range []struct {
			severity string
			issues   []analysis.PgBackRestIssue
		}{{"ERROR", m.Errors}, {"WARN", m.Warnings}} {
			for _, is := range l.issues {
				if is.ID == id {
					return &pgbrIssueDetail{Stanza: analysis.PgBackRestStanzaName(m.Stanza), Severity: l.severity, Issue: is}
				}
			}
		}
	}
	return nil
}

const pgbrDateLayout = "2006-01-02 15:04:05"

// frequency is the occurrences per minute between the first and the last
// dated one, 0 below two.
func (p pgbrIssueDetail) frequency() float64 { return occurrenceFrequency(p.Issue.Times) }

// occurrenceFrequency is the occurrences per minute between the first and the
// last of ts, sorted, 0 below two.
func occurrenceFrequency(ts []time.Time) float64 {
	if len(ts) < 2 {
		return 0
	}
	span := ts[len(ts)-1].Sub(ts[0])
	if span <= 0 {
		return 0
	}
	return float64(len(ts)) / span.Minutes()
}

// codeLabel is "103 - RepoInvalidError", "" without a code.
func (p pgbrIssueDetail) codeLabel() string {
	if !p.Issue.HasCode {
		return ""
	}
	s := fmt.Sprintf("%03d", p.Issue.Code)
	if name := analysis.PgBackRestErrorName(p.Issue.Code); name != "" {
		s += " - " + name
	}
	return s
}

var pgbrLongPathRe = regexp.MustCompile(`'/[^' ]*/([^/' ]+/[^/' ]+)'`)

// pattern is the normalized message followed by its cause, paths cut to
// their last two elements.
func (p pgbrIssueDetail) pattern() string {
	s := p.Issue.Message
	if d := p.detail(); len(d) > 0 {
		s += " " + pgbrLongPathRe.ReplaceAllString(d[0], "'.../$1'")
	}
	return s
}

// detail is the lines under the issue the example does not already
// hold.
func (p pgbrIssueDetail) detail() []string {
	var out []string
	for _, l := range p.Issue.Detail {
		if l != p.Issue.RepoLine {
			out = append(out, l)
		}
	}
	return out
}

// example is the verbatim first occurrence, the lines under it
// included.
func (p pgbrIssueDetail) example() []string {
	return append(strings.Split(p.Issue.Example, "\n"), p.detail()...)
}

func printPgBackRestIssueDetail(p pgbrIssueDetail) {
	bold, reset := ansiBold, ansiReset
	is := p.Issue
	fmt.Println(bold + "\nEVENT DETAILS" + reset)
	fmt.Println()
	if hist, labels := occurrenceHistogram(is.Times); hist != nil {
		PrintHistogram(hist, "Event count", "", 0, labels)
	}
	fmt.Printf("  %-21s: %s\n", "Id", is.ID)
	fmt.Printf("  %-21s: %s\n", "Severity", p.Severity)
	fmt.Printf("  %-21s: pgBackRest, stanza %s\n", "Source", p.Stanza)
	if c := p.codeLabel(); c != "" {
		fmt.Printf("  %-21s: %s\n", "Error code", c)
	}
	if len(is.Commands) > 0 {
		fmt.Printf("  %-21s: %s\n", "Commands", strings.Join(is.Commands, ", "))
	}
	fmt.Printf("  %-21s: %d\n", "Count", is.Count)
	if !is.First.IsZero() {
		fmt.Printf("  %-21s: %s\n", "First Seen", is.First.Format(pgbrDateLayout))
		fmt.Printf("  %-21s: %s\n", "Last Seen", is.Last.Format(pgbrDateLayout))
	}
	if f := p.frequency(); f > 0 {
		fmt.Printf("  %-21s: %.2f /min\n", "Frequency", f)
	}
	fmt.Println()
	fmt.Println(bold + "Normalized Pattern:" + reset)
	fmt.Println()
	fmt.Printf(" %s\n", p.pattern())
	fmt.Println()
	fmt.Println(bold + "Example:" + reset)
	fmt.Println()
	for _, l := range p.example() {
		fmt.Printf(" %s\n", l)
	}
}

func pgbrIssueDetailJSON(p pgbrIssueDetail) EventDetailJSON {
	is := p.Issue
	j := EventDetailJSON{
		ID: is.ID, Severity: p.Severity, Message: p.pattern(), Example: strings.Join(p.example(), "\n"),
		Count: is.Count, FrequencyMin: p.frequency(),
		Source: "pgbackrest", Stanza: p.Stanza, Commands: is.Commands, Detail: p.detail(),
	}
	if is.HasCode {
		j.ErrorCode, j.ErrorName = is.Code, analysis.PgBackRestErrorName(is.Code)
	}
	if !is.First.IsZero() {
		j.FirstSeen, j.LastSeen = is.First.Format(pgbrDateLayout), is.Last.Format(pgbrDateLayout)
	}
	for _, t := range is.Times {
		j.Dates = append(j.Dates, t.Format(pgbrDateLayout))
	}
	return j
}

func writePgBackRestIssueDetailMarkdown(bw *bufio.Writer, p pgbrIssueDetail) {
	is := p.Issue
	fmt.Fprintf(bw, "## `%s` — %s (pgBackRest, stanza %s)\n\n", is.ID, p.Severity, p.Stanza)
	if c := p.codeLabel(); c != "" {
		fmt.Fprintf(bw, "- **Error code**: %s\n", c)
	}
	if len(is.Commands) > 0 {
		fmt.Fprintf(bw, "- **Commands**: %s\n", strings.Join(is.Commands, ", "))
	}
	fmt.Fprintf(bw, "- **Normalized**: `%s`\n", p.pattern())
	fmt.Fprintf(bw, "- **Example**: `%s`\n", strings.Join(p.example(), " / "))
	fmt.Fprintf(bw, "- **Count**: %d\n", is.Count)
	if !is.First.IsZero() {
		fmt.Fprintf(bw, "- **First seen**: %s\n", is.First.Format(pgbrDateLayout))
		fmt.Fprintf(bw, "- **Last seen**: %s\n", is.Last.Format(pgbrDateLayout))
	}
	if f := p.frequency(); f > 0 {
		fmt.Fprintf(bw, "- **Frequency**: %.2f /min\n", f)
	}
	fmt.Fprintln(bw)
}
