// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"container/list"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// queryPrefix maps SQL keywords to short prefixes for query identification.
type queryPrefix struct {
	keyword string
	prefix  string
}

// queryPrefixes defines SQL command types and their corresponding ID prefixes.
var queryPrefixes = [...]queryPrefix{
	{"SELECT", "se-"},
	{"INSERT", "in-"},
	{"UPDATE", "up-"},
	{"DELETE", "de-"},
	{"MERGE", "me-"},
	{"COPY", "co-"},
	{"WITH", "wi-"},
	{"CREATE", "cr-"},
	{"DROP", "dr-"},
	{"ALTER", "al-"},
	{"TRUNCATE", "tr-"},
	{"COMMENT", "cm-"},
	{"REFRESH", "mv-"},
	{"BEGIN", "be-"},
	{"COMMIT", "ct-"},
	{"ROLLBACK", "rb-"},
	{"SAVEPOINT", "sv-"},
	{"RELEASE", "rl-"},
	{"START", "st-"},
	{"END", "en-"},
	{"ABORT", "ab-"},
	{"PREPARE", "pr-"},
	{"DEALLOCATE", "dl-"},
	{"DECLARE", "dc-"},
	{"FETCH", "fe-"},
	{"CLOSE", "cl-"},
	{"MOVE", "mo-"},
	{"EXPLAIN", "ex-"},
	{"ANALYZE", "an-"},
	{"VACUUM", "va-"},
	{"REINDEX", "ri-"},
	{"CLUSTER", "cu-"},
	{"LOCK", "lk-"},
	{"UNLISTEN", "ul-"},
	{"LISTEN", "li-"},
	{"NOTIFY", "no-"},
	{"DISCARD", "di-"},
	{"RESET", "re-"},
	{"SET", "se-"},
	{"SHOW", "sh-"},
	{"LOAD", "lo-"},
	{"CALL", "ca-"},
	{"DO", "do-"},
	{"EXECUTE", "xe-"},
	{"GRANT", "gr-"},
	{"REVOKE", "rv-"},
}

// QueryTypeFromID extracts the query type from a generated query ID.
func QueryTypeFromID(id string) string {
	if len(id) < 3 {
		return "OTHER"
	}

	switch id[:3] {
	case "se-":
		return "SELECT"
	case "in-":
		return "INSERT"
	case "up-":
		return "UPDATE"
	case "de-":
		return "DELETE"
	case "me-":
		return "MERGE"
	case "co-":
		return "COPY"
	case "wi-":
		return "WITH"
	case "cr-":
		return "CREATE"
	case "dr-":
		return "DROP"
	case "al-":
		return "ALTER"
	case "tr-":
		return "TRUNCATE"
	case "cm-":
		return "COMMENT"
	case "mv-":
		return "REFRESH"
	case "be-":
		return "BEGIN"
	case "ct-":
		return "COMMIT"
	case "rb-":
		return "ROLLBACK"
	case "sv-":
		return "SAVEPOINT"
	case "rl-":
		return "RELEASE"
	case "st-":
		return "START"
	case "en-":
		return "END"
	case "ab-":
		return "ABORT"
	case "pr-":
		return "PREPARE"
	case "dl-":
		return "DEALLOCATE"
	case "dc-":
		return "DECLARE"
	case "fe-":
		return "FETCH"
	case "cl-":
		return "CLOSE"
	case "mo-":
		return "MOVE"
	case "ex-":
		return "EXPLAIN"
	case "an-":
		return "ANALYZE"
	case "va-":
		return "VACUUM"
	case "ri-":
		return "REINDEX"
	case "cu-":
		return "CLUSTER"
	case "lk-":
		return "LOCK"
	case "ul-":
		return "UNLISTEN"
	case "li-":
		return "LISTEN"
	case "no-":
		return "NOTIFY"
	case "di-":
		return "DISCARD"
	case "re-":
		return "RESET"
	case "sh-":
		return "SHOW"
	case "lo-":
		return "LOAD"
	case "ca-":
		return "CALL"
	case "do-":
		return "DO"
	case "xe-":
		return "EXECUTE"
	case "gr-":
		return "GRANT"
	case "rv-":
		return "REVOKE"
	default:
		return "OTHER"
	}
}

// QueryCategory returns the high-level category for a query type.
func QueryCategory(queryType string) string {
	switch queryType {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE":
		return "DML"
	case "COPY":
		return "COPY"
	case "WITH":
		return "CTE"
	case "CREATE", "DROP", "ALTER", "TRUNCATE", "COMMENT", "REFRESH":
		return "DDL"
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "START", "END", "ABORT", "PREPARE", "DEALLOCATE":
		return "TCL"
	case "DECLARE", "FETCH", "CLOSE", "MOVE":
		return "CURSOR"
	case "EXPLAIN", "ANALYZE", "VACUUM", "REINDEX", "CLUSTER", "LOCK", "UNLISTEN", "LISTEN", "NOTIFY", "DISCARD", "RESET", "SHOW", "LOAD", "CALL", "DO", "EXECUTE", "GRANT", "REVOKE", "SET":
		return "UTILITY"
	default:
		return "OTHER"
	}
}

// QueryStat aggregates stats for one normalized SQL query pattern
// across all its executions.
type QueryStat struct {
	RawQuery        string // first occurrence (raw text)
	NormalizedQuery string // parameterized form used for grouping (e.g. "SELECT * FROM users WHERE id = $1")
	Count           int
	TotalTime       float64 // cumulative execution time, ms
	AvgTime         float64 // TotalTime / Count
	MaxTime         float64
	ID              string // short id (e.g. "se-123xaB")
	FullHash        string // full MD5 hex
	// LastPlan keeps only the most recent auto_explain plan per query
	// signature (memory-bounded; older plans are discarded).
	LastPlan string
	// PreparedNames lists the distinct prepared-statement names seen for
	// this query (extended protocol). "<unnamed>" for anonymous prepared
	// statements; bare identifiers for named ones (JDBC-style "S_24").
	// Empty when only simple-protocol "statement:" entries were observed.
	PreparedNames []string
	// SlowestRun captures the parameters of the slowest execution that
	// had a matching "DETAIL: parameters:" line. Nil when no DETAIL was
	// associated to any execution (simple protocol or DETAIL pairing miss).
	SlowestRun *SlowestRun
}

