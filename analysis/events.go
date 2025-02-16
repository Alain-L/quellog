// analysis/events.go
package analysis

import (
	"strings"

	"dalibo/quellog/parser"
)

// EventSummary represents the summary information for a specific event type.
type EventSummary struct {
	// Type of the event (e.g., ERROR, FATAL, LOG, etc.)
	Type string
	// Count of occurrences for this event type.
	Count int
	// Percentage of this event type relative to total events.
	Percentage float64
}

// predefinedEventTypes lists the event types we are interested in.
var predefinedEventTypes = []string{
	"PANIC",
	"FATAL",
	"ERROR",
	"WARNING",
	"LOG",
	"HINT",
	"INFO",
	"DEBUG",
}

// SummarizeEvents analyzes the provided log entries and returns a slice of EventSummary.
// It searches for each predefined event type in the log message (case-insensitive).
func SummarizeEvents(entries []parser.LogEntry) []EventSummary {
	// Map to count events per type.
	counts := make(map[string]int)
	// Total count for events that match one of the predefined types.
	total := 0

	// Loop through each log entry.
	for _, entry := range entries {
		upperMsg := strings.ToUpper(entry.Message)
		// Check for each predefined type in the message.
		for _, eventType := range predefinedEventTypes {
			if strings.Contains(upperMsg, eventType) {
				counts[eventType]++
				total++
				// Stop at first match to avoid double counting if a message contains multiple keywords.
				break
			}
		}
	}

	// Build the summary slice.
	var summary []EventSummary
	for _, eventType := range predefinedEventTypes {
		count := counts[eventType]
		percentage := 0.0
		if total > 0 {
			percentage = (float64(count) / float64(total)) * 100
		}
		summary = append(summary, EventSummary{
			Type:       eventType,
			Count:      count,
			Percentage: percentage,
		})
	}

	return summary
}
