// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// GlobalMetrics aggregates general statistics from PostgreSQL logs.
type GlobalMetrics struct {
	// Count is the total number of log entries processed.
	Count int

	// MinTimestamp is the timestamp of the earliest log entry.
	MinTimestamp time.Time

	// MaxTimestamp is the timestamp of the latest log entry.
	MaxTimestamp time.Time

	// ErrorCount is the number of ERROR-level messages.
	ErrorCount int

	// FatalCount is the number of FATAL-level messages.
	FatalCount int

	// PanicCount is the number of PANIC-level messages.
	PanicCount int

	// WarningCount is the number of WARNING-level messages.
	WarningCount int

	// LogCount is the number of LOG-level messages.
	LogCount int
}

// UniqueEntityMetrics tracks unique database entities (databases, users, applications, hosts).
// This helps understand the scope of database usage and identify which components are active.
type UniqueEntityMetrics struct {
	// UniqueDbs is the count of distinct databases referenced in logs.
	UniqueDbs int

	// UniqueUsers is the count of distinct users referenced in logs.
	UniqueUsers int

	// UniqueApps is the count of distinct applications referenced in logs.
	UniqueApps int

	// UniqueHosts is the count of distinct hosts/clients referenced in logs.
	UniqueHosts int

	// DBs is the sorted list of all unique database names.
	DBs []string

	// Users is the sorted list of all unique user names.
	Users []string

	// Apps is the sorted list of all unique application names.
	Apps []string

	// Hosts is the sorted list of all unique host/client addresses.
	Hosts []string

	// DBCounts maps each database name to its occurrence count in logs.
	DBCounts map[string]int

	// UserCounts maps each username to its occurrence count in logs.
	UserCounts map[string]int

	// AppCounts maps each application name to its occurrence count in logs.
	AppCounts map[string]int

	// HostCounts maps each host address to its occurrence count in logs.
	HostCounts map[string]int

	// UserDbCombos maps user×database combinations to their occurrence counts.
	// Key format: "username|database"
	UserDbCombos map[string]int

	// UserHostCombos maps user×host combinations to their occurrence counts.
	// Key format: "username|host"
	UserHostCombos map[string]int
}

