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

	// TriggeringQueries lists the normalized SQL queries that produced
	// this event pattern, with their occurrence count. Filled from the
	// STATEMENT continuation of each captured occurrence at flush time
	// (normalized + queryID identical to sql_performance.queries so the
	// HTML modal can hot-link from one section to the other). Sorted
	// desc by count at Finalize. Cap at TriggeringQueriesCap.
	TriggeringQueries []TriggeringQuery
}

// TriggeringQuery is the cross-link between an event pattern and the
// normalized SQL queries that fired it. Each entry is the canonical
// form of one distinct STATEMENT continuation observed for the parent
// EventStat, with its own queryID (shared with sql_performance) and
// the count of times it triggered the pattern.
type TriggeringQuery struct {
	ID              string
	NormalizedQuery string
	Count           int
}

// TriggeringQueriesCap is the maximum number of distinct normalized
// queries tracked per EventStat. Bounds the memory at roughly
// ~30 × ~200 B × 1000 patterns = 6 MB worst case while still keeping
// the full Pareto distribution on every real-world error we have seen
// (B.log's "relation does not exist" tops out at 11 distinct queries).
const TriggeringQueriesCap = 30

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

	// pendingByPID buffers a half-built sample while we wait for the
	// DETAIL/HINT/CONTEXT/STATEMENT continuation lines that PostgreSQL
	// emits right after an error on the same backend. Entries are
	// evicted on the next non-continuation arriving on the same PID,
	// and any remaining ones are flushed in Finalize().
	pendingByPID map[string]*pendingEvent
}

// pendingEvent is the in-flight state for an ERROR/FATAL/WARNING/PANIC
// while we wait for its STATEMENT continuation. statKey points back to
// the matching EventStat in EventAnalyzer.stats — flushing the entry
// folds the normalized STATEMENT into that EventStat's TriggeringQueries.
// DETAIL/HINT/CONTEXT are not retained: the aggregated triggering-queries
// view is what downstream code consumes, and keeping the raw textual
// continuations adds memory without changing what a DBA can act on.
type pendingEvent struct {
	statKey   string
	statement string
}

// NewEventAnalyzer creates a new event analyzer.
func NewEventAnalyzer() *EventAnalyzer {
	return &EventAnalyzer{
		counts:       make(map[string]int, len(PredefinedEventTypes)),
		stats:        make(map[string]*EventStat),
		pendingByPID: make(map[string]*pendingEvent),
	}
}

// containsContinuationMarker reports whether marker appears in msg as
// a free-standing severity token — i.e. immediately preceded by a
// space or at the start of the message. This rejects the false
// positive where "STATEMENT:" appears inside a normalized error
// pattern instead of as the continuation severity.
func containsContinuationMarker(msg, marker string) bool {
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return false
	}
	if idx == 0 {
		return true
	}
	return msg[idx-1] == ' '
}

// extractAfter returns the trimmed substring of msg starting right
// after the first occurrence of marker. Used to peel the continuation
// payload out of "[pid]: user=… DETAIL:  Failing row …" style lines.
func extractAfter(msg, marker string) string {
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(msg[idx+len(marker):])
}

// flushPending folds the buffered STATEMENT into the matching
// EventStat's TriggeringQueries — the aggregate Pareto view of "which
// queries caused this event". Bound by TriggeringQueriesCap.
func (a *EventAnalyzer) flushPending(pe *pendingEvent) {
	if pe == nil || pe.statKey == "" || pe.statement == "" {
		return
	}
	stat, ok := a.stats[pe.statKey]
	if !ok {
		return
	}
	recordTriggeringQuery(stat, pe.statement, pe.statKey)
}

