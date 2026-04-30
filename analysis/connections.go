// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"sort"
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

// ConnectionMetrics aggregates statistics on database connections and sessions.
type ConnectionMetrics struct {
	ConnectionReceivedCount int
	DisconnectionCount      int
	// TotalSessionTime: accumulated duration, only sessions with logged
	// duration (requires log_disconnections = on).
	TotalSessionTime time.Duration
	// Connections: timestamps of all connection events.
	Connections []time.Time
	// SessionStats: count/min/max/avg/median. Median is estimated via P²
	// (<5% error after 50 samples, <0.01% after 1000); the rest is exact.
	SessionStats     DurationStats
	SessionCumulated time.Duration // exact sum of session durations
	// SessionDistribution: counts per bucket ("< 1s", "1s - 1min", "1min -
	// 30min", "30min - 2h", "2h - 5h", "> 5h"). Computed in streaming so
	// the per-session slice can be discarded.
	SessionDistribution map[string]int
	// SessionEvents: start/end of every session, used for concurrent-
	// sessions over time computations.
	SessionEvents           []SessionEvent
	SessionsByUser          map[string]*StreamingDurationStats
	SessionsByDatabase      map[string]*StreamingDurationStats
	SessionsByHost          map[string]*StreamingDurationStats
	PeakConcurrentSessions  int
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
// compactSession stores a completed session as Unix-millisecond
// timestamps. 16 bytes vs 48 for SessionEvent (×3 denser) — the
// time.Location pointer is dropped, all materializations resolve in
// the local zone. Millisecond precision is required so the sweep-line
// peak counts correctly when many short sessions cluster within the
// same wall-clock second (cf. connection-pool fixtures).
type compactSession struct {
	startUnixMs int64
	endUnixMs   int64
}

// sessionsPerChunk is the fixed capacity of each compact-storage chunk.
// Chunks are append-only and never resized, eliminating the slice-doubling
// transient peaks that previously cost ~500 MB on J.log's 5.7M sessions.
const sessionsPerChunk = 65536

type ConnectionAnalyzer struct {
	connectionReceivedCount int
	disconnectionCount      int
	totalSessionTime        time.Duration

	// Compact chunked storage for received timestamps. Unix milliseconds
	// in fixed-capacity chunks to avoid append-doubling transient peaks.
	// Materialized to []time.Time at Finalize for the API. ×3 denser
	// than the previous []time.Time storage on long captures.
	receivedChunks [][]int64

	// Same scheme for completed sessions.
	sessionChunks [][]compactSession

	// loc is the location of the first event observed. Used to
	// materialize compact Unix-ms timestamps back into the same zone
	// the parser produced (typically UTC for PG default log_timezone),
	// so the JSON output keeps reading "19:00:00 UTC" not "21:00:00 CEST".
	loc *time.Location

	// Streaming accumulators that replace the previous full slice of
	// per-session durations. The slice held one time.Duration per session
	// solely so the renderers could later compute median + distribution
	// from it; on J.log this was ~45 MB of float64 retained for the whole
	// parse. Now each disconnect feeds globalSessionStats (P² for median,
	// exact for min/max/avg/sum) and increments one bucket of
	// sessionDistribution. The retained state is bounded.
	globalSessionStats  StreamingDurationStats
	sessionDistribution map[string]int

	sessionsByUser     map[string]*StreamingDurationStats
	sessionsByDatabase map[string]*StreamingDurationStats
	sessionsByHost     map[string]*StreamingDurationStats

	// activeConnections tracks live receiveds so the orphan-flush at
	// Finalize knows what's left dangling. The peak itself is computed
	// at Finalize via sweep-line on sessionEvents — the streaming
	// `len(activeConnections)` counter that lived here previously
	// silently undercounted whenever a PID was re-used before the
	// previous session for that PID had emitted its disconnect.
	activeConnections map[string]time.Time // PID -> received timestamp

	// Most recent entry timestamp seen by this analyzer. Used at
	// Finalize() as the EndTime for orphan sessions (connections that
	// were received but never disconnected within the log window), so
	// the concurrent-sessions histogram can account for them instead of
	// silently dropping them — which would make the histogram peak
	// diverge from PeakConcurrentSessions.
	lastSeenTimestamp time.Time
}

// NewConnectionAnalyzer creates a new connection analyzer.
func NewConnectionAnalyzer() *ConnectionAnalyzer {
	return &ConnectionAnalyzer{
		sessionDistribution: newSessionDistribution(),
		sessionsByUser:      make(map[string]*StreamingDurationStats, 100),
		sessionsByDatabase:  make(map[string]*StreamingDurationStats, 50),
		sessionsByHost:      make(map[string]*StreamingDurationStats, 100),
		activeConnections:   make(map[string]time.Time, 1000),
	}
}

// addReceived appends a received-timestamp (as Unix milliseconds) to
// the compact chunked storage.
func (a *ConnectionAnalyzer) addReceived(t time.Time) {
	if a.loc == nil {
		a.loc = t.Location()
	}
	ms := t.UnixMilli()
	n := len(a.receivedChunks)
	if n == 0 || len(a.receivedChunks[n-1]) == sessionsPerChunk {
		a.receivedChunks = append(a.receivedChunks, make([]int64, 0, sessionsPerChunk))
		n++
	}
	a.receivedChunks[n-1] = append(a.receivedChunks[n-1], ms)
}

// addSession appends a completed session (Unix-millisecond start/end)
// to the compact chunked storage.
func (a *ConnectionAnalyzer) addSession(start, end time.Time) {
	if a.loc == nil {
		a.loc = start.Location()
	}
	cs := compactSession{startUnixMs: start.UnixMilli(), endUnixMs: end.UnixMilli()}
	n := len(a.sessionChunks)
	if n == 0 || len(a.sessionChunks[n-1]) == sessionsPerChunk {
		a.sessionChunks = append(a.sessionChunks, make([]compactSession, 0, sessionsPerChunk))
		n++
	}
	a.sessionChunks[n-1] = append(a.sessionChunks[n-1], cs)
}

// totalSessions returns the total session count across all chunks.
func (a *ConnectionAnalyzer) totalSessions() int {
	n := 0
	for _, c := range a.sessionChunks {
		n += len(c)
	}
	return n
}

// newSessionDistribution returns a fresh distribution map with all
// buckets pre-initialized to zero — same key set as
// CalculateDurationDistribution so renderers see a stable shape even
// when no session was logged.
func newSessionDistribution() map[string]int {
	return map[string]int{
		"< 1s":         0,
		"1s - 1min":    0,
		"1min - 30min": 0,
		"30min - 2h":   0,
		"2h - 5h":      0,
		"> 5h":         0,
	}
}

// addToSessionDistribution increments the right bucket. Keep in sync
// with CalculateDurationDistribution in analysis/common.go.
func (a *ConnectionAnalyzer) addToSessionDistribution(d time.Duration) {
	switch {
	case d < time.Second:
		a.sessionDistribution["< 1s"]++
	case d < time.Minute:
		a.sessionDistribution["1s - 1min"]++
	case d < 30*time.Minute:
		a.sessionDistribution["1min - 30min"]++
	case d < 2*time.Hour:
		a.sessionDistribution["30min - 2h"]++
	case d < 5*time.Hour:
		a.sessionDistribution["2h - 5h"]++
	default:
		a.sessionDistribution["> 5h"]++
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

	// PID is pre-populated by the parser layer.
	pid := entry.PID

	// Keep track of the latest timestamp observed — used by Finalize()
	// to close orphan sessions.
	if entry.Timestamp.After(a.lastSeenTimestamp) {
		a.lastSeenTimestamp = entry.Timestamp
	}

	// Check if it's "connection received" or "disconnection"
	// "connection received" has 'c' at position idx
	// "disconnection" has 'd' before "connection" (idx-3: "dis")
	if idx >= 3 && msg[idx-3:idx] == "dis" {
		// It's "disconnection"
		a.disconnectionCount++

		// Remove from active connections using PID — but remember the
		// received timestamp in case the disconnect carries no parseable
		// session_time, so we can still bound this session for the
		// concurrent-sessions histogram.
		var receivedAt time.Time
		if pid != "" {
			if t, ok := a.activeConnections[pid]; ok {
				receivedAt = t
				delete(a.activeConnections, pid)
			}
		}

		if duration := extractSessionTime(msg); duration > 0 {
			a.totalSessionTime += duration
			a.globalSessionStats.Add(duration)
			a.addToSessionDistribution(duration)

			// Store session event for concurrent tracking
			startTime := entry.Timestamp.Add(-duration)
			a.addSession(startTime, entry.Timestamp)

			// Extract user, database, and host from disconnection message
			user := extractEntityFromMessage(msg, "user")
			database := extractEntityFromMessage(msg, "database")
			host := extractEntityFromMessage(msg, "host")

			// Store duration by user
			if user != "" {
				s, ok := a.sessionsByUser[user]
				if !ok {
					s = &StreamingDurationStats{}
					a.sessionsByUser[user] = s
				}
				s.Add(duration)
			}

			// Store duration by database
			if database != "" {
				s, ok := a.sessionsByDatabase[database]
				if !ok {
					s = &StreamingDurationStats{}
					a.sessionsByDatabase[database] = s
				}
				s.Add(duration)
			}

			// Store duration by host
			if host != "" {
				s, ok := a.sessionsByHost[host]
				if !ok {
					s = &StreamingDurationStats{}
					a.sessionsByHost[host] = s
				}
				s.Add(duration)
			}
		} else if !receivedAt.IsZero() {
			// Disconnect without a parseable session_time (unknown
			// format, truncated line, …) — fall back to the tracked
			// `connection received` timestamp so this session still
			// shows up on the concurrent-sessions histogram. We do NOT
			// feed the duration stats in this path: we have no reliable
			// PG-reported duration, only a coarse observed window.
			a.addSession(receivedAt, entry.Timestamp)
		}
	} else if idx+10 < len(msg) && msg[idx:idx+10] == "connection" {
		// Check if followed by " received"
		if idx+19 <= len(msg) && msg[idx:idx+19] == "connection received" {
			a.connectionReceivedCount++
			a.addReceived(entry.Timestamp)

			// Track active connections using PID so the orphan-flush at
			// Finalize knows what's left dangling. Fall back to a
			// timestamp-string key if no PID is extractable.
			//
			// The peak is NOT computed here anymore — the streaming
			// counter (`len(activeConnections)` after each received)
			// silently undercounts whenever PG re-uses a PID before the
			// previous session for that PID has emitted its disconnect
			// (the map insert overwrites the existing entry without
			// incrementing). Computed via a sweep-line on sessionEvents
			// at Finalize() instead — same algorithm as the histogram,
			// so chart peak and Maximum simultaneous converge by
			// construction.
			key := pid
			if key == "" {
				key = entry.Timestamp.String()
			}
			a.activeConnections[key] = entry.Timestamp
		}
	}
}

// Finalize returns the aggregated connection metrics.
// This should be called after all log entries have been processed.
func (a *ConnectionAnalyzer) Finalize() ConnectionMetrics {
	// Flush orphan sessions: connections that were received but never
	// saw a matching disconnect within the log window. Close them at
	// the last timestamp observed by the analyzer — they were
	// demonstrably still active then. Without this, the sweep-line
	// concurrent-sessions histogram silently drops them and its peak
	// ends up well below PeakConcurrentSessions on any log that gets
	// truncated mid-session (common in real captures: the last hour
	// holds connections still open when the operator stopped tailing).
	if !a.lastSeenTimestamp.IsZero() && len(a.activeConnections) > 0 {
		// Collect the orphan received-times and sort them so the
		// resulting sessions are appended in deterministic order —
		// Go's map iteration is randomized, and downstream golden tests
		// compare exact JSON ordering.
		orphans := make([]time.Time, 0, len(a.activeConnections))
		for _, receivedAt := range a.activeConnections {
			orphans = append(orphans, receivedAt)
		}
		sort.Slice(orphans, func(i, j int) bool {
			return orphans[i].Before(orphans[j])
		})
		for _, receivedAt := range orphans {
			a.addSession(receivedAt, a.lastSeenTimestamp)
		}
	}

	// Materialize compact storage back to API types. This happens once,
	// at output time, so the during-Process heap stays compact (~6×
	// denser) and the slice-doubling transients are gone.
	loc := a.loc
	if loc == nil {
		loc = time.UTC
	}
	connections := make([]time.Time, 0, a.totalReceived())
	for _, chunk := range a.receivedChunks {
		for _, ms := range chunk {
			connections = append(connections, time.UnixMilli(ms).In(loc))
		}
	}

	sessionEvents := make([]SessionEvent, 0, a.totalSessions())
	for _, chunk := range a.sessionChunks {
		for _, s := range chunk {
			sessionEvents = append(sessionEvents, SessionEvent{
				StartTime: time.UnixMilli(s.startUnixMs).In(loc),
				EndTime:   time.UnixMilli(s.endUnixMs).In(loc),
			})
		}
	}

	peakConcurrent, peakTimestamp := computePeakSweepline(sessionEvents)

	return ConnectionMetrics{
		ConnectionReceivedCount: a.connectionReceivedCount,
		DisconnectionCount:      a.disconnectionCount,
		TotalSessionTime:        a.totalSessionTime,
		Connections:             connections,
		SessionStats:            a.globalSessionStats.Stats(),
		SessionCumulated:        a.globalSessionStats.Cumulated(),
		SessionDistribution:     a.sessionDistribution,
		SessionEvents:           sessionEvents,
		SessionsByUser:          a.sessionsByUser,
		SessionsByDatabase:      a.sessionsByDatabase,
		SessionsByHost:          a.sessionsByHost,
		PeakConcurrentSessions:  peakConcurrent,
		PeakConcurrentTimestamp: peakTimestamp,
	}
}

// totalReceived returns the total received-event count across chunks.
func (a *ConnectionAnalyzer) totalReceived() int {
	n := 0
	for _, c := range a.receivedChunks {
		n += len(c)
	}
	return n
}

// computePeakSweepline returns the maximum number of overlapping sessions
// observed across `events` and the timestamp at which it occurred.
// Replaces the old streaming counter (incr-on-received with len(map))
// which silently undercounted whenever the OS re-used a PID before the
// previous session's disconnect was logged.
//
// The convention is "starts before ends at tied timestamps", same as
// histogram.go's computeConcurrentHistogram, so the histogram peak and
// this PeakConcurrentSessions value converge by construction.
func computePeakSweepline(events []SessionEvent) (int, time.Time) {
	if len(events) == 0 {
		return 0, time.Time{}
	}
	type tick struct {
		t     time.Time
		delta int
	}
	pts := make([]tick, 0, len(events)*2)
	for _, e := range events {
		if e.StartTime.IsZero() || e.EndTime.IsZero() {
			continue
		}
		pts = append(pts, tick{e.StartTime, +1})
		pts = append(pts, tick{e.EndTime, -1})
	}
	if len(pts) == 0 {
		return 0, time.Time{}
	}
	sort.Slice(pts, func(i, j int) bool {
		if !pts[i].t.Equal(pts[j].t) {
			return pts[i].t.Before(pts[j].t)
		}
		// +1 before -1 at the same timestamp captures the local peak
		return pts[i].delta > pts[j].delta
	})

	cur, peak := 0, 0
	var peakT time.Time
	for _, p := range pts {
		cur += p.delta
		if cur > peak {
			peak = cur
			peakT = p.t
		}
	}
	return peak, peakT
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

	// Detect comma-separated format (e.g., "user=X,db=Y,app=Z")
	commaSep := entityType == "application" &&
		(strings.Contains(msg, ",db=") || strings.Contains(msg, ",user=") || strings.Contains(msg, ",app="))

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

		// Find end position (first separator)
		endPos := startPos
		for endPos < len(msg) {
			c := msg[endPos]
			if c == ',' || c == '[' || c == ')' {
				break
			}
			if c == ' ' && !commaSep {
				break
			}
			endPos++
		}
		if commaSep {
			if pos := findSeverityMarker(msg[startPos:endPos]); pos != -1 {
				endPos = startPos + pos
			}
		}

		if endPos > startPos {
			return msg[startPos:endPos]
		}
	}

	return ""
}