// AggregatedMetrics combines all analysis metrics into a single structure.
// This is the final output of log analysis, containing statistics from all analyzers.
type AggregatedMetrics struct {
	// Global contains overall log statistics.
	Global GlobalMetrics

	// TempFiles contains temporary file usage statistics.
	TempFiles TempFileMetrics

	// Vacuum contains autovacuum and manual vacuum statistics.
	Vacuum VacuumMetrics

	// Checkpoints contains checkpoint statistics.
	Checkpoints CheckpointMetrics

	// Connections contains connection and session statistics.
	Connections ConnectionMetrics

	// Locks contains lock event statistics.
	Locks LockMetrics

	// UniqueEntities contains unique database entity statistics.
	UniqueEntities UniqueEntityMetrics

		// EventSummaries contains severity level distribution.

		// This is the main severity level distribution (ERROR, FATAL, LOG, etc.).

		EventSummaries []EventSummary

	

		// TopEvents contains the most frequent event signatures.

		// This includes specific error messages, warnings, and log patterns.

		TopEvents []EventStat

	

		// SQL contains SQL query statistics.

		SQL SqlMetrics

	}

	

	// ============================================================================

	// Streaming analysis orchestrator

	// ============================================================================

	

	// StreamingAnalyzer orchestrates multiple specialized analyzers to process

	// log entries in streaming mode without loading all data into memory.

	//

	// Usage:

	//

	//	analyzer := NewStreamingAnalyzer()

	//	for entry := range logEntries {

	//	    analyzer.Process(&entry)

	//	}

	//	metrics := analyzer.Finalize()

	type StreamingAnalyzer struct {

		global         GlobalMetrics

		tempFiles      *TempFileAnalyzer

		vacuum         *VacuumAnalyzer

		checkpoints    *CheckpointAnalyzer

		connections    *ConnectionAnalyzer

		locks          *LockAnalyzer

		events         *EventAnalyzer

		uniqueEntities *UniqueEntityAnalyzer

		sql            *SQLAnalyzer

	

		// Parallel SQL processing

		sqlChan    chan *parser.LogEntry

		parallelWg sync.WaitGroup

	}

	

	// NewStreamingAnalyzer creates a new streaming analyzer with all sub-analyzers initialized.

	// If enableParallel is true, SQLAnalyzer runs in a dedicated goroutine for better performance

	// on large files (>200MB). For smaller files, parallel overhead outweighs the gains.

	func NewStreamingAnalyzer(enableParallel bool) *StreamingAnalyzer {

		sa := &StreamingAnalyzer{

			tempFiles:      NewTempFileAnalyzer(),

			vacuum:         NewVacuumAnalyzer(),

			checkpoints:    NewCheckpointAnalyzer(),

			connections:    NewConnectionAnalyzer(),

			locks:          NewLockAnalyzer(),

			events:         NewEventAnalyzer(),

			uniqueEntities: NewUniqueEntityAnalyzer(),

			sql:            NewSQLAnalyzer(),

		}

	

		if enableParallel {

			// Start parallel SQL analyzer goroutine.

			// This provides ~20% wall clock speedup (benchmarked on 1GB+ files) by offloading

			// the most expensive analyzer to a dedicated goroutine, allowing better CPU utilization.

			sa.sqlChan = make(chan *parser.LogEntry, 65536)

			sa.parallelWg.Add(1)

			go func() {

				defer sa.parallelWg.Done()

				for entry := range sa.sqlChan {

					sa.sql.Process(entry)

				}

			}()

		}

	

		return sa

	}

	

	// Process analyzes a single log entry, dispatching it to all relevant sub-analyzers.

	// Each sub-analyzer filters and processes only the entries relevant to it.

	func (sa *StreamingAnalyzer) Process(entry *parser.LogEntry) {

		// Update global metrics (skip continuation lines for accurate count)

		if !entry.IsContinuation {

			sa.global.Count++

		}

	

		// Track timestamp range

		if sa.global.MinTimestamp.IsZero() || entry.Timestamp.Before(sa.global.MinTimestamp) {

			sa.global.MinTimestamp = entry.Timestamp

		}

		if sa.global.MaxTimestamp.IsZero() || entry.Timestamp.After(sa.global.MaxTimestamp) {

			sa.global.MaxTimestamp = entry.Timestamp

		}

	

		// Dispatch to specialized analyzers

		// Each analyzer performs its own filtering

		sa.vacuum.Process(entry)

		sa.checkpoints.Process(entry)

		sa.connections.Process(entry)

		sa.locks.Process(entry)

		sa.events.Process(entry)

		sa.uniqueEntities.Process(entry)

	

		// TempFiles: always sequential (faster than channel overhead for typical logs)

		sa.tempFiles.Process(entry)

	

		// SQL: parallel if enabled (most expensive analyzer)

		if sa.sqlChan != nil {

			sa.sqlChan <- entry

		} else {

			sa.sql.Process(entry)

		}

	}

	

	// Finalize computes final metrics after all log entries have been processed.

	// This should be called once after processing all entries.

	func (sa *StreamingAnalyzer) Finalize() AggregatedMetrics {

		// Close SQL channel and wait for goroutine to finish

		if sa.sqlChan != nil {

			close(sa.sqlChan)

			sa.parallelWg.Wait()

		}

	

		// Finalize all metrics

		tempFiles := sa.tempFiles.Finalize()

		locks := sa.locks.Finalize()

		sql := sa.sql.Finalize()

		eventSummaries, topEvents := sa.events.Finalize()

	

		// Collect queries without duration metrics from locks and tempfiles

		CollectQueriesWithoutDuration(&sql, &locks, &tempFiles)

	

		return AggregatedMetrics{

			Global:         sa.global,

			TempFiles:      tempFiles,

			Vacuum:         sa.vacuum.Finalize(),

			Checkpoints:    sa.checkpoints.Finalize(),

			Connections:    sa.connections.Finalize(),

			Locks:          locks,

			EventSummaries: eventSummaries,

			TopEvents:      topEvents,

			UniqueEntities: sa.uniqueEntities.Finalize(),

			SQL:            sql,

		}

	}

