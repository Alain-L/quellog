// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// GlobalMetrics aggregates general statistics from PostgreSQL logs.
type GlobalMetrics struct {
	// Count is the total number of log entries processed.
	Count int

	// MinTimestamp is the timestamp of the earliest log entry.
	MinTimestamp time.Time

	// MaxTimestamp is the timestamp of the latest log entry.
	MaxTimestamp time.Time

	// ErrorCount is the number of ERROR-level messages.
	ErrorCount int

	// FatalCount is the number of FATAL-level messages.
	FatalCount int

	// PanicCount is the number of PANIC-level messages.
	PanicCount int

	// WarningCount is the number of WARNING-level messages.
	WarningCount int

	// LogCount is the number of LOG-level messages.
	LogCount int
}

// UniqueEntityMetrics tracks unique database entities (databases, users, applications).
// This helps understand the scope of database usage and identify which components are active.
type UniqueEntityMetrics struct {
	// UniqueDbs is the count of distinct databases referenced in logs.
	UniqueDbs int

	// UniqueUsers is the count of distinct users referenced in logs.
	UniqueUsers int

	// UniqueApps is the count of distinct applications referenced in logs.
	UniqueApps int

	// DBs is the sorted list of all unique database names.
	DBs []string

	// Users is the sorted list of all unique user names.
	Users []string

	// Apps is the sorted list of all unique application names.
	Apps []string
}

// AggregatedMetrics combines all analysis metrics into a single structure.
// This is the final output of log analysis, containing statistics from all analyzers.
type AggregatedMetrics struct {
	// Global contains overall log statistics.
	Global GlobalMetrics

	// TempFiles contains temporary file usage statistics.
	TempFiles TempFileMetrics

	// Vacuum contains autovacuum and manual vacuum statistics.
	Vacuum VacuumMetrics

	// Checkpoints contains checkpoint statistics.
	Checkpoints CheckpointMetrics

	// Connections contains connection and session statistics.
	Connections ConnectionMetrics

	// UniqueEntities contains unique database entity statistics.
	UniqueEntities UniqueEntityMetrics

	// EventSummaries contains severity level distribution.
	EventSummaries []EventSummary

	// ErrorClasses contains SQLSTATE error class distribution.
	ErrorClasses []ErrorClassSummary

	// SQL contains SQL query statistics.
	SQL SqlMetrics
}

// ============================================================================
// Streaming analysis orchestrator
// ============================================================================

// StreamingAnalyzer orchestrates multiple specialized analyzers to process
// log entries in streaming mode without loading all data into memory.
//
// Usage:
//
//	analyzer := NewStreamingAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type StreamingAnalyzer struct {
	global         GlobalMetrics
	tempFiles      *TempFileAnalyzer
	vacuum         *VacuumAnalyzer
	checkpoints    *CheckpointAnalyzer
	connections    *ConnectionAnalyzer
	events         *EventAnalyzer
	errorClasses   *ErrorClassAnalyzer
	uniqueEntities *UniqueEntityAnalyzer
	sql            *SQLAnalyzer
}

// NewStreamingAnalyzer creates a new streaming analyzer with all sub-analyzers initialized.
func NewStreamingAnalyzer() *StreamingAnalyzer {
	return &StreamingAnalyzer{
		tempFiles:      NewTempFileAnalyzer(),
		vacuum:         NewVacuumAnalyzer(),
		checkpoints:    NewCheckpointAnalyzer(),
		connections:    NewConnectionAnalyzer(),
		events:         NewEventAnalyzer(),
		errorClasses:   NewErrorClassAnalyzer(),
		uniqueEntities: NewUniqueEntityAnalyzer(),
		sql:            NewSQLAnalyzer(),
	}
}

// Process analyzes a single log entry, dispatching it to all relevant sub-analyzers.
// Each sub-analyzer filters and processes only the entries relevant to it.
func (sa *StreamingAnalyzer) Process(entry *parser.LogEntry) {
	// Update global metrics
	sa.global.Count++

	// Track timestamp range
	if sa.global.MinTimestamp.IsZero() || entry.Timestamp.Before(sa.global.MinTimestamp) {
		sa.global.MinTimestamp = entry.Timestamp
	}
	if sa.global.MaxTimestamp.IsZero() || entry.Timestamp.After(sa.global.MaxTimestamp) {
		sa.global.MaxTimestamp = entry.Timestamp
	}

	// Dispatch to specialized analyzers
	// Each analyzer performs its own filtering
	if strings.Contains(entry.Message, "temporary file:") {
		sa.sql.ProcessTempFile(entry)
	} else if strings.Contains(entry.Message, "duration:") {
		sa.sql.Process(entry)
	}

	sa.vacuum.Process(entry)
	sa.checkpoints.Process(entry)
	sa.connections.Process(entry)
	sa.events.Process(entry)
	sa.errorClasses.Process(entry)
	sa.uniqueEntities.Process(entry)
}

