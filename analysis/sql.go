// analysis/sql.go
package analysis

import (
	"dalibo/quellog/parser"
	"regexp"
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

// For Histogram
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

// RunSQLSummary reads SQL log entries from the channel and aggregates SQL statistics.
// It uses regular expressions to extract the execution time and the SQL statement.
func RunSQLSummary(in <-chan parser.LogEntry) SqlMetrics {
	metrics := SqlMetrics{
		QueryStats: make(map[string]*QueryStat),
	}

	// Regex to capture the duration (in ms) of an SQL query.
	// Example: "duration: 123.45 ms"
	durationRegex := regexp.MustCompile(`duration:\s+(\d+\.?\d*)\s+ms\b`)

	// Regex to capture the SQL statement.
	// Example: "STATEMENT: SELECT * FROM table WHERE id = 1"
	statementRegex := regexp.MustCompile(`(?i)(?:statement|execute(?:\s+\S+)?):\s+(.+)`)

	// Loop over SQL entries.
	for entry := range in {
		message := entry.Message

		// Find duration and statement within the log message.
		durationMatch := durationRegex.FindStringSubmatch(message)
		statementMatch := statementRegex.FindStringSubmatch(message)
		if len(durationMatch) < 2 || len(statementMatch) < 2 {
			// Not an SQL log with both duration and statement; skip.
			continue
		}

		// Parse the duration.
		duration, err := strconv.ParseFloat(durationMatch[1], 64)
		if err != nil {
			continue
		}

		// Update global metrics.
		metrics.TotalQueries++
		metrics.Executions = append(metrics.Executions, QueryExecution{
			Timestamp: entry.Timestamp,
			Duration:  duration,
		})
		if metrics.StartTimestamp.IsZero() || entry.Timestamp.Before(metrics.StartTimestamp) {
			metrics.StartTimestamp = entry.Timestamp
		}
		if metrics.EndTimestamp.IsZero() || entry.Timestamp.After(metrics.EndTimestamp) {
			metrics.EndTimestamp = entry.Timestamp
		}

		// Extract and normalize the SQL query.
		rawQuery := strings.TrimSpace(statementMatch[1])
		normalizedQuery := normalizeQuery(rawQuery)

		// Generate a user-friendly ID and full hash from the query.
		id, fullHash := GenerateQueryID(rawQuery, normalizedQuery)

		// Use the normalized query as the key in the map.
		key := normalizedQuery
		if _, exists := metrics.QueryStats[key]; !exists {
			metrics.QueryStats[key] = &QueryStat{
				RawQuery:        rawQuery,
				NormalizedQuery: normalizedQuery,
				ID:              id,
				FullHash:        fullHash,
			}
		}

		// Update per-query statistics.
		stats := metrics.QueryStats[key]
		stats.Count++
		stats.TotalTime += duration
		if duration > stats.MaxTime {
			stats.MaxTime = duration
		}
		stats.AvgTime = stats.TotalTime / float64(stats.Count)

		// Update global duration stats.
		if metrics.MinQueryDuration == 0 || duration < metrics.MinQueryDuration {
			metrics.MinQueryDuration = duration
		}
		if duration > metrics.MaxQueryDuration {
			metrics.MaxQueryDuration = duration
		}
		metrics.SumQueryDuration += duration
	}

	// Compute median and 99th percentile durations based on Executions.
	if len(metrics.Executions) > 0 {
		// Extraire toutes les durées dans un slice pour le calcul.
		durations := make([]float64, len(metrics.Executions))
		for i, exec := range metrics.Executions {
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

	// Set the count of unique queries.
	metrics.UniqueQueries = len(metrics.QueryStats)
	return metrics
}
