// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// CSV field indices for PostgreSQL CSV log format
// PostgreSQL CSV logs have 23 fields (PostgreSQL < 13) or 26 fields (PostgreSQL >= 13).
const (
	csvFieldTimestamp     = 0  // log_time
	csvFieldUser          = 1  // user_name
	csvFieldDatabase      = 2  // database_name
	csvFieldPID           = 3  // process_id
	csvFieldClientAddr    = 4  // connection_from
	csvFieldSessionID     = 5  // session_id
	csvFieldSessionLine   = 6  // session_line_num
	csvFieldCommandTag    = 7  // command_tag
	csvFieldSessionStart  = 8  // session_start_time
	csvFieldVirtualTxID   = 9  // virtual_transaction_id
	csvFieldTxID          = 10 // transaction_id
	csvFieldErrorSeverity = 11 // error_severity
	csvFieldSQLState      = 12 // sql_state_code
	csvFieldMessage       = 13 // message
	csvFieldDetail        = 14 // detail
	csvFieldHint          = 15 // hint
	csvFieldInternalQuery = 16 // internal_query
	csvFieldInternalPos   = 17 // internal_query_pos
	csvFieldContext       = 18 // context
	csvFieldQuery         = 19 // query
	csvFieldQueryPos      = 20 // query_pos
	csvFieldLocation      = 21 // location
	csvFieldAppName       = 22 // application_name

	// PostgreSQL 13+ added fields
	csvFieldBackendType = 23 // backend_type
	csvFieldLeaderPID   = 24 // leader_pid (parallel group leader)
	csvFieldQueryID     = 25 // query_id
)

// CsvParser parses PostgreSQL logs in CSV format.
// It supports the standard PostgreSQL CSV log format:
//   - 23 fields (PostgreSQL < 13)
//   - 26 fields (PostgreSQL >= 13, adds backend_type, leader_pid, query_id)
//
// Expected CSV format (from postgresql.conf):
//
//	log_destination = 'csvlog'
//	logging_collector = on
//
// The CSV format includes fields like timestamp, user, database, message, query, etc.
// See: https://www.postgresql.org/docs/current/runtime-config-logging.html#RUNTIME-CONFIG-LOGGING-CSVLOG
type CsvParser struct{}

// Parse reads a PostgreSQL CSV format log file and streams parsed entries.
// The parser reads line-by-line (streaming) to handle large files efficiently.
//
// Each CSV record is expected to have at least 14 fields (up to message field).
// Records with fewer fields are logged as warnings and skipped.
//
// The message field is enriched with additional context if available:
//   - DETAIL lines are appended
//   - HINT lines are appended
//   - QUERY text is appended
//
// IMPORTANT: This function does NOT close the output channel. The caller is responsible
// for channel lifecycle management (as per LogParser interface contract).
func (p *CsvParser) Parse(filename string, out chan<- LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	// PostgreSQL CSV logs have 23 fields, but we'll be lenient
	reader.FieldsPerRecord = -1 // Variable number of fields (lenient mode)
	reader.TrimLeadingSpace = true

	lineNum := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[WARN] CSV parsing error at line ~%d: %v", lineNum, err)
			continue
		}

		lineNum++

		// Validate minimum fields (need at least timestamp and message)
		if len(record) < csvFieldMessage+1 {
			log.Printf("[WARN] Skipping CSV record at line %d: insufficient fields (got %d, need at least %d)",
				lineNum, len(record), csvFieldMessage+1)
			continue
		}

		// Extract and parse timestamp
		timestamp, err := parseCSVTimestamp(record[csvFieldTimestamp])
		if err != nil {
			log.Printf("[WARN] Skipping CSV record at line %d: invalid timestamp: %v", lineNum, err)
			continue
		}

		// Build complete message with context
		message := buildCSVMessage(record)

		out <- LogEntry{
			Timestamp: timestamp,
			Message:   message,
		}
	}

	return nil
}

