// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"bytes"
	"sort"
	"strings"

	"github.com/Alain-L/quellog/parser"
)

// ErrorClassSummary represents aggregated statistics for a specific PostgreSQL error class.
// Error classes are identified by the first two characters of the SQLSTATE code.
type ErrorClassSummary struct {
	// ClassCode is the two-character SQLSTATE error class code (e.g., "23", "42").
	ClassCode string

	// Description is the human-readable description of the error class.
	Description string

	// Count is the number of errors encountered in this class.
	Count int
}

// ============================================================================
// SQLSTATE error class definitions
// ============================================================================

// errorClassDescriptions maps SQLSTATE error class codes (first two characters)
// to their PostgreSQL-defined descriptions.
//
// Reference: https://www.postgresql.org/docs/current/errcodes-appendix.html
var errorClassDescriptions = map[string]string{
	// Class 00 — Successful Completion
	"00": "Successful Completion",

	// Class 01 — Warning
	"01": "Warning",

	// Class 02 — No Data
	"02": "No Data",

	// Class 03 — SQL Statement Not Yet Complete
	"03": "SQL Statement Not Yet Complete",

	// Class 08 — Connection Exception
	"08": "Connection Exception",

	// Class 09 — Triggered Action Exception
	"09": "Triggered Action Exception",

	// Class 0A — Feature Not Supported
	"0A": "Feature Not Supported",

	// Class 0B — Invalid Transaction Initiation
	"0B": "Invalid Transaction Initiation",

	// Class 0F — Locator Exception
	"0F": "Locator Exception",

	// Class 0L — Invalid Grantor
	"0L": "Invalid Grantor",

	// Class 0P — Invalid Role Specification
	"0P": "Invalid Role Specification",

	// Class 0Z — Diagnostics Exception
	"0Z": "Diagnostics Exception",

	// Class 20 — Case Not Found
	"20": "Case Not Found",

	// Class 21 — Cardinality Violation
	"21": "Cardinality Violation",

	// Class 22 — Data Exception
	"22": "Data Exception",

	// Class 23 — Integrity Constraint Violation
	"23": "Integrity Constraint Violation",

	// Class 24 — Invalid Cursor State
	"24": "Invalid Cursor State",

	// Class 25 — Invalid Transaction State
	"25": "Invalid Transaction State",

	// Class 26 — Invalid SQL Statement Name
	"26": "Invalid SQL Statement Name",

	// Class 27 — Triggered Data Change Violation
	"27": "Triggered Data Change Violation",

	// Class 28 — Invalid Authorization Specification
	"28": "Invalid Authorization Specification",

	// Class 2B — Dependent Privilege Descriptors Still Exist
	"2B": "Dependent Privilege Descriptors Still Exist",

	// Class 2D — Invalid Transaction Termination
	"2D": "Invalid Transaction Termination",

	// Class 2F — SQL Routine Exception
	"2F": "SQL Routine Exception",

	// Class 34 — Invalid Cursor Name
	"34": "Invalid Cursor Name",

	// Class 38 — External Routine Exception
	"38": "External Routine Exception",

	// Class 39 — External Routine Invocation Exception
	"39": "External Routine Invocation Exception",

	// Class 3B — Savepoint Exception
	"3B": "Savepoint Exception",

	// Class 3D — Invalid Catalog Name
	"3D": "Invalid Catalog Name",

	// Class 3F — Invalid Schema Name
	"3F": "Invalid Schema Name",

	// Class 40 — Transaction Rollback
	"40": "Transaction Rollback",

	// Class 42 — Syntax Error or Access Rule Violation
	"42": "Syntax Error or Access Rule Violation",

	// Class 44 — WITH CHECK OPTION Violation
	"44": "WITH CHECK OPTION Violation",

	// Class 53 — Insufficient Resources
	"53": "Insufficient Resources",

	// Class 54 — Program Limit Exceeded
	"54": "Program Limit Exceeded",

	// Class 55 — Object Not In Prerequisite State
	"55": "Object Not In Prerequisite State",

	// Class 57 — Operator Intervention
	"57": "Operator Intervention",

	// Class 58 — System Error (errors external to PostgreSQL)
	"58": "System Error",

	// Class F0 — Configuration File Error
	"F0": "Configuration File Error",

	// Class HV — Foreign Data Wrapper Error (SQL/MED)
	"HV": "Foreign Data Wrapper Error",

	// Class P0 — PL/pgSQL Error
	"P0": "PL/pgSQL Error",

	// Class XX — Internal Error
	"XX": "Internal Error",
}

// isSQLSTATEChar returns true if c is a valid SQLSTATE character [0-9A-Z]
func isSQLSTATEChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')
}

