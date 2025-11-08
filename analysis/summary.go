// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"
	"sync"
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

	// Locks contains lock event statistics.
	Locks LockMetrics

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
	locks          *LockAnalyzer
	events         *EventAnalyzer
	errorClasses   *ErrorClassAnalyzer
	uniqueEntities *UniqueEntityAnalyzer
	sql            *SQLAnalyzer

	// Parallel SQL processing
	sqlChan chan *parser.LogEntry
	sqlWg   sync.WaitGroup
}

// NewStreamingAnalyzer creates a new streaming analyzer with all sub-analyzers initialized.
// If enableParallel is true, SQLAnalyzer runs in a dedicated goroutine for better performance
// on large files (>200MB). For smaller files, parallel overhead outweighs the gains.
func NewStreamingAnalyzer(enableParallel bool) *StreamingAnalyzer {
	sa := &StreamingAnalyzer{
		tempFiles:      NewTempFileAnalyzer(),
		vacuum:         NewVacuumAnalyzer(),
		checkpoints:    NewCheckpointAnalyzer(),
		connections:    NewConnectionAnalyzer(),
		locks:          NewLockAnalyzer(),
		events:         NewEventAnalyzer(),
		errorClasses:   NewErrorClassAnalyzer(),
		uniqueEntities: NewUniqueEntityAnalyzer(),
		sql:            NewSQLAnalyzer(),
	}

	if enableParallel {
		// Start parallel SQL analyzer goroutine.
		// This provides ~20% wall clock speedup (benchmarked on 1GB+ files) by offloading
		// the most expensive analyzer to a dedicated goroutine, allowing better CPU utilization.
		sa.sqlChan = make(chan *parser.LogEntry, 10000)
		sa.sqlWg.Add(1)
		go func() {
			defer sa.sqlWg.Done()
			for entry := range sa.sqlChan {
				sa.sql.Process(entry)
			}
		}()
	}

	return sa
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
	sa.tempFiles.Process(entry)
	sa.vacuum.Process(entry)
	sa.checkpoints.Process(entry)
	sa.connections.Process(entry)
	sa.locks.Process(entry)
	sa.events.Process(entry)
	sa.errorClasses.Process(entry)
	sa.uniqueEntities.Process(entry)

	// SQLAnalyzer: run in parallel if enabled, otherwise sequential
	if sa.sqlChan != nil {
		sa.sqlChan <- entry
	} else {
		sa.sql.Process(entry)
	}
}

// Finalize computes final metrics after all log entries have been processed.
// This should be called once after processing all entries.
func (sa *StreamingAnalyzer) Finalize() AggregatedMetrics {
	// Close SQL channel and wait for goroutine to finish (if parallel mode)
	if sa.sqlChan != nil {
		close(sa.sqlChan)
		sa.sqlWg.Wait()
	}

	// Finalize all metrics
	tempFiles := sa.tempFiles.Finalize()
	locks := sa.locks.Finalize()
	sql := sa.sql.Finalize()

	// Collect queries without duration metrics from locks and tempfiles
	CollectQueriesWithoutDuration(&sql, &locks, &tempFiles)

	return AggregatedMetrics{
		Global:         sa.global,
		TempFiles:      tempFiles,
		Vacuum:         sa.vacuum.Finalize(),
		Checkpoints:    sa.checkpoints.Finalize(),
		Connections:    sa.connections.Finalize(),
		Locks:          locks,
		EventSummaries: sa.events.Finalize(),
		ErrorClasses:   sa.errorClasses.Finalize(),
		UniqueEntities: sa.uniqueEntities.Finalize(),
		SQL:            sql,
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
//
// fileSize is used to determine whether to enable parallel SQL analysis:
//   - Files > 200MB: parallel SQL analyzer (~20% speedup)
//   - Files < 200MB: sequential processing (avoids goroutine overhead)
func AggregateMetrics(in <-chan parser.LogEntry, fileSize int64) AggregatedMetrics {
	// Enable parallel SQL analysis for large files to improve performance.
	// Threshold of 200MB based on profiling: below this, goroutine overhead
	// outweighs parallelization gains.
	const thresholdMB = 200
	enableParallel := fileSize > thresholdMB*1024*1024

	// DEBUG: log which mode is selected (disabled in production)
	//fmt.Fprintf(os.Stderr, "[DEBUG] File size: %.1f MB, Parallel SQL: %v (threshold: %d MB)\n",
	//	float64(fileSize)/(1024*1024), enableParallel, thresholdMB)

	analyzer := NewStreamingAnalyzer(enableParallel)

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

	// Single-pass extraction: scan once for all three keys
	// This is much faster than calling extractKeyValue 3 times
	dbIdx := strings.Index(msg, "db=")
	userIdx := strings.Index(msg, "user=")
	appIdx := strings.Index(msg, "app=")

	// Extract database name
	if dbIdx != -1 {
		if dbName := extractValueAt(msg, dbIdx+3); dbName != "" {
			a.dbSet[dbName] = struct{}{}
		}
	}

	// Extract user name
	if userIdx != -1 {
		if userName := extractValueAt(msg, userIdx+5); userName != "" {
			a.userSet[userName] = struct{}{}
		}
	}

	// Extract application name
	if appIdx != -1 {
		if appName := extractValueAt(msg, appIdx+4); appName != "" {
			a.appSet[appName] = struct{}{}
		}
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

// extractValueAt extracts a value starting at a given position in the message.
// It stops at the first separator: space, comma, bracket, or parenthesis.
// Returns empty string if no valid value found.
// This is optimized to avoid allocating a slice of separators.
func extractValueAt(msg string, startPos int) string {
	if startPos >= len(msg) {
		return ""
	}

	// Find end position (first separator)
	endPos := startPos
	for endPos < len(msg) {
		c := msg[endPos]
		// Check for separators: space, comma, '[', ')'
		if c == ' ' || c == ',' || c == '[' || c == ')' {
			break
		}
		endPos++
	}

	if endPos == startPos {
		return ""
	}

	// Extract and normalize value
	val := msg[startPos:endPos]

	// Normalize "unknown" or "[unknown]" to "UNKNOWN"
	if val == "" || val == "unknown" || val == "[unknown]" {
		return "UNKNOWN"
	}

	// Case-insensitive check for "unknown" (rare but handle it)
	if len(val) == 7 && (val[0] == 'u' || val[0] == 'U') {
		if strings.EqualFold(val, "unknown") {
			return "UNKNOWN"
		}
	}

	return val
}

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

	// Extract value starting after the key
	val := extractValueAt(line, idx+len(key))
	if val == "" {
		return "", false
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
