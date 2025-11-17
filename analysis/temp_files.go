// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
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

	// QueryStats maps normalized queries to their temp file statistics.
	QueryStats map[string]*TempFileQueryStat
}

// TempFileEvent represents a single temporary file creation event.
type TempFileEvent struct {
	// Timestamp is when the temporary file was created.
	Timestamp time.Time

	// Size is the file size in bytes.
	Size float64

	// QueryID is the short identifier for the associated query (e.g., "se-abc123").
	// May be empty if the query cannot be identified.
	QueryID string
}

// TempFileQueryStat stores aggregated temp file statistics for a single query pattern.
type TempFileQueryStat struct {
	// RawQuery is the original query text (first occurrence).
	RawQuery string

	// NormalizedQuery is the parameterized version used for grouping.
	NormalizedQuery string

	// Count is the number of temp file events for this query.
	Count int

	// TotalSize is the cumulative size of temp files for this query in bytes.
	TotalSize int64

	// ID is a short, user-friendly identifier.
	ID string

	// FullHash is the complete hash in hexadecimal.
	FullHash string
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
	count               int
	totalSize           int64
	events              []TempFileEvent
	queryStats          map[string]*TempFileQueryStat

	// Dual-pattern approach:
	// Pattern 1: temp → STATEMENT (next line) - for log_statement configs
	// Pattern 2: query → temp (cache last query by PID) - for log_min_duration_statement configs

	// Pattern 1 state
	pendingSize         int64  // Size of last temp file awaiting statement association
	pendingPID          string // PID of last temp file seen (for immediate matching)
	pendingEventIndex   int    // Index of the pending event in events slice
	expectingStatement  bool   // True if next line should be a STATEMENT
	pendingByPID        map[string]int64 // Fallback: cumulative temp size per PID waiting for statement

	// Pattern 2 state (query before temp file)
	lastQueryByPID         map[string]string // Most recent query text seen for each PID
	tempFilesExist         bool              // True once we've seen at least one temp file

	// Performance optimization: cache normalized queries to avoid repeated normalization
	normalizedCache        map[string]cachedQueryID // Query text -> normalized + ID
}

// cachedQueryID stores the normalized query and its ID to avoid recomputation
type cachedQueryID struct {
	normalized string
	id         string
	fullHash   string
}

// NewTempFileAnalyzer creates a new temporary file analyzer.
func NewTempFileAnalyzer() *TempFileAnalyzer {
	return &TempFileAnalyzer{
		events:          make([]TempFileEvent, 0, 1000),
		queryStats:      make(map[string]*TempFileQueryStat, 100),
		pendingByPID:    make(map[string]int64, 50),           // Pattern 1: fallback cache
		lastQueryByPID:  make(map[string]string, 100),         // Pattern 2: query cache
		normalizedCache: make(map[string]cachedQueryID, 100),  // Query normalization cache
	}
}