// isValidSQLSTATEClass checks if the first char is a valid SQLSTATE class prefix.
// Valid classes start with: 0-5, F, H, P, X (per PostgreSQL error codes appendix)
func isValidSQLSTATEClass(c byte) bool {
	return (c >= '0' && c <= '5') || c == 'F' || c == 'H' || c == 'P' || c == 'X'
}

// extractSQLSTATEFromBytes extracts a 5-character SQLSTATE code from a log message.
// It looks for patterns like:
//   - SQLSTATE = '42P01' or SQLSTATE='42P01' (log message content)
//   - ERROR: 42P01: (with log_error_verbosity = verbose)
//   - ] 42P01 or ] 42P01: (with %e in log_line_prefix)
//
// Returns the 5-character code or empty string if not found.
// This version parses from []byte without allocating a string for the entire message.
func extractSQLSTATEFromBytes(msg []byte) string {
	// Pattern 1: SQLSTATE = '42P01' or SQLSTATE='42P01'
	if idx := bytes.Index(msg, []byte("SQLSTATE")); idx != -1 {
		// Skip "SQLSTATE" and optional whitespace/equals
		pos := idx + 8 // len("SQLSTATE")
		// Skip whitespace
		for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
			pos++
		}
		// Expect '='
		if pos < len(msg) && msg[pos] == '=' {
			pos++
			// Skip whitespace
			for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
				pos++
			}
			// Expect single quote
			if pos < len(msg) && msg[pos] == '\'' {
				pos++
				// Extract 5 chars
				if pos+5 <= len(msg) {
					codeBytes := msg[pos : pos+5]
					if isValidSQLSTATEClass(codeBytes[0]) &&
						isSQLSTATEChar(codeBytes[1]) && isSQLSTATEChar(codeBytes[2]) &&
						isSQLSTATEChar(codeBytes[3]) && isSQLSTATEChar(codeBytes[4]) {
						return string(codeBytes) // Only copy the 5-char SQLSTATE
					}
				}
			}
		}
	}

	// Pattern 2: ERROR: 42P01: (verbose mode)
	if idx := bytes.Index(msg, []byte("ERROR:")); idx != -1 {
		pos := idx + 6 // len("ERROR:")
		// Skip whitespace
		for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
			pos++
		}
		// Check for 5 SQLSTATE chars followed by ':'
		if pos+6 <= len(msg) && msg[pos+5] == ':' {
			codeBytes := msg[pos : pos+5]
			if isValidSQLSTATEClass(codeBytes[0]) &&
				isSQLSTATEChar(codeBytes[1]) && isSQLSTATEChar(codeBytes[2]) &&
				isSQLSTATEChar(codeBytes[3]) && isSQLSTATEChar(codeBytes[4]) {
				return string(codeBytes) // Only copy the 5-char SQLSTATE
			}
		}
	}

	// Pattern 3: ] 42P01 or ] 42P01: (log_line_prefix with %e)
	if idx := bytes.Index(msg, []byte("] ")); idx != -1 {
		pos := idx + 2 // len("] ")
		// Check for 5 SQLSTATE chars followed by space or colon
		if pos+5 <= len(msg) {
			codeBytes := msg[pos : pos+5]
			if isValidSQLSTATEClass(codeBytes[0]) &&
				isSQLSTATEChar(codeBytes[1]) && isSQLSTATEChar(codeBytes[2]) &&
				isSQLSTATEChar(codeBytes[3]) && isSQLSTATEChar(codeBytes[4]) {
				// Verify followed by space or colon (or end of string)
				if pos+5 == len(msg) || msg[pos+5] == ' ' || msg[pos+5] == ':' {
					return string(codeBytes) // Only copy the 5-char SQLSTATE
				}
			}
		}
	}

	return ""
}

