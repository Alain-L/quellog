//go:build !js

// Package output: per-event-pattern detail renderers (--event-detail).
//
// CLI counterpart of the HTML report's per-event modal. Looks up one or
// more EventStat patterns by their stable [<sev>-<4-char-hash>] ID and
// renders the full picture: normalized pattern, raw example,
// count/first/last/frequency, plus a small ASCII timeline. Excluded
// from the wasm build because PrintHistogram (text.go) is CLI-only —
// the HTML report uses its own JS-side rendering.
package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// EventDetailJSON is the per-pattern payload for --event-detail --json.
// Mirrors EventStatJSON but adds derived stats (first/last/frequency)
// so JSON consumers don't have to recompute them from the timestamps
// array.
type EventDetailJSON struct {
	ID                string                `json:"id"`
	Severity          string                `json:"severity"`
	SQLStateClass     string                `json:"sql_state_class,omitempty"`
	SQLStateDesc      string                `json:"sql_state_description,omitempty"`
	Message           string                `json:"normalized_message"`
	Example           string                `json:"example"`
	Count             int                   `json:"count"`
	FirstSeen         string                `json:"first_seen,omitempty"`
	LastSeen          string                `json:"last_seen,omitempty"`
	FrequencyMin      float64               `json:"frequency_per_minute,omitempty"`
	Timestamps        []int64               `json:"timestamps,omitempty"`
	TriggeringQueries []TriggeringQueryJSON `json:"triggering_queries,omitempty"`
}

// findEventByID returns the matching EventStat from m.TopEvents or nil
// if no pattern uses that ID.
func findEventByID(m analysis.AggregatedMetrics, id string) *analysis.EventStat {
	for i := range m.TopEvents {
		if m.TopEvents[i].ID == id {
			return &m.TopEvents[i]
		}
	}
	return nil
}

// computeEventStats derives first/last/frequency-per-minute from the
// raw Timestamps slice. Returns zero values when no timestamps were
// captured (e.g. the analyzer hit the per-pattern cap).
func computeEventStats(ts []int64) (first, last time.Time, freqPerMin float64) {
	if len(ts) == 0 {
		return time.Time{}, time.Time{}, 0
	}
	first = time.UnixMilli(ts[0])
	last = time.UnixMilli(ts[len(ts)-1])
	spanMs := ts[len(ts)-1] - ts[0]
	if spanMs <= 0 {
		return first, last, 0
	}
	freqPerMin = float64(len(ts)) / (float64(spanMs) / 60000.0)
	return first, last, freqPerMin
}

// PrintEventDetails writes a human-readable detail block to stdout for
// each requested ID. Layout mirrors PrintSQLDetails: a top-of-block
// occurrences-over-time bar chart, an aligned key:value stats panel,
// then the normalized pattern and the raw example as separate
// bold-headed sections. Unknown IDs are reported in-line and do not
// abort the run.
func PrintEventDetails(m analysis.AggregatedMetrics, ids []string) {
	bold := ansiBold
	reset := ansiReset

	for _, id := range ids {
		e := findEventByID(m, id)
		if e == nil {
			fmt.Printf("\nEvent ID '%s' not found.\n", id)
			continue
		}

		first, last, freq := computeEventStats(e.Timestamps)
		desc := ""
		if e.SQLStateClass != "" {
			desc = analysis.GetErrorClassDescription(e.SQLStateClass)
		}

		// EVENT DETAILS section header — same all-caps + bold style
		// the SQL detail uses.
		fmt.Println(bold + "\nEVENT DETAILS" + reset)
		fmt.Println()

		// Occurrences-over-time bar chart at the top — most actionable
		// signal to spot bursts vs steady drip. Skipped when fewer
		// than 2 timestamps are available (no meaningful range).
		// Empty unit matches the SQL detail's count histograms — the
		// "Event count" header is enough context.
		if len(e.Timestamps) > 1 {
			hist, labels := computeEventOccurrenceHistogram(e.Timestamps)
			if hist != nil {
				PrintHistogram(hist, "Event count", "", 0, labels)
			}
		}

		// Key:value stats — same column width (21) the SQL detail uses
		// so the two outputs sit side-by-side cleanly when piped.
		fmt.Printf("  %-21s: %s\n", "Id", e.ID)
		fmt.Printf("  %-21s: %s\n", "Severity", e.Severity)
		if e.SQLStateClass != "" {
			fmt.Printf("  %-21s: %s - %s\n", "SQLSTATE Class", e.SQLStateClass, desc)
		}
		fmt.Printf("  %-21s: %d\n", "Count", e.Count)
		if !first.IsZero() {
			fmt.Printf("  %-21s: %s\n", "First Seen", first.Format("2006-01-02 15:04:05 MST"))
			fmt.Printf("  %-21s: %s\n", "Last Seen", last.Format("2006-01-02 15:04:05 MST"))
			fmt.Printf("  %-21s: %.2f /min\n", "Frequency", freq)
		}

		// Pattern + Example sections — same bold-heading + leading
		// space layout as Normalized Query / Example Query in the SQL
		// detail.
		fmt.Println()
		fmt.Println(bold + "Normalized Pattern:" + reset)
		fmt.Println()
		fmt.Printf(" %s\n", e.Message)
		fmt.Println()
		fmt.Println(bold + "Example:" + reset)
		fmt.Println()
		fmt.Printf(" %s\n", e.Example)

		// Triggering queries — the Pareto view of which normalised SQL
		// signatures fired this pattern. Top of the list is the action
		// item: fix that query, drop a large fraction of the errors.
		if len(e.TriggeringQueries) > 0 {
			fmt.Println()
			fmt.Println(bold + "Triggering Queries:" + reset)
			fmt.Println()
			printTriggeringQueries(e.TriggeringQueries, e.Count)
		}

	}
	fmt.Println()
}

