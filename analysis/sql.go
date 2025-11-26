// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"container/list"
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

	// QueryID is the short identifier for this query (e.g., "se-abc123").
	QueryID string
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

	// QueriesWithoutDurationCount tracks queries identified from logs but without duration metrics.
	QueriesWithoutDurationCount struct {
		FromLocks     int // Queries seen in lock events
		FromTempfiles int // Queries seen in tempfile events
		Total         int // Total unique queries (may be < FromLocks + FromTempfiles due to overlap)
	}

	// QueryTypeStats contains statistics grouped by SQL query type.
	// Maps query type (SELECT, INSERT, etc.) to statistics.
	QueryTypeStats map[string]*QueryTypeStat

	// Query type breakdown by dimension (for --sql-overview)
	QueryTypesByDatabase map[string]map[string]*QueryTypeCount
	QueryTypesByUser     map[string]map[string]*QueryTypeCount
	QueryTypesByHost     map[string]map[string]*QueryTypeCount
	QueryTypesByApp      map[string]map[string]*QueryTypeCount
}

// QueryTypeStat contains aggregated statistics for a specific query type.
type QueryTypeStat struct {
	// Type is the SQL command type (SELECT, INSERT, UPDATE, DELETE, etc.)
	Type string

	// Category is the high-level category (DML, DDL, TCL, etc.)
	Category string

	// Count is the total number of executions of this type.
	Count int

	// UniqueQueries is the number of distinct queries of this type.
	UniqueQueries int

	// TotalTime is the cumulative execution time in milliseconds.
	TotalTime float64

	// AvgTime is the average execution time per query.
	AvgTime float64

	// MaxTime is the maximum execution time for this type.
	MaxTime float64
}

// QueryTypeCount tracks count and total time for a query type in a specific dimension.
type QueryTypeCount struct {
	Count     int
	TotalTime float64 // milliseconds
}

// ============================================================================
// LRU Cache for normalization
// ============================================================================

// lruCacheEntry represents a single cache entry
type lruCacheEntry struct {
	key   string
	value string
}

// lruCache is a simple LRU cache implementation
type lruCache struct {
	capacity int
	cache    map[string]*list.Element
	list     *list.List
}

// newLRUCache creates a new LRU cache with the given capacity
func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element, capacity),
		list:     list.New(),
	}
}

// Get retrieves a value from the cache
func (c *lruCache) Get(key string) (string, bool) {
	if elem, ok := c.cache[key]; ok {
		// Only move to front if not already there (optimization)
		if c.list.Front() != elem {
			c.list.MoveToFront(elem)
		}
		return elem.Value.(*lruCacheEntry).value, true
	}
	return "", false
}

// Put adds a value to the cache
func (c *lruCache) Put(key, value string) {
	if elem, ok := c.cache[key]; ok {
		// Only move to front if not already there (optimization)
		if c.list.Front() != elem {
			c.list.MoveToFront(elem)
		}
		elem.Value.(*lruCacheEntry).value = value
		return
	}

	entry := &lruCacheEntry{key: key, value: value}
	elem := c.list.PushFront(entry)
	c.cache[key] = elem

	if c.list.Len() > c.capacity {
		oldest := c.list.Back()
		if oldest != nil {
			c.list.Remove(oldest)
			delete(c.cache, oldest.Value.(*lruCacheEntry).key)
		}
	}
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

	// LRU cache to avoid re-normalizing identical raw queries
	// Limited capacity to prevent unbounded memory growth
	// Maps trimmed raw query → normalized query
	normalizationCache *lruCache

	// Query type breakdown by dimension (for --sql-overview)
	queryTypesByDatabase map[string]map[string]*QueryTypeCount
	queryTypesByUser     map[string]map[string]*QueryTypeCount
	queryTypesByHost     map[string]map[string]*QueryTypeCount
	queryTypesByApp      map[string]map[string]*QueryTypeCount
}

