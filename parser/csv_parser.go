// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
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
type CsvParser struct {
	cachedFormat string // last successful timestamp format (per-instance, no race)
	msgBuf       []byte // reusable scratch for buildCSVMessage; survives across records
}

// Parse reads a PostgreSQL CSV format log file and streams parsed entries.
// The parser reads line-by-line (streaming) to handle large files efficiently.
//
// Each CSV record is expected to have at least 14 fields (up to message field).
// Records with fewer fields are logged as warnings and skipped.
//
// The message field is enriched with additional context if available:
//   - User, database, application metadata (formatted as in stderr logs)
//   - DETAIL lines are appended
//   - HINT lines are appended
//   - QUERY text is appended
//
// IMPORTANT: This function does NOT close the output channel. The caller is responsible
// for channel lifecycle management (as per LogParser interface contract).
func (p *CsvParser) Parse(filename string, out chan<- []LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer f.Close()

	return p.parseReader(WithProgress(f), out)
}

// parseReader processes CSV records from any io.Reader.
func (p *CsvParser) parseReader(r io.Reader, out chan<- []LogEntry) error {
	bs := NewBatchSender(out)
	defer bs.Flush()

	// csv.NewReader's internal bufio defaults to 4 KB reads — ~300k
	// read(2) syscalls on a 1 GB file, which profiling showed as 4s of
	// rawsyscall time (the CSV hot path's single biggest cost). A 1 MB
	// outer buffer brings the syscall count down by ~256×; the other
	// parsers already size their scanners in megabytes.
	reader := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	// PostgreSQL CSV logs have 23 fields, but we'll be lenient
	reader.FieldsPerRecord = -1 // Variable number of fields (lenient mode)
	reader.TrimLeadingSpace = true
	// ReuseRecord lets csv.Reader reuse the []string slice across calls.
	// It does not eliminate per-field string allocations but it does
	// drop the slice header churn — measurable on >100 MB inputs.
	reader.ReuseRecord = true

	lineNum := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Warn("CSV parsing error", "line", lineNum, "err", err)
			continue
		}

		lineNum++

		// Validate minimum fields (need at least timestamp and message)
		if len(record) < csvFieldMessage+1 {
			slog.Warn("skipping CSV record: insufficient fields",
				"line", lineNum, "got", len(record), "need", csvFieldMessage+1)
			continue
		}

		// Extract and parse timestamp
		timestamp, err := p.parseCSVTimestamp(record[csvFieldTimestamp])
		if err != nil {
			slog.Warn("skipping CSV record: invalid timestamp", "line", lineNum, "err", err)
			continue
		}

		// Build complete message with context
		message := p.buildCSVMessage(record)

		bs.Send(NewLogEntry(timestamp, message, false))
	}

	return nil
}

// CSV timestamp formats in order of likelihood
var csvTimestampFormats = []string{
	"2006-01-02 15:04:05.999 MST",    // With millis and timezone
	"2006-01-02 15:04:05.999999 MST", // With micros and timezone
	"2006-01-02 15:04:05 MST",        // With timezone
	"2006-01-02 15:04:05.999",        // With millis
	"2006-01-02 15:04:05.999999",     // With micros
	"2006-01-02 15:04:05",            // Basic
}