// printTriggeringQueries renders the Pareto view of "which SQL
// signatures triggered this event pattern". Rows are already sorted
// desc by count upstream; no ranking column is rendered. eventTotal is
// the parent EventStat.Count, used as the denominator for the share
// column.
func printTriggeringQueries(tqs []analysis.TriggeringQuery, eventTotal int) {
	maxCount := 0
	for _, t := range tqs {
		if t.Count > maxCount {
			maxCount = t.Count
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}
	const queryWidth = 60
	const barWidth = 20
	fmt.Printf("  %-10s  %-*s  %5s  %6s\n", "QueryID", queryWidth, "QUERY", "COUNT", "%")
	for _, t := range tqs {
		share := 0.0
		if eventTotal > 0 {
			share = 100.0 * float64(t.Count) / float64(eventTotal)
		}
		q := t.NormalizedQuery
		if len(q) > queryWidth {
			q = q[:queryWidth-1] + "…"
		}
		filled := int(float64(barWidth) * float64(t.Count) / float64(maxCount))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("■", filled) + strings.Repeat(" ", barWidth-filled)
		fmt.Printf("  %-10s  %-*s  %5d  %5.1f%%  %s\n", t.ID, queryWidth, q, t.Count, share, bar)
	}
}

// computeEventOccurrenceHistogram bucketizes the per-pattern timestamps
// into 12 evenly-sized time buckets between first and last occurrence,
// returning the same shape PrintHistogram expects: count map keyed by
// "HH:MM - HH:MM" labels plus the explicit ordered label slice (so the
// bars print in chronological order rather than alphabetical).
func computeEventOccurrenceHistogram(ts []int64) (map[string]int, []string) {
	if len(ts) < 2 {
		return nil, nil
	}
	first := time.UnixMilli(ts[0])
	last := time.UnixMilli(ts[len(ts)-1])
	span := last.Sub(first)
	if span <= 0 {
		return nil, nil
	}
	const buckets = 12
	bucketDur := span / buckets
	if bucketDur <= 0 {
		bucketDur = time.Nanosecond
	}
	labels := make([]string, buckets)
	for i := 0; i < buckets; i++ {
		bs := first.Add(time.Duration(i) * bucketDur)
		be := first.Add(time.Duration(i+1) * bucketDur)
		labels[i] = fmt.Sprintf("%s - %s", bs.Format("15:04"), be.Format("15:04"))
	}
	hist := make(map[string]int, buckets)
	for _, t := range ts {
		offset := time.UnixMilli(t).Sub(first)
		bi := int(offset / bucketDur)
		if bi < 0 {
			bi = 0
		}
		if bi >= buckets {
			bi = buckets - 1
		}
		hist[labels[bi]]++
	}
	return hist, labels
}

// ExportEventDetailJSON writes one JSON document with a `patterns`
// array — one entry per requested ID. Unknown IDs return a stub entry
// with `not_found: true` so consumers can detect mismatches without
// re-running.
func ExportEventDetailJSON(w io.Writer, m analysis.AggregatedMetrics, ids []string) {
	type notFound struct {
		ID       string `json:"id"`
		NotFound bool   `json:"not_found"`
	}

	patterns := make([]any, 0, len(ids))
	for _, id := range ids {
		e := findEventByID(m, id)
		if e == nil {
			patterns = append(patterns, notFound{ID: id, NotFound: true})
			continue
		}
		first, last, freq := computeEventStats(e.Timestamps)
		desc := ""
		if e.SQLStateClass != "" {
			desc = analysis.GetErrorClassDescription(e.SQLStateClass)
		}
		ed := EventDetailJSON{
			ID:                e.ID,
			Severity:          e.Severity,
			SQLStateClass:     e.SQLStateClass,
			SQLStateDesc:      desc,
			Message:           e.Message,
			Example:           e.Example,
			Count:             e.Count,
			FrequencyMin:      freq,
			Timestamps:        e.Timestamps,
			TriggeringQueries: triggeringQueriesJSON(e.TriggeringQueries),
		}
		if !first.IsZero() {
			ed.FirstSeen = first.Format("2006-01-02 15:04:05 MST")
			ed.LastSeen = last.Format("2006-01-02 15:04:05 MST")
		}
		patterns = append(patterns, ed)
	}

	bw := bufio.NewWriter(w)
	defer bw.Flush()
	enc := json.NewEncoder(bw)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"patterns": patterns})
}

