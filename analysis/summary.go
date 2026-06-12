// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	Replication    ReplicationMetrics
	UniqueEntities UniqueEntityMetrics
	EventSummaries []EventSummary
	TopEvents      []EventStat
	SQL            SQLMetrics
	Server         ServerMetrics
}

// StreamingAnalyzer orchestrates the eight specialized analyzers in
// streaming mode, without loading all entries into memory.
//
// Usage:
//
//	a := NewStreamingAnalyzer()
//	for batch := range logBatches {
//	    a.ProcessBatch(batch)
//	}
//	metrics := a.Finalize()
//
// SQL, Locks and TempFiles are dispatched to dedicated goroutines fed
// by buffered channels — they were measured as the three most expensive
// analyzers (sql/locks ~22% each, tempFiles 14-34% on >200 MB inputs).
// uniqueEntities was tested as a 4th parallel goroutine but plafonned,
// so it stays inline.
//
// The hand-off is per-batch, not per-entry: profiling on multi-GB
// inputs showed three per-entry channel sends burning ~35% of total
// CPU in scheduler spin (runqsteal → usleep) — four goroutines
// parking/unparking around ~100M tiny sends. Forwarding the parser's
// 256-entry batches divides the synchronization count by the batch
// size; entries are shared read-only (no analyzer mutates them) and
// the batch returns to the parser pool when the LAST consumer
// releases it.
type StreamingAnalyzer struct {
	global         GlobalMetrics
	tempFiles      *TempFileAnalyzer
	vacuum         *VacuumAnalyzer
	checkpoints    *CheckpointAnalyzer
	connections    *ConnectionAnalyzer
	locks          *LockAnalyzer
	replication    *ReplicationAnalyzer
	events         *EventAnalyzer
	uniqueEntities *UniqueEntityAnalyzer
	sql            *SQLAnalyzer
	server         *ServerAnalyzer

	sqlChan    chan *sharedBatch
	locksChan  chan *sharedBatch
	tempChan   chan *sharedBatch
	parallelWg sync.WaitGroup
}

// sharedBatch carries one parser batch through the three channel-fed
// analyzers. refs starts at the number of consumers; the last release
// returns the slice to the parser pool.
type sharedBatch struct {
	entries []parser.LogEntry
	refs    atomic.Int32
}

// release decrements the reference count and recycles the underlying
// slice once every consumer is done with it.
func (b *sharedBatch) release() {
	if b.refs.Add(-1) == 0 {
		parser.PutBatch(b.entries)
	}
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
		replication:    NewReplicationAnalyzer(),
		events:         NewEventAnalyzer(),
		uniqueEntities: NewUniqueEntityAnalyzer(),
		sql:            NewSQLAnalyzer(),
		server:         NewServerAnalyzer(),
	}

	// 256 in-flight batches ≈ 65k entries of buffering — same depth as
	// the previous per-entry channels, ~256× fewer synchronizations.
	sa.sqlChan = make(chan *sharedBatch, 256)
	sa.locksChan = make(chan *sharedBatch, 256)
	sa.tempChan = make(chan *sharedBatch, 256)

	sa.parallelWg.Add(3)
	go func() {
		defer sa.parallelWg.Done()
		for sb := range sa.sqlChan {
			for i := range sb.entries {
				sa.sql.Process(&sb.entries[i])
			}
			sb.release()
		}
	}()
	go func() {
		defer sa.parallelWg.Done()
		for sb := range sa.locksChan {
			for i := range sb.entries {
				sa.locks.Process(&sb.entries[i])
			}
			sb.release()
		}
	}()
	go func() {
		defer sa.parallelWg.Done()
		for sb := range sa.tempChan {
			for i := range sb.entries {
				sa.tempFiles.Process(&sb.entries[i])
			}
			sb.release()
		}
	}()

	return sa
}

// ProcessBatch dispatches one parser batch to every analyzer. The
// seven inline analyzers run on the calling goroutine; the three
// channel-fed ones (sql, locks, tempFiles) receive the whole batch
// and share its entries read-only. Ownership of the batch transfers
// to the sharedBatch — the caller must NOT touch or recycle it after
// this returns; the last consumer returns it to the parser pool.
func (sa *StreamingAnalyzer) ProcessBatch(batch []parser.LogEntry) {
	for i := range batch {
		entry := &batch[i]
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
		sa.replication.Process(entry)
		sa.events.Process(entry)
		sa.uniqueEntities.Process(entry)
		sa.server.Process(entry)
	}

	sb := &sharedBatch{entries: batch}
	sb.refs.Store(3)
	sa.locksChan <- sb
	sa.tempChan <- sb
	sa.sqlChan <- sb
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
		Replication:    sa.replication.Finalize(),
		EventSummaries: eventSummaries,
		TopEvents:      topEvents,
		UniqueEntities: sa.uniqueEntities.Finalize(),
		SQL:            sql,
		Server:         sa.server.Finalize(),
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
			// ProcessBatch takes ownership: the batch is recycled by
			// the last of the three channel-fed analyzers, not here.
			analyzer.ProcessBatch(batch)
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

	// lastSeen amortizes consecutive identical-value inserts.
	last lastSeenCache
}

