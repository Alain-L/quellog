// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// SessionEvent represents a session with its start and end times, plus the
// interned indices of the user/database/host that owned it (0 = unknown —
// either no entity was extractable, e.g. a disconnect with no parseable
// session_time, or an orphan session closed at Finalize). Resolve a nonzero
// index through the sibling ConnectionMetrics.SessionUserNames /
// SessionDatabaseNames / SessionHostNames reverse tables.
type SessionEvent struct {
	StartTime   time.Time
	EndTime     time.Time
	UserIdx     int
	DatabaseIdx int
	HostIdx     int
	// Orphan marks a session that never saw a matching disconnect line and
	// was closed at Finalize at the last observed timestamp (see
	// ConnectionAnalyzer.Finalize). It is NOT a real disconnect and is
	// excluded from DisconnectionCount; the report's time-slider
	// re-aggregation reads this flag to tell an orphan from a genuine
	// disconnect instead of guessing from the end timestamp.
	Orphan bool
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
	// without allocating the full N×72 B slice (~410 MB on J.log).
	SessionEvents           []SessionEvent
	SessionsByUser          map[string]*StreamingDurationStats
	SessionsByDatabase      map[string]*StreamingDurationStats
	SessionsByHost          map[string]*StreamingDurationStats
	PeakConcurrentSessions  int
	PeakConcurrentTimestamp time.Time

	// Client I/O failures ("could not send/receive data ... client: reason").
	// ClientIOFailureCount is the total; the breakdown is split by direction
	// — ClientIORecv (could not receive *from* the client) vs ClientIOSend
	// (could not send *to* the client) — each cross-tabulating a normalized
	// strerror against the database it happened on (reason -> database ->
	// count). Renderers sort and, when a reason spans a single database,
	// collapse the database level away.
	ClientIOFailureCount int
	ClientIORecv         map[string]map[string]int
	ClientIOSend         map[string]map[string]int

	// receivedChunksRef and sessionChunksRef are the compact backing
	// storage moved from the analyzer at Finalize. The renderers iterate
	// these via IterateConnections / IterateSessionEvents instead of
	// allocating the full N×24 / N×72 B materialized slices, which on
	// J.log saved a 410 MB transient peak at output time. The per-session
	// entity indices ride for free in the unused high bits of each
	// compactSession timestamp (see compactSession) — no parallel array.
	//
	// locRef is the timezone captured from the first event observed by
	// the analyzer; reused when expanding compact Unix-ms timestamps so
	// the wall clock matches what the parser emitted.
	receivedChunksRef [][]int64
	sessionChunksRef  [][]compactSession
	locRef            *time.Location
	// sessionOrphanStart is the flat index into the session grid at which
	// the Finalize orphan-flush began: sessions at this index and beyond
	// are orphans (SessionEvent.Orphan == true). Real disconnects, all
	// appended during Process, occupy the lower indices.
	sessionOrphanStart int

	// SessionUserNames / SessionDatabaseNames / SessionHostNames: reverse
	// lookup tables for the interned indices carried by SessionEvent
	// (UserIdx / DatabaseIdx / HostIdx). Index 0 is always "" (unknown).
	// nil when no session ever had an extractable entity.
	SessionUserNames     []string
	SessionDatabaseNames []string
	SessionHostNames     []string
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
// order. Returning false stops iteration early. The timestamps and the
// entity indices are unpacked from the SAME compactSession — the indices
// live in its timestamps' unused high bits (see compactSession).
func (m *ConnectionMetrics) IterateSessionEvents(fn func(SessionEvent) bool) {
	if len(m.sessionChunksRef) > 0 {
		loc := m.locRef
		if loc == nil {
			loc = time.UTC
		}
		flat := 0
		for _, chunk := range m.sessionChunksRef {
			for _, s := range chunk {
				user, db, host := s.entities()
				if !fn(SessionEvent{
					StartTime:   time.UnixMilli(s.startMs()).In(loc),
					EndTime:     time.UnixMilli(s.endMs()).In(loc),
					UserIdx:     int(user),
					DatabaseIdx: int(db),
					HostIdx:     int(host),
					Orphan:      flat >= m.sessionOrphanStart,
				}) {
					return
				}
				flat++
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
// compactSession stores a completed session as two Unix-millisecond
// timestamps, 16 bytes vs 72 for SessionEvent (×4.5 denser). The
// time.Location pointer is dropped, all materializations resolve in the
// local zone. Millisecond precision is required so the sweep-line peak
// counts correctly when many short sessions cluster within the same
// wall-clock second (cf. connection-pool fixtures).
//
// Bit-packing: Unix-ms fits in ~43 bits (dates run to year 2248), so the
// top 21 bits of each int64 are free — 42 bits total, enough to carry the
// session's interned user/database/host indices for FREE (no parallel
// array). Layout:
//
//	startUnixMs: [63..54]=dbIdx(10b) [53..43]=userIdx(11b) [42..0]=start ms
//	endUnixMs:   [63..43]=hostIdx(21b)                     [42..0]=end ms
//
// host is the high-cardinality dimension, so it gets the wide 21-bit field.
// Read the real timestamps and indices ONLY through the masked accessors
// below — a raw field read leaks the entity bits into the ms value. All bit
// ops cast through uint64 so the shifts never sign-extend bit 63.
type compactSession struct {
	startUnixMs int64
	endUnixMs   int64
}

// tsBits is the number of low bits of each packed timestamp that hold the
// real Unix-millisecond value; tsMask isolates them.
const (
	tsBits = 43
	tsMask = int64(1)<<tsBits - 1
)

// startMs / endMs return the real Unix-millisecond timestamps with the
// packed entity bits masked away.
func (c compactSession) startMs() int64 { return int64(uint64(c.startUnixMs) & uint64(tsMask)) }
func (c compactSession) endMs() int64   { return int64(uint64(c.endUnixMs) & uint64(tsMask)) }

// entities unpacks the interned user/database/host indices (0 = unknown in
// each dimension) from the timestamps' high bits. host is uint32 because its
// 21-bit field exceeds uint16's range.
func (c compactSession) entities() (user, db uint16, host uint32) {
	user = uint16(uint64(c.startUnixMs) >> tsBits & 0x7FF)
	db = uint16(uint64(c.startUnixMs) >> 54 & 0x3FF)
	host = uint32(uint64(c.endUnixMs) >> tsBits & 0x1FFFFF)
	return
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

	// Same scheme for completed sessions. The per-session entity indices are
	// packed into the timestamps' unused high bits (see compactSession), so
	// there is no parallel entity array to retain.
	sessionChunks [][]compactSession

	// userIndex/userNames, databaseIndex/databaseNames, hostIndex/hostNames
	// intern the user/database/host strings extracted at a disconnect (see
	// extractEntityFromMessage) into small integer indices, which then ride
	// for free in the compactSession high bits instead of being retained as
	// strings. *Names[0] is the "" placeholder for index 0 ("unknown"); real
	// entities are interned from index 1. The per-dimension caps (see
	// internEntity) match the bit budgets: user 2047, db 1023, host 2097151.
	userIndex     map[string]uint32
	userNames     []string
	databaseIndex map[string]uint32
	databaseNames []string
	hostIndex     map[string]uint32
	hostNames     []string

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
	clientIORecv         map[string]map[string]int // reason -> database -> count (receive)
	clientIOSend         map[string]map[string]int // reason -> database -> count (send)

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
		userIndex:           make(map[string]uint32, 100),
		databaseIndex:       make(map[string]uint32, 50),
		hostIndex:           make(map[string]uint32, 100),
		activeConnections:   make(map[string]time.Time, 1000),
		clientIORecv:        make(map[string]map[string]int, 8),
		clientIOSend:        make(map[string]map[string]int, 8),
	}
}

// internEntity returns the 1-based interned index for name in the given
// (index, names) pair, allocating a new entry on first sight. Returns 0
// ("unknown") for an empty name. names[0] is lazily reserved as the ""
// placeholder for index 0 the first time a real name is interned, so a
// dimension that never sees an entity keeps names nil (omitted from JSON).
// Bounded at maxIdx distinct values — the width of the compactSession bit
// field this dimension packs into (user 2047, db 1023, host 2097151). A log
// whose entity cardinality overflows the field just stops interning past
// that point (returns 0), unrealistic for user/database/host in practice.
// The overflow degrades gracefully: past-cap entities collapse onto index 0
// ("unknown"), so the unfiltered totals still count them but they drop out of
// the report's client-side time-filtered per-entity breakdown (rebuilt from
// the interned indices, which no longer distinguish them). The cardinality
// ceilings are set so wide this cannot happen on any real cluster.
func internEntity(name string, index map[string]uint32, names *[]string, maxIdx uint32) uint32 {
	if name == "" {
		return 0
	}
	if idx, ok := index[name]; ok {
		return idx
	}
	if len(*names) == 0 {
		*names = append(*names, "") // reserve index 0 for "unknown"
	}
	if uint32(len(*names)) > maxIdx {
		return 0
	}
	idx := uint32(len(*names))
	*names = append(*names, name)
	index[name] = idx
	return idx
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

// addSession appends a completed session to the compact chunked storage,
// packing the interned user/database/host indices into the timestamps'
// unused high bits (see compactSession). All-zero indices ("unknown") mean
// the ms values are stored verbatim — the case for the receivedAt-only
// disconnect fallback and the Finalize orphan-flush.
func (a *ConnectionAnalyzer) addSession(start, end time.Time, userIdx, dbIdx, hostIdx uint32) {
	if a.loc == nil {
		a.loc = start.Location()
	}
	s := uint64(start.UnixMilli())&uint64(tsMask) | uint64(userIdx)<<tsBits | uint64(dbIdx)<<54
	e := uint64(end.UnixMilli())&uint64(tsMask) | uint64(hostIdx)<<tsBits
	cs := compactSession{startUnixMs: int64(s), endUnixMs: int64(e)}
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
	// Their body always starts with "could not ": when the dispatcher stamped
	// a body offset, anchor the gate there in O(1) instead of scanning the
	// whole message for " client: " on every entry.
	if off := int(entry.BodyOffset); off > 0 && off < len(msg) {
		if strings.HasPrefix(msg[off:], "could not ") {
			a.recordClientIOFailure(msg)
		}
	} else {
		a.recordClientIOFailure(msg)
	}

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

			// Extract user, database, and host from disconnection message,
			// and intern them into small integer indices — the indices ride
			// for free in the compact session storage (packed into the
			// timestamps' high bits), so the report's time filter can
			// re-scope the per-user/database/host tables without retaining
			// entity strings per session. Caps match the packing bit budgets.
			user := extractEntityFromMessage(msg, "user")
			database := extractEntityFromMessage(msg, "database")
			host := extractEntityFromMessage(msg, "host")
			userIdx := internEntity(user, a.userIndex, &a.userNames, 2047)
			dbIdx := internEntity(database, a.databaseIndex, &a.databaseNames, 1023)
			hostIdx := internEntity(host, a.hostIndex, &a.hostNames, 2097151)

			// Store session event for concurrent tracking
			startTime := entry.Timestamp.Add(-duration)
			a.addSession(startTime, entry.Timestamp, userIdx, dbIdx, hostIdx)

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
			// PG-reported duration, only a coarse observed window. No
			// entity was extracted either, so this session's indices are
			// all "unknown" (zero value).
			a.addSession(receivedAt, entry.Timestamp, 0, 0, 0)
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
	//
	// Record where the flush begins first: every session appended so far is
	// a real disconnect, everything appended from here on is an orphan. That
	// boundary index is what IterateSessionEvents reads to set
	// SessionEvent.Orphan, so the report can exclude orphans by flag instead
	// of guessing from the end timestamp (an orphan's end is the last
	// observed timestamp, which can sit below the global end_date).
	sessionOrphanStart := 0
	for _, c := range a.sessionChunks {
		sessionOrphanStart += len(c)
	}
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
			a.addSession(receivedAt, a.lastSeenTimestamp, 0, 0, 0)
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

		SessionUserNames:     a.userNames,
		SessionDatabaseNames: a.databaseNames,
		SessionHostNames:     a.hostNames,

		receivedChunksRef:  a.receivedChunks,
		sessionChunksRef:   a.sessionChunks,
		locRef:             loc,
		sessionOrphanStart: sessionOrphanStart,
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
	// Flatten the chunk grid into two plain []int64 lists — one of start
	// timestamps, one of end timestamps. The sweep below only ever reads
	// the millisecond VALUES, so sorting values directly with slices.Sort
	// (branch-free int64 pdqsort) replaces the previous uint32-index sort
	// whose comparator chased two pointers through the chunk grid on
	// every comparison. ~2× the transient footprint of the index version
	// (16 B vs 8 B per session) but still ~4× below the old SessionEvent
	// slice, and the sort drops from seconds to a fraction.
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	startMs := make([]int64, 0, n)
	endMs := make([]int64, 0, n)
	for _, chunk := range chunks {
		for _, s := range chunk {
			// Mask off the packed entity bits — the sweep needs the real ms.
			sm, em := s.startMs(), s.endMs()
			if sm == 0 || em == 0 {
				continue
			}
			startMs = append(startMs, sm)
			endMs = append(endMs, em)
		}
	}
	if len(startMs) == 0 {
		return 0, time.Time{}
	}
	slices.Sort(startMs)
	slices.Sort(endMs)

	// Sweep both index lists in lockstep. At each step take the earlier
	// pending timestamp; tie-break "starts (+1) before ends (-1)" so
	// the local peak is captured before the matching decrement.
	cur, peak := 0, 0
	var peakMs int64
	s, e := 0, 0
	for s < len(startMs) || e < len(endMs) {
		var ms int64
		var delta int
		switch {
		case e >= len(endMs):
			ms = startMs[s]
			delta = +1
			s++
		case s >= len(startMs):
			ms = endMs[e]
			delta = -1
			e++
		default:
			// startMs[s] <= endMs[e] → take start (covers tie: +1 before -1)
			if startMs[s] <= endMs[e] {
				ms = startMs[s]
				delta = +1
				s++
			} else {
				ms = endMs[e]
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

	var (
		dir    map[string]map[string]int
		reason string
	)
	switch {
	case strings.HasPrefix(body, "could not send data to client: "):
		dir, reason = a.clientIOSend, clientIOReason(body[len("could not send data to client: "):])
	case strings.HasPrefix(body, "could not receive data from client: "):
		dir, reason = a.clientIORecv, clientIOReason(body[len("could not receive data from client: "):])
	default:
		return
	}

	db := extractEntityFromMessage(msg, "database")
	if db == "" {
		db = "[unknown]"
	}
	if dir[reason] == nil {
		dir[reason] = make(map[string]int, 2)
	}
	dir[reason][db]++
	a.clientIOFailureCount++
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
	// Trim metadata that trails the strerror in some inputs and would fragment
	// or pollute the category: the statement's cursor position that stderr
	// appends ("... at character N"), and the SQLSTATE that the csv/json
	// parsers fold back into the message ("... SQLSTATE = 'XXXXX'"). Keep just
	// the OS error.
	for _, cut := range []string{" at character ", " sqlstate"} {
		if i := strings.Index(r, cut); i >= 0 {
			r = r[:i]
		}
	}
	r = strings.TrimSpace(r)
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