// ============================================================================
// Main analysis function
// ============================================================================

// AggregateMetrics processes a stream of log entries and returns aggregated metrics.
// This is the main entry point for log analysis, using streaming processing
// to avoid loading all entries into memory.
//
// The function reads entries from the input channel until it closes, then returns
// the complete analysis results.
//
// fileSize is used to determine whether to enable parallel SQL analysis:
//   - Files > 200MB: parallel SQL analyzer (~20% speedup)
//   - Files < 200MB: sequential processing (avoids goroutine overhead)
func AggregateMetrics(in <-chan parser.LogEntry, fileSize int64) AggregatedMetrics {
	// Enable parallel SQL analysis for large files to improve performance.
	// Threshold of 200MB based on profiling: below this, goroutine overhead
	// outweighs parallelization gains.
	const thresholdMB = 200
	enableParallel := fileSize > thresholdMB*1024*1024

	// DEBUG: log which mode is selected (disabled in production)
	//fmt.Fprintf(os.Stderr, "[DEBUG] File size: %.1f MB, Parallel SQL: %v (threshold: %d MB)\n",
	//	float64(fileSize)/(1024*1024), enableParallel, thresholdMB)

	analyzer := NewStreamingAnalyzer(enableParallel)

	// Process entries in streaming mode
	for entry := range in {
		analyzer.Process(&entry)
	}

	return analyzer.Finalize()
}

// ============================================================================
// Unique entity tracking
// ============================================================================

// UniqueEntityAnalyzer tracks unique database entities (databases, users, applications, hosts)
// encountered in log entries.
type UniqueEntityAnalyzer struct {
	// NOTE: We only keep count maps, not separate sets. The unique lists
	// are derived from map keys in Finalize(). This halves the number of
	// hash map operations per log entry.
	dbCounts   map[string]int
	userCounts map[string]int
	appCounts  map[string]int
	hostCounts map[string]int

	userDbCombos   map[string]int
	userHostCombos map[string]int
}

// NewUniqueEntityAnalyzer creates a new unique entity analyzer.
func NewUniqueEntityAnalyzer() *UniqueEntityAnalyzer {
	return &UniqueEntityAnalyzer{
		dbCounts:   make(map[string]int, 100),
		userCounts: make(map[string]int, 100),
		appCounts:  make(map[string]int, 100),
		hostCounts: make(map[string]int, 100),

		userDbCombos:   make(map[string]int, 200),
		userHostCombos: make(map[string]int, 200),
	}
}

