// Package parser provides types and interfaces for PostgreSQL log parsing.
package parser

import (
	"strings"
	"time"
)

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

// ExtractPID extracts the PostgreSQL process ID from a log message.
// Looks for the first occurrence of "[digits]" pattern.
//
// Examples:
//   - "[12345]: LOG: ..." → "12345"
//   - "postgre[12345]: LOG: ..." → "12345"
//   - "[12345-1]: LOG: ..." → "12345"
//
// Returns empty string if no PID found.
func ExtractPID(message string) string {
	// Find '[' followed by digits and ']'
	start := strings.IndexByte(message, '[')
	if start == -1 {
		return ""
	}

	// Look for the closing ']' and extract digits between
	end := strings.IndexByte(message[start:], ']')
	if end == -1 {
		return ""
	}

	pidCandidate := message[start+1 : start+end]

	// Verify it's all digits (or digits followed by -)
	if len(pidCandidate) == 0 {
		return ""
	}

	// Extract only the numeric part (before any dash)
	dashIdx := strings.IndexByte(pidCandidate, '-')
	if dashIdx != -1 {
		pidCandidate = pidCandidate[:dashIdx]
	}

	// Verify all digits
	for i := 0; i < len(pidCandidate); i++ {
		if pidCandidate[i] < '0' || pidCandidate[i] > '9' {
			return ""
		}
	}

	return pidCandidate
}