// SlowestRun records the slowest observed execution of a query whose
// parameters were logged via "DETAIL: parameters: $1 = ...".
//
// Database/User/App/Host capture the client identity behind the
// slowest run so operators can reproduce or trace it; they may be
// empty when the log prefix did not include the corresponding field.
type SlowestRun struct {
	DurationMs float64
	Timestamp  time.Time
	PID        string
	Parameters string // raw DETAIL payload: "$1 = '393', $2 = '5', ..."
	Database   string
	User       string
	App        string
	Host       string
}

// pendingExec records the latest execute: entry seen on a PID, waiting
// for an optional "DETAIL: parameters:" continuation that PostgreSQL
// emits on the next log record from the same backend.
type pendingExec struct {
	statsKey  string  // normalized query key into queryStats
	duration  float64 // ms
	timestamp time.Time
	database  string
	user      string
	app       string
	host      string
}

// QueryExecution is one SQL execution event. Returned by SQLMetrics
// iteration helpers (IterateExecutions / ExecutionAt) — the metrics
// struct stores events in compact parallel slices internally and
// expands them to QueryExecution one at a time on access.
//
// Database/User/App/Host are resolved from per-event dictionary
// indices on read; empty when the source log line did not carry that
// field on its prefix.
type QueryExecution struct {
	Timestamp time.Time
	Duration  float64 // ms
	QueryID   string  // short id (e.g. "se-abc123")
	Database  string
	User      string
	App       string
	Host      string
}

// ExecutionCount returns the number of recorded execution events.
func (m *SQLMetrics) ExecutionCount() int {
	if m.executions == nil {
		return 0
	}
	return m.executions.Len()
}

// ExecutionAt expands the i-th event to a full QueryExecution.
// Panics if i is out of bounds — guard with ExecutionCount.
func (m *SQLMetrics) ExecutionAt(i int) QueryExecution {
	return m.executions.At(i)
}

// IterateExecutions calls fn for each event in append order. Returning
// false from fn stops iteration early. No []QueryExecution slice is
// materialized.
func (m *SQLMetrics) IterateExecutions(fn func(QueryExecution) bool) {
	if m.executions == nil {
		return
	}
	m.executions.ForEach(fn)
}

// ExecutionDurations returns a fresh []float64 of every event's
// duration in append order. Cheaper than IterateExecutions when the
// caller only needs durations (percentile / median compute).
func (m *SQLMetrics) ExecutionDurations() []float64 {
	if m.executions == nil {
		return nil
	}
	return m.executions.Durations()
}

// ExecutionsCountAbove returns the number of events with
// Duration >= threshold. Avoids the per-event QueryExecution
// expansion that a manual loop would force.
func (m *SQLMetrics) ExecutionsCountAbove(threshold float64) int {
	if m.executions == nil {
		return 0
	}
	return m.executions.CountAbove(threshold)
}

// IterateExecutionsForID is like IterateExecutions but only yields
// events whose QueryID matches the given id. Implemented in terms of
// the compact storage's queryID table — no full iteration over events
// when the id is absent, and no intermediate slice when present.
func (m *SQLMetrics) IterateExecutionsForID(id string, fn func(QueryExecution) bool) {
	if m.executions == nil {
		return
	}
	// Resolve the id to its compact index (linear scan over the small
	// queryIDs table — typically a few hundred entries, not millions).
	idx := uint32(0)
	found := false
	for i, qid := range m.executions.queryIDs {
		if qid == id {
			idx = uint32(i)
			found = true
			break
		}
	}
	if !found {
		return
	}
	m.executions.ForEachID(idx, id, fn)
}

// DimensionCount is one (name, count) row produced by the per-query
// dimension breakdown. Sorted by count desc, then name asc on ties.
type DimensionCount struct {
	Name  string
	Count int
}

// QueryDimensions holds the top-N database/user/app/host names that
// have executed a single query id. Each slice already truncated to
// the limit requested by TopDimensionsForID; "" entries (no value on
// the log prefix) are skipped.
type QueryDimensions struct {
	Databases []DimensionCount
	Users     []DimensionCount
	Apps      []DimensionCount
	Hosts     []DimensionCount
}

// IsEmpty reports whether the breakdown has no rows at all. Output
// renderers use it to skip the DIMENSIONS section when the log prefix
// did not carry any of the four fields for this query.
func (q QueryDimensions) IsEmpty() bool {
	return len(q.Databases) == 0 && len(q.Users) == 0 && len(q.Apps) == 0 && len(q.Hosts) == 0
}

// TopDimensionsForID returns the top-`limit` (db/user/app/host) names
// that have executed the query id, with their counts. Computed by
// walking the compact executions for this id and tallying the
// dimension indices into local maps. O(N) per dimension where N is
// the number of executions of this specific query, not the full log.
func (m *SQLMetrics) TopDimensionsForID(id string, limit int) QueryDimensions {
	if m.executions == nil || limit <= 0 {
		return QueryDimensions{}
	}
	idx := uint32(0)
	found := false
	for i, qid := range m.executions.queryIDs {
		if qid == id {
			idx = uint32(i)
			found = true
			break
		}
	}
	if !found {
		return QueryDimensions{}
	}
	dbCounts := make(map[uint16]int)
	userCounts := make(map[uint16]int)
	appCounts := make(map[uint16]int)
	hostCounts := make(map[uint16]int)
	c := m.executions
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j, qix := range ch.queryIDIdx {
			if qix != idx {
				continue
			}
			db, user, app, host := c.dimsAt(ci, j)
			if db != 0 {
				dbCounts[db]++
			}
			if user != 0 {
				userCounts[user]++
			}
			if app != 0 {
				appCounts[app]++
			}
			if host != 0 {
				hostCounts[host]++
			}
		}
	}
	return QueryDimensions{
		Databases: topDimensionEntries(dbCounts, c.databases, limit),
		Users:     topDimensionEntries(userCounts, c.users, limit),
		Apps:      topDimensionEntries(appCounts, c.apps, limit),
		Hosts:     topDimensionEntries(hostCounts, c.hosts, limit),
	}
}

