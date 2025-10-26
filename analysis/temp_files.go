// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"dalibo/quellog/parser"
	"strconv"
	"strings"
	"time"
)

// TempFileMetrics aggregates statistics about PostgreSQL temporary file usage.
// Temporary files are created when queries need more memory than work_mem allows.
type TempFileMetrics struct {
	// Count is the number of temporary file creation events.
	Count int

	// TotalSize is the cumulative size of all temporary files in bytes.
	TotalSize int64

	// Events contains individual temporary file creation events.
	// Useful for timeline analysis and identifying memory pressure periods.
	Events []TempFileEvent
}

// TempFileEvent represents a single temporary file creation event.
type TempFileEvent struct {
	// Timestamp is when the temporary file was created.
	Timestamp time.Time

	// Size is the file size in bytes.
	Size float64
}

// ============================================================================
// Temporary file log patterns
// ============================================================================

// Temporary file log message patterns.
// PostgreSQL logs temporary file creation in these formats:
//   - "temporary file: path \"base/pgsql_tmp/pgsql_tmp12345.0\", size 1048576"
//   - "temporary file: path base/pgsql_tmp/pgsql_tmp12345.0, size 1048576"
const (
	tempFileMarker = "temporary file"
	tempFilePath   = "path"
	tempFileSize   = "size"
)

// ============================================================================
// Streaming temporary file analyzer
// ============================================================================

// TempFileAnalyzer processes log entries to track temporary file usage.
// Temporary files indicate queries exceeding work_mem and spilling to disk.
//
// Usage:
//
//	analyzer := NewTempFileAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type TempFileAnalyzer struct {
	count     int
	totalSize int64
	events    []TempFileEvent
}

// NewTempFileAnalyzer creates a new temporary file analyzer.
func NewTempFileAnalyzer() *TempFileAnalyzer {
	return &TempFileAnalyzer{
		events: make([]TempFileEvent, 0, 1000),
	}
}

// Process analyzes a single log entry for temporary file creation events.
//
// Expected log format:
//
//	LOG: temporary file: path "base/pgsql_tmp/pgsql_tmp12345.0", size 1048576
//
// The size is in bytes and appears after the "size" keyword.
func (a *TempFileAnalyzer) Process(entry *parser.LogEntry) {
	// Quick check: skip non-temp-file entries
	if !strings.Contains(entry.Message, tempFileMarker) {
		return
	}

	a.count++

	// Extract size from message
	size := extractTempFileSize(entry.Message)
	if size > 0 {
		a.totalSize += size
		a.events = append(a.events, TempFileEvent{
			Timestamp: entry.Timestamp,
			Size:      float64(size),
		})
	}
}

// Finalize returns the aggregated temporary file metrics.
// This should be called after all log entries have been processed.
func (a *TempFileAnalyzer) Finalize() TempFileMetrics {
	return TempFileMetrics{
		Count:     a.count,
		TotalSize: a.totalSize,
		Events:    a.events,
	}
}

// ============================================================================
// Size extraction
// ============================================================================

// extractTempFileSize parses the file size from a temporary file log message.
//
// PostgreSQL log format:
//
//	"temporary file: path \"...\", size 1048576"
//
// The function looks for "size" keyword followed by a number.
// Returns 0 if size cannot be parsed.
func extractTempFileSize(message string) int64 {
	// Find "size" keyword
	sizeIdx := strings.Index(message, tempFileSize)
	if sizeIdx == -1 {
		return 0
	}

	// Move past "size" keyword (4 characters)
	start := sizeIdx + len(tempFileSize)

	// Skip whitespace
	for start < len(message) && (message[start] == ' ' || message[start] == ':') {
		start++
	}

	if start >= len(message) {
		return 0
	}

	// Find end of number (next non-digit character)
	end := start
	for end < len(message) && message[end] >= '0' && message[end] <= '9' {
		end++
	}

	if end == start {
		return 0
	}

	// Parse the size
	sizeStr := message[start:end]
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0
	}

	return size
}

// ============================================================================
// Legacy API (for backward compatibility)
// ============================================================================

// CalculateTemporaryFileMetrics analyzes log entries for temporary file usage.
//
// Deprecated: This function loads all entries into memory. Use TempFileAnalyzer
// with streaming for better performance and memory efficiency.
//
// This function is maintained for backward compatibility and will be removed
// in a future version.
func CalculateTemporaryFileMetrics(entries *[]parser.LogEntry) TempFileMetrics {
	analyzer := NewTempFileAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
