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
	lockProcessPrefix    = "process "
	lockStillWaiting     = "still waiting for "
	lockAcquired         = "acquired "
	lockOnMarker         = " on "
	lockAfterMarker      = " after "
	lockMsSuffix         = " ms"
	lockDeadlock         = "deadlock detected"
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
	waitingEvents     int
	acquiredEvents    int
	deadlockEvents    int
	totalWaitTime     float64
	lockTypeStats     map[string]int
	resourceTypeStats map[string]int

	// Pre-allocated structures (initialized at creation)
	events         []LockEvent
	queryStats     map[string]*LockQueryStat
	lastQueryByPID map[string]string
	activeLocks    map[string]*activeLock

	// State machine optimization (like tempfiles)
	locksExist bool // True once we've seen at least one lock event
}

// activeLock tracks an individual lock to avoid counting the same wait time multiple times.
// PostgreSQL emits multiple "still waiting" messages followed by one "acquired" message.
type activeLock struct {
	processID    string
	lockType     string
	resource     string
	lastWaitTime float64 // Most recent wait time reported
	acquired     bool    // Whether this lock was acquired
	query        string  // Associated query if known
}

// NewLockAnalyzer creates a new lock event analyzer.
func NewLockAnalyzer() *LockAnalyzer {
	return &LockAnalyzer{
		lockTypeStats:     make(map[string]int, 20),
		resourceTypeStats: make(map[string]int, 10),
		events:            make([]LockEvent, 0, 1000),
		queryStats:        make(map[string]*LockQueryStat, 100),
		lastQueryByPID:    make(map[string]string, 100),
		activeLocks:       make(map[string]*activeLock, 200),
		locksExist:        false,
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

	// Fast path optimization: quick reject if message is too short
	if len(msg) < 20 {
		return
	}

	// State machine: if no locks seen yet, only look for lock events (not statements)
	// This dramatically reduces overhead for logs without locks
	if !a.locksExist {
		// OPTIMIZATION: Use IndexByte as ultra-fast pre-filter before expensive Index
		// strings.IndexByte is ~10-20x faster than strings.Index (SIMD vectorization)
		// This saves ~4s on I12.log (11GB, 62M lines) with zero locks
		hasP := strings.IndexByte(msg, 'p') >= 0
		hasD := strings.IndexByte(msg, 'd') >= 0

		// Quick reject if neither 'p' (process) nor 'd' (deadlock) present
		if !hasP && !hasD {
			return
		}

		// Now do the more expensive Index checks only if candidate chars found
		var processIdx, deadlockIdx int = -1, -1
		if hasP {
			processIdx = strings.Index(msg, "process ")
		}
		if hasD {
			deadlockIdx = strings.Index(msg, "deadlock")
		}

		// Quick reject if neither pattern found
		if processIdx == -1 && deadlockIdx == -1 {
			return
		}

		// Now check full lock patterns (we know at least one keyword exists)
		if processIdx >= 0 {
			// Check if it's a lock waiting or acquired message
			if strings.Contains(msg, lockStillWaiting) || strings.Contains(msg, lockAcquired) {
				a.locksExist = true
				// Fall through to full processing
			} else {
				return
			}
		} else if deadlockIdx >= 0 {
			// Check if it's a deadlock message
			if strings.Contains(msg, lockDeadlock) {
				a.locksExist = true
				// Fall through to full processing
			} else {
				return
			}
		} else {
			return
		}
	}

	// Now check specific patterns (only after first lock seen)
	hasLockWaiting := strings.Index(msg, lockStillWaiting) >= 0
	hasLockAcquired := strings.Index(msg, lockAcquired) >= 0
	hasDeadlock := strings.Index(msg, lockDeadlock) >= 0

	// OPTIMIZATION 3: Skip STATEMENT parsing until locks are actually seen
	// This avoids filling lastQueryByPID unnecessarily
	hasStatement := false
	if a.locksExist {
		// OPTIMIZATION: Check for "TATEMENT:" instead of two separate Contains calls
		// This pattern appears in both "STATEMENT:" and "statement:"
		if idx := strings.Index(msg, "TATEMENT:"); idx >= 0 {
			// Verify it's actually STATEMENT or statement (check preceding char)
			if idx == 0 || msg[idx-1] == 'S' || msg[idx-1] == 's' {
				hasStatement = true
			}
		}
	}

	// Skip if nothing relevant
	if !hasLockWaiting && !hasLockAcquired && !hasDeadlock && !hasStatement {
		return
	}

	// === STEP 1: Handle STATEMENT lines (cache queries) ===
	if hasStatement {
		var query string
		var pid string

		// Method 1: duration: X ms statement: QUERY
		// OPTIMIZATION: Use Index instead of Contains
		if durationIdx := strings.Index(msg, "duration:"); durationIdx >= 0 {
			if idx := strings.Index(msg, "statement:"); idx != -1 {
				query = strings.TrimSpace(msg[idx+10:])
			}
		}

		// Method 2: Standalone STATEMENT: line
		if query == "" {
			if idx := strings.Index(msg, "STATEMENT:"); idx != -1 {
				query = strings.TrimSpace(msg[idx+10:])
			} else if idx := strings.Index(msg, "statement:"); idx != -1 {
				query = strings.TrimSpace(msg[idx+10:])
			}
		}

		// Extract PID and cache query
		if query != "" {
			pid = parser.ExtractPID(msg)
			if pid != "" {
				a.lastQueryByPID[pid] = query

				// Also update any active locks for this PID that don't have a query yet
				for _, lock := range a.activeLocks {
					if lock.processID == pid && lock.query == "" {
						lock.query = query
					}
				}
			}
		}
		return
	}

	// === STEP 2: Handle deadlock detection ===
	if hasDeadlock && strings.HasPrefix(msg, "ERROR:") {
		a.deadlockEvents++
		a.totalEvents++
		pid := parser.ExtractPID(msg)

		// Generate QueryID if query is known
		queryID := ""
		if query, ok := a.lastQueryByPID[pid]; ok && query != "" {
			normalized := normalizeQuery(query)
			queryID, _ = GenerateQueryID(query, normalized)
		}

		a.events = append(a.events, LockEvent{
			Timestamp:  entry.Timestamp,
			EventType:  "deadlock",
			LockType:   "",
			WaitTime:   0,
			ProcessID:  pid,
			QueryID:    queryID,
		})
		return
	}

	// === STEP 3: Parse lock messages (waiting or acquired) ===
	if hasLockWaiting || hasLockAcquired {
		processID, lockType, resource, waitTime, eventType, ok := parseLockEvent(msg, hasLockWaiting)
		if !ok {
			return
		}

		resourceType := extractResourceType(resource)
		lockKey := processID + "-" + lockType + "-" + resource

		if eventType == "waiting" {
			lock, exists := a.activeLocks[lockKey]
			if !exists {
				// First time seeing this lock
				lock = &activeLock{
					processID:    processID,
					lockType:     lockType,
					resource:     resource,
					lastWaitTime: waitTime,
					acquired:     false,
				}
				// Get associated query if available
				if query, ok := a.lastQueryByPID[processID]; ok {
					lock.query = query
				}
				a.activeLocks[lockKey] = lock
			} else {
				// Update the wait time (don't add to total yet)
				lock.lastWaitTime = waitTime
			}

			a.waitingEvents++
			a.totalEvents++
			a.lockTypeStats[lockType]++
			a.resourceTypeStats[resourceType]++

			// Generate QueryID if query is known
			queryID := ""
			if lock.query != "" {
				normalized := normalizeQuery(lock.query)
				queryID, _ = GenerateQueryID(lock.query, normalized)
			}

			a.events = append(a.events, LockEvent{
				Timestamp:    entry.Timestamp,
				EventType:    "waiting",
				LockType:     lockType,
				ResourceType: resourceType,
				WaitTime:     waitTime,
				ProcessID:    processID,
				QueryID:      queryID,
			})
		} else { // "acquired"
			lock, exists := a.activeLocks[lockKey]
			if exists {
				// Lock was previously waiting, mark as acquired
				lock.acquired = true
				lock.lastWaitTime = waitTime
			} else {
				// Lock acquired without prior "waiting" message (fast acquisition)
				lock = &activeLock{
					processID:    processID,
					lockType:     lockType,
					resource:     resource,
					lastWaitTime: waitTime,
					acquired:     true,
				}
				// Get associated query if available
				if query, ok := a.lastQueryByPID[processID]; ok {
					lock.query = query
				}
				a.activeLocks[lockKey] = lock
			}

			// Add the final wait time to total (this is the real total time for this lock)
			a.totalWaitTime += waitTime

			a.acquiredEvents++
			a.totalEvents++
			a.lockTypeStats[lockType]++
			a.resourceTypeStats[resourceType]++

			// Generate QueryID if query is known
			queryID := ""
			if lock != nil && lock.query != "" {
				normalized := normalizeQuery(lock.query)
				queryID, _ = GenerateQueryID(lock.query, normalized)
			}

			a.events = append(a.events, LockEvent{
				Timestamp:    entry.Timestamp,
				EventType:    "acquired",
				LockType:     lockType,
				ResourceType: resourceType,
				WaitTime:     waitTime,
				ProcessID:    processID,
				QueryID:      queryID,
			})
		}
	}
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

	return LockMetrics{
		TotalEvents:       a.totalEvents,
		WaitingEvents:     a.waitingEvents,
		AcquiredEvents:    a.acquiredEvents,
		DeadlockEvents:    a.deadlockEvents,
		TotalWaitTime:     a.totalWaitTime,
		LockTypeStats:     a.lockTypeStats,
		ResourceTypeStats: a.resourceTypeStats,
		Events:            a.events,
		QueryStats:        queryStats,
	}
}
