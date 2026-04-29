// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// GlobalMetrics aggregates general statistics from PostgreSQL logs.
type GlobalMetrics struct {
	Count        int       // total number of log entries processed
	MinTimestamp time.Time // earliest entry
	MaxTimestamp time.Time // latest entry
	ErrorCount   int
	FatalCount   int
	PanicCount   int
	WarningCount int
	LogCount     int
}

// UniqueEntityMetrics tracks unique databases, users, applications and hosts.
type UniqueEntityMetrics struct {
	UniqueDbs   int
	UniqueUsers int
	UniqueApps  int
	UniqueHosts int

	DBs   []string // sorted lists, derived from the *Counts map keys
	Users []string
	Apps  []string
	Hosts []string

	DBCounts   map[string]int // name → occurrences
	UserCounts map[string]int
	AppCounts  map[string]int
	HostCounts map[string]int

	UserDbCombos   map[string]int // key format "user|db"
	UserHostCombos map[string]int // key format "user|host"
}

// AggregatedMetrics is the final output of log analysis — every analyzer's
// metrics combined into one struct. EventSummaries is the severity-level
// distribution (ERROR/FATAL/LOG/...); TopEvents are the most frequent
// individual event signatures.
type AggregatedMetrics struct {
	Global         GlobalMetrics
	TempFiles      TempFileMetrics
	Vacuum         VacuumMetrics
	Checkpoints    CheckpointMetrics
	Connections    ConnectionMetrics
	Locks          LockMetrics
	UniqueEntities UniqueEntityMetrics
	EventSummaries []EventSummary
	TopEvents      []EventStat
	SQL            SQLMetrics
}

// StreamingAnalyzer orchestrates the eight specialized analyzers in
// streaming mode, without loading all entries into memory.
//
// Usage:
//
//	a := NewStreamingAnalyzer()
//	for entry := range logEntries {
//	    a.Process(&entry)
//	}
//	metrics := a.Finalize()
//
// SQL, Locks and TempFiles are dispatched to dedicated goroutines fed
// by buffered channels — they were measured as the three most expensive
// analyzers (sql/locks ~22% each, tempFiles 14-34% on >200 MB inputs).
// uniqueEntities was tested as a 4th parallel goroutine but plafonned,
// so it stays inline.
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

	sqlChan    chan parser.LogEntry
	locksChan  chan parser.LogEntry
	tempChan   chan parser.LogEntry
	parallelWg sync.WaitGroup
}

// NewStreamingAnalyzer creates a streaming analyzer with all
// sub-analyzers initialized and the three parallel dispatch goroutines
// running.
func NewStreamingAnalyzer() *StreamingAnalyzer {
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

	sa.sqlChan = make(chan parser.LogEntry, 65536)
	sa.locksChan = make(chan parser.LogEntry, 65536)
	sa.tempChan = make(chan parser.LogEntry, 65536)

	sa.parallelWg.Add(3)
	go func() {
		defer sa.parallelWg.Done()
		for entry := range sa.sqlChan {
			sa.sql.Process(&entry)
		}
	}()
	go func() {
		defer sa.parallelWg.Done()
		for entry := range sa.locksChan {
			sa.locks.Process(&entry)
		}
	}()
	go func() {
		defer sa.parallelWg.Done()
		for entry := range sa.tempChan {
			sa.tempFiles.Process(&entry)
		}
	}()

	return sa
}

// Process dispatches one log entry to every analyzer. The five inline
// ones are cheap or stateful in a way that doesn't benefit from a
// goroutine hand-off; the three remaining (locks, tempFiles, sql) are
// channel-fed.
func (sa *StreamingAnalyzer) Process(entry *parser.LogEntry) {
	if !entry.IsContinuation {
		sa.global.Count++
	}
	if sa.global.MinTimestamp.IsZero() || entry.Timestamp.Before(sa.global.MinTimestamp) {
		sa.global.MinTimestamp = entry.Timestamp
	}
	if sa.global.MaxTimestamp.IsZero() || entry.Timestamp.After(sa.global.MaxTimestamp) {
		sa.global.MaxTimestamp = entry.Timestamp
	}

	sa.vacuum.Process(entry)
	sa.checkpoints.Process(entry)
	sa.connections.Process(entry)
	sa.events.Process(entry)
	sa.uniqueEntities.Process(entry)

	sa.locksChan <- *entry
	sa.tempChan <- *entry
	sa.sqlChan <- *entry
}

