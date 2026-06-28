// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"fmt"
	"io"
	"log/slog"
	"os"
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

	// Large plain CSV takes the parallel record-aligned segment path: CSV
	// parsing is otherwise the wall-clock bottleneck (~95% of the wall on a
	// 1.2 GB file). parseReader strips a leading BOM per-segment, so segment 0
	// needs no special handling.
	if st, err := f.Stat(); err == nil && st.Size() >= csvParallelMinSize {
		if workers := parallelWorkers(); workers >= 2 {
			return p.parseParallel(f, st.Size(), workers, out)
		}
	}
	return p.parseReader(WithProgress(f), out)
}

// parseReader processes CSV records from any io.Reader.
//
// Custom zero-copy scanner replaces encoding/csv on the hot path: it yields
// field views into its own 1 MB read buffer instead of a per-field heap string,
// cutting CSV-path allocations roughly in half (profiled). buildCSVMessage
// copies what it keeps, so the views never outlive the next Next() call.
func (p *CsvParser) parseReader(r io.Reader, out chan<- []LogEntry) error {
	return p.parseScanner(newCSVScanner(skipBOM(r)), out)
}

// parseScanner drives an already-bound scanner. Split out so the parallel path
// can hand a worker-reused scanner (and CsvParser) across segments instead of
// allocating fresh per segment.
func (p *CsvParser) parseScanner(sc *csvScanner, out chan<- []LogEntry) error {
	bs := NewBatchSender(out)
	defer bs.Flush()

	lineNum := 0
	for {
		ok, err := sc.Next()
		if err != nil {
			slog.Warn("CSV read error", "line", lineNum, "err", err)
			break
		}
		if !ok {
			break
		}
		record := sc.record()

		lineNum++

		// Validate minimum fields (need at least timestamp and message)
		if record.len() < csvFieldMessage+1 {
			slog.Warn("skipping CSV record: insufficient fields",
				"line", lineNum, "got", record.len(), "need", csvFieldMessage+1)
			continue
		}

		// Extract and parse timestamp (field 0 is never quoted/escaped)
		timestamp, err := p.parseCSVTimestamp(record.field(csvFieldTimestamp).raw)
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
// Field values arrive as csvField views into the scanner buffer; appendField
// copies them into msgBuf (collapsing "" escapes on the way), and the final
// string(b) is the one owned allocation per record. The scanner already trims
// leading/trailing spaces, so emptiness is a plain raw=="" check.
func (p *CsvParser) buildCSVMessage(record csvRecord) string {
	if cap(p.msgBuf) < 512 {
		p.msgBuf = make([]byte, 0, 512)
	} else {
		p.msgBuf = p.msgBuf[:0]
	}
	b := p.msgBuf

	// Add PID if present
	if pid := record.field(csvFieldPID); pid.raw != "" {
		b = append(b, '[')
		b = appendField(b, pid)
		b = append(b, ']', ':', ' ')
	}

	// Add user/db/app context (format: "user=X,db=Y,app=Z")
	hasUserDbApp := false
	if user := record.field(csvFieldUser); user.raw != "" {
		b = append(b, "user="...)
		b = appendField(b, user)
		hasUserDbApp = true
	}
	if database := record.field(csvFieldDatabase); database.raw != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "db="...)
		b = appendField(b, database)
		hasUserDbApp = true
	}
	if app := record.field(csvFieldAppName); app.raw != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "app="...)
		b = appendField(b, app)
		hasUserDbApp = true
	}
	if clientAddr := record.field(csvFieldClientAddr); clientAddr.raw != "" {
		if hasUserDbApp {
			b = append(b, ',')
		}
		b = append(b, "client="...)
		b = appendField(b, clientAddr)
		hasUserDbApp = true
	}
	if hasUserDbApp {
		b = append(b, ' ')
	}

	// Add severity and main message
	severity := record.field(csvFieldErrorSeverity)
	message := record.field(csvFieldMessage)

	if severity.raw != "" {
		b = appendField(b, severity)
		b = append(b, ':', ' ')
	}
	if message.raw != "" {
		b = appendField(b, message)
	}

	// Add DETAIL if present
	if detail := record.field(csvFieldDetail); detail.raw != "" {
		b = append(b, " DETAIL: "...)
		b = appendField(b, detail)
	}

	// Add HINT if present
	if hint := record.field(csvFieldHint); hint.raw != "" {
		b = append(b, " HINT: "...)
		b = appendField(b, hint)
	}

	// Add QUERY if present
	if query := record.field(csvFieldQuery); query.raw != "" {
		b = append(b, " QUERY: "...)
		b = appendField(b, query)
	}

	// Add CONTEXT if present (useful for debugging)
	if context := record.field(csvFieldContext); context.raw != "" {
		b = append(b, " CONTEXT: "...)
		b = appendField(b, context)
	}

	// Add SQLSTATE if present (for error classification)
	// Skip 00000 (successful completion) as it's not an error
	if sqlstate := record.field(csvFieldSQLState); sqlstate.raw != "" && sqlstate.raw != "00000" {
		b = append(b, " SQLSTATE = '"...)
		b = appendField(b, sqlstate)
		b = append(b, '\'')
	}

	p.msgBuf = b
	return string(b)
}
