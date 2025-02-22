package analysis

import (
	"dalibo/quellog/parser"
	"strings"
)

// EventSummary represents the summary information for a specific event type.
type EventSummary struct {
	Type       string  // Event type (e.g., ERROR, WARNING, etc.)
	Count      int     // Number of occurrences
	Percentage float64 // Percentage relative to total events
}

// Predefined event types to track.
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

// SummarizeEvents analyzes log entries and returns a summary of predefined event types.
func SummarizeEvents(entries []parser.LogEntry) []EventSummary {
	counts := make(map[string]int)
	total := 0

	for _, entry := range entries {
		upperMsg := strings.ToUpper(entry.Message)
		for _, eventType := range predefinedEventTypes {
			if strings.Contains(upperMsg, eventType) {
				counts[eventType]++
				total++
				break // Prevent counting multiple event types in one entry
			}
		}
	}

	// Build the summary list.
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
