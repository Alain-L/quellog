// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"github.com/Alain-L/quellog/parser"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CheckpointMetrics aggregates statistics related to PostgreSQL checkpoints.
// Checkpoints are critical events where PostgreSQL flushes dirty buffers to disk.
type CheckpointMetrics struct {
	// CompleteCount is the total number of completed checkpoints.
	CompleteCount int

	// TotalWriteTimeSeconds is the sum of all checkpoint write times.
	TotalWriteTimeSeconds float64

	// MaxWriteTimeSeconds is the longest checkpoint write time observed.
	MaxWriteTimeSeconds float64

	// Events contains the timestamp of every completed checkpoint.
	// Useful for calculating checkpoint frequency and distribution.
	Events []time.Time

	// TypeCounts maps checkpoint type to occurrence count.
	// Types include: "time", "xlog", "shutdown", "immediate", etc.
	TypeCounts map[string]int

	// TypeEvents maps checkpoint type to timestamps of occurrences.
	// Useful for analyzing frequency by type.
	TypeEvents map[string][]time.Time
}

// ============================================================================
// Checkpoint patterns and constants
// ============================================================================

// Checkpoint log message patterns
const (
	checkpointStarting = "checkpoint starting:"
	checkpointComplete = "checkpoint complete"
	writeTotalPrefix   = "total="
	writeTotalSuffix   = " s"
)

// Pre-compiled regex for extracting checkpoint type from "checkpoint starting: <type>"
var checkpointTypeRegex = regexp.MustCompile(`checkpoint starting:\s*(.+)$`)

// ============================================================================
// Streaming checkpoint analyzer
// ============================================================================

// CheckpointAnalyzer processes checkpoint events from log entries in streaming mode.
// It tracks checkpoint types, durations, and occurrences.
//
// Usage:
//
//	analyzer := NewCheckpointAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type CheckpointAnalyzer struct {
	// Aggregated statistics
	completeCount         int
	totalWriteTimeSeconds float64
	maxWriteTimeSeconds   float64
	events                []time.Time

	// Type tracking
	typeCounts map[string]int
	typeEvents map[string][]time.Time

	// State tracking (for associating "starting" with "complete")
	lastCheckpointType string
}

// NewCheckpointAnalyzer creates a new checkpoint analyzer.
func NewCheckpointAnalyzer() *CheckpointAnalyzer {
	return &CheckpointAnalyzer{
		events:     make([]time.Time, 0, 100),
		typeCounts: make(map[string]int),
		typeEvents: make(map[string][]time.Time),
	}
}

// Process analyzes a single log entry for checkpoint-related information.
// It handles both "checkpoint starting" and "checkpoint complete" messages.
//
// Checkpoint lifecycle:
//  1. "checkpoint starting: <type>" - Records the checkpoint type
//  2. "checkpoint complete: ..." - Records completion, duration, and associates with type
//
// The type from "starting" is associated with the next "complete" message.
func (a *CheckpointAnalyzer) Process(entry *parser.LogEntry) {
	// Handle "checkpoint starting: <type>"
	if strings.Contains(entry.Message, checkpointStarting) {
		a.processCheckpointStarting(entry)
		return
	}

	// Handle "checkpoint complete: ..."
	if strings.Contains(entry.Message, checkpointComplete) {
		a.processCheckpointComplete(entry)
	}
}

// processCheckpointStarting extracts and stores the checkpoint type.
// Example message: "checkpoint starting: time"
func (a *CheckpointAnalyzer) processCheckpointStarting(entry *parser.LogEntry) {
	matches := checkpointTypeRegex.FindStringSubmatch(entry.Message)
	if len(matches) > 1 {
		a.lastCheckpointType = strings.TrimSpace(matches[1])
	}
}

// processCheckpointComplete records checkpoint completion and extracts duration.
// Example message: "checkpoint complete: wrote 1234 buffers (75.5%); 0 WAL file(s) added, 0 removed, 1 recycled; write=0.123 s, sync=0.045 s, total=0.168 s"
func (a *CheckpointAnalyzer) processCheckpointComplete(entry *parser.LogEntry) {
	a.completeCount++
	a.events = append(a.events, entry.Timestamp)

	// Associate checkpoint type with completion
	cpType := a.lastCheckpointType
	if cpType == "" {
		cpType = "unknown"
	}
	a.typeCounts[cpType]++
	a.typeEvents[cpType] = append(a.typeEvents[cpType], entry.Timestamp)
	a.lastCheckpointType = "" // Reset for next checkpoint

	// Extract total write time
	if writeTime := extractWriteTime(entry.Message); writeTime > 0 {
		a.totalWriteTimeSeconds += writeTime
		if writeTime > a.maxWriteTimeSeconds {
			a.maxWriteTimeSeconds = writeTime
		}
	}
}

// extractWriteTime parses the total write time from a checkpoint complete message.
// Looks for "total=X.XXX s" pattern and returns the duration in seconds.
// Returns 0 if parsing fails.
func extractWriteTime(message string) float64 {
	// Find "total=" prefix
	idx := strings.Index(message, writeTotalPrefix)
	if idx < 0 {
		return 0
	}

	// Extract value after "total="
	rest := message[idx+len(writeTotalPrefix):]

	// Find " s" suffix
	end := strings.Index(rest, writeTotalSuffix)
	if end <= 0 {
		return 0
	}

	// Parse float value
	valueStr := strings.TrimSpace(rest[:end])
	seconds, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0
	}

	return seconds
}

// Finalize returns the aggregated checkpoint metrics.
// This should be called after all log entries have been processed.
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
// Legacy API (for backward compatibility)
// ============================================================================

// AnalyzeCheckpoints scans log entries to aggregate checkpoint-related metrics.
//
// Deprecated: This function loads all entries into memory. Use CheckpointAnalyzer
// with streaming for better performance and memory efficiency.
//
// This function is maintained for backward compatibility and will be removed
// in a future version.
func AnalyzeCheckpoints(entries *[]parser.LogEntry) CheckpointMetrics {
	analyzer := NewCheckpointAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
