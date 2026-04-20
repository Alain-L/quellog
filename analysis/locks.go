// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// LockMetrics aggregates statistics about PostgreSQL lock events.
// Locks are tracked when processes wait for or acquire locks on database resources.
type LockMetrics struct {
	// TotalEvents is the total number of lock-related events (waiting + acquired).
	TotalEvents int

	// WaitingEvents is the number of "still waiting" events.
	WaitingEvents int

	// AcquiredEvents is the number of "acquired" events.
	AcquiredEvents int

	// DeadlockEvents is the number of deadlock detection events.
	DeadlockEvents int

	// TotalWaitTime is the cumulative wait time across all lock events in milliseconds.
	TotalWaitTime float64

	// LockTypeStats maps lock types (e.g., "AccessShareLock", "ExclusiveLock") to their event counts.
	LockTypeStats map[string]int

	// ResourceTypeStats maps resource types (e.g., "relation", "transaction", "advisory lock") to their event counts.
	ResourceTypeStats map[string]int

	// RelationStats maps table names (from CONTEXT) to their lock event counts.
	RelationStats map[string]int

	// Events contains individual lock events for timeline analysis.
	Events []LockEvent

	// QueryStats maps normalized queries to their lock statistics.
	QueryStats map[string]*LockQueryStat
}

// LockEvent represents a single lock-related event.
type LockEvent struct {
	// Timestamp is when the lock event occurred.
	Timestamp time.Time

	// EventType is "waiting", "acquired", or "deadlock".
	EventType string

	// LockType is the PostgreSQL lock mode (e.g., "AccessShareLock", "ExclusiveLock").
	LockType string

	// ResourceType is the type of resource being locked (e.g., "relation", "transaction").
	ResourceType string

	// WaitTime is the duration waited for the lock in milliseconds (0 for deadlocks).
	WaitTime float64

	// ProcessID is the PID of the process involved.
	ProcessID string

	// QueryID is the short identifier for the associated query (e.g., "se-abc123").
	// May be empty if the query cannot be identified.
	QueryID string

	// BlockingPID is the PID of the process holding the lock (from DETAIL line).
	BlockingPID string

	// BlockingQueryID is the short identifier for the blocking query, if known.
	BlockingQueryID string

	// BlockingQuery is the normalized text of the blocking query, if known.
	BlockingQuery string

	// Relation is the table name from CONTEXT (e.g., "while locking tuple ... in relation X").
	Relation string
}

// LockQueryStat stores aggregated lock statistics for a single query pattern.
type LockQueryStat struct {
	// RawQuery is the original query text (first occurrence).
	RawQuery string

	// NormalizedQuery is the parameterized version used for grouping.
	NormalizedQuery string

	// AcquiredCount is the number of unique locks that were acquired.
	AcquiredCount int

	// AcquiredWaitTime is the cumulative wait time for acquired locks in milliseconds.
	AcquiredWaitTime float64

	// StillWaitingCount is the number of unique locks still waiting (never acquired).
	StillWaitingCount int

	// StillWaitingTime is the cumulative wait time for locks still waiting in milliseconds.
	StillWaitingTime float64

	// TotalWaitTime is the total wait time (acquired + still waiting) in milliseconds.
	TotalWaitTime float64

	// ID is a short, user-friendly identifier.
	ID string

	// FullHash is the complete hash in hexadecimal.
	FullHash string
}

// ============================================================================
// Lock log patterns
// ============================================================================

// Lock event patterns:
//   - "process 12345 still waiting for AccessShareLock on relation 123 of database 456 after 1000.072 ms"
//   - "process 12345 acquired ShareLock on transaction 789 after 2468.117 ms"
//   - "deadlock detected"

const (
	lockProcessPrefix = "process "
	lockStillWaiting  = "still waiting for "
	lockAcquired      = "acquired "
	lockOnMarker      = " on "
	lockAfterMarker   = " after "
	lockMsSuffix      = " ms"
	lockDeadlock      = "deadlock detected"
	lockBlockingPID   = "Process holding the lock: "
	lockRelationCtx   = "in relation \""
)

// ============================================================================
// Streaming lock analyzer
// ============================================================================