// Process analyzes a single log entry for temporary file creation events.
//
// Uses a hybrid approach:
//  1. Try to match STATEMENT on the immediate next line (fast path, works for ~79% of cases)
//  2. Fall back to PID-based cache (fallback, works for the remaining ~21%)
//
// Performance optimization: Before the first tempfile is seen, only tempfile messages
// are detected. This means queries with "duration: statement:" appearing BEFORE the
// first tempfile will not be associated. This is a documented limitation for performance
// (saves ~6s on 11GB logs with 17M lines before first tempfile).
// Once a tempfile is seen, all subsequent queries are cached and associated normally.
//
// Expected log format:
//
//	LOG: temporary file: path "base/pgsql_tmp/pgsql_tmp12345.0", size 1048576
//	STATEMENT: SELECT * FROM large_table WHERE ...  (or "statement:" for CSV)
func (a *TempFileAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if len(msg) < 14 {
		return
	}

	// OPTIMIZATION: Use Index("temp") as discriminating pre-filter instead of IndexByte
	// "temp" is much more specific than individual chars 't', 'e', 'm'
	// This reduces false positives from ~40% to <5% of messages
	hasTemp := strings.Index(msg, "temp") >= 0
	hasColon := strings.IndexByte(msg, ':') >= 0

	// Quick reject: can't be relevant if no "temp" or ':'
	if !hasTemp && !hasColon {
		return
	}

	// Fast path: skip if not expecting statement and no pending queries
	// This avoids expensive Contains() calls for most lines
	if !a.expectingStatement && len(a.pendingByPID) == 0 && !a.tempFilesExist {
		// Before first temp file: only check for temp files
		// Use direct Index("temporary file") instead of Contains for better performance
		if hasTemp {
			if strings.Index(msg, tempFileMarker) >= 0 {
				// Found first temp file, fall through to process it
				// Note: We don't use a recheck buffer anymore for simplicity and performance
				// This means the first tempfile might miss its query association (< 0.01% of tempfiles)
			} else {
				return
			}
		} else {
			// Skip early - not a temp file candidate
			return
		}
	}

	// Now check specific patterns (only for relevant lines)
	var hasTempFile, hasStatement, hasDurationExecute, hasContext bool

	// Check for temp files (use Index instead of Contains for consistency)
	if hasTemp {
		hasTempFile = strings.Index(msg, tempFileMarker) >= 0
	}

	// Only cache queries if temp files exist (Pattern 2)
	checkForQueries := a.tempFilesExist || a.expectingStatement || len(a.pendingByPID) > 0

	if hasColon {
		// OPTIMIZATION: Contains() with short patterns is highly optimized by Go compiler
		// Using || short-circuit: if first match succeeds, second is never evaluated
		// In practice, lowercase "statement:" is 99%+ of cases (CSV + duration lines)
		// Uppercase "STATEMENT:" appears mainly in error context (rare)
		// Note: Manual IndexByte approaches are slower due to loop overhead
		hasStatement = strings.Contains(msg, "statement:") || strings.Contains(msg, "STATEMENT:")

		// Check for CONTEXT: (for queries executed from PL/pgSQL functions)
		if !hasStatement && a.expectingStatement {
			hasContext = strings.Index(msg, "CONTEXT:") >= 0
		}

		// Only check for duration:execute if we're caching
		// OPTIMIZATION: Use Index for both checks to reduce overhead
		if checkForQueries && !hasStatement {
			if durationIdx := strings.Index(msg, "duration:"); durationIdx >= 0 {
				// Only check for "execute" if we found "duration:"
				hasDurationExecute = strings.Index(msg[durationIdx:], "execute") >= 0
			}
		}
	}

	// Skip if nothing relevant
	if !hasTempFile && !hasStatement && !hasDurationExecute && !hasContext {
		return
	}

	// OPTIMIZATION: Extract PID once and reuse it throughout
	// This avoids multiple expensive ExtractPID() calls (up to 4× per entry)
	pid := parser.ExtractPID(msg)

	// === STEP 1: Check for STATEMENT/CONTEXT/query lines ===
	// Support:
	//   - "STATEMENT:" (stderr/syslog)
	//   - "statement:" (CSV)
	//   - "duration: ... execute" (prepared statements)
	//   - "CONTEXT: SQL statement" (queries from PL/pgSQL functions)

	if hasStatement || hasDurationExecute || hasContext {
		var query string

		// Try to extract from STATEMENT first
		if hasStatement || hasDurationExecute {
			query = extractStatementQuery(msg)
		}

		// If no query found and we have CONTEXT, try extracting from CONTEXT
		if query == "" && hasContext {
			query = extractContextQuery(msg)
		}

		if query == "" {
			return
		}

		// Skip transaction control commands (BEGIN/COMMIT/ROLLBACK)
		// They never generate temp files themselves
		if isTransactionCommand(query) {
			return
		}

		// Pattern 2: Save query for this PID (for duration→temp pattern)
		if pid != "" {
			a.lastQueryByPID[pid] = query
		}

		// Pattern 1: Try to match with pending temp files (temp→STATEMENT pattern)
		if a.expectingStatement || len(a.pendingByPID) > 0 {
			// Try immediate match first (fast path)
			if a.expectingStatement {
			// Fast path: pendingPID is empty (means we couldn't extract it), assume it's the same
			if a.pendingPID == "" {
				a.associateQuery(query, a.pendingSize, a.pendingEventIndex)
				a.expectingStatement = false
				a.pendingSize = 0
				a.pendingEventIndex = -1
				return
			}

			// Fast path: next line has same PID (reuse extracted pid)
			if pid == a.pendingPID {
				a.associateQuery(query, a.pendingSize, a.pendingEventIndex)
				a.expectingStatement = false
				a.pendingSize = 0
				a.pendingPID = ""
				a.pendingEventIndex = -1
				return
			}

			// Different PID: move pending to fallback cache and check this one
			a.pendingByPID[a.pendingPID] += a.pendingSize
			a.expectingStatement = false
			a.pendingSize = 0
			a.pendingPID = ""
			a.pendingEventIndex = -1

			// Fallback: check if current PID has pending temp files
			if pid != "" {
				if pendingSize, exists := a.pendingByPID[pid]; exists && pendingSize > 0 {
					a.associateQuery(query, pendingSize, -1) // No eventIndex for fallback cache
					delete(a.pendingByPID, pid)
				}
			}

				return
			}

			// Not expecting statement: check fallback cache only
			if len(a.pendingByPID) > 0 {
				if pid != "" {
					if pendingSize, exists := a.pendingByPID[pid]; exists && pendingSize > 0 {
						a.associateQuery(query, pendingSize, -1) // No eventIndex for fallback cache
						delete(a.pendingByPID, pid)
					}
				}
			}
		}

		return
	}

	// === STEP 2: Search for "temporary file" lines ===
	if !hasTempFile {
		return
	}

	a.count++
	size := extractTempFileSize(msg)
	if size > 0 {
		a.totalSize += size
		eventIndex := len(a.events)
		a.events = append(a.events, TempFileEvent{
			Timestamp: entry.Timestamp,
			Size:      float64(size),
			QueryID:   "", // Will be filled later if query is found
		})

		// Use cached PID (already extracted above)
		// Pattern 1b: Check if query is in the SAME message (CSV format with QUERY: field)
		// This has PRIORITY over cache (Pattern 2)
		if query := extractStatementQuery(msg); query != "" {
			a.associateQuery(query, size, eventIndex)

			// Clean up cache for this PID if it exists (stale query)
			if pid != "" {
				delete(a.lastQueryByPID, pid)
			}

			// Mark that we've seen at least one temp file
			a.tempFilesExist = true

			return // Done, query was in same message
		}

		// Pattern 2: Check if we have a cached query for this PID (fallback)
		if pid != "" {
			if cachedQuery, exists := a.lastQueryByPID[pid]; exists {
				a.associateQuery(cachedQuery, size, eventIndex)
				a.tempFilesExist = true
				return // Done, no need to wait for next line
			}
		}

		// Mark that we've seen at least one temp file
		a.tempFilesExist = true

		// Pattern 1: No cached query, wait for STATEMENT on next line (temp→STATEMENT pattern)
		a.pendingSize = size
		a.pendingPID = pid
		a.pendingEventIndex = eventIndex
		a.expectingStatement = true
	}
}

