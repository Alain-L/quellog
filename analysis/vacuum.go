// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"

	"github.com/Alain-L/quellog/parser"
)

// PostgreSQL constants
const (
	// pageSize is the standard PostgreSQL page size in bytes.
	// PostgreSQL stores data in 8 KB pages.
	pageSize int64 = 8192
)

// VacuumMetrics aggregates statistics for autovacuum and autoanalyze operations.
// These operations are critical for PostgreSQL performance and space management.
type VacuumMetrics struct {
	// VacuumCount is the total number of automatic vacuum operations.
	VacuumCount int

	// AnalyzeCount is the total number of automatic analyze operations.
	AnalyzeCount int

	// VacuumTableCounts maps table names to their vacuum operation count.
	// Useful for identifying tables that are vacuumed frequently.
	VacuumTableCounts map[string]int

	// AnalyzeTableCounts maps table names to their analyze operation count.
	// Useful for understanding statistics update patterns.
	AnalyzeTableCounts map[string]int

	// VacuumSpaceRecovered maps table names to total disk space recovered in bytes.
	// This represents dead tuple space reclaimed by vacuum operations.
	VacuumSpaceRecovered map[string]int64
}

// ============================================================================
// Vacuum log patterns
// ============================================================================

// Vacuum log message patterns
const (
	autoVacuumMarker  = "automatic vacuum of table"
	autoAnalyzeMarker = "automatic analyze of table"
	pagesRemovedKey   = "pages: "
	pagesRemovedWord  = " removed"
)

// ============================================================================
// Streaming vacuum analyzer
// ============================================================================

// VacuumAnalyzer processes autovacuum and autoanalyze events from log entries.
// It tracks operation counts and space recovery per table.
//
// Usage:
//
//	analyzer := NewVacuumAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type VacuumAnalyzer struct {
	vacuumCount          int
	analyzeCount         int
	vacuumTableCounts    map[string]int
	analyzeTableCounts   map[string]int
	vacuumSpaceRecovered map[string]int64
}

// NewVacuumAnalyzer creates a new vacuum analyzer.
func NewVacuumAnalyzer() *VacuumAnalyzer {
	return &VacuumAnalyzer{
		vacuumTableCounts:    make(map[string]int, 100),
		analyzeTableCounts:   make(map[string]int, 100),
		vacuumSpaceRecovered: make(map[string]int64, 100),
	}
}

// Process analyzes a single log entry for vacuum and analyze operations.
//
// Expected log formats:
//   - Vacuum: "automatic vacuum of table \"schema.table\": index scans: 0, pages: 123 removed, ..."
//   - Analyze: "automatic analyze of table \"schema.table\" system usage: CPU: ..."
func (a *VacuumAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if len(msg) < 18 {
		return
	}

	// Fast pre-filter: check for "uto" before expensive Index
	// "uto" is highly specific to "automatic" and eliminates ~99%+ of messages
	if strings.Index(msg, "uto") < 0 {
		return
	}

	// Search for "automatic vacuum" or "automatic analyze" anywhere
	idx := strings.Index(msg, "automatic")
	if idx < 0 {
		return
	}

	// Check what follows "automatic "
	rest := msg[idx+10:]

	if strings.HasPrefix(rest, "vacuum") {
		tableName := extractTableName(msg)
		a.vacuumCount++
		a.vacuumTableCounts[tableName]++
		if removedPages := extractRemovedPages(msg); removedPages > 0 {
			a.vacuumSpaceRecovered[tableName] += removedPages * pageSize
		}
		return
	}

	if strings.HasPrefix(rest, "analyze") {
		tableName := extractTableName(msg)
		a.analyzeCount++
		a.analyzeTableCounts[tableName]++
	}
}

// Finalize returns the aggregated vacuum metrics.
// This should be called after all log entries have been processed.
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
// Extraction helpers
// ============================================================================

// extractTableName retrieves the table name from a vacuum/analyze log message.
// PostgreSQL logs table names in quotes: "schema.table" or "public.users"
//
// Returns "UNKNOWN" if the table name cannot be extracted.
func extractTableName(logMsg string) string {
	// Find first quote
	firstQuote := strings.Index(logMsg, `"`)
	if firstQuote == -1 {
		return "UNKNOWN"
	}

	// Find second quote (closing quote)
	secondQuote := strings.Index(logMsg[firstQuote+1:], `"`)
	if secondQuote == -1 {
		return "UNKNOWN"
	}

	// Extract table name between quotes
	tableName := logMsg[firstQuote+1 : firstQuote+1+secondQuote]
	if tableName == "" {
		return "UNKNOWN"
	}

	return tableName
}

// extractRemovedPages retrieves the number of removed pages from a vacuum log message.
//
// Expected format: "pages: 123 removed, 456 remain"
// The function extracts the number after "pages: " and before " removed".
//
// Returns 0 if the removed pages count cannot be extracted.
func extractRemovedPages(logMsg string) int64 {
	// Find "pages: " marker
	idx := strings.Index(logMsg, pagesRemovedKey)
	if idx == -1 {
		return 0
	}

	// Move past "pages: " marker
	start := idx + len(pagesRemovedKey)
	if start >= len(logMsg) {
		return 0
	}

	// Find " removed" or next space
	sub := logMsg[start:]
	spaceIdx := strings.Index(sub, " ")
	if spaceIdx == -1 {
		return 0
	}

	// Parse the number
	numStr := sub[:spaceIdx]
	removedPages, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}

	return removedPages
}