// LockAnalyzer processes log entries to track lock events.
// Locks indicate contention and potential performance issues.
//
// Usage:
//
//	analyzer := NewLockAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type LockAnalyzer struct {
	totalEvents       int
	acquiredEvents    int
	deadlockEvents    int
	totalWaitTime     float64
	lockTypeStats     map[string]int
	resourceTypeStats map[string]int
	relationStats     map[string]int

	// Pre-allocated structures (initialized at creation)
	events             []LockEvent
	queryStats         map[string]*LockQueryStat
	lastQueryByPID     map[string]string
	pendingBlockingPID map[string]string // Maps waiting PID → blocking PID (from DETAIL line)
	activeLocks        map[string]*activeLock

	// State machine optimization (like tempfiles)
	locksExist bool // True once we've seen at least one lock event
}

// activeLock tracks an individual lock to avoid counting the same wait time multiple times.
// PostgreSQL emits multiple "still waiting" messages followed by one "acquired" message.
type activeLock struct {
	processID      string
	lockType       string
	resource       string
	lastWaitTime   float64 // Most recent wait time reported
	acquired       bool    // Whether this lock was acquired
	query          string  // Associated query if known
	waitingEventID int     // Index into events slice for the waiting event (-1 if none)
	blockingPID    string  // PID of the process holding the lock
	relation       string  // Table name from CONTEXT
}

// NewLockAnalyzer creates a new lock event analyzer.
func NewLockAnalyzer() *LockAnalyzer {
	return &LockAnalyzer{
		lockTypeStats:      make(map[string]int, 20),
		resourceTypeStats:  make(map[string]int, 10),
		relationStats:      make(map[string]int, 50),
		events:             make([]LockEvent, 0, 1000),
		queryStats:         make(map[string]*LockQueryStat, 100),
		lastQueryByPID:     make(map[string]string, 100),
		pendingBlockingPID: make(map[string]string, 50),
		activeLocks:        make(map[string]*activeLock, 200),
		locksExist:         false,
	}
}

// Process analyzes a single log entry for lock events.
//
// Expected log formats:
//
//	LOG: process 12345 still waiting for AccessShareLock on relation 123 of database 456 after 1000.072 ms
//	DETAIL: Process holding the lock: 12344. Wait queue: 12345.
//	STATEMENT: SELECT * FROM table WHERE ...
//
//	LOG: process 12345 acquired ShareLock on transaction 789 after 2468.117 ms
//	STATEMENT: SELECT * FROM table WHERE ...
//
//	ERROR: deadlock detected
//	DETAIL: Process 12345 waits for ShareLock on transaction 789; blocked by process 12346.
func (a *LockAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	// Fast path: reject lines too short to carry any lock pattern.
	if len(msg) < 20 {
		return
	}

	// State-machine short-circuit: until we've seen the first lock event,
	// only look for lock events (skip STATEMENT / DETAIL / CONTEXT lines).
	// This saves a lot of work on logs that contain no lock contention.
	if !a.locksExist && !a.isFirstLockEvent(msg) {
		return
	}

	// Classify what patterns this line carries.
	f := a.detectPatterns(msg)
	if !f.relevant() {
		return
	}

	// Dispatch. Each handler below performs its own bookkeeping and may
	// return early — Process itself stays linear and readable.
	if f.hasBlockingDetail {
		a.processBlockingDetail(entry, msg)
		return
	}
	if f.hasRelationCtx {
		a.processRelationContext(entry, msg)
		return
	}
	if f.hasStatement || f.hasQuery {
		a.processQueryContinuation(entry, msg)
		// A STATEMENT-only line (no lock event on the same line) is done.
		// CSV embeds QUERY: in the lock message itself, so we continue.
		if f.hasStatement && !f.hasLockWaiting && !f.hasLockAcquired && !f.hasDeadlock {
			return
		}
	}
	if f.hasDeadlock && strings.Contains(msg, "ERROR:") {
		a.processDeadlock(entry)
		return
	}
	if f.hasLockWaiting || f.hasLockAcquired {
		a.processLockEvent(entry, msg, f.hasLockWaiting)
	}
}

