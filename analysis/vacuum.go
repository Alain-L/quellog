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

// ============================================================================
// VERSION STREAMING
// ============================================================================

// VacuumAnalyzer traite les opérations vacuum/analyze au fil de l'eau.
type VacuumAnalyzer struct {
	vacuumCount          int
	analyzeCount         int
	vacuumTableCounts    map[string]int
	analyzeTableCounts   map[string]int
	vacuumSpaceRecovered map[string]int64
}

// NewVacuumAnalyzer crée un nouvel analyseur de vacuum.
func NewVacuumAnalyzer() *VacuumAnalyzer {
	return &VacuumAnalyzer{
		vacuumTableCounts:    make(map[string]int, 100),
		analyzeTableCounts:   make(map[string]int, 100),
		vacuumSpaceRecovered: make(map[string]int64, 100),
	}
}

// Process traite une entrée de log pour détecter vacuum/analyze.
func (a *VacuumAnalyzer) Process(entry *parser.LogEntry) {
	msg := &entry.Message

	if strings.Contains(*msg, "automatic vacuum of table") {
		tableName := extractTableName(*msg)
		a.vacuumCount++
		a.vacuumTableCounts[tableName]++

		// Extract "pages: X removed" if present
		if removedPages := extractRemovedPages(msg); removedPages > 0 {
			a.vacuumSpaceRecovered[tableName] += removedPages * pageSize
		}

	} else if strings.Contains(*msg, "automatic analyze of table") {
		tableName := extractTableName(*msg)
		a.analyzeCount++
		a.analyzeTableCounts[tableName]++
	}
}

// Finalize retourne les métriques finales.
func (a *VacuumAnalyzer) Finalize() VacuumMetrics {
	return VacuumMetrics{
		VacuumCount:          a.vacuumCount,
		AnalyzeCount:         a.analyzeCount,
		VacuumTableCounts:    a.vacuumTableCounts,
		AnalyzeTableCounts:   a.analyzeTableCounts,
		VacuumSpaceRecovered: a.vacuumSpaceRecovered,
	}
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// AnalyzeVacuum processes log entries and updates VacuumMetrics.
// À supprimer une fois le refactoring terminé.
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

	// Use the streaming analyzer internally
	analyzer := &VacuumAnalyzer{
		vacuumTableCounts:    metrics.VacuumTableCounts,
		analyzeTableCounts:   metrics.AnalyzeTableCounts,
		vacuumSpaceRecovered: metrics.VacuumSpaceRecovered,
	}

	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}

	metrics.VacuumCount = analyzer.vacuumCount
	metrics.AnalyzeCount = analyzer.analyzeCount
}

// ============================================================================
// HELPERS (inchangés)
// ============================================================================

// extractTableName retrieves the table name from a log entry.
func extractTableName(logMsg string) string {
	parts := strings.SplitN(logMsg, `"`, 3)
	if len(parts) < 3 {
		return "UNKNOWN"
	}
	return parts[1]
}

// extractRemovedPages retrieves the number of removed pages from a log entry.
func extractRemovedPages(logMsg *string) int64 {
	idx := strings.Index(*logMsg, "pages: ")
	if idx == -1 {
		return 0
	}

	sub := (*logMsg)[idx+7:]
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
