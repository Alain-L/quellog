// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// QueryStat stores aggregated statistics for a single SQL query pattern.
// Multiple executions of the same normalized query are aggregated into one QueryStat.
type QueryStat struct {
	// RawQuery is the original query text (first occurrence).
	RawQuery string

	// NormalizedQuery is the parameterized version used for grouping.
	// Example: "SELECT * FROM users WHERE id = $1"
	NormalizedQuery string

	// Count is the number of times this query was executed.
	Count int

	// TotalTime is the cumulative execution time in milliseconds.
	TotalTime float64

	// AvgTime is the average execution time in milliseconds.
	// Calculated as TotalTime / Count.
	AvgTime float64

	// MaxTime is the maximum execution time observed in milliseconds.
	MaxTime float64

	// ID is a short, user-friendly identifier (e.g., "se-123xaB").
	ID string

	// FullHash is the complete hash in hexadecimal (e.g., 32-character MD5).
	FullHash string
}

// QueryExecution represents a single SQL query execution event.
type QueryExecution struct {
	// Timestamp is when the query was executed.
	Timestamp time.Time

	// Duration is the execution time in milliseconds.
	Duration float64
}

// SqlMetrics aggregates SQL query statistics from log analysis.
// It provides both per-query statistics and global metrics.
type SqlMetrics struct {
	// QueryStats maps normalized queries to their aggregated statistics.
	QueryStats map[string]*QueryStat

	// TotalQueries is the total number of SQL queries executed.
	TotalQueries int

	// UniqueQueries is the number of distinct normalized queries.
	UniqueQueries int

	// MinQueryDuration is the fastest query duration in milliseconds.
	MinQueryDuration float64

	// MaxQueryDuration is the slowest query duration in milliseconds.
	MaxQueryDuration float64

	// SumQueryDuration is the total execution time of all queries in milliseconds.
	SumQueryDuration float64

	// StartTimestamp is when the first query was executed.
	StartTimestamp time.Time

	// EndTimestamp is when the last query was executed.
	EndTimestamp time.Time

	// Executions contains all individual query executions.
	// Useful for timeline analysis and percentile calculations.
	Executions []QueryExecution

	// MedianQueryDuration is the 50th percentile of query durations.
	MedianQueryDuration float64

	// P99QueryDuration is the 99th percentile of query durations.
	P99QueryDuration float64
}

// ============================================================================
// Streaming SQL analyzer
// ============================================================================

// SQLAnalyzer processes SQL queries from log entries in streaming mode.
// It extracts query durations, normalizes queries, and aggregates statistics.
//
// Usage:
//
//	analyzer := NewSQLAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type SQLAnalyzer struct {
	queryStats       map[string]*QueryStat
	totalQueries     int
	minQueryDuration float64
	maxQueryDuration float64
	sumQueryDuration float64
	startTimestamp   time.Time
	endTimestamp     time.Time
	executions       []QueryExecution
}

// NewSQLAnalyzer creates a new SQL analyzer with pre-allocated capacity.
func NewSQLAnalyzer() *SQLAnalyzer {
	return &SQLAnalyzer{
		queryStats: make(map[string]*QueryStat, 1000),
		executions: make([]QueryExecution, 0, 10000),
	}
}

// Process analyzes a single log entry for SQL query information.
// It extracts duration and query text, normalizes the query, and updates statistics.
//
// Expected log format:
//
//	"LOG: duration: 5.123 ms execute <unnamed>: SELECT * FROM users WHERE id = 1"
//	"LOG: duration: 10.456 ms statement: UPDATE users SET name = 'John' WHERE id = 1"
func (a *SQLAnalyzer) Process(entry *parser.LogEntry) {
	// Extract duration and query from log message

	duration, query, ok := extractDurationAndQuery(entry.Message)
	if !ok {
		return
	}

	// Update global execution metrics
	a.totalQueries++
	a.executions = append(a.executions, QueryExecution{
		Timestamp: entry.Timestamp,
		Duration:  duration,
	})

	// Track timestamp range
	if a.startTimestamp.IsZero() || entry.Timestamp.Before(a.startTimestamp) {
		a.startTimestamp = entry.Timestamp
	}
	if a.endTimestamp.IsZero() || entry.Timestamp.After(a.endTimestamp) {
		a.endTimestamp = entry.Timestamp
	}

	// Normalize query for aggregation
	rawQuery := strings.TrimSpace(query)
	normalizedQuery := normalizeQuery(rawQuery)

	// Update or create query statistics
	stats, exists := a.queryStats[normalizedQuery]
	if !exists {
		// Generate ID only for new queries (expensive operation)
		id, fullHash := GenerateQueryID(rawQuery, normalizedQuery)
		stats = &QueryStat{
			RawQuery:        rawQuery,
			NormalizedQuery: normalizedQuery,
			ID:              id,
			FullHash:        fullHash,
		}
		a.queryStats[normalizedQuery] = stats
	}

	// Update per-query statistics
	stats.Count++
	stats.TotalTime += duration
	if duration > stats.MaxTime {
		stats.MaxTime = duration
	}

	// Update global duration statistics
	if a.minQueryDuration == 0 || duration < a.minQueryDuration {
		a.minQueryDuration = duration
	}
	if duration > a.maxQueryDuration {
		a.maxQueryDuration = duration
	}
	a.sumQueryDuration += duration
}