// isFirstLockEvent reports whether msg is the very first lock event the
// analyzer has ever seen (used to flip locksExist). Encapsulates the
// cheap pre-filters ("ss " / "ock") that cut 99%+ of unrelated lines.
func (a *LockAnalyzer) isFirstLockEvent(msg string) bool {
	hasSS := strings.Contains(msg, "ss ")
	hasOck := strings.Contains(msg, "ock")
	if !hasSS && !hasOck {
		return false
	}

	processIdx, deadlockIdx := -1, -1
	if hasSS {
		processIdx = strings.Index(msg, "process ")
	}
	if hasOck {
		deadlockIdx = strings.Index(msg, "deadlock")
	}
	if processIdx == -1 && deadlockIdx == -1 {
		return false
	}

	if processIdx >= 0 {
		if strings.Contains(msg, lockStillWaiting) || strings.Contains(msg, lockAcquired) {
			a.locksExist = true
			return true
		}
	}
	if deadlockIdx >= 0 {
		if strings.Contains(msg, lockDeadlock) {
			a.locksExist = true
			return true
		}
	}
	return false
}

// lockPatternFlags summarises which patterns a single message carries.
// Cheaper to compute once at the top of Process than to re-scan in
// every handler.
type lockPatternFlags struct {
	hasLockWaiting    bool
	hasLockAcquired   bool
	hasDeadlock       bool
	hasStatement      bool // STATEMENT: (stderr) or statement: after duration:
	hasQuery          bool // QUERY: (CSV-style)
	hasBlockingDetail bool // DETAIL: Process holding the lock: ...
	hasRelationCtx    bool // CONTEXT: ... in relation "..."
}

func (f lockPatternFlags) relevant() bool {
	return f.hasLockWaiting || f.hasLockAcquired || f.hasDeadlock ||
		f.hasStatement || f.hasQuery ||
		f.hasBlockingDetail || f.hasRelationCtx
}

func (a *LockAnalyzer) detectPatterns(msg string) lockPatternFlags {
	var f lockPatternFlags
	f.hasLockWaiting = strings.Contains(msg, lockStillWaiting)
	f.hasLockAcquired = strings.Contains(msg, lockAcquired)
	f.hasDeadlock = strings.Contains(msg, lockDeadlock)

	if a.locksExist {
		// Check for "TATEMENT:" (covers STATEMENT: and statement:)
		if idx := strings.Index(msg, "TATEMENT:"); idx >= 0 {
			if idx == 0 || msg[idx-1] == 'S' || msg[idx-1] == 's' {
				f.hasStatement = true
			}
		}
		if !f.hasStatement && strings.Contains(msg, "QUERY:") {
			f.hasQuery = true
		}
		if strings.Contains(msg, lockBlockingPID) {
			f.hasBlockingDetail = true
		}
		if strings.Contains(msg, lockRelationCtx) {
			f.hasRelationCtx = true
		}
	}
	return f
}

// processBlockingDetail handles a "DETAIL: Process holding the lock: N.
// Wait queue: M." line. DETAIL arrives AFTER the "still waiting" entry
// for the same PID; we update that event in place.
func (a *LockAnalyzer) processBlockingDetail(entry *parser.LogEntry, msg string) {
	bPID := extractBlockingPID(msg)
	if bPID == "" {
		return
	}
	waitingPID := entry.PID
	if waitingPID == "" {
		return
	}

	// Update the last waiting event for this PID.
	for i := len(a.events) - 1; i >= 0; i-- {
		if a.events[i].ProcessID == waitingPID && a.events[i].EventType == "waiting" {
			a.events[i].BlockingPID = bPID
			// Try to resolve blocking query (text + ID together).
			if bQuery, ok := a.lastQueryByPID[bPID]; ok {
				normalized := normalizeQuery(bQuery)
				a.events[i].BlockingQueryID, _ = GenerateQueryID(bQuery, normalized)
				a.events[i].BlockingQuery = normalized
			}
			break
		}
	}
	// Mirror the blocking PID into the active lock map.
	for _, lock := range a.activeLocks {
		if lock.processID == waitingPID && !lock.acquired {
			lock.blockingPID = bPID
			break
		}
	}
}