// extractSQLSTATE extracts a 5-character SQLSTATE code from a log message.
// It looks for patterns like:
//   - SQLSTATE = '42P01' or SQLSTATE='42P01' (log message content)
//   - ERROR: 42P01: (with log_error_verbosity = verbose)
//   - ] 42P01 or ] 42P01: (with %e in log_line_prefix)
//
// Returns the 5-character code or empty string if not found.
// Deprecated: Use extractSQLSTATEFromBytes for better memory efficiency.
func extractSQLSTATE(msg string) string {
	// Pattern 1: SQLSTATE = '42P01' or SQLSTATE='42P01'
	if idx := strings.Index(msg, "SQLSTATE"); idx != -1 {
		// Skip "SQLSTATE" and optional whitespace/equals
		pos := idx + 8 // len("SQLSTATE")
		// Skip whitespace
		for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
			pos++
		}
		// Expect '='
		if pos < len(msg) && msg[pos] == '=' {
			pos++
			// Skip whitespace
			for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
				pos++
			}
			// Expect single quote
			if pos < len(msg) && msg[pos] == '\'' {
				pos++
				// Extract 5 chars
				if pos+5 <= len(msg) {
					code := msg[pos : pos+5]
					if isValidSQLSTATEClass(code[0]) &&
						isSQLSTATEChar(code[1]) && isSQLSTATEChar(code[2]) &&
						isSQLSTATEChar(code[3]) && isSQLSTATEChar(code[4]) {
						return code
					}
				}
			}
		}
	}

	// Pattern 2: ERROR: 42P01: (verbose mode)
	if idx := strings.Index(msg, "ERROR:"); idx != -1 {
		pos := idx + 6 // len("ERROR:")
		// Skip whitespace
		for pos < len(msg) && (msg[pos] == ' ' || msg[pos] == '\t') {
			pos++
		}
		// Check for 5 SQLSTATE chars followed by ':'
		if pos+6 <= len(msg) && msg[pos+5] == ':' {
			code := msg[pos : pos+5]
			if isValidSQLSTATEClass(code[0]) &&
				isSQLSTATEChar(code[1]) && isSQLSTATEChar(code[2]) &&
				isSQLSTATEChar(code[3]) && isSQLSTATEChar(code[4]) {
				return code
			}
		}
	}

	// Pattern 3: ] 42P01 or ] 42P01: (log_line_prefix with %e)
	if idx := strings.Index(msg, "] "); idx != -1 {
		pos := idx + 2 // len("] ")
		// Check for 5 SQLSTATE chars followed by space or colon
		if pos+5 <= len(msg) {
			code := msg[pos : pos+5]
			if isValidSQLSTATEClass(code[0]) &&
				isSQLSTATEChar(code[1]) && isSQLSTATEChar(code[2]) &&
				isSQLSTATEChar(code[3]) && isSQLSTATEChar(code[4]) {
				// Verify followed by space or colon (or end of string)
				if pos+5 == len(msg) || msg[pos+5] == ' ' || msg[pos+5] == ':' {
					return code
				}
			}
		}
	}

	return ""
}

// ============================================================================
// Streaming error class analyzer
// ============================================================================

// ErrorClassAnalyzer processes log entries to extract and count SQLSTATE error classes.
// It maintains aggregated counts for each error class encountered in the logs.
//
// Usage:
//
//	analyzer := NewErrorClassAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	summaries := analyzer.Finalize()
type ErrorClassAnalyzer struct {
	counts map[string]int
}

// NewErrorClassAnalyzer creates a new error class analyzer.
func NewErrorClassAnalyzer() *ErrorClassAnalyzer {
	return &ErrorClassAnalyzer{
		counts: make(map[string]int, 50),
	}
}

// Process analyzes a single log entry for SQLSTATE error codes.
// It extracts the error class (first two characters of SQLSTATE) and increments its count.
//
// Example messages:
//   - 'ERROR: relation "users" does not exist SQLSTATE = '42P01"
//   - 'ERROR: duplicate key value violates unique constraint SQLSTATE='23505"
func (a *ErrorClassAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.MessageBytes

	// Quick checks before expensive operations
	// Check for error-level messages or SQLSTATE keyword
	// Include FATAL and PANIC which also have SQLSTATE codes
	hasError := bytes.Contains(msg, []byte("ERROR:")) ||
		bytes.Contains(msg, []byte("FATAL:")) ||
		bytes.Contains(msg, []byte("PANIC:"))
	hasSQLSTATE := bytes.Contains(msg, []byte("SQLSTATE"))

	if !hasError && !hasSQLSTATE {
		return
	}

	// Extract SQLSTATE code using manual parsing (faster than regex)
	sqlstate := extractSQLSTATEFromBytes(msg)
	if len(sqlstate) >= 2 {
		classCode := sqlstate[:2]
		a.counts[classCode]++
	}
}

// Finalize returns the aggregated error class summaries, sorted by count (descending).
// This should be called after all log entries have been processed.
//
// The returned summaries are sorted from most frequent to least frequent error class.
func (a *ErrorClassAnalyzer) Finalize() []ErrorClassSummary {
	summaries := make([]ErrorClassSummary, 0, len(a.counts))

	// Build summaries
	for classCode, count := range a.counts {
		description := errorClassDescriptions[classCode]
		if description == "" {
			description = "Unknown Error Class"
		}

		summaries = append(summaries, ErrorClassSummary{
			ClassCode:   classCode,
			Description: description,
			Count:       count,
		})
	}

	// Sort by count (descending), then by class code (ascending) for ties
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Count != summaries[j].Count {
			return summaries[i].Count > summaries[j].Count
		}
		return summaries[i].ClassCode < summaries[j].ClassCode
	})

	return summaries
}
