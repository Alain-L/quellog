// analysis/sql.go
package analysis

import (
	"dalibo/quellog/parser"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QueryStat stores aggregated statistics for ONE SQL query.
type QueryStat struct {
	RawQuery        string  // Original raw query.
	NormalizedQuery string  // Normalized query (used for stats).
	Count           int     // Number of occurrences.
	TotalTime       float64 // Total execution time in milliseconds.
	AvgTime         float64 // Average execution time in milliseconds.
	MaxTime         float64 // Maximum execution time in milliseconds.
	ID              string  // User-friendly identifier, e.g. "se-123xaB".
	FullHash        string  // Full hash in hexadecimal (e.g., 32-character MD5).
}

// QueryExecution stores the timestamp and duration of a single SQL query.
type QueryExecution struct {
	Timestamp time.Time
	Duration  float64 // in milliseconds
}

// SqlMetrics aggregates overall SQL metrics from parsed log entries.
type SqlMetrics struct {
	QueryStats          map[string]*QueryStat // Aggregation by normalized query.
	TotalQueries        int                   // Total number of parsed SQL queries.
	UniqueQueries       int                   // Number of unique (normalized) queries.
	MinQueryDuration    float64               // Minimum query duration in ms.
	MaxQueryDuration    float64               // Maximum query duration in ms.
	SumQueryDuration    float64               // Sum of all query durations in ms.
	StartTimestamp      time.Time             // Timestamp of the first SQL query.
	EndTimestamp        time.Time             // Timestamp of the last SQL query.
	Executions          []QueryExecution      // List of individual query executions.
	MedianQueryDuration float64               // Median (50th percentile) of query durations.
	P99QueryDuration    float64               // 99th percentile of query durations.
}

// ============================================================================
// VERSION STREAMING
// ============================================================================

// SQLAnalyzer traite les requêtes SQL au fil de l'eau.
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

// NewSQLAnalyzer crée un nouvel analyseur SQL.
func NewSQLAnalyzer() *SQLAnalyzer {
	return &SQLAnalyzer{
		queryStats: make(map[string]*QueryStat, 1000),
		executions: make([]QueryExecution, 0, 10000),
	}
}

// Process traite une entrée de log pour détecter et analyser les requêtes SQL.
func (a *SQLAnalyzer) Process(entry *parser.LogEntry) {
	message := &entry.Message

	// Extract duration and query without regex
	duration, query, ok := extractDurationAndQuery(*message)
	if !ok {
		return
	}

	// Update global metrics
	a.totalQueries++
	a.executions = append(a.executions, QueryExecution{
		Timestamp: entry.Timestamp,
		Duration:  duration,
	})

	if a.startTimestamp.IsZero() || entry.Timestamp.Before(a.startTimestamp) {
		a.startTimestamp = entry.Timestamp
	}
	if a.endTimestamp.IsZero() || entry.Timestamp.After(a.endTimestamp) {
		a.endTimestamp = entry.Timestamp
	}

	// Extract and normalize the SQL query
	rawQuery := strings.TrimSpace(query)
	normalizedQuery := normalizeQuery(rawQuery)

	// Generate a user-friendly ID and full hash from the query
	id, fullHash := GenerateQueryID(rawQuery, normalizedQuery)

	// Use the normalized query as the key
	key := normalizedQuery
	if _, exists := a.queryStats[key]; !exists {
		a.queryStats[key] = &QueryStat{
			RawQuery:        rawQuery,
			NormalizedQuery: normalizedQuery,
			ID:              id,
			FullHash:        fullHash,
		}
	}

	// Update per-query statistics
	stats := a.queryStats[key]
	stats.Count++
	stats.TotalTime += duration
	if duration > stats.MaxTime {
		stats.MaxTime = duration
	}
	stats.AvgTime = stats.TotalTime / float64(stats.Count)

	// Update global duration stats
	if a.minQueryDuration == 0 || duration < a.minQueryDuration {
		a.minQueryDuration = duration
	}
	if duration > a.maxQueryDuration {
		a.maxQueryDuration = duration
	}
	a.sumQueryDuration += duration
}

// Finalize retourne les métriques finales.
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

	// Compute median and 99th percentile durations
	if len(a.executions) > 0 {
		durations := make([]float64, len(a.executions))
		for i, exec := range a.executions {
			durations[i] = exec.Duration
		}
		sort.Float64s(durations)
		n := len(durations)

		if n%2 == 1 {
			metrics.MedianQueryDuration = durations[n/2]
		} else {
			metrics.MedianQueryDuration = (durations[n/2-1] + durations[n/2]) / 2.0
		}

		index := int(0.99 * float64(n))
		if index >= n {
			index = n - 1
		}
		metrics.P99QueryDuration = durations[index]
	}

	return metrics
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// RunSQLSummary reads SQL log entries from the channel and aggregates SQL statistics.
// À supprimer une fois le refactoring terminé.
func RunSQLSummary(in <-chan parser.LogEntry) SqlMetrics {
	analyzer := NewSQLAnalyzer()
	for entry := range in {
		analyzer.Process(&entry)
	}
	return analyzer.Finalize()
}

// ============================================================================
// HELPERS (inchangés)
// ============================================================================

func extractDurationAndQuery(message string) (duration float64, query string, ok bool) {
	// Find "duration:"
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return 0, "", false
	}

	afterDur := message[durIdx+9:] // Skip "duration:"
	afterDur = strings.TrimSpace(afterDur)

	tokens := strings.SplitN(afterDur, " ", 2)
	if len(tokens) < 2 {
		return 0, "", false
	}

	dur, err := strconv.ParseFloat(tokens[0], 64)
	if err != nil {
		return 0, "", false
	}

	// Search for "execute" or "statement"
	execIdx := strings.Index(message, "execute")
	stmtIdx := strings.Index(message, "statement")

	var markerIdx int
	var marker string
	if execIdx != -1 && (stmtIdx == -1 || execIdx < stmtIdx) {
		markerIdx = execIdx
		marker = "execute"
	} else if stmtIdx != -1 {
		markerIdx = stmtIdx
		marker = "statement"
	} else {
		return dur, "", false
	}

	queryStart := markerIdx + len(marker)
	colonIdx := strings.Index(message[queryStart:], ":")
	if colonIdx != -1 {
		queryStart += colonIdx + 1
	}
	query = strings.TrimSpace(message[queryStart:])
	return dur, query, true
}