// Finalize computes final metrics after all log entries have been processed.

// This should be called once after processing all entries.

func (sa *StreamingAnalyzer) Finalize() AggregatedMetrics {

	// Drain the parallel-analyzer goroutines before reading their state.
	close(sa.sqlChan)
	close(sa.locksChan)
	close(sa.tempChan)
	sa.parallelWg.Wait()

	tempFiles := sa.tempFiles.Finalize()
	locks := sa.locks.Finalize()
	sql := sa.sql.Finalize()
	eventSummaries, topEvents := sa.events.Finalize()
	CollectQueriesWithoutDuration(&sql, &locks, &tempFiles)

	// Roll severity counts from EventAnalyzer into the global summary.
	// Without this, Global.{Error,Fatal,Panic,Warning,Log}Count stay at 0
	// even when EventSummaries already has them — surprising for users
	// reading summary.error_count.
	for _, s := range eventSummaries {
		switch s.Type {
		case "ERROR":
			sa.global.ErrorCount = s.Count
		case "FATAL":
			sa.global.FatalCount = s.Count
		case "PANIC":
			sa.global.PanicCount = s.Count
		case "WARNING":
			sa.global.WarningCount = s.Count
		case "LOG":
			sa.global.LogCount = s.Count
		}
	}

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

// AggregateMetrics processes a stream of log entries from `in` and
// returns the aggregated result. Streaming — entries are not held in
// memory beyond their current batch.
//
// On ctx cancellation it returns whatever has been processed so far,
// after draining `in` so upstream producers can exit cleanly.
func AggregateMetrics(ctx context.Context, in <-chan []parser.LogEntry) AggregatedMetrics {
	analyzer := NewStreamingAnalyzer()

	// Process batches in streaming mode. ctx checked once per batch so a
	// cancelled run (CTRL+C, follow-mode shutdown) doesn't wait for full drain.
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case batch, ok := <-in:
			if !ok {
				break loop
			}
			for i := range batch {
				analyzer.Process(&batch[i])
			}
			parser.PutBatch(batch)
		}
	}

	// If ctx was cancelled mid-stream, drain remaining batches.
	if ctx.Err() != nil {
		for range in {
		}
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

	// Combos use struct keys to avoid allocating a fresh string
	// (`user+"|"+db`) on every entry — that concatenation alone was
	// 37 MB of cumulative heap on I_250mb (10% of total alloc), and
	// stays alive forever in TinyGo's gc=leaking runtime.
	userDbCombos   map[entityCombo]int
	userHostCombos map[entityCombo]int
}

// entityCombo is the keyed pair (user, db) or (user, host).
type entityCombo struct {
	a, b string
}

// NewUniqueEntityAnalyzer creates a new unique entity analyzer.
func NewUniqueEntityAnalyzer() *UniqueEntityAnalyzer {
	return &UniqueEntityAnalyzer{
		dbCounts:   make(map[string]int, 100),
		userCounts: make(map[string]int, 100),
		appCounts:  make(map[string]int, 100),
		hostCounts: make(map[string]int, 100),

		userDbCombos:   make(map[entityCombo]int, 200),
		userHostCombos: make(map[entityCombo]int, 200),
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
					// application_name is the long form, typically the
					// last entity on a log_line_prefix or appended at the
					// end of disconnection "session time: ..." messages.
					// The value can legitimately contain spaces (e.g.
					// "Envois Commande Baudu", "DBeaver 26 - SQLEditor
					// <foo.sql>"). Always run with commaSep=true so it
					// stops at commas, brackets, or a marker from
					// findSeverityMarker (which also lists " SSL " to
					// catch the PG "connection authorized: …
					// application_name=NAME SSL enabled (…)" suffix).
					if appName := extractValueAt(msg, eqIdx+1, true); appName != "" {
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
				commaSep := eqIdx >= 4 && msg[eqIdx-4] == ','
				if appName := extractValueAt(msg, eqIdx+1, commaSep); appName != "" {
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

	// Build combinations (struct key avoids per-entry string concatenation).
	if currentUser != "" && currentDb != "" {
		a.userDbCombos[entityCombo{currentUser, currentDb}]++
	}
	if currentUser != "" && currentHost != "" {
		a.userHostCombos[entityCombo{currentUser, currentHost}]++
	}
}

// flattenCombos converts an internal entityCombo map into the public
// "a|b" string-keyed map. Done once at Finalize so the per-entry hot
// path stays allocation-free.
func flattenCombos(in map[entityCombo]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k.a+"|"+k.b] = v
	}
	return out
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
		UserDbCombos:   flattenCombos(a.userDbCombos),
		UserHostCombos: flattenCombos(a.userHostCombos),
	}
}