// topDimensionEntries sorts the (index → count) tally by count desc,
// then by name asc on ties, and truncates to limit. Returns nil when
// the tally is empty.
func topDimensionEntries(counts map[uint16]int, table []string, limit int) []DimensionCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]DimensionCount, 0, len(counts))
	for k, v := range counts {
		if int(k) >= len(table) {
			continue
		}
		out = append(out, DimensionCount{Name: table[k], Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

// TopDimensionsByQuery computes the top-`limit` (db/user/app/host)
// breakdown for every query id, walking the executions in a single
// pass. Returns a map keyed by query id, only populated for queries
// that have at least one non-empty dimension. O(N) where N is the
// total execution count — much cheaper than calling TopDimensionsForID
// once per query (O(N × K) for K unique queries).
//
// Used by the JSON exporter to enrich the sql_performance.queries
// array consumed by the HTML modal.
func (m *SQLMetrics) TopDimensionsByQuery(limit int) map[string]QueryDimensions {
	if m.executions == nil || limit <= 0 {
		return nil
	}
	c := m.executions
	// Per-query tallies: queryIDIdx → dimension → index → count.
	dbCounts := make(map[uint32]map[uint16]int, len(c.queryIDs))
	userCounts := make(map[uint32]map[uint16]int, len(c.queryIDs))
	appCounts := make(map[uint32]map[uint16]int, len(c.queryIDs))
	hostCounts := make(map[uint32]map[uint16]int, len(c.queryIDs))
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j, qix := range ch.queryIDIdx {
			db, user, app, host := c.dimsAt(ci, j)
			if db != 0 {
				inner, ok := dbCounts[qix]
				if !ok {
					inner = make(map[uint16]int)
					dbCounts[qix] = inner
				}
				inner[db]++
			}
			if user != 0 {
				inner, ok := userCounts[qix]
				if !ok {
					inner = make(map[uint16]int)
					userCounts[qix] = inner
				}
				inner[user]++
			}
			if app != 0 {
				inner, ok := appCounts[qix]
				if !ok {
					inner = make(map[uint16]int)
					appCounts[qix] = inner
				}
				inner[app]++
			}
			if host != 0 {
				inner, ok := hostCounts[qix]
				if !ok {
					inner = make(map[uint16]int)
					hostCounts[qix] = inner
				}
				inner[host]++
			}
		}
	}
	out := make(map[string]QueryDimensions, len(c.queryIDs))
	for qix, id := range c.queryIDs {
		dims := QueryDimensions{
			Databases: topDimensionEntries(dbCounts[uint32(qix)], c.databases, limit),
			Users:     topDimensionEntries(userCounts[uint32(qix)], c.users, limit),
			Apps:      topDimensionEntries(appCounts[uint32(qix)], c.apps, limit),
			Hosts:     topDimensionEntries(hostCounts[uint32(qix)], c.hosts, limit),
		}
		if !dims.IsEmpty() {
			out[id] = dims
		}
	}
	return out
}

// SQLMetrics combines per-query stats and global SQL metrics.
type SQLMetrics struct {
	QueryStats       map[string]*QueryStat // normalized query → stats
	TotalQueries     int
	UniqueQueries    int
	MinQueryDuration float64
	MaxQueryDuration float64
	SumQueryDuration float64
	StartTimestamp   time.Time
	EndTimestamp     time.Time
	// executions is the compact storage of all execution events. Use
	// IterateExecutions / ExecutionAt / ExecutionCount instead of
	// reaching into the slice — the field is intentionally private to
	// keep the parallel-slice layout opaque to consumers.
	executions          *compactExecutions
	MedianQueryDuration float64 // 50th percentile
	P99QueryDuration    float64

	// QueriesWithoutDurationCount tracks queries identified from logs
	// (lock events, tempfile events) but without a duration recorded.
	// Total may be < FromLocks + FromTempfiles when a query appears in
	// both.
	QueriesWithoutDurationCount struct {
		FromLocks     int
		FromTempfiles int
		Total         int
	}

	// QueryTypeStats: type (SELECT, INSERT, ...) → stats.
	QueryTypeStats map[string]*QueryTypeStat

	// Query type breakdown by dimension (for --sql-overview).
	QueryTypesByDatabase map[string]map[string]*QueryTypeCount
	QueryTypesByUser     map[string]map[string]*QueryTypeCount
	QueryTypesByHost     map[string]map[string]*QueryTypeCount
	QueryTypesByApp      map[string]map[string]*QueryTypeCount
}

// QueryTypeStat contains aggregated statistics for a specific query type.
type QueryTypeStat struct {
	Type          string  // SELECT, INSERT, UPDATE, ...
	Category      string  // DML, DDL, TCL, ...
	Count         int     // executions of this type
	UniqueQueries int     // distinct queries of this type
	TotalTime     float64 // cumulative ms
	AvgTime       float64 // ms per query
	MaxTime       float64
}

// QueryTypeCount tracks count and total time for a query type in a specific dimension.
type QueryTypeCount struct {
	Count     int
	TotalTime float64 // milliseconds
}

// ============================================================================
// LRU Cache for normalization
// ============================================================================

// lruCacheEntry represents a single cache entry
type lruCacheEntry struct {
	key   string
	value string
}

// lruCache is a simple LRU cache implementation
type lruCache struct {
	capacity int
	cache    map[string]*list.Element
	list     *list.List
}

// newLRUCache creates a new LRU cache with the given capacity
func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element, capacity),
		list:     list.New(),
	}
}

