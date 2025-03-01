// analysis/connections.go
package analysis

import (
	"strings"
	"time"

	"dalibo/quellog/parser"
)

// ConnectionMetrics aggregates statistics related to connections and sessions.
type ConnectionMetrics struct {
	ConnectionReceivedCount int           // Number of received connections
	DisconnectionCount      int           // Number of disconnections
	TotalSessionTime        time.Duration // Total accumulated session duration
}

// AnalyzeConnections scans log entries to count connection and disconnection events.
func AnalyzeConnections(entries []parser.LogEntry) ConnectionMetrics {
	var metrics ConnectionMetrics

	for _, entry := range entries {
		lowerMsg := strings.ToLower(entry.Message)

		// Detect connection events (e.g., "connection received")
		if strings.Contains(lowerMsg, "connection received") {
			metrics.ConnectionReceivedCount++
		}

		// Detect disconnection events (e.g., "disconnection: session time: 00:00:05")
		if strings.Contains(lowerMsg, "disconnection") {
			metrics.DisconnectionCount++

			// Extract session duration if available
			if duration := extractSessionTime(lowerMsg); duration > 0 {
				metrics.TotalSessionTime += duration
			}
		}
	}

	return metrics
}

// extractSessionTime extracts the session duration from a disconnection log entry.
// Example log line: "disconnection: session time: 0:00:05.123"
func extractSessionTime(message string) time.Duration {
	// Look for "session time: HH:MM:SS.mmm" pattern
	parts := strings.Split(message, "session time: ")
	if len(parts) < 2 {
		return 0
	}
	timeStr := strings.Fields(parts[1])[0] // Extract first word after "session time:"
	parsedDuration, err := time.ParseDuration(timeStr + "s")
	if err != nil {
		return 0
	}
	return parsedDuration
}
