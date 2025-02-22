// analysis/checkpoints.go
package analysis

import (
	"dalibo/quellog/parser"
	"strings"
)

// CheckpointMetrics aggregates statistics related to checkpoints.
type CheckpointMetrics struct {
	CompleteCount         int     // Number of completed checkpoints
	TotalWriteTimeSeconds float64 // Sum of checkpoint write times
}

// AnalyzeCheckpoints scans the log entries to aggregate checkpoint-related metrics.
func AnalyzeCheckpoints(entries []parser.LogEntry) CheckpointMetrics {
	var summary CheckpointMetrics
	for _, entry := range entries {
		// Exemple : on considère qu'une entrée indiquant "checkpoint complete" correspond à un checkpoint.
		if strings.Contains(strings.ToLower(entry.Message), "checkpoint complete") {
			summary.CompleteCount++
			// Si vous avez une logique pour extraire le temps d'écriture, vous pouvez l'ajouter ici.
			// Par exemple :
			// writeTime := extractCheckpointWriteTime(entry.Message)
			// summary.TotalWriteTime += writeTime
		}
	}
	return summary
}