// parseCSVTimestamp parses the timestamp from PostgreSQL CSV logs.
// Supports multiple formats:
//   - "2006-01-02 15:04:05.999 MST"  (with fractional seconds and timezone)
//   - "2006-01-02 15:04:05 MST"      (with timezone)
//   - "2006-01-02 15:04:05.999"      (with fractional seconds)
//   - "2006-01-02 15:04:05"          (basic format)
func parseCSVTimestamp(timestampStr string) (time.Time, error) {
	// Try formats in order of likelihood
	formats := []string{
		"2006-01-02 15:04:05.999 MST",    // With millis and timezone
		"2006-01-02 15:04:05.999999 MST", // With micros and timezone
		"2006-01-02 15:04:05 MST",        // With timezone
		"2006-01-02 15:04:05.999",        // With millis
		"2006-01-02 15:04:05.999999",     // With micros
		"2006-01-02 15:04:05",            // Basic
	}

	for _, format := range formats {
		if t, err := time.Parse(format, timestampStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", timestampStr)
}

// buildCSVMessage constructs a complete log message from CSV fields.
// It combines the main message with additional context fields:
//   - Error severity (LOG, ERROR, WARNING, etc.)
//   - Main message text
//   - DETAIL (if present)
//   - HINT (if present)
//   - QUERY (if present)
//
// Format: "SEVERITY: message DETAIL: detail HINT: hint QUERY: query"
func buildCSVMessage(record []string) string {
	var parts []string

	// Start with severity and message
	severity := getField(record, csvFieldErrorSeverity)
	message := getField(record, csvFieldMessage)

	if severity != "" {
		parts = append(parts, severity+":")
	}
	if message != "" {
		parts = append(parts, message)
	}

	// Add DETAIL if present
	if detail := getField(record, csvFieldDetail); detail != "" {
		parts = append(parts, "DETAIL: "+detail)
	}

	// Add HINT if present
	if hint := getField(record, csvFieldHint); hint != "" {
		parts = append(parts, "HINT: "+hint)
	}

	// Add QUERY if present
	if query := getField(record, csvFieldQuery); query != "" {
		parts = append(parts, "QUERY: "+query)
	}

	// Add CONTEXT if present (useful for debugging)
	if context := getField(record, csvFieldContext); context != "" {
		parts = append(parts, "CONTEXT: "+context)
	}

	return strings.Join(parts, " ")
}

// getField safely retrieves a field from a CSV record.
// Returns empty string if the index is out of bounds or the field is empty.
func getField(record []string, index int) string {
	if index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

// ExtractCSVFields extracts structured fields from a CSV record for filtering.
// This is useful for applying filters on database, user, app, etc.
//
// Returns a map with available fields:
//   - "db": database name
//   - "user": user name
//   - "app": application name
//   - "severity": error severity
//   - "pid": process ID
//   - "backend_type": backend type (PostgreSQL >= 13)
//   - "query_id": query ID (PostgreSQL >= 13)
//
// This function is exported for use by the filtering logic.
func ExtractCSVFields(record []string) map[string]string {
	fields := make(map[string]string)

	if db := getField(record, csvFieldDatabase); db != "" {
		fields["db"] = db
	}
	if user := getField(record, csvFieldUser); user != "" {
		fields["user"] = user
	}
	if app := getField(record, csvFieldAppName); app != "" {
		fields["app"] = app
	}
	if severity := getField(record, csvFieldErrorSeverity); severity != "" {
		fields["severity"] = severity
	}
	if pid := getField(record, csvFieldPID); pid != "" {
		fields["pid"] = pid
	}

	// PostgreSQL 13+ fields (optional)
	if backendType := getField(record, csvFieldBackendType); backendType != "" {
		fields["backend_type"] = backendType
	}
	if queryID := getField(record, csvFieldQueryID); queryID != "" {
		fields["query_id"] = queryID
	}

	return fields
}