// Get retrieves a value from the cache
func (c *lruCache) Get(key string) (string, bool) {
	if elem, ok := c.cache[key]; ok {
		// Only move to front if not already there (optimization)
		if c.list.Front() != elem {
			c.list.MoveToFront(elem)
		}
		return elem.Value.(*lruCacheEntry).value, true
	}
	return "", false
}

// Put adds a value to the cache
func (c *lruCache) Put(key, value string) {
	if elem, ok := c.cache[key]; ok {
		// Only move to front if not already there (optimization)
		if c.list.Front() != elem {
			c.list.MoveToFront(elem)
		}
		elem.Value.(*lruCacheEntry).value = value
		return
	}

	entry := &lruCacheEntry{key: key, value: value}
	elem := c.list.PushFront(entry)
	c.cache[key] = elem

	if c.list.Len() > c.capacity {
		oldest := c.list.Back()
		if oldest != nil {
			c.list.Remove(oldest)
			delete(c.cache, oldest.Value.(*lruCacheEntry).key)
		}
	}
}

// ============================================================================
// Streaming SQL analyzer
// ============================================================================

// SQLAnalyzer processes SQL queries from log entries in streaming mode.
// It extracts query durations, normalizes queries, and aggregates statistics.
//
// Usage:
//
//	analyzer := NewSQLAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type SQLAnalyzer struct {
	queryStats       map[string]*QueryStat
	totalQueries     int
	minQueryDuration float64
	maxQueryDuration float64
	sumQueryDuration float64
	startTimestamp   time.Time
	endTimestamp     time.Time
	executions       *compactExecutions

	// LRU cache to avoid re-normalizing identical raw queries
	// Limited capacity to prevent unbounded memory growth
	// Maps trimmed raw query → normalized query
	normalizationCache *lruCache

	// Query type breakdown by dimension (for --sql-overview)
	queryTypesByDatabase map[string]map[string]*QueryTypeCount
	queryTypesByUser     map[string]map[string]*QueryTypeCount
	queryTypesByHost     map[string]map[string]*QueryTypeCount
	queryTypesByApp      map[string]map[string]*QueryTypeCount

	// pendingPlanByPID stores auto_explain plan text temporarily, keyed by PID.
	// When a plan: entry arrives, we store it here. When the subsequent
	// statement: entry arrives for the same PID, we attach the plan to the query.
	pendingPlanByPID map[string]string

	// pendingExecByPID stores the latest execute: per PID, waiting for an
	// optional "DETAIL: parameters:" continuation on the same backend.
	pendingExecByPID map[string]pendingExec
}

// NewSQLAnalyzer creates a new SQL analyzer with pre-allocated capacity.
// Pre-allocates space for 10,000 unique queries to reduce map reallocation overhead.
// Uses LRU cache with 5000 entry capacity to balance performance and memory usage.
// Larger cache reduces normalizeQuery() calls, saving ~300MB on 11GB files.
func NewSQLAnalyzer() *SQLAnalyzer {
	return NewSQLAnalyzerWithSize(0)
}

// NewSQLAnalyzerWithSize creates a SQL analyzer with capacity estimated from input size.
// inputBytes is the size of the input data in bytes. If 0, uses default capacity.
//
// Heuristic: ~1 timed query per 1000 bytes. Calibrated on I_250mb.log
// (260k executions / 250 MB = 1 per ~960 B). The earlier 1/200 ratio
// over-allocated by ~5x — invisible in CLI (Go GC reclaims unused slots
// fast) but a flat 48 MB cumulative-alloc waste in TinyGo's gc=leaking
// runtime, where the empty preallocated slots stay alive forever.
// Underestimating is cheap: the slice grows by doubling.
func NewSQLAnalyzerWithSize(inputBytes int64) *SQLAnalyzer {
	execCap := 10000
	if inputBytes > 0 {
		estimated := int(inputBytes / 1000)
		if estimated > execCap {
			execCap = estimated
		}
		// Cap initial preallocation at 2M; the slice can still grow past
		// this if the log actually has more queries.
		if execCap > 2000000 {
			execCap = 2000000
		}
	}

	return &SQLAnalyzer{
		queryStats:           make(map[string]*QueryStat, 10000),
		executions:           newCompactExecutions(execCap),
		normalizationCache:   newLRUCache(5000), // LRU cache for raw→normalized mapping
		queryTypesByDatabase: make(map[string]map[string]*QueryTypeCount),
		queryTypesByUser:     make(map[string]map[string]*QueryTypeCount),
		queryTypesByHost:     make(map[string]map[string]*QueryTypeCount),
		queryTypesByApp:      make(map[string]map[string]*QueryTypeCount),
		pendingPlanByPID:     make(map[string]string),
		pendingExecByPID:     make(map[string]pendingExec),
	}
}

