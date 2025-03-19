// analysis/vacuum.go
package analysis

import (
	"strconv"
	"strings"

	"dalibo/quellog/parser"
)

// PostgreSQL page size in bytes.
const pageSize int64 = 8192 // 8 KB

// VacuumMetrics aggregates statistics for vacuum and analyze operations.
type VacuumMetrics struct {
	VacuumCount          int              // Total number of automatic vacuum operations.
	AnalyzeCount         int              // Total number of automatic analyze operations.
	VacuumTableCounts    map[string]int   // Count of vacuum operations per table.
	AnalyzeTableCounts   map[string]int   // Count of analyze operations per table.
	VacuumSpaceRecovered map[string]int64 // Disk space recovered by vacuum (in bytes).
}

// AnalyzeVacuum processes log entries and updates VacuumMetrics.
func AnalyzeVacuum(metrics *VacuumMetrics, entries *[]parser.LogEntry) {
	// Initialize maps if nil.
	if metrics.VacuumTableCounts == nil {
		metrics.VacuumTableCounts = make(map[string]int)
	}
	if metrics.AnalyzeTableCounts == nil {
		metrics.AnalyzeTableCounts = make(map[string]int)
	}
	if metrics.VacuumSpaceRecovered == nil {
		metrics.VacuumSpaceRecovered = make(map[string]int64)
	}

	// Iterate over log entries (avoiding copies).
	for i := range *entries {
		entry := &(*entries)[i] // Direct pointer access.

		if strings.Contains(entry.Message, "automatic vacuum of table") {
			tableName := extractTableName(entry.Message)
			metrics.VacuumCount++
			metrics.VacuumTableCounts[tableName]++

			// Extract "pages: X removed" if present.
			// Instead of accessing the map multiple times (once for lookup, once for update, and once for write),
			// we use a temporary variable (`recovered`) to store the retrieved value, modify it,
			// and then write it back once. This reduces redundant hash lookups, improving performance.
			if removedPages := extractRemovedPages(&entry.Message); removedPages > 0 {
				recovered := metrics.VacuumSpaceRecovered[tableName] // Retrieve current value.
				recovered += removedPages * pageSize                 // Compute new total.
				metrics.VacuumSpaceRecovered[tableName] = recovered  // Update map.
			}

		} else if strings.Contains(entry.Message, "automatic analyze of table") {
			tableName := extractTableName(entry.Message)
			metrics.AnalyzeCount++
			metrics.AnalyzeTableCounts[tableName]++
		}
	}
}

// extractTableName retrieves the table name from a log entry.
func extractTableName(logMsg string) string {
	parts := strings.SplitN(logMsg, `"`, 3) // Découpe en 3 morceaux max
	if len(parts) < 3 {
		return "UNKNOWN" // Retourne UNKNOXN si pas assez de `"`
	}
	return parts[1] // Retourne ce qu'il y a entre les deux premiers `"`
}

// extractRemovedPages retrieves the number of removed pages from a log entry.
// Instead of scanning the entire message for keywords, this approach
// directly looks for "pages: " and extracts the number following it.
// This avoids unnecessary iterations and reduces string operations,
// improving performance when processing large log files.
func extractRemovedPages(logMsg *string) int64 {
	idx := strings.Index(*logMsg, "pages: ")
	if idx == -1 {
		return 0
	}

	sub := (*logMsg)[idx+7:] // Extract substring after "pages: ".
	spaceIdx := strings.Index(sub, " ")
	if spaceIdx == -1 {
		return 0
	}

	removedPages, err := strconv.ParseInt(sub[:spaceIdx], 10, 64)
	if err != nil {
		return 0
	}
	return removedPages
}
