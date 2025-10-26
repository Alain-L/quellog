// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"

	"dalibo/quellog/parser"
)

// EventSummary represents aggregated statistics for a specific PostgreSQL log event type.
// Event types correspond to PostgreSQL severity levels (ERROR, WARNING, LOG, etc.).
type EventSummary struct {
	// Type is the event/severity level (e.g., "ERROR", "WARNING", "LOG").
	Type string

	// Count is the number of occurrences of this event type.
	Count int

	// Percentage is the proportion of this event type relative to all counted events.
	Percentage float64
}

// ============================================================================
// Event type definitions
// ============================================================================

// predefinedEventTypes defines the PostgreSQL log severity levels to track.
// These correspond to the standard PostgreSQL message severity levels.
//
// Reference: https://www.postgresql.org/docs/current/runtime-config-logging.html#RUNTIME-CONFIG-SEVERITY-LEVELS
//
// Severity levels (highest to lowest):
//
//	PANIC   - Severe error causing database shutdown
//	FATAL   - Session-terminating error
//	ERROR   - Error that aborted the current command
//	WARNING - Warning message
//	NOTICE  - Notice message
//	LOG     - Informational message (for administrators)
//	INFO    - Informational message (for users)
//	DEBUG   - Debug information (5 levels: DEBUG1 to DEBUG5)
var predefinedEventTypes = []string{
	"PANIC",
	"FATAL",
	"ERROR",
	"WARNING",
	"NOTICE", // Added - was missing in original
	"LOG",
	"INFO",
	"DEBUG",
}

// ============================================================================
// Streaming event analyzer
// ============================================================================

// EventAnalyzer processes log entries to count occurrences of different event types.
// It tracks PostgreSQL severity levels and calculates their distribution.
//
// Usage:
//
//	analyzer := NewEventAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	summaries := analyzer.Finalize()
type EventAnalyzer struct {
	counts map[string]int
	total  int
}

// NewEventAnalyzer creates a new event analyzer.
func NewEventAnalyzer() *EventAnalyzer {
	return &EventAnalyzer{
		counts: make(map[string]int, len(predefinedEventTypes)),
	}
}

// Process analyzes a single log entry to identify and count its event type.
// It checks for predefined event types using simple string matching.
//
// Example messages:
//   - "ERROR: relation \"users\" does not exist"
//   - "LOG: database system is ready"
//   - "WARNING: out of shared memory"
func (a *EventAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	// Check for predefined event types
	for _, eventType := range predefinedEventTypes {
		if strings.Contains(msg, eventType) {
			a.counts[eventType]++
			a.total++
			break // Prevent counting multiple event types in one entry
		}
	}
}

// Finalize returns the aggregated event summaries, sorted by count (descending).
// This should be called after all log entries have been processed.
//
// Only event types with at least one occurrence are included in the results.
func (a *EventAnalyzer) Finalize() []EventSummary {
	summary := make([]EventSummary, 0, len(predefinedEventTypes))

	for _, eventType := range predefinedEventTypes {
		count := a.counts[eventType]
		if count == 0 {
			continue // Skip event types with no occurrences
		}

		percentage := 0.0
		if a.total > 0 {
			percentage = (float64(count) / float64(a.total)) * 100
		}

		summary = append(summary, EventSummary{
			Type:       eventType,
			Count:      count,
			Percentage: percentage,
		})
	}

	// Sort by count (descending) for better readability
	sort.Slice(summary, func(i, j int) bool {
		return summary[i].Count > summary[j].Count
	})

	return summary
}

// ============================================================================
// Legacy API (for backward compatibility)
// ============================================================================

// SummarizeEvents analyzes log entries and returns a summary of event types.
//
// Deprecated: This function loads all entries into memory. Use EventAnalyzer
// with streaming for better performance and memory efficiency.
//
// This function is maintained for backward compatibility and will be removed
// in a future version.
func SummarizeEvents(entries *[]parser.LogEntry) []EventSummary {
	analyzer := NewEventAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