// Process analyzes a single log entry for SQL query information.
// It extracts duration and query text, normalizes the query, and updates statistics.
//
// Expected log format:
//
//	"LOG: duration: 5.123 ms execute <unnamed>: SELECT * FROM users WHERE id = 1"
//	"LOG: duration: 10.456 ms statement: UPDATE users SET name = 'John' WHERE id = 1"
func (a *SQLAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	// Intercept auto_explain plan: messages before normal processing.
	// These arrive BEFORE the corresponding statement: entry for the same PID.
	if isPlanMessage(msg) {
		pid := entry.PID
		if pid != "" {
			if plan := extractPlanText(msg); plan != "" {
				a.pendingPlanByPID[pid] = plan
			}
		}
		return
	}

	// Intercept "DETAIL: parameters:" entries. These follow an execute:
	// entry on the same backend; pair them by PID with pendingExecByPID
	// to remember the parameter values of the slowest run per query.
	if isParamsMessage(msg) {
		pid := entry.PID
		if pid != "" {
			if pe, ok := a.pendingExecByPID[pid]; ok {
				if stat, found := a.queryStats[pe.statsKey]; found {
					if stat.SlowestRun == nil || pe.duration > stat.SlowestRun.DurationMs {
						params := extractParameters(msg)
						if len(params) > slowestRunParamsCap {
							params = params[:slowestRunParamsCap]
						}
						stat.SlowestRun = &SlowestRun{
							DurationMs: pe.duration,
							Timestamp:  pe.timestamp,
							PID:        pid,
							Parameters: params,
							Database:   pe.database,
							User:       pe.user,
							App:        pe.app,
							Host:       pe.host,
						}
					}
				}
				delete(a.pendingExecByPID, pid)
			}
		}
		return
	}

	// Extract duration, query, and optional prepared-statement name.
	duration, query, preparedName, ok := extractDurationAndQuery(msg)
	if !ok {
		return
	}

	// Update global execution metrics
	a.totalQueries++

	// Track timestamp range
	if a.startTimestamp.IsZero() || entry.Timestamp.Before(a.startTimestamp) {
		a.startTimestamp = entry.Timestamp
	}
	if a.endTimestamp.IsZero() || entry.Timestamp.After(a.endTimestamp) {
		a.endTimestamp = entry.Timestamp
	}

	// Normalize query for aggregation (with LRU caching)
	trimmedQuery := strings.TrimSpace(query)

	// Normalize whitespace (newlines to spaces) for consistent raw_query across formats
	// normalizeWhitespace has a fast-path that avoids allocation for already-normalized strings
	rawQuery := normalizeWhitespace(trimmedQuery)

	// Check LRU cache first to avoid expensive re-normalization
	normalizedQuery, cached := a.normalizationCache.Get(rawQuery)
	if !cached {
		normalizedQuery = normalizeQuery(rawQuery)
		a.normalizationCache.Put(rawQuery, normalizedQuery)
	}

	// Update or create query statistics
	stats, exists := a.queryStats[normalizedQuery]
	if !exists {
		// Generate ID only for new queries (expensive operation)
		id, fullHash := GenerateQueryID(rawQuery, normalizedQuery)
		stats = &QueryStat{
			RawQuery:        rawQuery,
			NormalizedQuery: normalizedQuery,
			ID:              id,
			FullHash:        fullHash,
		}
		a.queryStats[normalizedQuery] = stats
	} else {
		// For deterministic JSON output, always keep the alphabetically first raw query
		if rawQuery < stats.RawQuery {
			stats.RawQuery = rawQuery
		}
	}

	// Extract per-event prefix fields once. They feed three downstream
	// sinks: the compact-storage dictionary indices, the pendingExec
	// snapshot for SlowestRun pairing, and the legacy QueryTypesByX
	// aggregation below.
	database, user, host, app := extractPrefixFields(entry.Message)

	// Add execution with query ID (after stats are created/retrieved).
	// Compact storage: parallel slices + interned query IDs + per-event
	// dimension indices.
	a.executions.append(entry.Timestamp, duration, stats.ID, database, user, app, host)

	// Associate pending auto_explain plan (same PID, arrived just before)
	pid := entry.PID
	if pid != "" {
		if plan, hasPlan := a.pendingPlanByPID[pid]; hasPlan {
			stats.LastPlan = plan
			delete(a.pendingPlanByPID, pid)
		}
	}

	// Record the prepared-statement name (deduplicated). Cap the per-stat
	// set to a small bound so a pathological workload that prepares under
	// thousands of distinct names cannot blow up memory.
	if preparedName != "" {
		stats.PreparedNames = appendPreparedName(stats.PreparedNames, preparedName)
	}

	// Remember this execute for an optional "DETAIL: parameters:" follow-up
	// on the same backend. Only stored when the entry has a PID — without
	// it we cannot pair the next continuation reliably.
	if pid != "" {
		a.pendingExecByPID[pid] = pendingExec{
			statsKey:  normalizedQuery,
			duration:  duration,
			timestamp: entry.Timestamp,
			database:  database,
			user:      user,
			app:       app,
			host:      host,
		}
	}

	// Update per-query statistics
	stats.Count++
	stats.TotalTime += duration
	if duration > stats.MaxTime {
		stats.MaxTime = duration
	}

	// Update global duration statistics
	if a.minQueryDuration == 0 || duration < a.minQueryDuration {
		a.minQueryDuration = duration
	}
	if duration > a.maxQueryDuration {
		a.maxQueryDuration = duration
	}
	a.sumQueryDuration += duration

	// Track query type breakdown by dimension (database, user, host, app)
	queryType := QueryTypeFromID(stats.ID)

	// Track query type breakdown by dimension - cache inner map refs
	if database != "" {
		dbMap := a.queryTypesByDatabase[database]
		if dbMap == nil {
			dbMap = make(map[string]*QueryTypeCount)
			a.queryTypesByDatabase[database] = dbMap
		}
		if entry := dbMap[queryType]; entry != nil {
			entry.Count++
			entry.TotalTime += duration
		} else {
			dbMap[queryType] = &QueryTypeCount{Count: 1, TotalTime: duration}
		}
	}
	if user != "" {
		uMap := a.queryTypesByUser[user]
		if uMap == nil {
			uMap = make(map[string]*QueryTypeCount)
			a.queryTypesByUser[user] = uMap
		}
		if entry := uMap[queryType]; entry != nil {
			entry.Count++
			entry.TotalTime += duration
		} else {
			uMap[queryType] = &QueryTypeCount{Count: 1, TotalTime: duration}
		}
	}
	if host != "" {
		hMap := a.queryTypesByHost[host]
		if hMap == nil {
			hMap = make(map[string]*QueryTypeCount)
			a.queryTypesByHost[host] = hMap
		}
		if entry := hMap[queryType]; entry != nil {
			entry.Count++
			entry.TotalTime += duration
		} else {
			hMap[queryType] = &QueryTypeCount{Count: 1, TotalTime: duration}
		}
	}
	if app != "" {
		aMap := a.queryTypesByApp[app]
		if aMap == nil {
			aMap = make(map[string]*QueryTypeCount)
			a.queryTypesByApp[app] = aMap
		}
		if entry := aMap[queryType]; entry != nil {
			entry.Count++
			entry.TotalTime += duration
		} else {
			aMap[queryType] = &QueryTypeCount{Count: 1, TotalTime: duration}
		}
	}
}