// ============================================================================
// Helper functions
// ============================================================================

// isSeparator is a lookup table for fast separator detection.
// Using a 256-byte array is faster than multiple comparisons.
var isSeparator [256]bool

func init() {
	isSeparator[' '] = true
	isSeparator[','] = true
	isSeparator['['] = true
	isSeparator[')'] = true
	isSeparator['"'] = true // CSV field delimiter can appear at end of message
}

// extractValueAt extracts a value starting at a given position in the message.
// When commaSep is true, space is not treated as a separator, and severity
// markers (" LOG:", " ERROR:", etc.) act as terminators instead.
func extractValueAt(msg string, startPos int, commaSep ...bool) string {
	if startPos >= len(msg) {
		return ""
	}
	skipSpace := len(commaSep) > 0 && commaSep[0]

	endPos := startPos
	for endPos < len(msg) {
		c := msg[endPos]
		if isSeparator[c] && !(c == ' ' && skipSpace) {
			break
		}
		endPos++
	}
	if skipSpace {
		if pos := findSeverityMarker(msg[startPos:endPos]); pos != -1 {
			endPos = startPos + pos
		}
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

// findSeverityMarker returns the position of the first PostgreSQL
// severity (or continuation) marker in s, or -1 if not found. Used by
// extractValueAt when running with commaSep=true to stop a value-with-
// spaces from greedily swallowing the rest of the message. We include
// every marker PostgreSQL emits after the prefix:
//
//   - The 8 severity levels (PANIC, FATAL, ERROR, WARNING, NOTICE,
//     LOG, INFO, DEBUG). DEBUG actually has 5 numbered variants
//     (DEBUG1..DEBUG5) but the marker scan only needs the prefix.
//   - The 6 continuation markers (DETAIL, HINT, CONTEXT, STATEMENT,
//     QUERY, LOCATION) which can appear inline on long composite
//     log lines (typical of custom RAISE chains, e.g.
//     "application_name=monitor-agent NOTICE: ... NOTICE: ...").
//
// Without NOTICE / continuation markers the previous list missed
// these patterns and pulled them into the extracted entity, producing
// fake variants like "monitor-agent NOTICE: ... table" in TOP APPS.
func findSeverityMarker(s string) int {
	markers := []string{
		" LOG:", " ERROR:", " WARNING:", " FATAL:", " PANIC:",
		" NOTICE:", " INFO:", " DEBUG:",
		" DETAIL:", " HINT:", " CONTEXT:", " STATEMENT:", " QUERY:", " LOCATION:",
		// PostgreSQL appends " SSL <state> (protocol=…, cipher=…, …)"
		// after application_name in "connection authorized:" log
		// messages. Without this marker the comma-aware extractor would
		// pull the whole "favier SSL enabled (protocol=TLSv1.2" tail
		// into the captured value.
		" SSL ",
	}
	earliest := -1
	for _, sev := range markers {
		if pos := strings.Index(s, sev); pos != -1 {
			if earliest == -1 || pos < earliest {
				earliest = pos
			}
		}
	}
	return earliest
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
