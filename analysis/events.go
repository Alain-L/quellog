// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"

	"github.com/Alain-L/quellog/parser"
)

// EventSummary aggregates one PostgreSQL severity level (ERROR, WARNING, LOG, ...).
type EventSummary struct {
	Type       string  // severity level
	Count      int     // occurrences of this type
	Percentage float64 // share of all counted events
}

// EventStat holds statistics for a unique normalized message pattern.
type EventStat struct {
	// ID is a stable short handle of the form <sev>-<4-char-hash>
	// (e.g. wa-aBc1, er-Qr5p). Generated from severity + normalized
	// message via GenerateEventID. Used as the CLI selector for
	// `--event-detail` and as the click-target id in the HTML modal.
	ID            string
	Message       string // normalized message
	Count         int
	Severity      string
	Example       string // raw example
	SQLStateClass string // 2-char SQLSTATE class (e.g. "23", "42"), empty if N/A
	// Timestamps captures every occurrence as Unix milliseconds. Used by
	// the HTML report's per-event modal to render an occurrences-over-time
	// sparkline. 8 B/event packed; on logs with the analyzer's 1000-pattern
	// cap and typical occurrence skew this stays under 10 MB on the largest
	// corpora we benchmark.
	Timestamps []int64
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

// errorClassDescriptions maps SQLSTATE error class codes (first two
// characters) to their PostgreSQL descriptions. Reference:
// https://www.postgresql.org/docs/current/errcodes-appendix.html
var errorClassDescriptions = map[string]string{
	"00": "Successful Completion",
	"01": "Warning",
	"02": "No Data",
	"03": "SQL Statement Not Yet Complete",
	"08": "Connection Exception",
	"09": "Triggered Action Exception",
	"0A": "Feature Not Supported",
	"0B": "Invalid Transaction Initiation",
	"0F": "Locator Exception",
	"0L": "Invalid Grantor",
	"0P": "Invalid Role Specification",
	"0Z": "Diagnostics Exception",
	"20": "Case Not Found",
	"21": "Cardinality Violation",
	"22": "Data Exception",
	"23": "Integrity Constraint Violation",
	"24": "Invalid Cursor State",
	"25": "Invalid Transaction State",
	"26": "Invalid SQL Statement Name",
	"27": "Triggered Data Change Violation",
	"28": "Invalid Authorization Specification",
	"2B": "Dependent Privilege Descriptors Still Exist",
	"2D": "Invalid Transaction Termination",
	"2F": "SQL Routine Exception",
	"34": "Invalid Cursor Name",
	"38": "External Routine Exception",
	"39": "External Routine Invocation Exception",
	"3B": "Savepoint Exception",
	"3D": "Invalid Catalog Name",
	"3F": "Invalid Schema Name",
	"40": "Transaction Rollback",
	"42": "Syntax Error or Access Rule Violation",
	"44": "WITH CHECK OPTION Violation",
	"53": "Insufficient Resources",
	"54": "Program Limit Exceeded",
	"55": "Object Not In Prerequisite State",
	"57": "Operator Intervention",
	"58": "System Error", // external to PostgreSQL
	"F0": "Configuration File Error",
	"HV": "Foreign Data Wrapper Error", // SQL/MED
	"P0": "PL/pgSQL Error",
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
				ts := entry.Timestamp.UnixMilli()
				if stat, ok := a.stats[pattern]; ok {
					stat.Count++
					stat.Timestamps = append(stat.Timestamps, ts)
				} else if len(a.stats) < 1000 {
					// Extract SQLSTATE class if present
					sqlStateClass := ""
					if code := extractSQLSTATE(msg); len(code) >= 2 {
						sqlStateClass = code[:2]
					}

					// Limit unique patterns to prevent memory explosion
					a.stats[pattern] = &EventStat{
						ID:            GenerateEventID(severity, pattern),
						Message:       pattern,
						Count:         1,
						Severity:      severity,
						Example:       msg,
						SQLStateClass: sqlStateClass,
						Timestamps:    []int64{ts},
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
