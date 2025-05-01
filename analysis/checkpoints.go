// analysis/checkpoints.go
package analysis

import (
	"dalibo/quellog/parser"
	"strconv"
	"strings"
	"time"
)

// CheckpointMetrics aggregates statistics related to checkpoints.
type CheckpointMetrics struct {
	CompleteCount         int         // Number of completed checkpoints
	TotalWriteTimeSeconds float64     // Sum of checkpoint write times
	MaxWriteTimeSeconds   float64     // Sum of checkpoint write times
	Events                []time.Time // every checkpoints
}

// AnalyzeCheckpoints scans log entries to aggregate checkpoint-related metrics.
func AnalyzeCheckpoints(entries *[]parser.LogEntry) CheckpointMetrics {
	var summary CheckpointMetrics

	for i := range *entries {
		entry := &(*entries)[i]

		if strings.Contains(entry.Message, "checkpoint complete") {
			summary.CompleteCount++
			summary.Events = append(summary.Events, entry.Timestamp)

			// Extraction manuelle du temps total après "total="
			const prefix = "total="
			idx := strings.Index(entry.Message, prefix)
			if idx >= 0 {
				// Commencer après "total="
				rest := entry.Message[idx+len(prefix):]

				// Trouver la fin du nombre (avant " s")
				end := strings.Index(rest, " s")
				if end > 0 {
					valueStr := rest[:end]
					if seconds, err := strconv.ParseFloat(strings.TrimSpace(valueStr), 64); err == nil {
						summary.TotalWriteTimeSeconds += seconds
						if seconds > summary.MaxWriteTimeSeconds {
							summary.MaxWriteTimeSeconds = seconds
						}
					}
				}
			}
		}
	}

	return summary
}
