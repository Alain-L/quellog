// analysis/checkpoints.go
package analysis

import (
	"dalibo/quellog/parser"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CheckpointMetrics aggregates statistics related to checkpoints.
type CheckpointMetrics struct {
	CompleteCount         int                    // Number of completed checkpoints
	TotalWriteTimeSeconds float64                // Sum of checkpoint write times
	MaxWriteTimeSeconds   float64                // Max checkpoint write time
	Events                []time.Time            // Timestamp of every checkpoint
	TypeCounts            map[string]int         // Count by checkpoint type (time, xlog, shutdown, etc.)
	TypeEvents            map[string][]time.Time // Events by type for rate calculation
}

// ============================================================================
// VERSION STREAMING
// ============================================================================

// CheckpointAnalyzer traite les checkpoints au fil de l'eau.
type CheckpointAnalyzer struct {
	completeCount         int
	totalWriteTimeSeconds float64
	maxWriteTimeSeconds   float64
	events                []time.Time
	typeCounts            map[string]int
	typeEvents            map[string][]time.Time
	lastCheckpointType    string // Pour associer le type au complete
}

// NewCheckpointAnalyzer crée un nouvel analyseur de checkpoints.
func NewCheckpointAnalyzer() *CheckpointAnalyzer {
	return &CheckpointAnalyzer{
		events:     make([]time.Time, 0, 100),
		typeCounts: make(map[string]int),
		typeEvents: make(map[string][]time.Time),
	}
}

// Regex pour extraire le type de checkpoint (après "checkpoint starting:")
var checkpointTypeRegex = regexp.MustCompile(`checkpoint starting:\s*(.+)$`)

// Process traite une entrée de log pour détecter les checkpoints.
func (a *CheckpointAnalyzer) Process(entry *parser.LogEntry) {
	// Détection du type de checkpoint (starting)
	if strings.Contains(entry.Message, "checkpoint starting:") {
		if matches := checkpointTypeRegex.FindStringSubmatch(entry.Message); len(matches) > 1 {
			a.lastCheckpointType = strings.TrimSpace(matches[1])
		}
		return
	}

	// Détection du checkpoint complete
	if !strings.Contains(entry.Message, "checkpoint complete") {
		return
	}

	a.completeCount++
	a.events = append(a.events, entry.Timestamp)

	// Associer le type au complete
	cpType := a.lastCheckpointType
	if cpType == "" {
		cpType = "unknown"
	}
	a.typeCounts[cpType]++
	a.typeEvents[cpType] = append(a.typeEvents[cpType], entry.Timestamp)
	a.lastCheckpointType = "" // Reset pour le prochain checkpoint

	// Extract write time after "total="
	const prefix = "total="
	idx := strings.Index(entry.Message, prefix)
	if idx < 0 {
		return
	}

	rest := entry.Message[idx+len(prefix):]
	end := strings.Index(rest, " s")
	if end <= 0 {
		return
	}

	valueStr := rest[:end]
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(valueStr), 64); err == nil {
		a.totalWriteTimeSeconds += seconds
		if seconds > a.maxWriteTimeSeconds {
			a.maxWriteTimeSeconds = seconds
		}
	}
}

// Finalize retourne les métriques finales.
func (a *CheckpointAnalyzer) Finalize() CheckpointMetrics {
	return CheckpointMetrics{
		CompleteCount:         a.completeCount,
		TotalWriteTimeSeconds: a.totalWriteTimeSeconds,
		MaxWriteTimeSeconds:   a.maxWriteTimeSeconds,
		Events:                a.events,
		TypeCounts:            a.typeCounts,
		TypeEvents:            a.typeEvents,
	}
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// AnalyzeCheckpoints scans log entries to aggregate checkpoint-related metrics.
// À supprimer une fois le refactoring terminé.
func AnalyzeCheckpoints(entries *[]parser.LogEntry) CheckpointMetrics {
	analyzer := NewCheckpointAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