// processRelationContext handles a "CONTEXT: ... in relation \"X\"" line.
// Fills the Relation field on the most recent waiting event and lock
// for the same PID.
func (a *LockAnalyzer) processRelationContext(entry *parser.LogEntry, msg string) {
	rel := extractRelation(msg)
	if rel == "" {
		return
	}
	pid := entry.PID
	if pid == "" {
		return
	}

	for i := len(a.events) - 1; i >= 0; i-- {
		if a.events[i].ProcessID == pid && a.events[i].EventType == "waiting" && a.events[i].Relation == "" {
			a.events[i].Relation = rel
			break
		}
	}
	for _, lock := range a.activeLocks {
		if lock.processID == pid && lock.relation == "" {
			lock.relation = rel
			a.relationStats[rel]++
			break
		}
	}
}

// processQueryContinuation extracts the query text from STATEMENT: /
// statement: / QUERY: lines and caches it under the PID so subsequent
// lock events can associate a query_id. Also back-fills any active lock
// for the same PID that was missing a query when the wait started.
func (a *LockAnalyzer) processQueryContinuation(entry *parser.LogEntry, msg string) {
	var query string

	// Method 1: "duration: X ms statement: QUERY" (single-line log_min_duration)
	if durationIdx := strings.Index(msg, "duration:"); durationIdx >= 0 {
		if idx := strings.Index(msg, "statement:"); idx != -1 {
			query = strings.TrimSpace(msg[idx+10:])
		}
	}
	// Method 2: standalone STATEMENT: or statement: line
	if query == "" {
		if idx := strings.Index(msg, "STATEMENT:"); idx != -1 {
			query = strings.TrimSpace(msg[idx+10:])
		} else if idx := strings.Index(msg, "statement:"); idx != -1 {
			query = strings.TrimSpace(msg[idx+10:])
		}
	}
	// Method 3: QUERY: (used by the CSV parser)
	if query == "" {
		if idx := strings.Index(msg, "QUERY:"); idx != -1 {
			query = strings.TrimSpace(msg[idx+6:])
			// QUERY: may be followed by CONTEXT:, strip it.
			if ctxIdx := strings.Index(query, " CONTEXT:"); ctxIdx != -1 {
				query = strings.TrimSpace(query[:ctxIdx])
			}
		}
	}

	if query == "" {
		return
	}
	pid := entry.PID
	if pid == "" {
		return
	}

	// Normalize whitespace (newlines to spaces) so the raw_query stays
	// consistent across stderr/CSV/JSON formats.
	query = normalizeWhitespace(query)
	a.lastQueryByPID[pid] = query

	// Back-fill any active lock for this PID that was missing a query.
	for _, lock := range a.activeLocks {
		if lock.processID == pid && lock.query == "" {
			lock.query = query
			if lock.waitingEventID >= 0 && lock.waitingEventID < len(a.events) {
				normalized := normalizeQuery(query)
				queryID, _ := GenerateQueryID(query, normalized)
				a.events[lock.waitingEventID].QueryID = queryID
			}
		}
	}
}

// processDeadlock handles an "ERROR: deadlock detected" line. A deadlock
// is a failed lock acquisition; we promote the matching waiting event
// for this PID instead of creating a new event, so total_events stays
// the canonical count.
func (a *LockAnalyzer) processDeadlock(entry *parser.LogEntry) {
	a.deadlockEvents++
	pid := entry.PID
	if pid == "" {
		return
	}
	for i := len(a.events) - 1; i >= 0; i-- {
		if a.events[i].ProcessID == pid && a.events[i].EventType == "waiting" {
			a.events[i].EventType = "deadlock"
			break
		}
	}
}

// processLockEvent handles the core "process X still waiting ..." and
// "process X acquired ..." lines: parses the fields, maintains the
// activeLocks map, deduplicates repeated "still waiting" messages for
// the same lock, and emits LockEvents.
func (a *LockAnalyzer) processLockEvent(entry *parser.LogEntry, msg string, isWaiting bool) {
	processID, lockType, resource, waitTime, eventType, ok := parseLockEvent(msg, isWaiting)
	if !ok {
		return
	}

	resourceType := extractResourceType(resource)
	lockKey := processID + "-" + lockType + "-" + resource

	blockingPID := extractBlockingPID(msg)
	relation := extractRelation(msg)

	if eventType == "waiting" {
		a.handleWaiting(entry, processID, lockType, resource, resourceType, lockKey, waitTime, blockingPID, relation)
		return
	}
	a.handleAcquired(entry, processID, lockType, resource, resourceType, lockKey, waitTime, blockingPID, relation)
}

