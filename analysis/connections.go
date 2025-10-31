// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

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
}

// NewConnectionAnalyzer creates a new connection analyzer.
func NewConnectionAnalyzer() *ConnectionAnalyzer {
	return &ConnectionAnalyzer{
		connections: make([]time.Time, 0, 1000),
	}
}

// optimized version of Process for connection-related events.
func (a *ConnectionAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if len(msg) < 12 {
		return
	}

	// Detect connection events
	if strings.Contains(msg, connectionReceived) {
		a.connectionReceivedCount++
		a.connections = append(a.connections, entry.Timestamp)
		return
	}

	// Detect disconnection events
	if strings.Contains(msg, disconnection) {
		a.disconnectionCount++
		if duration := extractSessionTime(msg); duration > 0 {
			a.totalSessionTime += duration
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
func parsePostgreSQLDuration(s string) time.Duration {
	// Split by ":"
	components := strings.Split(s, ":")
	if len(components) != 3 {
		return 0
	}

	// Parse hours
	hours, err := strconv.Atoi(components[0])
	if err != nil {
		return 0
	}

	// Parse minutes
	minutes, err := strconv.Atoi(components[1])
	if err != nil || minutes < 0 || minutes > 59 {
		return 0
	}

	// Parse seconds (may include fractional part)
	seconds, err := strconv.ParseFloat(components[2], 64)
	if err != nil || seconds < 0 || seconds >= 60 {
		return 0
	}

	// Calculate total duration
	duration := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))

	return duration
}
