// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"regexp"
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

var (
	// lockWaitingPattern matches "still waiting" messages
	lockWaitingPattern = regexp.MustCompile(`process (\d+) still waiting for (\w+) on (.+) after ([\d.]+) ms`)

	// lockAcquiredPattern matches "acquired" messages
	lockAcquiredPattern = regexp.MustCompile(`process (\d+) acquired (\w+) on (.+) after ([\d.]+) ms`)

	// deadlockPattern matches deadlock detection messages
	deadlockPattern = regexp.MustCompile(`deadlock detected`)
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
	events            []LockEvent
	queryStats        map[string]*LockQueryStat

	// Query association state
	lastQueryByPID map[string]string // Most recent query text seen for each PID

	// Track individual locks to avoid double-counting wait times
	activeLocks map[string]*activeLock // key: "PID-LockType-Resource"
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

	// Fast path optimization: quick reject if message is too short or doesn't contain key markers
	if len(msg) < 20 {
		return
	}

	// Cache query text for later association with lock events
	// Handle both "duration: ... statement: ..." and standalone "STATEMENT:" lines
	if strings.Contains(msg, "statement:") || strings.Contains(msg, "STATEMENT:") {
		var query string
		var pid string

		// Method 1: duration: X ms statement: QUERY
		if strings.Contains(msg, "duration:") {
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

		// Extract PID from message (format: "[12345]: ..." or "process 12345")
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

	// Check for deadlock detection
	if deadlockPattern.MatchString(msg) && strings.HasPrefix(msg, "ERROR:") {
		a.deadlockEvents++
		a.totalEvents++
		pid := parser.ExtractPID(msg)
		a.events = append(a.events, LockEvent{
			Timestamp:  entry.Timestamp,
			EventType:  "deadlock",
			LockType:   "",
			WaitTime:   0,
			ProcessID:  pid,
		})
		return
	}

	// Check for "still waiting" pattern
	if matches := lockWaitingPattern.FindStringSubmatch(msg); matches != nil {
		processID := matches[1]
		lockType := matches[2]
		resource := matches[3]
		waitTime, _ := strconv.ParseFloat(matches[4], 64)

		resourceType := extractResourceType(resource)

		// Track this lock to avoid double-counting
		lockKey := processID + "-" + lockType + "-" + resource
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

		a.events = append(a.events, LockEvent{
			Timestamp:    entry.Timestamp,
			EventType:    "waiting",
			LockType:     lockType,
			ResourceType: resourceType,
			WaitTime:     waitTime,
			ProcessID:    processID,
		})

		return
	}

	// Check for "acquired" pattern
	if matches := lockAcquiredPattern.FindStringSubmatch(msg); matches != nil {
		processID := matches[1]
		lockType := matches[2]
		resource := matches[3]
		waitTime, _ := strconv.ParseFloat(matches[4], 64)

		resourceType := extractResourceType(resource)

		// Track this lock and mark as acquired
		lockKey := processID + "-" + lockType + "-" + resource
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

		a.events = append(a.events, LockEvent{
			Timestamp:    entry.Timestamp,
			EventType:    "acquired",
			LockType:     lockType,
			ResourceType: resourceType,
			WaitTime:     waitTime,
			ProcessID:    processID,
		})

		return
	}
}

// extractResourceType extracts the resource type from the resource description.
// Examples:
//   - "relation 123 of database 456" -> "relation"
//   - "transaction 789" -> "transaction"
//   - "advisory lock [16385,929248354,809055841,1]" -> "advisory lock"
func extractResourceType(resource string) string {
	if strings.HasPrefix(resource, "relation ") {
		return "relation"
	}
	if strings.HasPrefix(resource, "transaction ") {
		return "transaction"
	}
	if strings.HasPrefix(resource, "advisory lock ") {
		return "advisory lock"
	}
	if strings.HasPrefix(resource, "tuple ") {
		return "tuple"
	}
	if strings.HasPrefix(resource, "page ") {
		return "page"
	}
	if strings.HasPrefix(resource, "extend ") {
		return "extend"
	}
	// Default: return the first word
	parts := strings.Fields(resource)
	if len(parts) > 0 {
		return parts[0]
	}
	return "unknown"
}

// extractStatementText extracts the SQL query from a STATEMENT line.
func extractStatementText(msg string) string {
	// Handle both "STATEMENT: " and "statement: " prefixes
	if idx := strings.Index(msg, ": "); idx >= 0 {
		return strings.TrimSpace(msg[idx+2:])
	}
	return ""
}

// Finalize returns the aggregated lock metrics.
// This should be called after all log entries have been processed.
func (a *LockAnalyzer) Finalize() LockMetrics {
	// Build query statistics from individual locks
	a.queryStats = make(map[string]*LockQueryStat, 100)

	for _, lock := range a.activeLocks {
		// Note: We do NOT add wait time for locks that were never acquired to the global total.
		// We only count completed (acquired) locks in the global total wait time.
		// Never-acquired locks are tracked separately in StillWaitingTime per query.

		// Associate lock with query if known
		if lock.query != "" {
			normalized := normalizeQuery(lock.query)
			shortID, fullHash := GenerateQueryID(lock.query, normalized)

			stat, exists := a.queryStats[fullHash]
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
				a.queryStats[fullHash] = stat
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
		QueryStats:        a.queryStats,
	}
}
