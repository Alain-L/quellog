//go:build !js

package output

import (
	"bufio"
	"fmt"

	"github.com/Alain-L/quellog/analysis"
)

// findPgBouncerError returns the pgBouncer warning or error with ID id and
// the pooler that logged it, or nil.
func findPgBouncerError(poolers []analysis.PgBouncerMetrics, id string) (*analysis.PgBouncerError, string) {
	for _, m := range poolers {
		for i := range m.Errors {
			if m.Errors[i].ID == id {
				return &m.Errors[i], m.Name
			}
		}
	}
	return nil, ""
}

const pgbDetailTS = "2006-01-02 15:04:05 MST"

func printPgBouncerErrorDetail(e *analysis.PgBouncerError, pooler string) {
	fmt.Println(ansiBold + "\nEVENT DETAILS" + ansiReset)
	fmt.Println()
	if hist, labels := occurrenceHistogram(e.Times); hist != nil {
		PrintHistogram(hist, "Event count", "", 0, labels)
	}
	fmt.Printf("  %-21s: %s\n", "Id", e.ID)
	fmt.Printf("  %-21s: %s\n", "Severity", e.Level)
	fmt.Printf("  %-21s: pgBouncer, %s\n", "Source", pooler)
	fmt.Printf("  %-21s: %d\n", "Count", e.Count)
	fmt.Printf("  %-21s: %s\n", "First Seen", e.First.Format(pgbDetailTS))
	fmt.Printf("  %-21s: %s\n", "Last Seen", e.Last.Format(pgbDetailTS))
	if f := occurrenceFrequency(e.Times); f > 0 {
		fmt.Printf("  %-21s: %.2f /min\n", "Frequency", f)
	}
	fmt.Println(ansiBold + "\nNormalized Pattern:\n" + ansiReset)
	fmt.Printf(" %s\n", e.Message)
	fmt.Println(ansiBold + "\nExample:\n" + ansiReset)
	fmt.Printf(" %s\n", e.Example)
}

func pgBouncerErrorDetailJSON(e *analysis.PgBouncerError, pooler string) EventDetailJSON {
	j := EventDetailJSON{
		ID: e.ID, Severity: e.Level, Message: e.Message, Example: e.Example,
		Count: e.Count, FrequencyMin: occurrenceFrequency(e.Times), Source: "pgbouncer", Pooler: pooler,
		FirstSeen: e.First.Format(pgbDetailTS), LastSeen: e.Last.Format(pgbDetailTS),
	}
	for _, t := range e.Times {
		j.Dates = append(j.Dates, t.Format(pgbDetailTS))
	}
	return j
}

func writePgBouncerErrorDetailMarkdown(bw *bufio.Writer, e *analysis.PgBouncerError, pooler string) {
	fmt.Fprintf(bw, "## `%s` — %s (pgBouncer, %s)\n\n", e.ID, e.Level, pooler)
	fmt.Fprintf(bw, "- **Normalized**: `%s`\n", e.Message)
	fmt.Fprintf(bw, "- **Example**: `%s`\n", e.Example)
	fmt.Fprintf(bw, "- **Count**: %d\n", e.Count)
	fmt.Fprintf(bw, "- **First seen**: %s\n", e.First.Format(pgbDetailTS))
	fmt.Fprintf(bw, "- **Last seen**: %s\n", e.Last.Format(pgbDetailTS))
	if f := occurrenceFrequency(e.Times); f > 0 {
		fmt.Fprintf(bw, "- **Frequency**: %.2f /min\n", f)
	}
	fmt.Fprintln(bw)
}