// extractPrefixFields extracts db, user, host, and app from the log prefix in a single pass.
// Format: "user=app_user,db=app_db,app=pgadmin,client=192.168.1.1" or "[pid]: user=x,db=y LOG: ..."
// This is faster than calling extractPrefixValue 4 times as it only scans the message once.
func extractPrefixFields(message string) (database, user, host, app string) {
	// Scan the first 200 chars of the message for key=value patterns
	end := 200
	if len(message) < end {
		end = len(message)
	}
	prefix := message[:end]
	n := len(prefix)

	// Detect comma-separated format (e.g., "user=X,db=Y,app=Z")
	commaSep := strings.Contains(prefix, ",db=") || strings.Contains(prefix, ",user=") || strings.Contains(prefix, ",app=")

	// Fast scan using '=' as anchor point
	for i := 0; i < n-1; i++ {
		if prefix[i] != '=' {
			continue
		}
		// Found '=', check what key it is
		if i >= 2 && prefix[i-2:i] == "db" && (i == 2 || !isAlnum(prefix[i-3])) {
			database = extractPrefixValueAt(prefix, i+1, false)
		} else if i >= 4 && prefix[i-4:i] == "user" && (i == 4 || !isAlnum(prefix[i-5])) {
			user = extractPrefixValueAt(prefix, i+1, false)
		} else if i >= 4 && prefix[i-4:i] == "host" && (i == 4 || !isAlnum(prefix[i-5])) {
			host = extractPrefixValueAt(prefix, i+1, false)
		} else if i >= 6 && prefix[i-6:i] == "client" && (i == 6 || !isAlnum(prefix[i-7])) {
			host = extractPrefixValueAt(prefix, i+1, false) // client= is alias for host
		} else if i >= 3 && prefix[i-3:i] == "app" && (i == 3 || !isAlnum(prefix[i-4])) {
			app = extractPrefixValueAt(prefix, i+1, commaSep)
		}
	}
	return
}

// isAlnum returns true if c is alphanumeric
func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// extractPrefixValueAt extracts a value starting at position i until a separator.
// When skipSpace is true (comma-separated format), spaces are allowed in values
// and severity markers (" LOG:", " ERROR:", etc.) act as terminators instead.
func extractPrefixValueAt(s string, start int, skipSpace bool) string {
	end := start
	n := len(s)
	for end < n {
		c := s[end]
		if c == ',' || c == ']' || c == '[' {
			break
		}
		if c == ' ' && !skipSpace {
			break
		}
		end++
	}
	if skipSpace {
		if pos := findSeverityMarker(s[start:end]); pos != -1 {
			end = start + pos
		}
	}
	return s[start:end]
}

// Finalize returns the aggregated SQL metrics.
// This should be called after all log entries have been processed.
//
// It calculates:
//   - Average execution time for each query
//   - Median and 99th percentile of all query durations
//   - Query type statistics
func (a *SQLAnalyzer) Finalize() SQLMetrics {
	metrics := SQLMetrics{
		QueryStats:           a.queryStats,
		TotalQueries:         a.totalQueries,
		UniqueQueries:        len(a.queryStats),
		MinQueryDuration:     a.minQueryDuration,
		MaxQueryDuration:     a.maxQueryDuration,
		SumQueryDuration:     a.sumQueryDuration,
		StartTimestamp:       a.startTimestamp,
		EndTimestamp:         a.endTimestamp,
		executions:           a.executions,
		QueryTypeStats:       make(map[string]*QueryTypeStat),
		QueryTypesByDatabase: a.queryTypesByDatabase,
		QueryTypesByUser:     a.queryTypesByUser,
		QueryTypesByHost:     a.queryTypesByHost,
		QueryTypesByApp:      a.queryTypesByApp,
	}

	// Calculate average time for each query and aggregate by type
	for _, stat := range a.queryStats {
		stat.AvgTime = stat.TotalTime / float64(stat.Count)

		// Get query type from ID
		queryType := QueryTypeFromID(stat.ID)
		category := QueryCategory(queryType)

		// Update type statistics
		typeStat, exists := metrics.QueryTypeStats[queryType]
		if !exists {
			typeStat = &QueryTypeStat{
				Type:     queryType,
				Category: category,
			}
			metrics.QueryTypeStats[queryType] = typeStat
		}

		typeStat.UniqueQueries++
		typeStat.Count += stat.Count
		typeStat.TotalTime += stat.TotalTime
		if stat.MaxTime > typeStat.MaxTime {
			typeStat.MaxTime = stat.MaxTime
		}
	}

	// Calculate average time per type
	for _, typeStat := range metrics.QueryTypeStats {
		if typeStat.Count > 0 {
			typeStat.AvgTime = typeStat.TotalTime / float64(typeStat.Count)
		}
	}

	// Calculate percentiles. Pull durations once via the compact
	// storage so we don't pay 48 B × N for the iteration.
	if a.executions.Len() > 0 {
		durs := a.executions.Durations()
		sort.Float64s(durs)
		metrics.MedianQueryDuration = medianFromSorted(durs)
		metrics.P99QueryDuration = percentileFromSorted(durs, 99)
	}

	// queryIDIndex was only needed for the parser-side append path;
	// release it now that no further events will be recorded.
	a.executions.freeIndex()

	return metrics
}

// ============================================================================
// Percentile calculation helpers
// ============================================================================

// medianFromSorted returns the median of an already-sorted []float64.
// Caller is responsible for the sort — Finalize() does it once and
// reuses the result for both median and P99.
func medianFromSorted(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2.0
}

// percentileFromSorted returns the Nth percentile of an
// already-sorted []float64. percentile should be between 0 and 100.
func percentileFromSorted(sorted []float64, percentile int) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	index := int(float64(percentile) / 100.0 * float64(n))
	if index >= n {
		index = n - 1
	}
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