// ExportEventDetailYAML reuses the JSON encoder via jsonToYAML, the
// same trick the SQL detail YAML path uses.
func ExportEventDetailYAML(w io.Writer, m analysis.AggregatedMetrics, ids []string) {
	var buf bytes.Buffer
	ExportEventDetailJSON(&buf, m, ids)
	jsonToYAML(w, buf.Bytes())
}

// ExportEventDetailMarkdown writes a short heading + bullet block per
// requested ID, suitable for embedding in incident reports.
func ExportEventDetailMarkdown(w io.Writer, m analysis.AggregatedMetrics, ids []string) {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	for _, id := range ids {
		e := findEventByID(m, id)
		if e == nil {
			fmt.Fprintf(bw, "## `%s` (not found)\n\n", id)
			continue
		}
		first, last, freq := computeEventStats(e.Timestamps)
		desc := ""
		if e.SQLStateClass != "" {
			desc = analysis.GetErrorClassDescription(e.SQLStateClass)
		}
		fmt.Fprintf(bw, "## `%s` — %s", e.ID, e.Severity)
		if e.SQLStateClass != "" {
			fmt.Fprintf(bw, " (SQLSTATE %s — %s)", e.SQLStateClass, desc)
		}
		fmt.Fprintln(bw)
		fmt.Fprintln(bw)
		fmt.Fprintf(bw, "- **Normalized**: `%s`\n", e.Message)
		fmt.Fprintf(bw, "- **Example**: `%s`\n", e.Example)
		fmt.Fprintf(bw, "- **Count**: %d\n", e.Count)
		if !first.IsZero() {
			fmt.Fprintf(bw, "- **First seen**: %s\n", first.Format("2006-01-02 15:04:05 MST"))
			fmt.Fprintf(bw, "- **Last seen**: %s\n", last.Format("2006-01-02 15:04:05 MST"))
			fmt.Fprintf(bw, "- **Frequency**: %.2f /min\n", freq)
		}

		if len(e.TriggeringQueries) > 0 {
			fmt.Fprintln(bw)
			fmt.Fprintln(bw, "### Triggering queries")
			fmt.Fprintln(bw)
			rows := make([][]string, 0, len(e.TriggeringQueries))
			for _, t := range e.TriggeringQueries {
				share := 0.0
				if e.Count > 0 {
					share = 100.0 * float64(t.Count) / float64(e.Count)
				}
				normalized := t.NormalizedQuery
				if len(normalized) > 80 {
					normalized = normalized[:79] + "…"
				}
				rows = append(rows, []string{"`" + t.ID + "`", "`" + normalized + "`", fmt.Sprintf("%d", t.Count), fmt.Sprintf("%.1f%%", share)})
			}
			var sb strings.Builder
			mdTable(&sb, []string{"QueryID", "Query", "Count", "%"}, "llrr", rows)
			bw.WriteString(sb.String())
		}

		fmt.Fprintln(bw)
	}
}
