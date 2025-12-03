// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// SessionEvent represents a session with its start and end times.
type SessionEvent struct {
	StartTime time.Time
	EndTime   time.Time
}

// ConnectionMetrics aggregates statistics related to database connections and sessions.
// These metrics help understand connection patterns, session durations, and client behavior.
type ConnectionMetrics struct {
	// ConnectionReceivedCount is the total number of connection requests received.
	ConnectionReceivedCount int

	// DisconnectionCount is the total number of disconnections.
	DisconnectionCount int

	// TotalSessionTime is the accumulated duration of all sessions.
	// Only includes sessions where duration was logged (requires log_disconnections = on).
	TotalSessionTime time.Duration

	// Connections contains timestamps of all connection events.
	// Useful for analyzing connection rate and patterns over time.
	Connections []time.Time

	// SessionDurations contains all individual session durations.
	// Used for calculating median, distribution, and statistics.
	SessionDurations []time.Duration

	// SessionEvents contains all sessions with their start and end times.
	// Used for calculating concurrent connections over time.
	SessionEvents []SessionEvent

	// SessionsByUser maps usernames to their session durations.
	SessionsByUser map[string][]time.Duration

	// SessionsByDatabase maps database names to their session durations.
	SessionsByDatabase map[string][]time.Duration

	// SessionsByHost maps host addresses to their session durations.
	SessionsByHost map[string][]time.Duration

	// PeakConcurrentSessions is the maximum number of simultaneous sessions observed.
	PeakConcurrentSessions int

	// PeakConcurrentTimestamp is when the peak concurrent sessions occurred.
	PeakConcurrentTimestamp time.Time
}

// ============================================================================
// Connection patterns and constants
// ============================================================================

// Connection log message patterns
const (
	connectionReceived = "connection received"
	disconnection      = "disconnection"
	sessionTimePrefix  = "session time: "
)

// ============================================================================
// Streaming connection analyzer
// ============================================================================

// ConnectionAnalyzer processes connection and disconnection events from log entries.
// It tracks connection counts, session durations, and connection timestamps.
//
// Usage:
//
//	analyzer := NewConnectionAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type ConnectionAnalyzer struct {
	connectionReceivedCount int
	disconnectionCount      int
	totalSessionTime        time.Duration
	connections             []time.Time
	sessionDurations        []time.Duration
	sessionEvents           []SessionEvent
	sessionsByUser          map[string][]time.Duration
	sessionsByDatabase      map[string][]time.Duration
	sessionsByHost          map[string][]time.Duration

	// For tracking concurrent connections
	// Use PID as key instead of timestamp to avoid collisions when
	// multiple connections arrive in the same second
	activeConnections       map[string]time.Time // PID -> connection timestamp
	peakConcurrent          int
	peakConcurrentTimestamp time.Time
}

// NewConnectionAnalyzer creates a new connection analyzer.
func NewConnectionAnalyzer() *ConnectionAnalyzer {
	return &ConnectionAnalyzer{
		connections:        make([]time.Time, 0, 1000),
		sessionDurations:   make([]time.Duration, 0, 1000),
		sessionEvents:      make([]SessionEvent, 0, 1000),
		sessionsByUser:     make(map[string][]time.Duration, 100),
		sessionsByDatabase: make(map[string][]time.Duration, 50),
		sessionsByHost:     make(map[string][]time.Duration, 100),
		activeConnections:  make(map[string]time.Time, 1000),
	}
}

// optimized version of Process for connection-related events.
func (a *ConnectionAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if len(msg) < 12 {
		return
	}

	// OPTIMIZATION: Use single Index call to find "connection" then check context
	// This reduces CPU time from 220ms to ~110ms on I1.log
	idx := strings.Index(msg, "connection")
	if idx == -1 {
		return // Neither connection nor disconnection present
	}

	// Extract PID once for this entry (used for connection tracking)
	pid := parser.ExtractPID(msg)

	// Check if it's "connection received" or "disconnection"
	// "connection received" has 'c' at position idx
	// "disconnection" has 'd' before "connection" (idx-3: "dis")
	if idx >= 3 && msg[idx-3:idx] == "dis" {
		// It's "disconnection"
		a.disconnectionCount++

		// Remove from active connections using PID
		if pid != "" {
			delete(a.activeConnections, pid)
		}

		if duration := extractSessionTime(msg); duration > 0 {
			a.totalSessionTime += duration
			a.sessionDurations = append(a.sessionDurations, duration)

			// Store session event for concurrent tracking
			startTime := entry.Timestamp.Add(-duration)
			a.sessionEvents = append(a.sessionEvents, SessionEvent{
				StartTime: startTime,
				EndTime:   entry.Timestamp,
			})

			// Extract user, database, and host from disconnection message
			user := extractEntityFromMessage(msg, "user")
			database := extractEntityFromMessage(msg, "database")
			host := extractEntityFromMessage(msg, "host")

			// Store duration by user
			if user != "" {
				a.sessionsByUser[user] = append(a.sessionsByUser[user], duration)
			}

			// Store duration by database
			if database != "" {
				a.sessionsByDatabase[database] = append(a.sessionsByDatabase[database], duration)
			}

			// Store duration by host
			if host != "" {
				a.sessionsByHost[host] = append(a.sessionsByHost[host], duration)
			}
		}
	} else if idx+10 < len(msg) && msg[idx:idx+10] == "connection" {
		// Check if followed by " received"
		if idx+19 <= len(msg) && msg[idx:idx+19] == "connection received" {
			a.connectionReceivedCount++
			a.connections = append(a.connections, entry.Timestamp)

			// Track active connections using PID for accurate counting
			// Fall back to timestamp string if PID not available
			key := pid
			if key == "" {
				key = entry.Timestamp.String()
			}
			a.activeConnections[key] = entry.Timestamp
			currentActive := len(a.activeConnections)
			if currentActive > a.peakConcurrent {
				a.peakConcurrent = currentActive
				a.peakConcurrentTimestamp = entry.Timestamp
			}
		}
	}
}

