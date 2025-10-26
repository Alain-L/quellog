// Package parser provides types and interfaces for PostgreSQL log parsing.
package parser

import "time"

// LogEntry represents a single parsed PostgreSQL log entry.
// Each entry consists of a timestamp and the complete log message,
// including any continuation lines (DETAIL, HINT, STATEMENT, etc.).
//
// Example stderr format entry:
//
//	2025-01-01 12:00:00 CET LOG: database system is ready to accept connections
//
// Example CSV format entry:
//
//	2025-01-01 12:00:00.123 CET,,,12345,,LOG,00000,"database system is ready",,0
//
// Example JSON format entry:
//
//	{"timestamp":"2025-01-01T12:00:00Z","message":"database system is ready"}
type LogEntry struct {
	// Timestamp is the time when the log entry was created.
	// For formats without a year (e.g., syslog), the current year is assumed.
	Timestamp time.Time

	// Message is the complete log message text, including severity level.
	// Multi-line entries (continuation lines) are concatenated with spaces.
	//
	// Examples:
	//   "LOG: database system is ready"
	//   "ERROR: relation \"users\" does not exist HINT: Did you mean \"user\"?"
	Message string
}

// LogParser defines the interface that all format-specific parsers must implement.
// Parsers read a log file and stream parsed LogEntry records through a channel,
// enabling efficient processing of large log files without loading everything into memory.
//
// Implementations:
//   - StderrParser: Parses stderr/syslog format logs
//   - CsvParser: Parses PostgreSQL CSV format logs
//   - JsonParser: Parses JSON format logs
//
// Usage example:
//
//	entries := make(chan LogEntry, 1000)
//	parser := &StderrParser{}
//	go func() {
//	    if err := parser.Parse("postgresql.log", entries); err != nil {
//	        log.Fatal(err)
//	    }
//	    close(entries)
//	}()
//	for entry := range entries {
//	    // Process entry
//	}
type LogParser interface {
	// Parse reads a PostgreSQL log file and sends parsed entries to the output channel.
	// The parser is responsible for:
	//   - Opening and reading the file
	//   - Detecting and handling multi-line entries
	//   - Parsing timestamps and messages
	//   - Handling format-specific quirks
	//
	// The output channel should NOT be closed by the parser; the caller is responsible
	// for channel lifecycle management.
	//
	// Returns an error if:
	//   - The file cannot be opened or read
	//   - A critical parsing error occurs
	//
	// Note: Individual malformed lines may be logged as warnings but should not
	// cause the entire parsing operation to fail.
	Parse(filename string, out chan<- LogEntry) error
}
