// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// CheckpointWAL holds the WAL distance and estimate for a single checkpoint.
//   - DistanceKB: actual WAL generated since the previous checkpoint (kB)
//   - EstimateKB: PostgreSQL's prediction for the next checkpoint cycle (kB)
type CheckpointWAL struct {
	Timestamp  time.Time
	DistanceKB int64
	EstimateKB int64
}

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

	// WALDistances contains per-checkpoint WAL distance and estimate values.
	WALDistances []CheckpointWAL

	// TotalDistanceKB is the cumulative WAL distance across all checkpoints.
	TotalDistanceKB int64

	// MaxDistanceKB is the largest WAL distance observed for a single checkpoint.
	MaxDistanceKB int64

	// WarningCount is the number of "checkpoints are occurring too frequently" warnings.
	WarningCount int

	// WarningEvents contains the timestamp of every checkpoint frequency warning.
	WarningEvents []time.Time

	// WarningMinIntervalSeconds is the shortest interval reported in warnings (in seconds).
	WarningMinIntervalSeconds int

	// WarningMaxIntervalSeconds is the longest interval reported in warnings (in seconds).
	WarningMaxIntervalSeconds int
}

// ============================================================================
// Checkpoint patterns and constants
// ============================================================================

// Checkpoint log message patterns
const (
	checkpointStarting = "checkpoint starting:"
	checkpointComplete = "checkpoint complete"
	checkpointWarning  = "checkpoints are occurring too frequently"
	writeTotalPrefix   = "total="
	writeTotalSuffix   = " s"
	distancePrefix     = "distance="
	estimatePrefix     = "estimate="
	kbSuffix           = " kB"
)

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

	// WAL distance tracking
	walDistances    []CheckpointWAL
	totalDistanceKB int64
	maxDistanceKB   int64

	// State tracking (for associating "starting" with "complete")
	lastCheckpointType string

	// Warning tracking
	warningCount              int
	warningEvents             []time.Time
	warningMinIntervalSeconds int
	warningMaxIntervalSeconds int
}

// NewCheckpointAnalyzer creates a new checkpoint analyzer.
func NewCheckpointAnalyzer() *CheckpointAnalyzer {
	return &CheckpointAnalyzer{
		events:        make([]time.Time, 0, 100),
		typeCounts:    make(map[string]int),
		typeEvents:    make(map[string][]time.Time),
		walDistances:  make([]CheckpointWAL, 0, 100),
		warningEvents: make([]time.Time, 0, 10),
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
	msg := entry.Message

	if len(msg) < 10 {
		return
	}

	// Search for "checkpoint" anywhere
	idx := strings.Index(msg, "checkpoint")
	if idx < 0 {
		return
	}

	// Check character after "checkpoint " (position idx+11)
	pos := idx + 11
	if pos >= len(msg) {
		return
	}

	// Dispatch on the character after "checkpoint ":
	//   's' → "checkpoint starting:"
	//   'c' → "checkpoint complete:"
	//   ' ' → "checkpoints are occurring too frequently" (plural, extra 's' shifts position)
	switch msg[pos] {
	case 's': // "checkpoint starting"
		a.processCheckpointStarting(entry)
	case 'c': // "checkpoint complete"
		a.processCheckpointComplete(entry)
	case ' ': // "checkpoints are occurring too frequently"
		if strings.Contains(msg, checkpointWarning) {
			a.processCheckpointWarning(entry)
		}
	}
}

// processCheckpointStarting extracts and stores the checkpoint type.
// Example message: "checkpoint starting: time"
func (a *CheckpointAnalyzer) processCheckpointStarting(entry *parser.LogEntry) {
	idx := strings.Index(entry.Message, checkpointStarting)
	if idx < 0 {
		return
	}
	cpType := strings.TrimSpace(entry.Message[idx+len(checkpointStarting):])
	if cpType != "" {
		a.lastCheckpointType = cpType
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

	// Extract WAL distance and estimate
	distKB := extractKBValue(entry.Message, distancePrefix)
	estKB := extractKBValue(entry.Message, estimatePrefix)
	if distKB >= 0 || estKB >= 0 {
		if distKB < 0 {
			distKB = 0
		}
		if estKB < 0 {
			estKB = 0
		}
		a.walDistances = append(a.walDistances, CheckpointWAL{
			Timestamp:  entry.Timestamp,
			DistanceKB: distKB,
			EstimateKB: estKB,
		})
		a.totalDistanceKB += distKB
		if distKB > a.maxDistanceKB {
			a.maxDistanceKB = distKB
		}
	}
}

// processCheckpointWarning records a checkpoint frequency warning and extracts the interval.
// Example message: "checkpoints are occurring too frequently (27 seconds apart)"
func (a *CheckpointAnalyzer) processCheckpointWarning(entry *parser.LogEntry) {
	msg := entry.Message

	// Extract interval: find '(' then ' second' after it
	open := strings.IndexByte(msg, '(')
	if open < 0 {
		return
	}
	rest := msg[open+1:]
	end := strings.Index(rest, " second")
	if end <= 0 {
		return
	}
	interval, err := strconv.Atoi(rest[:end])
	if err != nil {
		return
	}

	a.warningCount++
	a.warningEvents = append(a.warningEvents, entry.Timestamp)
	if a.warningCount == 1 || interval < a.warningMinIntervalSeconds {
		a.warningMinIntervalSeconds = interval
	}
	if interval > a.warningMaxIntervalSeconds {
		a.warningMaxIntervalSeconds = interval
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

// extractKBValue parses an integer kB value from a "prefix=XXXX kB" pattern.
// Returns -1 if the prefix is not found or parsing fails.
func extractKBValue(message string, prefix string) int64 {
	idx := strings.Index(message, prefix)
	if idx < 0 {
		return -1
	}
	rest := message[idx+len(prefix):]
	end := strings.Index(rest, kbSuffix)
	if end <= 0 {
		return -1
	}
	val, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return -1
	}
	return val
}

// Finalize returns the aggregated checkpoint metrics.
// This should be called after all log entries have been processed.
func (a *CheckpointAnalyzer) Finalize() CheckpointMetrics {
	return CheckpointMetrics{
		CompleteCount:             a.completeCount,
		TotalWriteTimeSeconds:    a.totalWriteTimeSeconds,
		MaxWriteTimeSeconds:      a.maxWriteTimeSeconds,
		Events:                   a.events,
		TypeCounts:               a.typeCounts,
		TypeEvents:               a.typeEvents,
		WALDistances:            a.walDistances,
		TotalDistanceKB:         a.totalDistanceKB,
		MaxDistanceKB:           a.maxDistanceKB,
		WarningCount:             a.warningCount,
		WarningEvents:            a.warningEvents,
		WarningMinIntervalSeconds: a.warningMinIntervalSeconds,
		WarningMaxIntervalSeconds: a.warningMaxIntervalSeconds,
	}
}