// ============================================================================
// Query extraction from log messages
// ============================================================================

// extractDurationAndQuery parses duration, query text, and the optional
// prepared-statement name from a PostgreSQL log message.
//
// Expected format:
//
//	"duration: X.XXX ms execute <name>: QUERY"
//	"duration: X.XXX ms statement: QUERY"
//
// Returns:
//   - duration: execution time in milliseconds
//   - query: SQL query text
//   - preparedName: name between "execute " and ":" ("" for simple-protocol
//     "statement:" entries)
//   - ok: true if parsing succeeded
//
// This function is optimized for performance:
//   - Single pass parsing
//   - No intermediate string allocations
//   - Manual whitespace skipping
func extractDurationAndQuery(message string) (duration float64, query, preparedName string, ok bool) {
	// Quick length check
	if len(message) < 20 {
		return 0, "", "", false
	}

	// Find "duration:" marker
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return 0, "", "", false
	}

	// Parse duration value
	start := durIdx + 9 // Skip "duration:"

	// Skip leading whitespace
	for start < len(message) && message[start] == ' ' {
		start++
	}

	// Find end of duration number (next space)
	end := start
	for end < len(message) && message[end] != ' ' {
		end++
	}

	if end == start {
		return 0, "", "", false
	}

	// Parse float duration
	dur, err := strconv.ParseFloat(message[start:end], 64)
	if err != nil {
		return 0, "", "", false
	}

	// Find query marker ("execute" or "statement")
	// Search after duration marker for efficiency
	var markerIdx int
	var markerLen int
	isExecute := false

	execIdx := indexAfter(message, "execute", durIdx)
	stmtIdx := indexAfter(message, "statement", durIdx)

	if execIdx != -1 && (stmtIdx == -1 || execIdx < stmtIdx) {
		markerIdx = execIdx
		markerLen = 7 // len("execute")
		isExecute = true
	} else if stmtIdx != -1 {
		markerIdx = stmtIdx
		markerLen = 9 // len("statement")
	} else {
		return dur, "", "", false
	}

	// For execute: capture the prepared-statement name between "execute "
	// and the next ":". Skip a single leading space.
	nameStart := markerIdx + markerLen
	if isExecute && nameStart < len(message) && message[nameStart] == ' ' {
		nameStart++
	}

	// Find ':' after marker
	queryStart := markerIdx + markerLen
	for queryStart < len(message) && message[queryStart] != ':' {
		queryStart++
	}
	if queryStart >= len(message) {
		return dur, "", "", false
	}
	if isExecute && queryStart > nameStart {
		preparedName = message[nameStart:queryStart]
	}
	queryStart++ // Skip ':'

	// Skip leading whitespace before query
	for queryStart < len(message) && message[queryStart] == ' ' {
		queryStart++
	}

	if queryStart >= len(message) {
		return dur, "", preparedName, false
	}

	query = message[queryStart:]
	return dur, query, preparedName, true
}

// indexAfter finds the first occurrence of substr in s, starting after the given position.
// Returns -1 if not found.
func indexAfter(s, substr string, after int) int {
	if after >= len(s) {
		return -1
	}
	idx := strings.Index(s[after:], substr)
	if idx == -1 {
		return -1
	}
	return after + idx
}

// ============================================================================
// auto_explain plan extraction
// ============================================================================

// preparedNamesCap caps how many distinct prepared-statement names we
// track per query. JDBC-style workloads usually map a query to a single
// name (or just "<unnamed>"); the cap protects against pathological
// generators that mint a fresh name per execution.
const preparedNamesCap = 16

// slowestRunParamsCap caps the raw "DETAIL: parameters:" payload we
// retain per query stat. Pathological workloads can ship multi-MB
// array literals (observed: 18 MB on a single bind on I.log); without
// a cap we would keep one such string per slowest run and bloat the
// process memory plus every downstream output. Anything beyond the
// cap is truncated; the slowest-run header still pinpoints the original
// log line via timestamp + PID.
const slowestRunParamsCap = 8192

// appendPreparedName adds name to the deduplicated set, preserving
// observation order. Linear scan is fine — the set is bounded by
// preparedNamesCap.
func appendPreparedName(names []string, name string) []string {
	for _, n := range names {
		if n == name {
			return names
		}
	}
	if len(names) >= preparedNamesCap {
		return names
	}
	return append(names, name)
}

// isParamsMessage returns true if the message is a "DETAIL: parameters:"
// continuation. These follow an execute: entry on the same backend and
// carry the actual values bound to the prepared-statement placeholders.
func isParamsMessage(message string) bool {
	if !strings.Contains(message, "parameters:") {
		return false
	}
	return strings.Contains(message, "DETAIL")
}

// extractParameters returns the payload after "parameters:" in a DETAIL line.
// Example input  : "... DETAIL:  parameters: $1 = '393', $2 = '5'"
// Example output : "$1 = '393', $2 = '5'"
func extractParameters(message string) string {
	idx := strings.Index(message, "parameters:")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(message[idx+len("parameters:"):])
}

// isPlanMessage returns true if the message is an auto_explain plan entry.
// These contain "duration:" followed by "plan:" but NOT "statement:" or "execute:".
func isPlanMessage(message string) bool {
	// Fast reject: "plan:" is rare, check it first
	if !strings.Contains(message, "plan:") {
		return false
	}
	durIdx := strings.Index(message, "duration:")
	if durIdx == -1 {
		return false
	}
	rest := message[durIdx:]
	// Exclude normal statement/execute entries that happen to contain "plan" in the query text
	if strings.Contains(rest, "statement:") || strings.Contains(rest, "execute") {
		return false
	}
	return true
}

