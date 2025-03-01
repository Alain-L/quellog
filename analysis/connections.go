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
func AnalyzeConnections(entries *[]parser.LogEntry) ConnectionMetrics {
	var metrics ConnectionMetrics

	for i := range *entries {
		entry := &(*entries)[i] // Use pointer to avoid copy

		// Detect connection events
		if strings.Contains(entry.Message, "connection received") {
			metrics.ConnectionReceivedCount++
		}

		// Detect disconnection events
		if strings.Contains(entry.Message, "disconnection") {
			metrics.DisconnectionCount++

			// Extract session duration if available
			if duration := extractSessionTime(entry.Message); duration > 0 {
				metrics.TotalSessionTime += duration
			}
		}
	}

	return metrics
}

// extractSessionTime extracts the session duration from a disconnection log entry.
// Example log line: "disconnection: session time: 0:00:05.123"
func extractSessionTime(message string) time.Duration {
	// Locate the session time portion directly
	idx := strings.Index(message, "session time: ")
	if idx == -1 {
		return 0
	}

	// Extract the time string
	timePart := message[idx+14:] // Skip "session time: "
	spaceIdx := strings.IndexByte(timePart, ' ')
	if spaceIdx != -1 {
		timePart = timePart[:spaceIdx]
	}

	// Convert "HH:MM:SS.mmm" format to duration
	components := strings.SplitN(timePart, ":", 3)
	switch len(components) {
	case 3: // Full HH:MM:SS.mmm format
		h, _ := time.ParseDuration(components[0] + "h")
		m, _ := time.ParseDuration(components[1] + "m")
		s, _ := time.ParseDuration(components[2] + "s")
		return h + m + s
	default:
		return 0
	}
}