// Process extracts database, user, application, and host names from a log entry.
//
// Expected patterns in log messages:
//   - "db=mydb" or "database=mydb"
//   - "user=postgres"
//   - "app=psql" or "application_name=app"
//   - "host=192.168.1.1" or "client=192.168.1.1"
//
// Known limitation: In stderr format, parallel workers have empty log_line_prefix
// fields (db=,user=,app=,client=) because PostgreSQL doesn't populate them.
// CSV/JSON capture this data from pg_stat_activity. This causes slight count
// differences between formats. See docs/POSTGRESQL_PATCHES.md.
func (a *UniqueEntityAnalyzer) Process(entry *parser.LogEntry) {
	// Skip continuation lines (STATEMENT, DETAIL, HINT, CONTEXT) to avoid double-counting
	if entry.IsContinuation {
		return
	}

	msg := entry.Message

	// Quick pre-filter: skip if no '=' present
	if strings.IndexByte(msg, '=') == -1 {
		return
	}

	// Track extracted values for building combinations and avoiding double-counting
	var currentUser, currentDb, currentHost, currentApp string

	// Single-pass extraction: scan once, extract all matches
	i := 0
	msgLen := len(msg)

	for i < msgLen {
		// Find next '='
		eqIdx := strings.IndexByte(msg[i:], '=')
		if eqIdx == -1 {
			break
		}
		eqIdx += i

		if eqIdx < 2 {
			i = eqIdx + 1
			continue
		}

		lastChar := msg[eqIdx-1]

		if lastChar == 'e' {
			if eqIdx >= 16 && msg[eqIdx-16:eqIdx] == "application_name" {
				if currentApp == "" {
					if appName := extractValueAt(msg, eqIdx+1); appName != "" {
						currentApp = appName
					}
				}
			} else if eqIdx >= 8 && msg[eqIdx-8:eqIdx] == "database" {
				if currentDb == "" {
					if dbName := extractValueAt(msg, eqIdx+1); dbName != "" {
						currentDb = dbName
					}
				}
			}
		} else if lastChar == 't' {
			if eqIdx >= 6 && msg[eqIdx-6:eqIdx] == "client" {
				if currentHost == "" {
					if hostName := extractValueAt(msg, eqIdx+1); hostName != "" {
						currentHost = normalizeHost(hostName)
					}
				}
			} else if eqIdx >= 4 && msg[eqIdx-4:eqIdx] == "host" {
				if currentHost == "" {
					if hostName := extractValueAt(msg, eqIdx+1); hostName != "" {
						currentHost = normalizeHost(hostName)
					}
				}
			}
		} else if lastChar == 'r' && eqIdx >= 4 && msg[eqIdx-4:eqIdx] == "user" {
			if currentUser == "" {
				if userName := extractValueAt(msg, eqIdx+1); userName != "" {
					currentUser = userName
				}
			}
		} else if lastChar == 'p' && eqIdx >= 3 && msg[eqIdx-3:eqIdx] == "app" {
			if currentApp == "" {
				if appName := extractValueAt(msg, eqIdx+1); appName != "" {
					currentApp = appName
				}
			}
		} else if lastChar == 'b' && eqIdx >= 2 && msg[eqIdx-2:eqIdx] == "db" {
			if currentDb == "" {
				if dbName := extractValueAt(msg, eqIdx+1); dbName != "" {
					currentDb = dbName
				}
			}
		}

		i = eqIdx + 1
	}

	// Count entities
	if currentUser != "" {
		a.userCounts[currentUser]++
	}
	if currentDb != "" {
		a.dbCounts[currentDb]++
	}
	if currentApp != "" {
		a.appCounts[currentApp]++
	}
	if currentHost != "" {
		a.hostCounts[currentHost]++
	}

	// Build combinations
	if currentUser != "" && currentDb != "" {
		a.userDbCombos[currentUser+"|"+currentDb]++
	}
	if currentUser != "" && currentHost != "" {
		a.userHostCombos[currentUser+"|"+currentHost]++
	}
}

// Finalize returns the unique entity metrics with sorted lists.
func (a *UniqueEntityAnalyzer) Finalize() UniqueEntityMetrics {
	// Derive unique lists from count map keys (no need for separate sets)
	return UniqueEntityMetrics{
		UniqueDbs:      len(a.dbCounts),
		UniqueUsers:    len(a.userCounts),
		UniqueApps:     len(a.appCounts),
		UniqueHosts:    len(a.hostCounts),
		DBs:            countMapKeysAsSlice(a.dbCounts),
		Users:          countMapKeysAsSlice(a.userCounts),
		Apps:           countMapKeysAsSlice(a.appCounts),
		Hosts:          countMapKeysAsSlice(a.hostCounts),
		DBCounts:       a.dbCounts,
		UserCounts:     a.userCounts,
		AppCounts:      a.appCounts,
		HostCounts:     a.hostCounts,
		UserDbCombos:   a.userDbCombos,
		UserHostCombos: a.userHostCombos,
	}
}