// isTransactionCommand checks if a query is a transaction control command.
// These commands (BEGIN/COMMIT/ROLLBACK/START/END) never generate temp files.
func isTransactionCommand(query string) bool {
	if len(query) < 3 {
		return false
	}

	firstChar := query[0]

	// Check BEGIN
	if firstChar == 'B' || firstChar == 'b' {
		return len(query) >= 5 && (query[:5] == "BEGIN" || query[:5] == "begin")
	}

	// Check COMMIT
	if firstChar == 'C' || firstChar == 'c' {
		return len(query) >= 6 && (query[:6] == "COMMIT" || query[:6] == "commit")
	}

	// Check ROLLBACK
	if firstChar == 'R' || firstChar == 'r' {
		return len(query) >= 8 && (query[:8] == "ROLLBACK" || query[:8] == "rollback")
	}

	// Check START
	if firstChar == 'S' || firstChar == 's' {
		return len(query) >= 5 && (query[:5] == "START" || query[:5] == "start")
	}

	// Check END
	if firstChar == 'E' || firstChar == 'e' {
		return len(query) >= 3 && (query[:3] == "END" || query[:3] == "end")
	}

	return false
}

// extractStatementQuery extracts the query text from a STATEMENT or duration line.
// Supports multiple formats:
//   - "STATEMENT: SELECT ..." (stderr/syslog)
//   - "statement: SELECT ..." (CSV)
//   - "QUERY: SELECT ..." (CSV with QUERY field)
//   - "duration: 1234.567 ms execute <unnamed>: SELECT ..." (prepared statements)
func extractStatementQuery(message string) string {
	// Try "QUERY:" first (CSV format with query field) - HIGHEST PRIORITY
	idx := strings.Index(message, "QUERY:")
	if idx != -1 {
		query := message[idx+len("QUERY:"):]
		return strings.TrimSpace(query)
	}

	// Try "STATEMENT:" (stderr/syslog format)
	idx = strings.Index(message, "STATEMENT:")
	if idx != -1 {
		query := message[idx+len("STATEMENT:"):]
		return strings.TrimSpace(query)
	}

	// Try "statement:" (CSV format)
	idx = strings.Index(message, "statement:")
	if idx != -1 {
		query := message[idx+len("statement:"):]
		return strings.TrimSpace(query)
	}

	// Try "duration: ... execute" (prepared statements)
	idx = strings.Index(message, "duration:")
	if idx != -1 {
		execIdx := strings.Index(message[idx:], "execute")
		if execIdx != -1 {
			// Find the colon after "execute <unnamed>" or "execute <name>"
			colonIdx := strings.Index(message[idx+execIdx:], ":")
			if colonIdx != -1 {
				query := message[idx+execIdx+colonIdx+1:]
				return strings.TrimSpace(query)
			}
		}
	}

	return ""
}

