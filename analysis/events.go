// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"

	"github.com/Alain-L/quellog/parser"
)

// EventSummary represents aggregated statistics for a specific PostgreSQL log event type.
// Event types correspond to PostgreSQL severity levels (ERROR, WARNING, LOG, etc.).
type EventSummary struct {
	// Type is the event/severity level (e.g., "ERROR", "WARNING", "LOG").
	Type string

	// Count is the number of occurrences of this event type.
	Count int

	// Percentage is the proportion of this event type relative to all counted events.
	Percentage float64
}

// EventStat represents statistics for a unique event message pattern.
type EventStat struct {
	// Message is the normalized version of the event message.
	Message string

	// Count is the number of times this event was encountered.
	Count int

	// Severity is the log level of the event (ERROR, WARNING, etc.).
	Severity string

	// Example is a raw example of the message.
	Example string

	// SQLStateClass is the 2-character SQLSTATE class code (e.g., "23", "42").
	// Empty if not applicable or not found.
	SQLStateClass string
}

// ============================================================================
// Event type definitions
// ============================================================================

// PredefinedEventTypes defines the PostgreSQL log severity levels to track.
// These correspond to the standard PostgreSQL message severity levels.
//
// Reference: https://www.postgresql.org/docs/current/runtime-config-logging.html#RUNTIME-CONFIG-SEVERITY-LEVELS
//
// Severity levels (highest to lowest):
//
//	PANIC   - Severe error causing database shutdown
//	FATAL   - Session-terminating error
//	ERROR   - Error that aborted the current command
//	WARNING - Warning message
//	NOTICE  - Notice message
//	LOG     - Informational message (for administrators)
//	INFO    - Informational message (for users)
//	DEBUG   - Debug information (5 levels: DEBUG1 to DEBUG5)
var PredefinedEventTypes = []string{
	"PANIC",
	"FATAL",
	"ERROR",
	"WARNING",
	"NOTICE", // Added - was missing in original
	"LOG",
	"INFO",
	"DEBUG",
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

// GetErrorClassDescription returns the description for a given SQLSTATE class code.
func GetErrorClassDescription(classCode string) string {
	if desc, ok := errorClassDescriptions[classCode]; ok {
		return desc
	}
	return "Unknown Error Class"
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

// extractSQLSTATE extracts a 5-character SQLSTATE code from a log message.
// It looks for patterns like:
//   - SQLSTATE = '42P01' or SQLSTATE='42P01' (log message content)
//   - ERROR: 42P01: (with log_error_verbosity = verbose)
//   - ] 42P01 or ] 42P01: (with %e in log_line_prefix)
//
// Returns the 5-character code or empty string if not found.
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
// Streaming event analyzer
// ============================================================================

// EventAnalyzer processes log entries to count occurrences of different event types.
// It tracks PostgreSQL severity levels and calculates their distribution.
//
// Usage:
//
//	analyzer := NewEventAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	summaries, stats := analyzer.Finalize()
type EventAnalyzer struct {
	counts map[string]int
	total  int

	// stats tracks unique event signatures
	stats map[string]*EventStat
}

// NewEventAnalyzer creates a new event analyzer.
func NewEventAnalyzer() *EventAnalyzer {
	return &EventAnalyzer{
		counts: make(map[string]int, len(PredefinedEventTypes)),
		stats:  make(map[string]*EventStat),
	}
}

// Process analyzes a single log entry to identify and count its event type.
func (a *EventAnalyzer) Process(entry *parser.LogEntry) {
	if entry.IsContinuation {
		return
	}

	msg := entry.Message
	if len(msg) < 3 {
		return
	}

	severity := ""

	// Fast path detection
	firstChar := msg[0]
	switch firstChar {
	case 'L':
		if len(msg) >= 3 && msg[0:3] == "LOG" {
			severity = "LOG"
		}
	case 'E':
		if len(msg) >= 5 && msg[0:5] == "ERROR" {
			severity = "ERROR"
		}
	case 'W':
		if len(msg) >= 7 && msg[0:7] == "WARNING" {
			severity = "WARNING"
		}
	case 'F':
		if len(msg) >= 5 && msg[0:5] == "FATAL" {
			severity = "FATAL"
		}
	case 'I':
		if len(msg) >= 4 && msg[0:4] == "INFO" {
			severity = "INFO"
		}
	case 'N':
		if len(msg) >= 6 && msg[0:6] == "NOTICE" {
			severity = "NOTICE"
		}
	case 'D':
		if len(msg) >= 5 && msg[0:5] == "DEBUG" {
			severity = "DEBUG"
		}
	case 'P':
		if len(msg) >= 5 && msg[0:5] == "PANIC" {
			severity = "PANIC"
		}
	}

	// Fallback detection
	if severity == "" {
		for _, eventType := range PredefinedEventTypes {
			if strings.Contains(msg, eventType+":") {
				severity = eventType
				break
			}
		}
	}

	if severity != "" {
		a.counts[severity]++
		a.total++

		// Track message pattern for non-LOG messages (errors, warnings, fatals)
		if severity != "LOG" && severity != "INFO" && severity != "DEBUG" && severity != "NOTICE" {
			pattern := NormalizeEvent(msg)
			if pattern != "" {
				if stat, ok := a.stats[pattern]; ok {
					stat.Count++
				} else if len(a.stats) < 1000 {
					// Extract SQLSTATE class if present
					sqlStateClass := ""
					if code := extractSQLSTATE(msg); len(code) >= 2 {
						sqlStateClass = code[:2]
					}

					// Limit unique patterns to prevent memory explosion
					a.stats[pattern] = &EventStat{
						Message:       pattern,
						Count:         1,
						Severity:      severity,
						Example:       msg,
						SQLStateClass: sqlStateClass,
					}
				}
			}
		}
	}
}

// Finalize returns the aggregated summaries and top event signatures.
func (a *EventAnalyzer) Finalize() ([]EventSummary, []EventStat) {
	summaries := make([]EventSummary, 0, len(PredefinedEventTypes))

	for _, eventType := range PredefinedEventTypes {
		count := a.counts[eventType]
		if count == 0 {
			continue
		}

		percentage := 0.0
		if a.total > 0 {
			percentage = (float64(count) / float64(a.total)) * 100
		}

		summaries = append(summaries, EventSummary{
			Type:       eventType,
			Count:      count,
			Percentage: percentage,
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Count > summaries[j].Count
	})

	// Finalize top event stats
	eventStats := make([]EventStat, 0, len(a.stats))
	for _, stat := range a.stats {
		eventStats = append(eventStats, *stat)
	}

	sort.Slice(eventStats, func(i, j int) bool {
		if eventStats[i].Count != eventStats[j].Count {
			return eventStats[i].Count > eventStats[j].Count
		}
		return eventStats[i].Message < eventStats[j].Message
	})

	// Limit to top 100 for the report
	if len(eventStats) > 100 {
		eventStats = eventStats[:100]
	}

	return summaries, eventStats
}
