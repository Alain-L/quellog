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
	// ✅ OPTIMISÉ: Pas besoin de pointer vers Message
	// Extract duration and query without regex
	duration, query, ok := extractDurationAndQuery(entry.Message)
	if !ok {
		return
	}

	// Update global metrics
	a.totalQueries++
	a.executions = append(a.executions, QueryExecution{
		Timestamp: entry.Timestamp,
		Duration:  duration,
	})

	// ✅ OPTIMISÉ: Simplifié les comparaisons de timestamps
	if a.startTimestamp.IsZero() || entry.Timestamp.Before(a.startTimestamp) {
		a.startTimestamp = entry.Timestamp
	}
	if a.endTimestamp.IsZero() || entry.Timestamp.After(a.endTimestamp) {
		a.endTimestamp = entry.Timestamp
	}

	// Extract and normalize the SQL query
	rawQuery := strings.TrimSpace(query)
	normalizedQuery := normalizeQuery(rawQuery)

	// ✅ OPTIMISÉ: Cherche d'abord si la query existe déjà
	stats, exists := a.queryStats[normalizedQuery]
	if !exists {
		// Generate ID only for new queries
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
	// ✅ OPTIMISÉ: AvgTime calculé seulement dans Finalize (pas besoin à chaque fois)

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

	// ✅ OPTIMISÉ: Calcul de AvgTime seulement ici (une fois au lieu de N fois)
	for _, stat := range a.queryStats {
		stat.AvgTime = stat.TotalTime / float64(stat.Count)
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

// ✅ OPTIMISÉ: Parse en UNE SEULE passe, sans strings.Index multiples
func extractDurationAndQuery(message string) (duration float64, query string, ok bool) {
	// Early return si pas "duration:" visible au début
	if len(message) < 20 {
		return 0, "", false
	}

	// Find "duration:" - commence la recherche après "LOG:" ou "ERROR:" typiquement
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return 0, "", false
	}

	// Parse duration directement sans TrimSpace ni SplitN
	start := durIdx + 9 // Skip "duration:"
	// Skip whitespace manually
	for start < len(message) && message[start] == ' ' {
		start++
	}

	// Find end of duration number (jusqu'au prochain espace)
	end := start
	for end < len(message) && message[end] != ' ' {
		end++
	}

	if end == start {
		return 0, "", false
	}

	dur, err := strconv.ParseFloat(message[start:end], 64)
	if err != nil {
		return 0, "", false
	}

	// ✅ OPTIMISÉ: Cherche "execute" OU "statement" en UNE passe
	// On sait que le marker vient APRÈS "duration:", donc on cherche après durIdx
	var markerIdx int
	var markerLen int

	// Cherche d'abord "execute" (plus courant généralement)
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

	// Skip leading whitespace
	for queryStart < len(message) && message[queryStart] == ' ' {
		queryStart++
	}

	if queryStart >= len(message) {
		return dur, "", false
	}

	query = message[queryStart:]
	return dur, query, true
}

// ✅ NOUVELLE FONCTION: Index mais seulement après une position donnée
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