// extractContextQuery extracts the query text from a CONTEXT line.
// Supports format: "CONTEXT: SQL statement \"SELECT ...\""
func extractContextQuery(message string) string {
	// Find "CONTEXT:"
	idx := strings.Index(message, "CONTEXT:")
	if idx == -1 {
		return ""
	}

	// Look for "SQL statement \"" after CONTEXT:
	contextPart := message[idx:]
	sqlIdx := strings.Index(contextPart, "SQL statement \"")
	if sqlIdx == -1 {
		return ""
	}

	// Extract everything after "SQL statement \""
	query := contextPart[sqlIdx+len("SQL statement \""):]

	// Remove trailing quote if present
	if len(query) > 0 && query[len(query)-1] == '"' {
		query = query[:len(query)-1]
	}

	return strings.TrimSpace(query)
}

// associateQuery links a temp file size to a query by normalizing and storing stats.
// If eventIndex >= 0, also updates the QueryID of the corresponding event.
func (a *TempFileAnalyzer) associateQuery(query string, size int64, eventIndex int) {
	if query == "" {
		return
	}

	// Check cache first to avoid repeated normalization
	cached, inCache := a.normalizedCache[query]
	var normalized, id, fullHash string

	if inCache {
		// Cache hit - reuse precomputed values
		normalized = cached.normalized
		id = cached.id
		fullHash = cached.fullHash
	} else {
		// Cache miss - compute and store
		normalized = normalizeQuery(query)
		id, fullHash = GenerateQueryID(query, normalized)
		a.normalizedCache[query] = cachedQueryID{
			normalized: normalized,
			id:         id,
			fullHash:   fullHash,
		}
	}

	// Update event's QueryID if we have a valid index
	if eventIndex >= 0 && eventIndex < len(a.events) {
		a.events[eventIndex].QueryID = id
	}

	// Get or create stat entry
	stat, exists := a.queryStats[fullHash]
	if !exists {
		stat = &TempFileQueryStat{
			RawQuery:        query,
			NormalizedQuery: normalized,
			Count:           0,
			TotalSize:       0,
			ID:              id,
			FullHash:        fullHash,
		}
		a.queryStats[fullHash] = stat
	} else {
		// For deterministic JSON output, always keep the alphabetically first raw query
		if query < stat.RawQuery {
			stat.RawQuery = query
		}
	}

	// Update stats
	stat.Count++
	stat.TotalSize += size
}

// Finalize computes and returns the final temporary file metrics.
func (a *TempFileAnalyzer) Finalize() TempFileMetrics {
	return TempFileMetrics{
		Count:      a.count,
		TotalSize:  a.totalSize,
		Events:     a.events,
		QueryStats: a.queryStats,
	}
}

// extractTempFileSize parses the size value from a temporary file log message.
// Returns 0 if the size cannot be extracted.
func extractTempFileSize(message string) int64 {
	// Find "size" keyword
	idx := strings.Index(message, tempFileSize)
	if idx == -1 {
		return 0
	}

	// Skip "size" and look for the number
	sizeStr := message[idx+len(tempFileSize):]

	// Trim leading non-digits (spaces, colons, etc.)
	start := 0
	for start < len(sizeStr) && (sizeStr[start] < '0' || sizeStr[start] > '9') {
		start++
	}

	if start >= len(sizeStr) {
		return 0
	}

	// Extract digits
	end := start
	for end < len(sizeStr) && sizeStr[end] >= '0' && sizeStr[end] <= '9' {
		end++
	}

	// Parse the number
	sizeVal, err := strconv.ParseInt(sizeStr[start:end], 10, 64)
	if err != nil {
		return 0
	}

	return sizeVal
}