// NewSQLAnalyzer creates a new SQL analyzer with pre-allocated capacity.
// Pre-allocates space for 10,000 unique queries to reduce map reallocation overhead.
// Uses LRU cache with 5000 entry capacity to balance performance and memory usage.
// Larger cache reduces normalizeQuery() calls, saving ~300MB on 11GB files.
func NewSQLAnalyzer() *SQLAnalyzer {
	return &SQLAnalyzer{
		queryStats:           make(map[string]*QueryStat, 10000),
		executions:           make([]QueryExecution, 0, 10000),
		normalizationCache:   newLRUCache(5000), // LRU cache for raw→normalized mapping
		queryTypesByDatabase: make(map[string]map[string]*QueryTypeCount),
		queryTypesByUser:     make(map[string]map[string]*QueryTypeCount),
		queryTypesByHost:     make(map[string]map[string]*QueryTypeCount),
		queryTypesByApp:      make(map[string]map[string]*QueryTypeCount),
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

	// Track timestamp range
	if a.startTimestamp.IsZero() || entry.Timestamp.Before(a.startTimestamp) {
		a.startTimestamp = entry.Timestamp
	}
	if a.endTimestamp.IsZero() || entry.Timestamp.After(a.endTimestamp) {
		a.endTimestamp = entry.Timestamp
	}

	// Normalize query for aggregation (with LRU caching)
	rawQuery := strings.TrimSpace(query)

	// Check LRU cache first to avoid expensive re-normalization
	normalizedQuery, cached := a.normalizationCache.Get(rawQuery)
	if !cached {
		normalizedQuery = normalizeQuery(rawQuery)
		a.normalizationCache.Put(rawQuery, normalizedQuery)
	}

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
	} else {
		// For deterministic JSON output, always keep the alphabetically first raw query
		if rawQuery < stats.RawQuery {
			stats.RawQuery = rawQuery
		}
	}

	// Add execution with query ID (after stats are created/retrieved)
	a.executions = append(a.executions, QueryExecution{
		Timestamp: entry.Timestamp,
		Duration:  duration,
		QueryID:   stats.ID,
	})

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

	// Track query type breakdown by dimension (database, user, host, app)
	// Extract all fields in a single pass for performance
	queryType := QueryTypeFromID(stats.ID)
	database, user, host, app := extractPrefixFields(entry.Message)

	a.trackQueryTypeByDimension(database, queryType, duration, &a.queryTypesByDatabase)
	a.trackQueryTypeByDimension(user, queryType, duration, &a.queryTypesByUser)
	a.trackQueryTypeByDimension(host, queryType, duration, &a.queryTypesByHost)
	a.trackQueryTypeByDimension(app, queryType, duration, &a.queryTypesByApp)
}

// trackQueryTypeByDimension updates the query type count and total time for a dimension value.
func (a *SQLAnalyzer) trackQueryTypeByDimension(dimensionValue, queryType string, duration float64, breakdownMap *map[string]map[string]*QueryTypeCount) {
	// Skip empty dimension values
	if dimensionValue == "" {
		return
	}

	// Initialize dimension entry if needed
	if (*breakdownMap)[dimensionValue] == nil {
		(*breakdownMap)[dimensionValue] = make(map[string]*QueryTypeCount)
	}

	// Initialize query type entry if needed
	if (*breakdownMap)[dimensionValue][queryType] == nil {
		(*breakdownMap)[dimensionValue][queryType] = &QueryTypeCount{}
	}

	// Update count and total time
	(*breakdownMap)[dimensionValue][queryType].Count++
	(*breakdownMap)[dimensionValue][queryType].TotalTime += duration
}

// extractPrefixFields extracts db, user, host, and app from the log prefix in a single pass.
// Format: "user=app_user,db=app_db,app=pgadmin" or "[pid]: [line] user=x,db=y,app=z LOG: ..."
// This is faster than calling extractPrefixValue 4 times as it only scans the message once.
func extractPrefixFields(message string) (database, user, host, app string) {
	// Most efficient approach: find the first ']' which marks the end of the prefix
	// Then scan that substring once for all fields
	end := strings.IndexByte(message, ']')
	if end == -1 {
		// No prefix bracket found, scan first 200 chars (prefix is typically < 100 chars)
		if len(message) > 200 {
			end = 200
		} else {
			end = len(message)
		}
	}

	prefix := message[:end]

	// Scan the prefix once and extract all fields
	i := 0
	n := len(prefix)
	for i < n {
		// Look for key= patterns
		if i+3 < n && prefix[i:i+3] == "db=" {
			i += 3
			database = extractPrefixValueAt(prefix, i)
		} else if i+5 < n && prefix[i:i+5] == "user=" {
			i += 5
			user = extractPrefixValueAt(prefix, i)
		} else if i+5 < n && prefix[i:i+5] == "host=" {
			i += 5
			host = extractPrefixValueAt(prefix, i)
		} else if i+4 < n && prefix[i:i+4] == "app=" {
			i += 4
			app = extractPrefixValueAt(prefix, i)
		} else {
			i++
		}
	}

	return
}