// Finalize computes final metrics after all log entries have been processed.
// This should be called once after processing all entries.
func (sa *StreamingAnalyzer) Finalize() AggregatedMetrics {
	return AggregatedMetrics{
		Global:         sa.global,
		TempFiles:      sa.tempFiles.Finalize(),
		Vacuum:         sa.vacuum.Finalize(),
		Checkpoints:    sa.checkpoints.Finalize(),
		Connections:    sa.connections.Finalize(),
		EventSummaries: sa.events.Finalize(),
		ErrorClasses:   sa.errorClasses.Finalize(),
		UniqueEntities: sa.uniqueEntities.Finalize(),
		SQL:            sa.sql.Finalize(),
	}
}

// ============================================================================
// Main analysis function
// ============================================================================

// AggregateMetrics processes a stream of log entries and returns aggregated metrics.
// This is the main entry point for log analysis, using streaming processing
// to avoid loading all entries into memory.
//
// The function reads entries from the input channel until it closes, then returns
// the complete analysis results.
func AggregateMetrics(in <-chan parser.LogEntry) AggregatedMetrics {
	analyzer := NewStreamingAnalyzer()

	// Process entries in streaming mode
	for entry := range in {
		analyzer.Process(&entry)
	}

	return analyzer.Finalize()
}

// ============================================================================
// Unique entity tracking
// ============================================================================

// UniqueEntityAnalyzer tracks unique database entities (databases, users, applications)
// encountered in log entries.
type UniqueEntityAnalyzer struct {
	dbSet   map[string]struct{}
	userSet map[string]struct{}
	appSet  map[string]struct{}
}

// NewUniqueEntityAnalyzer creates a new unique entity analyzer.
func NewUniqueEntityAnalyzer() *UniqueEntityAnalyzer {
	return &UniqueEntityAnalyzer{
		dbSet:   make(map[string]struct{}, 100),
		userSet: make(map[string]struct{}, 100),
		appSet:  make(map[string]struct{}, 100),
	}
}

// Process extracts database, user, and application names from a log entry.
//
// Expected patterns in log messages:
//   - "db=mydb"
//   - "user=postgres"
//   - "app=psql"
func (a *UniqueEntityAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	// Extract database name
	if dbName, found := extractKeyValue(msg, "db="); found {
		a.dbSet[dbName] = struct{}{}
	}

	// Extract user name
	if userName, found := extractKeyValue(msg, "user="); found {
		a.userSet[userName] = struct{}{}
	}

	// Extract application name
	if appName, found := extractKeyValue(msg, "app="); found {
		a.appSet[appName] = struct{}{}
	}
}

// Finalize returns the unique entity metrics with sorted lists.
func (a *UniqueEntityAnalyzer) Finalize() UniqueEntityMetrics {
	return UniqueEntityMetrics{
		UniqueDbs:   len(a.dbSet),
		UniqueUsers: len(a.userSet),
		UniqueApps:  len(a.appSet),
		DBs:         mapKeysAsSlice(a.dbSet),
		Users:       mapKeysAsSlice(a.userSet),
		Apps:        mapKeysAsSlice(a.appSet),
	}
}

// ============================================================================
// Helper functions
// ============================================================================

// extractKeyValue extracts a value from a log message for a given key.
// It handles common PostgreSQL log formats where key-value pairs are separated
// by spaces, commas, brackets, or parentheses.
//
// Example patterns:
//   - "db=mydb user=postgres"
//   - "db=mydb,user=postgres"
//   - "connection authorized: user=postgres database=mydb"
//
// Returns the extracted value and true if found, or empty string and false if not found.
// Values of "unknown" or "[unknown]" are normalized to "UNKNOWN".
func extractKeyValue(line, key string) (string, bool) {
	// Find the key in the message
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}

	// Get the substring after the key
	rest := line[idx+len(key):]

	// Find the end of the value (first occurrence of separator)
	separators := []rune{' ', ',', '[', ')'}
	minPos := len(rest)
	for _, sep := range separators {
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < minPos {
			minPos = pos
		}
	}

	// Extract and normalize the value
	val := strings.TrimSpace(rest[:minPos])
	if val == "" || strings.EqualFold(val, "unknown") || strings.EqualFold(val, "[unknown]") {
		val = "UNKNOWN"
	}

	return val, true
}

// mapKeysAsSlice converts map keys to a sorted slice.
// This provides deterministic ordering for consistent output.
func mapKeysAsSlice(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