// Finalize returns the aggregated connection metrics.
// This should be called after all log entries have been processed.
func (a *ConnectionAnalyzer) Finalize() ConnectionMetrics {
	return ConnectionMetrics{
		ConnectionReceivedCount: a.connectionReceivedCount,
		DisconnectionCount:      a.disconnectionCount,
		TotalSessionTime:        a.totalSessionTime,
		Connections:             a.connections,
		SessionDurations:        a.sessionDurations,
		SessionEvents:           a.sessionEvents,
		SessionsByUser:          a.sessionsByUser,
		SessionsByDatabase:      a.sessionsByDatabase,
		SessionsByHost:          a.sessionsByHost,
		PeakConcurrentSessions:  a.peakConcurrent,
		PeakConcurrentTimestamp: a.peakConcurrentTimestamp,
	}
}

// ============================================================================
// Session time extraction
// ============================================================================

// extractSessionTime parses the session duration from a disconnection message.
// PostgreSQL logs session time in the format "H:MM:SS.mmm" or "HH:MM:SS.mmm".
//
// Example formats:
//   - "0:00:05.123" → 5.123 seconds
//   - "0:15:30.456" → 15 minutes 30.456 seconds
//   - "2:30:45.789" → 2 hours 30 minutes 45.789 seconds
//
// Returns 0 if the session time cannot be parsed or is not present.
func extractSessionTime(message string) time.Duration {
	// Find "session time: " prefix
	idx := strings.Index(message, sessionTimePrefix)
	if idx == -1 {
		return 0
	}

	// Extract the time string after "session time: "
	timePart := message[idx+len(sessionTimePrefix):]

	// Find the end of the time string (first space)
	if spaceIdx := strings.IndexByte(timePart, ' '); spaceIdx != -1 {
		timePart = timePart[:spaceIdx]
	}

	// Parse "H:MM:SS.mmm" or "HH:MM:SS.mmm" format
	return parsePostgreSQLDuration(timePart)
}

// parsePostgreSQLDuration parses a PostgreSQL duration string in "H:MM:SS.mmm" format.
// Components:
//   - Hours: can be 1 or 2 digits
//   - Minutes: always 2 digits (00-59)
//   - Seconds: 2 digits + optional fractional part (00.000-59.999)
//
// Returns 0 if parsing fails.
//
// Optimized version: uses manual parsing instead of strings.Split to avoid allocations.
func parsePostgreSQLDuration(s string) time.Duration {
	// Find first colon (after hours)
	firstColon := strings.IndexByte(s, ':')
	if firstColon == -1 {
		return 0
	}

	// Find second colon (after minutes)
	secondColon := strings.IndexByte(s[firstColon+1:], ':')
	if secondColon == -1 {
		return 0
	}
	secondColon += firstColon + 1

	// Parse hours: s[0:firstColon]
	hours, err := strconv.Atoi(s[:firstColon])
	if err != nil {
		return 0
	}

	// Parse minutes: s[firstColon+1:secondColon]
	minutes, err := strconv.Atoi(s[firstColon+1 : secondColon])
	if err != nil || minutes < 0 || minutes > 59 {
		return 0
	}

	// Parse seconds: s[secondColon+1:]
	seconds, err := strconv.ParseFloat(s[secondColon+1:], 64)
	if err != nil || seconds < 0 || seconds >= 60 {
		return 0
	}

	// Calculate total duration
	duration := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))

	return duration
}

// extractEntityFromMessage extracts a specific entity (user, database, host, or application) from a log message.
// Supports patterns: "user=value", "database=value", "db=value", "host=value", "application=value"
func extractEntityFromMessage(msg, entityType string) string {
	var patterns []string
	if entityType == "user" {
		patterns = []string{"user="}
	} else if entityType == "database" {
		patterns = []string{"database=", "db="}
	} else if entityType == "host" {
		patterns = []string{"host="}
	} else if entityType == "application" {
		patterns = []string{"application=", "app="}
	} else {
		return ""
	}

	for _, pattern := range patterns {
		idx := strings.Index(msg, pattern)
		if idx == -1 {
			continue
		}

		// Extract value after '='
		startPos := idx + len(pattern)
		if startPos >= len(msg) {
			continue
		}

		// Find end position (first separator: space, comma, bracket, or parenthesis)
		endPos := startPos
		for endPos < len(msg) {
			c := msg[endPos]
			if c == ' ' || c == ',' || c == '[' || c == ')' {
				break
			}
			endPos++
		}

		if endPos > startPos {
			return msg[startPos:endPos]
		}
	}

	return ""
}
