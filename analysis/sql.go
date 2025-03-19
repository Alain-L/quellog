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
	//durationRegex := regexp.MustCompile(`duration:\s+(\d+\.?\d*)\s+ms\b`)

	// Regex to capture the SQL statement.
	// Example: "STATEMENT: SELECT * FROM table WHERE id = 1"
	//statementRegex := regexp.MustCompile(`(?i)(?:statement|execute(?:\s+\S+)?):\s+(.+)`)

	// Loop over SQL entries.
	for entry := range in {
		message := entry.Message

		// Extraire la durée et la requête sans regex
		duration, query, ok := extractDurationAndQuery(message)
		if !ok {
			// La ligne n'a pas le format attendu ; passer à l'entrée suivante.
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
		rawQuery := strings.TrimSpace(query)
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

func extractDurationAndQuery(message string) (duration float64, query string, ok bool) {
	// Trouver la position de "duration:"
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return 0, "", false
	}
	// Extraire la partie après "duration:"
	afterDur := message[durIdx+len("duration:"):]
	afterDur = strings.TrimSpace(afterDur)
	// La durée est le premier token (jusqu'au premier espace)
	tokens := strings.SplitN(afterDur, " ", 2)
	if len(tokens) < 2 {
		return 0, "", false
	}
	// Convertir la durée
	dur, err := strconv.ParseFloat(tokens[0], 64)
	if err != nil {
		return 0, "", false
	}

	// Rechercher "execute" ou "statement"
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

	// Après le marqueur, chercher le deux-points qui sépare le libellé du SQL
	queryStart := markerIdx + len(marker)
	colonIdx := strings.Index(message[queryStart:], ":")
	if colonIdx != -1 {
		queryStart += colonIdx + 1
	}
	query = strings.TrimSpace(message[queryStart:])
	return dur, query, true
}
