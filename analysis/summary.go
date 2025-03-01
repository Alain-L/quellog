package analysis

import (
	"sort"
	"strings"
	"time"

	"dalibo/quellog/parser"
)

// GlobalMetrics aggregates general log statistics.
type GlobalMetrics struct {
	Count        int
	MinTimestamp time.Time
	MaxTimestamp time.Time
	ErrorCount   int
	FatalCount   int
	PanicCount   int
	WarningCount int
	LogCount     int
}

// UniqueEntityMetrics tracks unique database entities (DBs, Users, Apps).
type UniqueEntityMetrics struct {
	UniqueDbs   int
	UniqueUsers int
	UniqueApps  int
	DBs         []string
	Users       []string
	Apps        []string
}

// AggregatedMetrics is the final structure combining all metrics.
type AggregatedMetrics struct {
	Global         GlobalMetrics
	TempFiles      TempFileMetrics
	Vacuum         VacuumMetrics
	Checkpoints    CheckpointMetrics
	Connections    ConnectionMetrics
	UniqueEntities UniqueEntityMetrics
	EventSummaries []EventSummary
	SQL            SqlMetrics
}

// AggregateMetrics collects statistics from log entries and delegates domain-specific metrics.
func AggregateMetrics(in <-chan parser.LogEntry) AggregatedMetrics {
	var metrics AggregatedMetrics
	var allEntries []parser.LogEntry

	// Collect global metrics while storing entries for later domain-specific analysis.
	for entry := range in {
		allEntries = append(allEntries, entry)
		metrics.Global.Count++
		if metrics.Global.MinTimestamp.IsZero() || entry.Timestamp.Before(metrics.Global.MinTimestamp) {
			metrics.Global.MinTimestamp = entry.Timestamp
		}
		if metrics.Global.MaxTimestamp.IsZero() || entry.Timestamp.After(metrics.Global.MaxTimestamp) {
			metrics.Global.MaxTimestamp = entry.Timestamp
		}
		lowerMsg := strings.ToLower(entry.Message)
		if strings.Contains(lowerMsg, "error") {
			metrics.Global.ErrorCount++
		}
		if strings.Contains(lowerMsg, "fatal") {
			metrics.Global.FatalCount++
		}
		if strings.Contains(lowerMsg, "panic") {
			metrics.Global.PanicCount++
		}
		if strings.Contains(lowerMsg, "warning") {
			metrics.Global.WarningCount++
		}
		if strings.Contains(lowerMsg, "log:") {
			metrics.Global.LogCount++
		}
	}

	// Delegate specialized metrics calculations.
	metrics.TempFiles.Count, metrics.TempFiles.TotalSize =
		CalculateTemporaryFileMetrics(&allEntries)

	vacuumEntries := AnalyzeVacuum(allEntries)
	metrics.Vacuum.VacuumCount = len(vacuumEntries)

	metrics.Checkpoints = AnalyzeCheckpoints(allEntries)
	metrics.EventSummaries = SummarizeEvents(allEntries)

	// Connection/session metrics can be analyzed separately if needed.
	metrics.Connections = AnalyzeConnections(allEntries)

	// Unique entities metrics (DBs, Users, Apps) could also be processed separately.
	metrics.UniqueEntities = AnalyzeUniqueEntities(allEntries)

	// Analyze SQL metrics
	sqlLogs := make(chan parser.LogEntry, len(allEntries))
	for _, entry := range allEntries {
		sqlLogs <- entry
	}
	close(sqlLogs)

	metrics.SQL = RunSQLSummary(sqlLogs) // NEW: Run SQL summary

	return metrics
}

// AnalyzeUniqueEntities scans log entries and extracts unique databases, users, and applications.
func AnalyzeUniqueEntities(entries []parser.LogEntry) UniqueEntityMetrics {
	var metrics UniqueEntityMetrics

	// Use maps to store unique values
	dbSet := make(map[string]struct{})
	userSet := make(map[string]struct{})
	appSet := make(map[string]struct{})

	for _, entry := range entries {
		// Extract database name
		if dbName, found := extractKeyValue(entry.Message, "db="); found {
			dbSet[dbName] = struct{}{}
		}

		// Extract user name
		if userName, found := extractKeyValue(entry.Message, "user="); found {
			userSet[userName] = struct{}{}
		}

		// Extract application name
		if appName, found := extractKeyValue(entry.Message, "app="); found {
			appSet[appName] = struct{}{}
		}
	}

	// Convert maps to slices and count unique occurrences
	metrics.UniqueDbs = len(dbSet)
	metrics.UniqueUsers = len(userSet)
	metrics.UniqueApps = len(appSet)
	metrics.DBs = mapKeysAsSlice(dbSet)
	metrics.Users = mapKeysAsSlice(userSet)
	metrics.Apps = mapKeysAsSlice(appSet)

	return metrics
}

// extractKeyValue extracts a value from a log message based on a given key (e.g., "db=").
func extractKeyValue(line, key string) (string, bool) {
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len(key):]

	// Define possible separators for the extracted value
	separators := []rune{' ', ',', '[', ')'}
	minPos := len(rest)
	for _, sep := range separators {
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < minPos {
			minPos = pos
		}
	}

	val := strings.TrimSpace(rest[:minPos])
	if val == "" || strings.EqualFold(val, "unknown") || strings.EqualFold(val, "[unknown]") {
		val = "UNKNOWN"
	}
	return val, true
}

// mapKeysAsSlice converts a map's keys into a sorted slice.
func mapKeysAsSlice(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
