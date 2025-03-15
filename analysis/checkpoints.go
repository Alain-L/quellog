// analysis/checkpoints.go
package analysis

import (
	"dalibo/quellog/parser"
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
	var lastCheckpointStart time.Time // Stores the most recent "checkpoint starting" timestamp

	// Iterate over log entries by reference to avoid unnecessary copies
	for i := range *entries {
		entry := &(*entries)[i]

		// Detect checkpoint starting
		if strings.Contains(entry.Message, "checkpoint starting") {
			lastCheckpointStart = entry.Timestamp
		}

		// Detect checkpoint completion
		if strings.Contains(entry.Message, "checkpoint complete") {
			summary.CompleteCount++
			// Add the event: timestamp of checkpoint completion.
			summary.Events = append(summary.Events, entry.Timestamp)

			// If a start time was recorded, calculate the write duration.
			if !lastCheckpointStart.IsZero() {
				writeTime := entry.Timestamp.Sub(lastCheckpointStart).Seconds()
				summary.TotalWriteTimeSeconds += writeTime
				if writeTime > summary.MaxWriteTimeSeconds {
					summary.MaxWriteTimeSeconds = writeTime
				}
				lastCheckpointStart = time.Time{} // Reset after processing.
			}
		}
	}

	return summary
}