// Finalize returns the aggregated SQL metrics.
// This should be called after all log entries have been processed.
//
// It calculates:
//   - Average execution time for each query
//   - Median and 99th percentile of all query durations
func (a *SQLAnalyzer) Finalize() SqlMetrics {
	metrics := SqlMetrics{
		QueryStats:       a.queryStats,
		TotalQueries:     a.totalQueries,
		UniqueQueries:    len(a.queryStats),
		MinQueryDuration: a.minQueryDuration,
		MaxQueryDuration: a.maxQueryDuration,
		SumQueryDuration: a.sumQueryDuration,
		StartTimestamp:   a.startTimestamp,
		EndTimestamp:     a.endTimestamp,
		Executions:       a.executions,
	}

	// Calculate average time for each query
	// Done here (once) instead of in Process (N times) for efficiency
	for _, stat := range a.queryStats {
		stat.AvgTime = stat.TotalTime / float64(stat.Count)
	}

	// Calculate percentiles
	if len(a.executions) > 0 {
		metrics.MedianQueryDuration = calculateMedian(a.executions)
		metrics.P99QueryDuration = calculatePercentile(a.executions, 99)
	}

	return metrics
}

// ============================================================================
// Percentile calculation helpers
// ============================================================================

// calculateMedian computes the median (50th percentile) of query durations.
func calculateMedian(executions []QueryExecution) float64 {
	durations := extractDurations(executions)
	sort.Float64s(durations)

	n := len(durations)
	if n%2 == 1 {
		return durations[n/2]
	}
	return (durations[n/2-1] + durations[n/2]) / 2.0
}

// calculatePercentile computes the Nth percentile of query durations.
// percentile should be between 0 and 100.
func calculatePercentile(executions []QueryExecution, percentile int) float64 {
	durations := extractDurations(executions)
	sort.Float64s(durations)

	n := len(durations)
	index := int(float64(percentile) / 100.0 * float64(n))
	if index >= n {
		index = n - 1
	}
	if index < 0 {
		index = 0
	}

	return durations[index]
}

// extractDurations extracts duration values from query executions.
func extractDurations(executions []QueryExecution) []float64 {
	durations := make([]float64, len(executions))
	for i, exec := range executions {
		durations[i] = exec.Duration
	}
	return durations
}

// ============================================================================
// Query extraction from log messages
// ============================================================================

// extractDurationAndQuery parses duration and query text from a PostgreSQL log message.
//
// Expected format:
//
//	"duration: X.XXX ms execute <name>: QUERY"
//	"duration: X.XXX ms statement: QUERY"
//
// Returns:
//   - duration: execution time in milliseconds
//   - query: SQL query text
//   - ok: true if parsing succeeded
//
// This function is optimized for performance:
//   - Single pass parsing
//   - No intermediate string allocations
//   - Manual whitespace skipping
func extractDurationAndQuery(message string) (duration float64, query string, ok bool) {
	// Quick length check
	if len(message) < 20 {
		return 0, "", false
	}

	// Find "duration:" marker
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return 0, "", false
	}

	// Parse duration value
	start := durIdx + 9 // Skip "duration:"

	// Skip leading whitespace
	for start < len(message) && message[start] == ' ' {
		start++
	}

	// Find end of duration number (next space)
	end := start
	for end < len(message) && message[end] != ' ' {
		end++
	}

	if end == start {
		return 0, "", false
	}

	// Parse float duration
	dur, err := strconv.ParseFloat(message[start:end], 64)
	if err != nil {
		return 0, "", false
	}

	// Find query marker ("execute" or "statement")
	// Search after duration marker for efficiency
	var markerIdx int
	var markerLen int

	execIdx := indexAfter(message, "execute", durIdx)
	stmtIdx := indexAfter(message, "statement", durIdx)

	if execIdx != -1 && (stmtIdx == -1 || execIdx < stmtIdx) {
		markerIdx = execIdx
		markerLen = 7 // len("execute")
	} else if stmtIdx != -1 {
		markerIdx = stmtIdx
		markerLen = 9 // len("statement")
	} else {
		return dur, "", false
	}

	// Find ':' after marker
	queryStart := markerIdx + markerLen
	for queryStart < len(message) && message[queryStart] != ':' {
		queryStart++
	}
	if queryStart >= len(message) {
		return dur, "", false
	}
	queryStart++ // Skip ':'

	// Skip leading whitespace before query
	for queryStart < len(message) && message[queryStart] == ' ' {
		queryStart++
	}

	if queryStart >= len(message) {
		return dur, "", false
	}

	query = message[queryStart:]
	return dur, query, true
}

// indexAfter finds the first occurrence of substr in s, starting after the given position.
// Returns -1 if not found.
func indexAfter(s, substr string, after int) int {
	if after >= len(s) {
		return -1
	}
	idx := strings.Index(s[after:], substr)
	if idx == -1 {
		return -1
	}
	return after + idx
}

// ============================================================================
// Legacy API (for backward compatibility)
// ============================================================================

// RunSQLSummary reads SQL log entries from a channel and aggregates statistics.
//
// Deprecated: This function is designed for the old channel-based API.
// Use SQLAnalyzer with streaming for better control and flexibility.
//
// This function is maintained for backward compatibility and will be removed
// in a future version.
func RunSQLSummary(in <-chan parser.LogEntry) SqlMetrics {
	analyzer := NewSQLAnalyzer()
	for entry := range in {
		analyzer.Process(&entry)
	}
	return analyzer.Finalize()
}