// extractPrefixValueAt extracts a value starting at position i until comma, space, or bracket
func extractPrefixValueAt(s string, start int) string {
	end := start
	n := len(s)
	for end < n && s[end] != ',' && s[end] != ' ' && s[end] != ']' && s[end] != '[' {
		end++
	}
	return s[start:end]
}

// Finalize returns the aggregated SQL metrics.
// This should be called after all log entries have been processed.
//
// It calculates:
//   - Average execution time for each query
//   - Median and 99th percentile of all query durations
//   - Query type statistics
func (a *SQLAnalyzer) Finalize() SqlMetrics {
	metrics := SqlMetrics{
		QueryStats:           a.queryStats,
		TotalQueries:         a.totalQueries,
		UniqueQueries:        len(a.queryStats),
		MinQueryDuration:     a.minQueryDuration,
		MaxQueryDuration:     a.maxQueryDuration,
		SumQueryDuration:     a.sumQueryDuration,
		StartTimestamp:       a.startTimestamp,
		EndTimestamp:         a.endTimestamp,
		Executions:           a.executions,
		QueryTypeStats:       make(map[string]*QueryTypeStat),
		QueryTypesByDatabase: a.queryTypesByDatabase,
		QueryTypesByUser:     a.queryTypesByUser,
		QueryTypesByHost:     a.queryTypesByHost,
		QueryTypesByApp:      a.queryTypesByApp,
	}

	// Calculate average time for each query and aggregate by type
	for _, stat := range a.queryStats {
		stat.AvgTime = stat.TotalTime / float64(stat.Count)

		// Get query type from ID
		queryType := QueryTypeFromID(stat.ID)
		category := QueryCategory(queryType)

		// Update type statistics
		typeStat, exists := metrics.QueryTypeStats[queryType]
		if !exists {
			typeStat = &QueryTypeStat{
				Type:     queryType,
				Category: category,
			}
			metrics.QueryTypeStats[queryType] = typeStat
		}

		typeStat.UniqueQueries++
		typeStat.Count += stat.Count
		typeStat.TotalTime += stat.TotalTime
		if stat.MaxTime > typeStat.MaxTime {
			typeStat.MaxTime = stat.MaxTime
		}
	}

	// Calculate average time per type
	for _, typeStat := range metrics.QueryTypeStats {
		if typeStat.Count > 0 {
			typeStat.AvgTime = typeStat.TotalTime / float64(typeStat.Count)
		}
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
// Queries without duration metrics
// ============================================================================

// CollectQueriesWithoutDuration populates the count of queries identified
// from logs (lock events, tempfile events) but without duration metrics.
// This is useful when logs contain STATEMENT lines but no duration messages.
func CollectQueriesWithoutDuration(sql *SqlMetrics, locks *LockMetrics, tempfiles *TempFileMetrics) {
	seen := make(map[string]bool)

	// Count from locks
	for hash := range locks.QueryStats {
		if _, hasMetrics := sql.QueryStats[hash]; !hasMetrics {
			if !seen[hash] {
				sql.QueriesWithoutDurationCount.FromLocks++
				seen[hash] = true
			}
		}
	}

	// Count from tempfiles
	for hash := range tempfiles.QueryStats {
		if _, hasMetrics := sql.QueryStats[hash]; !hasMetrics {
			if !seen[hash] {
				sql.QueriesWithoutDurationCount.FromTempfiles++
				seen[hash] = true
			}
		}
	}

	sql.QueriesWithoutDurationCount.Total = len(seen)
}

