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
//
// Iterating connections and session events:
//
// The Connections []time.Time and SessionEvents []SessionEvent fields
// are intentionally NOT materialized at Finalize on large logs (5.7 M
// sessions on J.log = 410 MB transient peak). Use the iterator methods
// IterateConnections / IterateSessionEvents and the count helpers
// ConnectionsCount / SessionEventsCount instead. The deprecated slice
// fields are still populated for the *_test paths that snapshot the
// full set; see materializeSlices() and the lazy-on-first-access
// behavior of the renderers.
type ConnectionMetrics struct {
	ConnectionReceivedCount int
	DisconnectionCount      int
	// TotalSessionTime: accumulated duration, only sessions with logged
	// duration (requires log_disconnections = on).
	TotalSessionTime time.Duration
	// Connections: deprecated direct slice. Always nil after Finalize on
	// the streaming path — call IterateConnections to walk events without
	// allocating the full N×24 B slice.
	Connections []time.Time
	// SessionStats: count/min/max/avg/median. Median is estimated via P²
	// (<5% error after 50 samples, <0.01% after 1000); the rest is exact.
	SessionStats     DurationStats
	SessionCumulated time.Duration // exact sum of session durations
	// SessionDistribution: counts per bucket ("< 1s", "1s - 1min", "1min -
	// 30min", "30min - 2h", "2h - 5h", "> 5h"). Computed in streaming so
	// the per-session slice can be discarded.
	SessionDistribution map[string]int
	// SessionEvents: deprecated direct slice. Always nil after Finalize on
	// the streaming path — call IterateSessionEvents to walk events
	// without allocating the full N×48 B slice (273 MB on J.log).
	SessionEvents           []SessionEvent
	SessionsByUser          map[string]*StreamingDurationStats
	SessionsByDatabase      map[string]*StreamingDurationStats
	SessionsByHost          map[string]*StreamingDurationStats
	PeakConcurrentSessions  int
	PeakConcurrentTimestamp time.Time

	// Client I/O failures ("could not send/receive data ... client: reason").
	// ClientIOFailureCount is the total; the breakdown is split by direction
	// — ClientIORecv (could not receive *from* the client) vs ClientIOSend
	// (could not send *to* the client) — each mapping a normalized strerror
	// to its count, plus a by-database tally. Renderers sort them.
	ClientIOFailureCount int
	ClientIORecv         map[string]int
	ClientIOSend         map[string]int
	ClientIOByDatabase   map[string]int

	// receivedChunksRef and sessionChunksRef are the compact backing
	// storage moved from the analyzer at Finalize. The renderers iterate
	// these via IterateConnections / IterateSessionEvents instead of
	// allocating the full N×24 / N×48 B materialized slices, which on
	// J.log saved a 410 MB transient peak at output time.
	//
	// locRef is the timezone captured from the first event observed by
	// the analyzer; reused when expanding compact Unix-ms timestamps so
	// the wall clock matches what the parser emitted.
	receivedChunksRef [][]int64
	sessionChunksRef  [][]compactSession
	locRef            *time.Location
}

// IterateConnections yields every received-connection timestamp in
// observed order. Returning false stops iteration early. No intermediate
// []time.Time slice is built.
func (m *ConnectionMetrics) IterateConnections(fn func(time.Time) bool) {
	if len(m.receivedChunksRef) > 0 {
		loc := m.locRef
		if loc == nil {
			loc = time.UTC
		}
		for _, chunk := range m.receivedChunksRef {
			for _, ms := range chunk {
				if !fn(time.UnixMilli(ms).In(loc)) {
					return
				}
			}
		}
		return
	}
	// Fallback: legacy materialized slice (Mock metrics in tests, JSON
	// inputs from --json-input, etc.).
	for _, t := range m.Connections {
		if !fn(t) {
			return
		}
	}
}

// IterateSessionEvents yields every completed session in observed
// order. Returning false stops iteration early.
func (m *ConnectionMetrics) IterateSessionEvents(fn func(SessionEvent) bool) {
	if len(m.sessionChunksRef) > 0 {
		loc := m.locRef
		if loc == nil {
			loc = time.UTC
		}
		for _, chunk := range m.sessionChunksRef {
			for _, s := range chunk {
				if !fn(SessionEvent{
					StartTime: time.UnixMilli(s.startUnixMs).In(loc),
					EndTime:   time.UnixMilli(s.endUnixMs).In(loc),
				}) {
					return
				}
			}
		}
		return
	}
	for _, s := range m.SessionEvents {
		if !fn(s) {
			return
		}
	}
}

