//go:build !js

package output

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/Alain-L/quellog/analysis"
)

// findPatroniMessage returns the Patroni warning or error with ID id, or
// nil.
func findPatroniMessage(m *analysis.PatroniMetrics, id string) *analysis.PatroniMessage {
	if m == nil {
		return nil
	}
	for i := range m.Messages {
		if m.Messages[i].ID == id {
			return &m.Messages[i]
		}
	}
	return nil
}

func printPatroniMessageDetail(msg *analysis.PatroniMessage) {
	fmt.Println(ansiBold + "\nEVENT DETAILS" + ansiReset)
	fmt.Println()
	if hist, labels := occurrenceHistogram(msg.Times); hist != nil {
		PrintHistogram(hist, "Event count", "", 0, labels)
	}
	fmt.Printf("  %-21s: %s\n", "Id", msg.ID)
	fmt.Printf("  %-21s: %s\n", "Severity", msg.Level)
	fmt.Printf("  %-21s: Patroni, %s\n", "Source", strings.Join(msg.Members, ", "))
	fmt.Printf("  %-21s: %d\n", "Count", msg.Count)
	if n := len(msg.Times); n > 0 {
		fmt.Printf("  %-21s: %s\n", "First Seen", msg.Times[0].Format(patroniTS))
		fmt.Printf("  %-21s: %s\n", "Last Seen", msg.Times[n-1].Format(patroniTS))
	}
	if f := occurrenceFrequency(msg.Times); f > 0 {
		fmt.Printf("  %-21s: %.2f /min\n", "Frequency", f)
	}
	fmt.Println(ansiBold + "\nNormalized Pattern:\n" + ansiReset)
	fmt.Printf(" %s\n", msg.Message)
	fmt.Println(ansiBold + "\nExample:\n" + ansiReset)
	fmt.Printf(" %s\n", msg.Example)
}

func patroniMessageDetailJSON(msg *analysis.PatroniMessage) EventDetailJSON {
	j := EventDetailJSON{
		ID: msg.ID, Severity: msg.Level, Message: msg.Message, Example: msg.Example,
		Count: msg.Count, FrequencyMin: occurrenceFrequency(msg.Times), Source: "patroni", Members: msg.Members,
	}
	if n := len(msg.Times); n > 0 {
		j.FirstSeen, j.LastSeen = msg.Times[0].Format(patroniTS), msg.Times[n-1].Format(patroniTS)
	}
	for _, t := range msg.Times {
		j.Dates = append(j.Dates, t.Format(patroniTS))
	}
	return j
}

func writePatroniMessageDetailMarkdown(bw *bufio.Writer, msg *analysis.PatroniMessage) {
	fmt.Fprintf(bw, "## `%s` — %s (Patroni, %s)\n\n", msg.ID, msg.Level, strings.Join(msg.Members, ", "))
	fmt.Fprintf(bw, "- **Normalized**: `%s`\n", msg.Message)
	fmt.Fprintf(bw, "- **Example**: `%s`\n", msg.Example)
	fmt.Fprintf(bw, "- **Count**: %d\n", msg.Count)
	if n := len(msg.Times); n > 0 {
		fmt.Fprintf(bw, "- **First seen**: %s\n", msg.Times[0].Format(patroniTS))
		fmt.Fprintf(bw, "- **Last seen**: %s\n", msg.Times[n-1].Format(patroniTS))
	}
	if f := occurrenceFrequency(msg.Times); f > 0 {
		fmt.Fprintf(bw, "- **Frequency**: %.2f /min\n", f)
	}
	fmt.Fprintln(bw)
}

// PrintPatroniReport writes the Patroni section alone, detailed or not:
// --patroni-detail.
func PrintPatroniReport(m *analysis.PatroniMetrics, detail bool) {
	printPatroniSection(m, detail, ansiBold, ansiReset)
}

// ExportPatroniMarkdown writes the Patroni section alone in Markdown.
func ExportPatroniMarkdown(w io.Writer, m *analysis.PatroniMetrics, detail bool) {
	var b strings.Builder
	writePatroniMarkdown(&b, m, detail)
	fmt.Fprintln(w, b.String())
}