// recordTriggeringQuery normalises a raw STATEMENT, builds its queryID
// (same hash used by sql_performance so the two sections cross-link by
// id), and increments the count on the matching entry of
// stat.TriggeringQueries. Insertion is capped at TriggeringQueriesCap;
// when the cap is reached, only already-known queries keep counting and
// new ones are dropped — the dominant queries (i.e. the ones we care
// about) are already in the list by definition.
func recordTriggeringQuery(stat *EventStat, statement, statKey string) {
	raw := normalizeWhitespace(strings.TrimSpace(statement))
	if raw == "" {
		return
	}
	normalized := normalizeQuery(raw)
	if normalized == "" {
		return
	}
	id, _ := GenerateQueryID(raw, normalized)
	for i := range stat.TriggeringQueries {
		if stat.TriggeringQueries[i].ID == id {
			stat.TriggeringQueries[i].Count++
			return
		}
	}
	if len(stat.TriggeringQueries) >= TriggeringQueriesCap {
		return
	}
	stat.TriggeringQueries = append(stat.TriggeringQueries, TriggeringQuery{
		ID:              id,
		NormalizedQuery: normalized,
		Count:           1,
	})
}

// Process analyzes a single log entry to identify and count its event type.
//
// Continuation entries (DETAIL/HINT/CONTEXT/STATEMENT) are routed to
// the per-PID pendingByPID buffer so they can be stitched back onto
// the EventStat of the error they belong to. Non-continuation entries
// flush any buffered continuation for the same PID first — the arrival
// of a new main entry means no more continuations will follow.
func (a *EventAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if entry.IsContinuation {
		pid := entry.PID
		if pid == "" {
			return
		}
		pe := a.pendingByPID[pid]
		if pe == nil {
			return
		}
		// PostgreSQL emits the continuation severity embedded in the
		// log-line prefix, e.g.
		//   "[12345]: user=app,db=appdb STATEMENT:  INSERT INTO …"
		// so we look up the marker with strings.Index rather than
		// HasPrefix. Only STATEMENT is retained — that is what feeds
		// the per-event TriggeringQueries Pareto view. DETAIL / HINT /
		// CONTEXT are intentionally ignored: the aggregated view is
		// what downstream code consumes.
		if containsContinuationMarker(msg, "STATEMENT:") {
			pe.statement = extractAfter(msg, "STATEMENT:")
		}
		return
	}

	if len(msg) < 3 {
		return
	}

	// Non-continuation: any pending event on this PID closes here. A
	// new main entry arriving means PostgreSQL has finished emitting
	// continuations for the previous one (continuations always come
	// immediately after their parent on the same backend).
	if pid := entry.PID; pid != "" {
		if pe := a.pendingByPID[pid]; pe != nil {
			a.flushPending(pe)
			delete(a.pendingByPID, pid)
		}
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
				tracked := false
				if stat, ok := a.stats[pattern]; ok {
					stat.Count++
					stat.Timestamps = append(stat.Timestamps, ts)
					tracked = true
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
					tracked = true
				}
				// Open a continuation buffer for this PID so the next
				// STATEMENT line attaches to this event's triggering
				// queries when it arrives.
				if tracked {
					if pid := entry.PID; pid != "" {
						a.pendingByPID[pid] = &pendingEvent{
							statKey: pattern,
						}
					}
				}
			}
		}
	}
}

// Finalize returns the aggregated summaries and top event signatures.
func (a *EventAnalyzer) Finalize() ([]EventSummary, []EventStat) {
	// Flush any pending continuation buffers — the input stream has
	// ended, so we won't see a "next main entry" that would otherwise
	// trigger the eviction.
	for pid, pe := range a.pendingByPID {
		a.flushPending(pe)
		delete(a.pendingByPID, pid)
	}
	// TriggeringQueries: rank by count desc, tie-break on ID so the same
	// input always serialises the same way.
	for _, stat := range a.stats {
		if len(stat.TriggeringQueries) >= 2 {
			sort.Slice(stat.TriggeringQueries, func(i, j int) bool {
				if stat.TriggeringQueries[i].Count != stat.TriggeringQueries[j].Count {
					return stat.TriggeringQueries[i].Count > stat.TriggeringQueries[j].Count
				}
				return stat.TriggeringQueries[i].ID < stat.TriggeringQueries[j].ID
			})
		}
	}

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