// ConnectionsCount returns the total number of received-connection
// events stored, whether in compact chunks or in the legacy slice.
func (m *ConnectionMetrics) ConnectionsCount() int {
	if len(m.receivedChunksRef) > 0 {
		n := 0
		for _, c := range m.receivedChunksRef {
			n += len(c)
		}
		return n
	}
	return len(m.Connections)
}

// SessionEventsCount returns the total number of session events stored.
func (m *ConnectionMetrics) SessionEventsCount() int {
	if len(m.sessionChunksRef) > 0 {
		n := 0
		for _, c := range m.sessionChunksRef {
			n += len(c)
		}
		return n
	}
	return len(m.SessionEvents)
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
//
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

	// Client I/O failures: "could not send/receive data to/from client:
	// <reason>" (LOG level). These signal abnormal client disconnects
	// (broken pipe, connection reset, timeout) — network/app instability
	// that no other section surfaces. The terminal "connection to client
	// lost" (FATAL) is intentionally left to the EVENTS panel to avoid
	// double-counting. Detected on the non-"connection" bail path.
	clientIOFailureCount int
	clientIORecv         map[string]int // reason -> count (could not receive from client)
	clientIOSend         map[string]int // reason -> count (could not send to client)
	clientIOByDatabase   map[string]int // db -> count

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
		clientIORecv:        make(map[string]int, 8),
		clientIOSend:        make(map[string]int, 8),
		clientIOByDatabase:  make(map[string]int, 16),
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
	// Client I/O failures ("could not send/receive data ... client: <reason>")
	// are checked independently of the "connection" gate below: their message
	// carries no lowercase "connection", but the prefix might (e.g.
	// app=connection-pool), which would otherwise route them past this check.
	a.recordClientIOFailure(msg)

	idx := strings.Index(msg, "connection")
	if idx == -1 {
		return
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

	// Hand the compact chunked storage off to the metrics struct without
	// materializing the API-typed slices. Renderers iterate via
	// IterateConnections / IterateSessionEvents on the chunks directly,
	// avoiding the 410 MB transient peak that the previous N×24 / N×48 B
	// allocations cost on J.log (5.7 M sessions).
	loc := a.loc
	if loc == nil {
		loc = time.UTC
	}

	peakConcurrent, peakTimestamp := computePeakSweepline(a.sessionChunks, loc)

	return ConnectionMetrics{
		ConnectionReceivedCount: a.connectionReceivedCount,
		DisconnectionCount:      a.disconnectionCount,
		TotalSessionTime:        a.totalSessionTime,
		SessionStats:            a.globalSessionStats.Stats(),
		SessionCumulated:        a.globalSessionStats.Cumulated(),
		SessionDistribution:     a.sessionDistribution,
		SessionsByUser:          a.sessionsByUser,
		SessionsByDatabase:      a.sessionsByDatabase,
		SessionsByHost:          a.sessionsByHost,
		PeakConcurrentSessions:  peakConcurrent,
		PeakConcurrentTimestamp: peakTimestamp,

		ClientIOFailureCount: a.clientIOFailureCount,
		ClientIORecv:         a.clientIORecv,
		ClientIOSend:         a.clientIOSend,
		ClientIOByDatabase:   a.clientIOByDatabase,

		receivedChunksRef: a.receivedChunks,
		sessionChunksRef:  a.sessionChunks,
		locRef:            loc,
	}
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
func computePeakSweepline(chunks [][]compactSession, loc *time.Location) (int, time.Time) {
	if len(chunks) == 0 {
		return 0, time.Time{}
	}
	if loc == nil {
		loc = time.UTC
	}
	// Build flat uint32 indices into the chunk grid: hi 16 bits = chunk
	// index, lo 16 bits = position inside the chunk (sessionsPerChunk =
	// 1<<16). Two parallel lists — one for start order, one for end order
	// — give the same 8× shrink (4 B vs 32 B per entry) the previous
	// flat-slice version had, but without materializing the N×16 B
	// compact slice nor the N×48 B SessionEvent slice. On J.log this
	// drops the Finalize peak from ~410 MB to ~45 MB.
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	startIdx := make([]uint32, 0, n)
	endIdx := make([]uint32, 0, n)
	for ci, chunk := range chunks {
		base := uint32(ci) << 16
		for ei, s := range chunk {
			if s.startUnixMs == 0 || s.endUnixMs == 0 {
				continue
			}
			idx := base | uint32(ei)
			startIdx = append(startIdx, idx)
			endIdx = append(endIdx, idx)
		}
	}
	if len(startIdx) == 0 {
		return 0, time.Time{}
	}
	startMs := func(idx uint32) int64 { return chunks[idx>>16][idx&0xFFFF].startUnixMs }
	endMs := func(idx uint32) int64 { return chunks[idx>>16][idx&0xFFFF].endUnixMs }
	sort.Slice(startIdx, func(i, j int) bool {
		return startMs(startIdx[i]) < startMs(startIdx[j])
	})
	sort.Slice(endIdx, func(i, j int) bool {
		return endMs(endIdx[i]) < endMs(endIdx[j])
	})

	// Sweep both index lists in lockstep. At each step take the earlier
	// pending timestamp; tie-break "starts (+1) before ends (-1)" so
	// the local peak is captured before the matching decrement.
	cur, peak := 0, 0
	var peakMs int64
	s, e := 0, 0
	for s < len(startIdx) || e < len(endIdx) {
		var ms int64
		var delta int
		switch {
		case e >= len(endIdx):
			ms = startMs(startIdx[s])
			delta = +1
			s++
		case s >= len(startIdx):
			ms = endMs(endIdx[e])
			delta = -1
			e++
		default:
			sMs := startMs(startIdx[s])
			eMs := endMs(endIdx[e])
			// sMs <= eMs → take start (covers tie: +1 before -1)
			if sMs <= eMs {
				ms = sMs
				delta = +1
				s++
			} else {
				ms = eMs
				delta = -1
				e++
			}
		}
		cur += delta
		if cur > peak {
			peak = cur
			peakMs = ms
		}
	}
	if peak == 0 {
		return 0, time.Time{}
	}
	return peak, time.UnixMilli(peakMs).In(loc)
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

// recordClientIOFailure folds a "could not send/receive data ... client:
// <reason>" message into the client-I/O aggregates, attributing it to its
// database when the prefix carries one. The " client: " guard fails fast on
// the SQL-duration lines that dominate this bail path, so the two Index
// probes run only on the handful of messages that actually mention a client.
func (a *ConnectionAnalyzer) recordClientIOFailure(msg string) {
	if !strings.Contains(msg, " client: ") {
		return
	}
	// Anchor on the message body: a genuine I/O error IS the whole message
	// ("LOG:  could not send data to client: <reason>"), so the pattern must
	// sit at the start of the body — right after the severity marker and an
	// optional inline SQLSTATE. A query whose text merely contains "could not
	// send data to client:" (in a duration:/statement: line) starts with
	// something else and is rejected.
	body := clientErrorBody(msg)

	switch {
	case strings.HasPrefix(body, "could not send data to client: "):
		a.clientIOSend[clientIOReason(body[len("could not send data to client: "):])]++
	case strings.HasPrefix(body, "could not receive data from client: "):
		a.clientIORecv[clientIOReason(body[len("could not receive data from client: "):])]++
	default:
		return
	}

	a.clientIOFailureCount++
	if db := extractEntityFromMessage(msg, "database"); db != "" {
		a.clientIOByDatabase[db]++
	}
}

// clientErrorBody returns the message text right after the severity marker
// (e.g. " LOG:") and an optional inline 5-character SQLSTATE ("08006: "),
// i.e. the start of what PostgreSQL actually logged. Returns "" when no
// severity marker is present.
func clientErrorBody(msg string) string {
	m := findSeverityMarker(msg)
	if m < 0 {
		return ""
	}
	body := msg[m:]
	colon := strings.IndexByte(body, ':') // end of the " LOG:" / " FATAL:" marker
	if colon < 0 {
		return ""
	}
	body = strings.TrimLeft(body[colon+1:], " ")
	if len(body) >= 7 && body[5] == ':' && body[6] == ' ' && isSQLState(body[:5]) {
		body = body[7:]
	}
	return body
}

// isSQLState reports whether s is five alphanumerics (a PostgreSQL SQLSTATE
// like "08006" or "00000").
func isSQLState(s string) bool {
	if len(s) != 5 {
		return false
	}
	for i := 0; i < 5; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

// clientIOReason normalizes a strerror into a stable category key, e.g.
// "Broken pipe" -> "broken pipe". The direction is tracked by which map the
// caller increments, so it is not part of the key.
func clientIOReason(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	// PostgreSQL sometimes decorates the strerror with the current statement's
	// cursor position ("... at character N"). That position is per-query noise
	// here and would fragment one category into per-position variants, so drop
	// it and keep just the OS error.
	if i := strings.Index(r, " at character "); i >= 0 {
		r = r[:i]
	}
	if len(r) > 40 {
		r = r[:40]
	}
	return r
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