// extractPlanText extracts the execution plan text from an auto_explain message.
// It strips the "duration: X ms  plan:" prefix and the "Query Text: ..." line.
// Returns the plan nodes text only.
func extractPlanText(message string) string {
	// Find "plan:" marker
	planIdx := strings.Index(message, "plan:")
	if planIdx == -1 {
		return ""
	}
	text := message[planIdx+5:] // Skip "plan:"

	// Skip leading whitespace/newline
	text = strings.TrimLeft(text, " \n\t")

	// Strip "Query Text: ..." line (present in all auto_explain output)
	// It's redundant with the statement: entry
	if strings.HasPrefix(text, "Query Text:") {
		// Find end of Query Text line: next newline or next plan node keyword
		if nlIdx := strings.IndexByte(text, '\n'); nlIdx != -1 {
			// Format with preserved newlines (JSON/CSV)
			text = strings.TrimLeft(text[nlIdx+1:], " \n\t")
		} else {
			// Format without newlines (stderr): find earliest plan node keyword
			planKeywords := []string{
				"Result ", "Seq Scan ", "Index Scan ", "Index Only Scan ",
				"Bitmap ", "Nested Loop", "Hash Join", "Merge Join",
				"Sort ", "Aggregate", "Group", "Limit ", "Append",
				"HashAggregate", "GroupAggregate", "WindowAgg",
				"Subquery Scan", "Materialize", "CTE Scan",
				"{", // JSON plan format
			}
			earliest := -1
			for _, keyword := range planKeywords {
				if idx := strings.Index(text, keyword); idx > 0 && (earliest == -1 || idx < earliest) {
					earliest = idx
				}
			}
			if earliest > 0 {
				text = text[earliest:]
			}
		}
	}

	text = strings.TrimSpace(text)

	// If no newlines (stderr format), reformat for readability
	if !strings.Contains(text, "\n") && len(text) > 0 {
		text = reformatFlatPlan(text)
	}

	return text
}

// reformatFlatPlan re-inserts newlines into a flat plan string (from stderr format
// where continuation lines were joined with spaces). Handles both TEXT and JSON
// auto_explain formats.
func reformatFlatPlan(plan string) string {
	// JSON plan format: detect and pretty-print
	if strings.HasPrefix(strings.TrimSpace(plan), "{") {
		return reformatJSONPlan(plan)
	}

	// TEXT plan format: re-insert newlines with depth tracking
	return reformatTextPlan(plan)
}

// reformatJSONPlan pretty-prints a flat JSON plan string.
func reformatJSONPlan(plan string) string {
	var b strings.Builder
	b.Grow(len(plan) * 2)
	indent := 0
	inString := false

	for i := 0; i < len(plan); i++ {
		c := plan[i]
		if inString {
			b.WriteByte(c)
			if c == '"' && (i == 0 || plan[i-1] != '\\') {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			b.WriteByte(c)
			inString = true
		case '{', '[':
			b.WriteByte(c)
			indent++
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("  ", indent))
		case '}', ']':
			indent--
			if indent < 0 {
				indent = 0
			}
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("  ", indent))
			b.WriteByte(c)
		case ',':
			b.WriteByte(c)
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("  ", indent))
		case ':':
			b.WriteString(": ")
		case ' ':
			// skip extra spaces between tokens
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// planFields lists keywords that start detail lines within a plan node.
var planFields = []string{
	"Output: ",
	"Filter: ",
	"Index Cond: ",
	"Hash Cond: ",
	"Merge Cond: ",
	"Join Filter: ",
	"Recheck Cond: ",
	"Sort Key: ",
	"Sort Method: ",
	"Group Key: ",
	"Rows Removed by ",
	"Buffers: ",
	"Batches: ",
	"Buckets: ",
	"Memory Usage: ",
	"Inner Unique: ",
	"Planning Time: ",
	"Execution Time: ",
	"InitPlan ",
	"SubPlan ",
}

// reformatTextPlan re-inserts newlines into a flat TEXT plan with depth tracking.
func reformatTextPlan(plan string) string {
	var b strings.Builder
	b.Grow(len(plan) + 200)

	depth := 0
	i := 0
	for i < len(plan) {
		// Check for child node marker "->  "
		if i > 0 && i+4 <= len(plan) && plan[i:i+4] == "->  " {
			depth++
			indent := strings.Repeat("  ", depth)
			// Trim trailing space
			s := b.String()
			if len(s) > 0 && s[len(s)-1] == ' ' {
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
			b.WriteString("\n" + indent + "->  ")
			i += 4
			continue
		}

		// Check for plan detail fields
		matched := false
		if i > 0 && plan[i-1] == ' ' {
			for _, field := range planFields {
				if i+len(field) <= len(plan) && plan[i:i+len(field)] == field {
					indent := strings.Repeat("  ", depth+1)
					// Trim trailing space
					s := b.String()
					if len(s) > 0 && s[len(s)-1] == ' ' {
						b.Reset()
						b.WriteString(s[:len(s)-1])
					}
					b.WriteString("\n" + indent + field)
					i += len(field)
					matched = true
					break
				}
			}
		}

		if !matched {
			b.WriteByte(plan[i])
			i++
		}
	}

	return b.String()
}

// ============================================================================
// Queries without duration metrics
// ============================================================================

// CollectQueriesWithoutDuration populates the count of queries identified
// from logs (lock events, tempfile events) but without duration metrics.
// This is useful when logs contain STATEMENT lines but no duration messages.
func CollectQueriesWithoutDuration(sql *SQLMetrics, locks *LockMetrics, tempfiles *TempFileMetrics) {
	seen := make(map[string]bool)

	// Count from locks
	for hash := range locks.QueryStats {
		if _, hasMetrics := sql.QueryStats[hash]; !hasMetrics {
			if !seen[hash] {
				sql.QueriesWithoutDurationCount.FromLocks++
				seen[hash] = true
			}
		}
	}

	// Count from tempfiles
	for hash := range tempfiles.QueryStats {
		if _, hasMetrics := sql.QueryStats[hash]; !hasMetrics {
			if !seen[hash] {
				sql.QueriesWithoutDurationCount.FromTempfiles++
				seen[hash] = true
			}
		}
	}

	sql.QueriesWithoutDurationCount.Total = len(seen)
}