// handleWaiting is the "still waiting" branch of processLockEvent.
// Creates or updates the activeLock entry and emits a "waiting" event.
func (a *LockAnalyzer) handleWaiting(
	entry *parser.LogEntry,
	processID, lockType, resource, resourceType, lockKey string,
	waitTime float64,
	blockingPID, relation string,
) {
	lock, exists := a.activeLocks[lockKey]
	if !exists {
		lock = &activeLock{
			processID:      processID,
			lockType:       lockType,
			resource:       resource,
			lastWaitTime:   waitTime,
			acquired:       false,
			waitingEventID: -1, // Will be set after we append the event.
			blockingPID:    blockingPID,
			relation:       relation,
		}
		if query, ok := a.lastQueryByPID[processID]; ok {
			lock.query = query
		}
		a.activeLocks[lockKey] = lock

		// Count only on first occurrence of this lock.
		a.totalEvents++
		a.lockTypeStats[lockType]++
		a.resourceTypeStats[resourceType]++
		if relation != "" {
			a.relationStats[relation]++
		}
	} else {
		// Repeated "still waiting" for same lock — refresh wait time only.
		lock.lastWaitTime = waitTime
	}

	queryID := ""
	if lock.query != "" {
		normalized := normalizeQuery(lock.query)
		queryID, _ = GenerateQueryID(lock.query, normalized)
	}

	blockingQueryID := ""
	if blockingPID != "" {
		if bQuery, ok := a.lastQueryByPID[blockingPID]; ok {
			normalized := normalizeQuery(bQuery)
			blockingQueryID, _ = GenerateQueryID(bQuery, normalized)
		}
	}

	eventIdx := len(a.events)
	a.events = append(a.events, LockEvent{
		Timestamp:       entry.Timestamp,
		EventType:       "waiting",
		LockType:        lockType,
		ResourceType:    resourceType,
		WaitTime:        waitTime,
		ProcessID:       processID,
		QueryID:         queryID,
		BlockingPID:     blockingPID,
		BlockingQueryID: blockingQueryID,
		Relation:        relation,
	})
	// Remember the event index so a later STATEMENT line can update
	// query_id in place.
	lock.waitingEventID = eventIdx
}

// handleAcquired is the "acquired" branch of processLockEvent.
// Either promotes an existing waiting lock, or creates a new one
// (fast acquisition without a prior "still waiting" message).
func (a *LockAnalyzer) handleAcquired(
	entry *parser.LogEntry,
	processID, lockType, resource, resourceType, lockKey string,
	waitTime float64,
	blockingPID, relation string,
) {
	lock, exists := a.activeLocks[lockKey]
	if exists {
		lock.acquired = true
		lock.lastWaitTime = waitTime
	} else {
		lock = &activeLock{
			processID:      processID,
			lockType:       lockType,
			resource:       resource,
			lastWaitTime:   waitTime,
			acquired:       true,
			waitingEventID: -1,
		}
		if query, ok := a.lastQueryByPID[processID]; ok {
			lock.query = query
		}
		a.activeLocks[lockKey] = lock
	}

	a.totalWaitTime += waitTime
	a.acquiredEvents++
	if !exists {
		// Direct acquisition (no prior "waiting") — count as a new lock.
		a.totalEvents++
		a.lockTypeStats[lockType]++
		a.resourceTypeStats[resourceType]++
		if relation != "" {
			a.relationStats[relation]++
		}
	}

	queryID := ""
	if lock.query != "" {
		normalized := normalizeQuery(lock.query)
		queryID, _ = GenerateQueryID(lock.query, normalized)
	}

	// Prefer the blocking PID captured during the waiting phase if the
	// acquired message doesn't carry one of its own.
	acquiredBlockingPID := blockingPID
	if lock.blockingPID != "" && acquiredBlockingPID == "" {
		acquiredBlockingPID = lock.blockingPID
	}
	acquiredRelation := relation
	if lock.relation != "" && acquiredRelation == "" {
		acquiredRelation = lock.relation
	}

	a.events = append(a.events, LockEvent{
		Timestamp:    entry.Timestamp,
		EventType:    "acquired",
		LockType:     lockType,
		ResourceType: resourceType,
		WaitTime:     waitTime,
		ProcessID:    processID,
		QueryID:      queryID,
		BlockingPID:  acquiredBlockingPID,
		Relation:     acquiredRelation,
	})
}

