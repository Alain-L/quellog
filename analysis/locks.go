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

	// WaitingCount is the number of waiting events for this query.
	WaitingCount int

	// AcquiredCount is the number of acquired events for this query.
	AcquiredCount int

	// TotalWaitTime is the cumulative wait time for this query in milliseconds.
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
}

// NewLockAnalyzer creates a new lock event analyzer.
func NewLockAnalyzer() *LockAnalyzer {
	return &LockAnalyzer{
		lockTypeStats:     make(map[string]int, 20),
		resourceTypeStats: make(map[string]int, 10),
		events:            make([]LockEvent, 0, 1000),
		queryStats:        make(map[string]*LockQueryStat, 100),
		lastQueryByPID:    make(map[string]string, 100),
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

		a.waitingEvents++
		a.totalEvents++
		a.totalWaitTime += waitTime
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

		// Associate with query if available
		if query, ok := a.lastQueryByPID[processID]; ok {
			a.associateQueryWithLock(query, waitTime, "waiting")
		}
		return
	}

	// Check for "acquired" pattern
	if matches := lockAcquiredPattern.FindStringSubmatch(msg); matches != nil {
		processID := matches[1]
		lockType := matches[2]
		resource := matches[3]
		waitTime, _ := strconv.ParseFloat(matches[4], 64)

		resourceType := extractResourceType(resource)

		a.acquiredEvents++
		a.totalEvents++
		a.totalWaitTime += waitTime
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

		// Associate with query if available
		if query, ok := a.lastQueryByPID[processID]; ok {
			a.associateQueryWithLock(query, waitTime, "acquired")
		}
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

// associateQueryWithLock associates a query with lock statistics.
func (a *LockAnalyzer) associateQueryWithLock(query string, waitTime float64, eventType string) {
	normalized := normalizeQuery(query)
	shortID, fullHash := GenerateQueryID(query, normalized)

	stat, exists := a.queryStats[fullHash]
	if !exists {
		stat = &LockQueryStat{
			RawQuery:         query,
			NormalizedQuery:  normalized,
			WaitingCount:     0,
			AcquiredCount:    0,
			TotalWaitTime:    0,
			ID:               shortID,
			FullHash:         fullHash,
		}
		a.queryStats[fullHash] = stat
	}

	stat.TotalWaitTime += waitTime
	if eventType == "waiting" {
		stat.WaitingCount++
	} else if eventType == "acquired" {
		stat.AcquiredCount++
	}
}

// Finalize returns the aggregated lock metrics.
// This should be called after all log entries have been processed.
func (a *LockAnalyzer) Finalize() LockMetrics {
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