// entityCombo is the keyed pair (user, db) or (user, host).
type entityCombo struct {
	a, b string
}

// lastSeenCache batches consecutive same-value inserts to amortize the
// per-map-operation cost (~53 bytes/op in tinygo wasm gc=leaking,
// confirmed via bisection on J_250mb). PG logs often have long runs of
// the same user/db/host, so caching "value + run length" and flushing
// only on transitions cuts map operations by 10-100×.
type lastSeenCache struct {
	user, db, app, host        string
	userN, dbN, appN, hostN    int
	userDb, userHost           entityCombo
	userDbValid, userHostValid bool
	userDbN, userHostN         int
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

	// Count entities via lastSeen cache. Per-field: if the new value
	// matches the cached one, just increment the local counter (no map
	// op). On transition, flush the previous (value, count) into the
	// map. This amortizes the per-op cost of tinygo wasm map runtime
	// (~53 bytes/op leak on gc=leaking, dominant in J.log).
	if currentUser == a.last.user {
		if currentUser != "" {
			a.last.userN++
		}
	} else {
		if a.last.userN > 0 {
			a.userCounts[a.last.user] += a.last.userN
		}
		a.last.user = currentUser
		if currentUser != "" {
			a.last.userN = 1
		} else {
			a.last.userN = 0
		}
	}
	if currentDb == a.last.db {
		if currentDb != "" {
			a.last.dbN++
		}
	} else {
		if a.last.dbN > 0 {
			a.dbCounts[a.last.db] += a.last.dbN
		}
		a.last.db = currentDb
		if currentDb != "" {
			a.last.dbN = 1
		} else {
			a.last.dbN = 0
		}
	}
	if currentApp == a.last.app {
		if currentApp != "" {
			a.last.appN++
		}
	} else {
		if a.last.appN > 0 {
			a.appCounts[a.last.app] += a.last.appN
		}
		a.last.app = currentApp
		if currentApp != "" {
			a.last.appN = 1
		} else {
			a.last.appN = 0
		}
	}
	if currentHost == a.last.host {
		if currentHost != "" {
			a.last.hostN++
		}
	} else {
		if a.last.hostN > 0 {
			a.hostCounts[a.last.host] += a.last.hostN
		}
		a.last.host = currentHost
		if currentHost != "" {
			a.last.hostN = 1
		} else {
			a.last.hostN = 0
		}
	}

	// Combos: same pattern, but only valid when both fields non-empty.
	if currentUser != "" && currentDb != "" {
		c := entityCombo{currentUser, currentDb}
		if a.last.userDbValid && c == a.last.userDb {
			a.last.userDbN++
		} else {
			if a.last.userDbValid && a.last.userDbN > 0 {
				a.userDbCombos[a.last.userDb] += a.last.userDbN
			}
			a.last.userDb = c
			a.last.userDbValid = true
			a.last.userDbN = 1
		}
	}
	if currentUser != "" && currentHost != "" {
		c := entityCombo{currentUser, currentHost}
		if a.last.userHostValid && c == a.last.userHost {
			a.last.userHostN++
		} else {
			if a.last.userHostValid && a.last.userHostN > 0 {
				a.userHostCombos[a.last.userHost] += a.last.userHostN
			}
			a.last.userHost = c
			a.last.userHostValid = true
			a.last.userHostN = 1
		}
	}
}

// flushLastSeen drains pending counters from the lastSeenCache into
// the count maps. Must be called once at Finalize.
func (a *UniqueEntityAnalyzer) flushLastSeen() {
	if a.last.userN > 0 {
		a.userCounts[a.last.user] += a.last.userN
		a.last.userN = 0
	}
	if a.last.dbN > 0 {
		a.dbCounts[a.last.db] += a.last.dbN
		a.last.dbN = 0
	}
	if a.last.appN > 0 {
		a.appCounts[a.last.app] += a.last.appN
		a.last.appN = 0
	}
	if a.last.hostN > 0 {
		a.hostCounts[a.last.host] += a.last.hostN
		a.last.hostN = 0
	}
	if a.last.userDbValid && a.last.userDbN > 0 {
		a.userDbCombos[a.last.userDb] += a.last.userDbN
		a.last.userDbN = 0
		a.last.userDbValid = false
	}
	if a.last.userHostValid && a.last.userHostN > 0 {
		a.userHostCombos[a.last.userHost] += a.last.userHostN
		a.last.userHostN = 0
		a.last.userHostValid = false
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
	a.flushLastSeen()
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
// severityMarkers is a package-level immutable list. Hoisted out of
// findSeverityMarker because that function is called once per log
// entry on logs with application_name= in the prefix; allocating a
// 15-element []string literal per call leaked ~500 MB on J_250mb in
// tinygo wasm gc=leaking (one of the dominant cumulative allocators).
var severityMarkers = [...]string{
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

func findSeverityMarker(s string) int {
	earliest := -1
	for _, sev := range severityMarkers {
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
