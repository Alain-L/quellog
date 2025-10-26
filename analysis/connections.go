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
	Connections             []time.Time   // Timestamp of every connection
}

// ============================================================================
// VERSION STREAMING
// ============================================================================

// ConnectionAnalyzer traite les connexions au fil de l'eau.
type ConnectionAnalyzer struct {
	connectionReceivedCount int
	disconnectionCount      int
	totalSessionTime        time.Duration
	connections             []time.Time
}

// NewConnectionAnalyzer crée un nouvel analyseur de connexions.
func NewConnectionAnalyzer() *ConnectionAnalyzer {
	return &ConnectionAnalyzer{
		connections: make([]time.Time, 0, 1000),
	}
}

// Process traite une entrée de log pour détecter connexions/disconnexions.
func (a *ConnectionAnalyzer) Process(entry *parser.LogEntry) {
	msg := &entry.Message

	// Detect connection events
	if strings.Contains(*msg, "connection received") {
		a.connectionReceivedCount++
		a.connections = append(a.connections, entry.Timestamp)
	}

	// Detect disconnection events
	if strings.Contains(*msg, "disconnection") {
		a.disconnectionCount++

		// Extract session duration if available
		if duration := extractSessionTime(*msg); duration > 0 {
			a.totalSessionTime += duration
		}
	}
}

// Finalize retourne les métriques finales.
func (a *ConnectionAnalyzer) Finalize() ConnectionMetrics {
	return ConnectionMetrics{
		ConnectionReceivedCount: a.connectionReceivedCount,
		DisconnectionCount:      a.disconnectionCount,
		TotalSessionTime:        a.totalSessionTime,
		Connections:             a.connections,
	}
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// AnalyzeConnections scans log entries to count connection and disconnection events.
// À supprimer une fois le refactoring terminé.
func AnalyzeConnections(entries *[]parser.LogEntry) ConnectionMetrics {
	analyzer := NewConnectionAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}

// ============================================================================
// HELPERS (inchangés)
// ============================================================================

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
