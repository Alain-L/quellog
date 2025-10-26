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

// ============================================================================
// VERSION STREAMING
// ============================================================================

// EventAnalyzer traite les events au fil de l'eau.
type EventAnalyzer struct {
	counts map[string]int
	total  int
}

// NewEventAnalyzer crée un nouvel analyseur d'événements.
func NewEventAnalyzer() *EventAnalyzer {
	return &EventAnalyzer{
		counts: make(map[string]int, len(predefinedEventTypes)),
	}
}

// Process traite une entrée de log pour détecter les event types.
func (a *EventAnalyzer) Process(entry *parser.LogEntry) {
	msg := &entry.Message

	// Check for predefined event types
	for _, eventType := range predefinedEventTypes {
		if strings.Contains(*msg, eventType) {
			a.counts[eventType]++
			a.total++
			break // Prevent counting multiple event types in one entry
		}
	}
}

// Finalize retourne les métriques finales.
func (a *EventAnalyzer) Finalize() []EventSummary {
	summary := make([]EventSummary, 0, len(predefinedEventTypes))

	for _, eventType := range predefinedEventTypes {
		count := a.counts[eventType]
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

	return summary
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// SummarizeEvents analyzes log entries and updates the summary of predefined event types.
// À supprimer une fois le refactoring terminé.
func SummarizeEvents(entries *[]parser.LogEntry) []EventSummary {
	analyzer := NewEventAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