// ============================================================================
// Helper functions
// ============================================================================

// isSeparator is a lookup table for fast separator detection.
// Using a 256-byte array is faster than multiple comparisons.
var isSeparator [256]bool

func init() {
	// Mark separator characters
	isSeparator[' '] = true
	isSeparator[','] = true
	isSeparator['['] = true
	isSeparator[')'] = true
	isSeparator['"'] = true // CSV field delimiter can appear at end of message
}

// extractValueAt extracts a value starting at a given position in the message.
// It stops at the first separator: space, comma, bracket, or parenthesis.
// Returns empty string if no valid value found.
// This is optimized to avoid allocating a slice of separators.
func extractValueAt(msg string, startPos int) string {
	if startPos >= len(msg) {
		return ""
	}

	// Find end position (first separator)
	endPos := startPos
	for endPos < len(msg) {
		c := msg[endPos]
		// Fast lookup table check (single array access vs 4 comparisons)
		if isSeparator[c] {
			break
		}
		endPos++
	}

	if endPos == startPos {
		return ""
	}

	// Extract and normalize value
	val := msg[startPos:endPos]

	// Remove surrounding quotes if present (e.g., user="postgres" → postgres)
	if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
		val = val[1 : len(val)-1]
	}

	// Normalize "unknown" or "[unknown]" to "UNKNOWN"
	if val == "" || val == "unknown" || val == "[unknown]" {
		return "UNKNOWN"
	}

	// Case-insensitive check for "unknown" (rare but handle it)
	if len(val) == 7 && (val[0] == 'u' || val[0] == 'U') {
		if strings.EqualFold(val, "unknown") {
			return "UNKNOWN"
		}
	}

	// Filter PostgreSQL log_line_prefix placeholders (e.g., %u, %d, %a, %h)
	// These appear in "log_line_prefix changed to..." messages
	if len(val) == 2 && val[0] == '%' {
		return ""
	}

	return val
}

// normalizeHost removes the port from a host address.
// Handles formats like "192.168.1.1(12345)" or "192.168.1.1:5432" or "[::1](12345)".
// Returns just the IP/hostname part.
func normalizeHost(host string) string {
	if host == "" {
		return host
	}

	// Handle IPv6 with brackets: [::1](port) or [::1]:port
	if host[0] == '[' {
		if idx := strings.Index(host, "]"); idx > 0 {
			return host[:idx+1] // Include the closing bracket
		}
		return host
	}

	// Handle IP(port) format - common in PostgreSQL logs
	if idx := strings.Index(host, "("); idx > 0 {
		return host[:idx]
	}

	// Handle IP:port format
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		// Make sure it's not IPv6 (multiple colons)
		if strings.Count(host, ":") == 1 {
			return host[:idx]
		}
	}

	return host
}

// countMapKeysAsSlice extracts keys from a count map and returns them as a sorted slice.
// This is used to derive unique entity lists from count maps without needing separate sets.
func countMapKeysAsSlice(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// extractKeyValue extracts a value from a log message for a given key.
// It handles common PostgreSQL log formats where key-value pairs are separated
// by spaces, commas, brackets, or parentheses.
//
// Example patterns:
//   - "db=mydb user=postgres"
//   - "db=mydb,user=postgres"
//   - "connection authorized: user=postgres database=mydb"
//
// Returns the extracted value and true if found, or empty string and false if not found.
// Values of "unknown" or "[unknown]" are normalized to "UNKNOWN".
func extractKeyValue(line, key string) (string, bool) {
	// Find the key in the message
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}

	// Extract value starting after the key
	val := extractValueAt(line, idx+len(key))
	if val == "" {
		return "", false
	}

	return val, true
}

// mapKeysAsSlice converts map keys to a sorted slice.
// This provides deterministic ordering for consistent output.
func mapKeysAsSlice(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