// parseLockEvent parses a lock waiting/acquired message using string operations.
// Returns: processID, lockType, resource, waitTime, eventType, ok
func parseLockEvent(msg string, isWaiting bool) (string, string, string, float64, string, bool) {
	// Find "process XXX"
	procIdx := strings.Index(msg, lockProcessPrefix)
	if procIdx == -1 {
		return "", "", "", 0, "", false
	}

	// Extract PID
	pidStart := procIdx + len(lockProcessPrefix)
	pidEnd := pidStart
	for pidEnd < len(msg) && msg[pidEnd] >= '0' && msg[pidEnd] <= '9' {
		pidEnd++
	}
	if pidEnd == pidStart {
		return "", "", "", 0, "", false
	}
	processID := msg[pidStart:pidEnd]

	var markerIdx int
	var eventType string

	if isWaiting {
		markerIdx = strings.Index(msg[pidEnd:], lockStillWaiting)
		if markerIdx == -1 {
			return "", "", "", 0, "", false
		}
		markerIdx += pidEnd
		eventType = "waiting"
	} else {
		markerIdx = strings.Index(msg[pidEnd:], lockAcquired)
		if markerIdx == -1 {
			return "", "", "", 0, "", false
		}
		markerIdx += pidEnd
		eventType = "acquired"
	}

	// Extract lock type (between marker and " on ")
	lockTypeStart := markerIdx + len(lockStillWaiting)
	if !isWaiting {
		lockTypeStart = markerIdx + len(lockAcquired)
	}

	onIdx := strings.Index(msg[lockTypeStart:], lockOnMarker)
	if onIdx == -1 {
		return "", "", "", 0, "", false
	}
	lockType := msg[lockTypeStart : lockTypeStart+onIdx]

	// Extract resource (between " on " and " after ")
	resourceStart := lockTypeStart + onIdx + len(lockOnMarker)
	afterIdx := strings.Index(msg[resourceStart:], lockAfterMarker)
	if afterIdx == -1 {
		return "", "", "", 0, "", false
	}
	resource := msg[resourceStart : resourceStart+afterIdx]

	// Extract wait time (after " after ", before " ms")
	waitTimeStart := resourceStart + afterIdx + len(lockAfterMarker)
	waitTimeEnd := waitTimeStart
	hasDot := false
	for waitTimeEnd < len(msg) {
		ch := msg[waitTimeEnd]
		if ch >= '0' && ch <= '9' {
			waitTimeEnd++
		} else if ch == '.' && !hasDot {
			hasDot = true
			waitTimeEnd++
		} else {
			break
		}
	}

	if waitTimeEnd == waitTimeStart {
		return "", "", "", 0, "", false
	}

	waitTime, err := strconv.ParseFloat(msg[waitTimeStart:waitTimeEnd], 64)
	if err != nil {
		return "", "", "", 0, "", false
	}

	return processID, lockType, resource, waitTime, eventType, true
}

// extractResourceType extracts the resource type from the resource description.
// Examples:
//   - "relation 123 of database 456" -> "relation"
//   - "transaction 789" -> "transaction"
//   - "advisory lock [16385,929248354,809055841,1]" -> "advisory lock"
func extractResourceType(resource string) string {
	if len(resource) < 4 {
		return "unknown"
	}

	// Use first character for quick branching
	switch resource[0] {
	case 'r':
		if len(resource) >= 9 && resource[1] == 'e' && resource[:9] == "relation " {
			return "relation"
		}
	case 't':
		if len(resource) >= 12 && resource[1] == 'r' && resource[:12] == "transaction " {
			return "transaction"
		}
		if len(resource) >= 6 && resource[1] == 'u' && resource[:6] == "tuple " {
			return "tuple"
		}
	case 'a':
		if len(resource) >= 14 && resource[1] == 'd' && resource[:14] == "advisory lock " {
			return "advisory lock"
		}
	case 'p':
		if len(resource) >= 5 && resource[:5] == "page " {
			return "page"
		}
	case 'e':
		if len(resource) >= 7 && resource[:7] == "extend " {
			return "extend"
		}
	}

	// Default: return the first word
	spaceIdx := strings.IndexByte(resource, ' ')
	if spaceIdx > 0 {
		return resource[:spaceIdx]
	}

	return resource
}

