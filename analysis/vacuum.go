// analysis/vacuum.go
package analysis

import (
	"strings"

	"dalibo/quellog/parser"
)

// VacuumMetrics aggregates statistics related to vacuum and analyze operations.
type VacuumMetrics struct {
	VacuumCount          int              // Total automatic vacuum operations
	AnalyzeCount         int              // Total automatic analyze operations
	VacuumTableCounts    map[string]int   // Vacuum operations per table
	AnalyzeTableCounts   map[string]int   // Analyze operations per table
	VacuumSpaceRecovered map[string]int64 // Space recovered by vacuum (in bytes)
}

// AnalyzeVacuum filters log entries related to vacuum operations.
func AnalyzeVacuum(entries []parser.LogEntry) []parser.LogEntry {
	var vacuumEntries []parser.LogEntry
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Message), "vacuum") {
			vacuumEntries = append(vacuumEntries, entry)
		}
	}
	return vacuumEntries
}