// parseCSVTimestamp parses the timestamp from PostgreSQL CSV logs.
// The last successful format is cached per parser instance to avoid
// re-trying all formats on every line (safe for concurrent use since
// each goroutine gets its own CsvParser).
func (p *CsvParser) parseCSVTimestamp(timestampStr string) (time.Time, error) {
	// Fast path: try cached format first
	if p.cachedFormat != "" {
		if t, err := parseTime(p.cachedFormat, timestampStr); err == nil {
			return t, nil
		}
	}

	// Slow path: try all formats
	for _, format := range csvTimestampFormats {
		if t, err := parseTime(format, timestampStr); err == nil {
			p.cachedFormat = format
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", timestampStr)
}

// buildCSVMessage constructs a complete log message from CSV fields.
// It combines the main message with additional context fields to match the stderr format:
//   - PID (if present)
//   - User, database, application context (formatted as "user=X,db=Y,app=Z")
//   - Error severity (LOG, ERROR, WARNING, etc.)
//   - Main message text
//   - DETAIL (if present)
//   - HINT (if present)
//   - QUERY (if present)
//   - CONTEXT (if present)
//
// Format: "[pid]: user=X,db=Y,app=Z SEVERITY: message DETAIL: detail HINT: hint QUERY: query"
//
// Writes into the receiver's reusable scratch buffer to avoid the
// per-record strings.Builder allocations that dominated CSV parsing
// on multi-hundred-MB inputs (the 512-byte initial buffer + grow
// cycles were leaking under tinygo gc=leaking).
func (p *CsvParser) buildCSVMessage(record []string) string {
	if cap(p.msgBuf) < 512 {
		p.msgBuf = make([]byte, 0, 512)
	} else {
		p.msgBuf = p.msgBuf[:0]
	}
	b := p.msgBuf

	// Add PID if present
	if pid := getField(record, csvFieldPID); pid != "" {
		b = append(b, '[')
		b = append(b, pid...)
		b = append(b, ']', ':', ' ')
	}

	// Add user/db/app context (format: "user=X,db=Y,app=Z")
	hasUserDbApp := false
	if user := getField(record, csvFieldUser); user != "" {
		b = append(b, "user="...)
		b = append(b, user...)
		hasUserDbApp = true
	}
	if database := getField(record, csvFieldDatabase); database != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "db="...)
		b = append(b, database...)
		hasUserDbApp = true
	}
	if app := getField(record, csvFieldAppName); app != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "app="...)
		b = append(b, app...)
		hasUserDbApp = true
	}
	if clientAddr := getField(record, csvFieldClientAddr); clientAddr != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "client="...)
		b = append(b, clientAddr...)
		hasUserDbApp = true
	}
	if hasUserDbApp {
		b = append(b, ' ')
	}

	// Add severity and main message
	severity := getField(record, csvFieldErrorSeverity)
	message := getField(record, csvFieldMessage)

	if severity != "" {
		b = append(b, severity...)
		b = append(b, ':', ' ')
	}
	if message != "" {
		b = append(b, message...)
	}

	// Add DETAIL if present
	if detail := getField(record, csvFieldDetail); detail != "" {
		b = append(b, " DETAIL: "...)
		b = append(b, detail...)
	}

	// Add HINT if present
	if hint := getField(record, csvFieldHint); hint != "" {
		b = append(b, " HINT: "...)
		b = append(b, hint...)
	}

	// Add QUERY if present
	if query := getField(record, csvFieldQuery); query != "" {
		b = append(b, " QUERY: "...)
		b = append(b, query...)
	}

	// Add CONTEXT if present (useful for debugging)
	if context := getField(record, csvFieldContext); context != "" {
		b = append(b, " CONTEXT: "...)
		b = append(b, context...)
	}

	// Add SQLSTATE if present (for error classification)
	// Skip 00000 (successful completion) as it's not an error
	if sqlstate := getField(record, csvFieldSQLState); sqlstate != "" && sqlstate != "00000" {
		b = append(b, " SQLSTATE = '"...)
		b = append(b, sqlstate...)
		b = append(b, '\'')
	}

	p.msgBuf = b
	return string(b)
}

// getField safely retrieves a field from a CSV record.
// Returns empty string if the index is out of bounds or the field is empty.
// Note: CSV reader already trims leading space, we only trim trailing for safety.
func getField(record []string, index int) string {
	if index >= len(record) {
		return ""
	}
	s := record[index]
	// Fast path: most fields don't have trailing spaces
	if len(s) == 0 || s[len(s)-1] != ' ' {
		return s
	}
	return strings.TrimRight(s, " ")
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