// extractBlockingPID extracts the blocking process ID from a DETAIL line.
// Format: "Process holding the lock: 101. Wait queue: 102."
// Returns empty string if not found.
func extractBlockingPID(msg string) string {
	idx := strings.Index(msg, lockBlockingPID)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(lockBlockingPID):]
	end := strings.IndexByte(rest, '.')
	if end <= 0 {
		return ""
	}
	pid := rest[:end]
	// Validate it's numeric
	for i := 0; i < len(pid); i++ {
		if pid[i] < '0' || pid[i] > '9' {
			return ""
		}
	}
	return pid
}

func extractRelation(msg string) string {
	idx := strings.Index(msg, lockRelationCtx)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(lockRelationCtx):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

// Finalize returns the aggregated lock metrics.
// This should be called after all log entries have been processed.
func (a *LockAnalyzer) Finalize() LockMetrics {
	// If no locks were found, return empty metrics
	if !a.locksExist {
		return LockMetrics{
			TotalEvents:       0,
			WaitingEvents:     0,
			AcquiredEvents:    0,
			DeadlockEvents:    0,
			TotalWaitTime:     0,
			LockTypeStats:     make(map[string]int),
			ResourceTypeStats: make(map[string]int),
			Events:            nil,
			QueryStats:        nil,
		}
	}

	// Resolve blocking query IDs that weren't available during streaming.
	// By now all entries have been processed, so lastQueryByPID is complete.
	// Only resolve events that have NO blocking query yet — events resolved
	// during streaming already have the correct query from that point in time.
	for i := range a.events {
		if a.events[i].BlockingPID != "" && a.events[i].BlockingQueryID == "" {
			if bQuery, ok := a.lastQueryByPID[a.events[i].BlockingPID]; ok {
				normalized := normalizeQuery(bQuery)
				a.events[i].BlockingQueryID, _ = GenerateQueryID(bQuery, normalized)
				a.events[i].BlockingQuery = normalized
			}
		}
	}

	// Build query statistics from individual locks
	queryStats := make(map[string]*LockQueryStat, 100)

	for _, lock := range a.activeLocks {
		// Note: We do NOT add wait time for locks that were never acquired to the global total.
		// We only count completed (acquired) locks in the global total wait time.
		// Never-acquired locks are tracked separately in StillWaitingTime per query.

		// Associate lock with query if known
		if lock.query != "" {
			normalized := normalizeQuery(lock.query)
			shortID, fullHash := GenerateQueryID(lock.query, normalized)

			stat, exists := queryStats[fullHash]
			if !exists {
				stat = &LockQueryStat{
					RawQuery:          lock.query,
					NormalizedQuery:   normalized,
					AcquiredCount:     0,
					AcquiredWaitTime:  0,
					StillWaitingCount: 0,
					StillWaitingTime:  0,
					TotalWaitTime:     0,
					ID:                shortID,
					FullHash:          fullHash,
				}
				queryStats[fullHash] = stat
			} else {
				// For deterministic JSON output, always keep the alphabetically first raw query
				if lock.query < stat.RawQuery {
					stat.RawQuery = lock.query
				}
			}

			// Add this lock's wait time (counted once per lock, not per event)
			// Separate acquired vs still waiting
			if lock.acquired {
				stat.AcquiredCount++
				stat.AcquiredWaitTime += lock.lastWaitTime
			} else {
				stat.StillWaitingCount++
				stat.StillWaitingTime += lock.lastWaitTime
			}

			stat.TotalWaitTime += lock.lastWaitTime
		}
	}

	stillWaiting := a.totalEvents - a.acquiredEvents
	if stillWaiting < 0 {
		stillWaiting = 0
	}

	return LockMetrics{
		TotalEvents:       a.totalEvents,
		WaitingEvents:     stillWaiting,
		AcquiredEvents:    a.acquiredEvents,
		DeadlockEvents:    a.deadlockEvents,
		TotalWaitTime:     a.totalWaitTime,
		LockTypeStats:     a.lockTypeStats,
		ResourceTypeStats: a.resourceTypeStats,
		RelationStats:     a.relationStats,
		Events:            a.events,
		QueryStats:        queryStats,
	}
}
