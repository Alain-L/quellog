// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// ReplicationMetrics aggregates PostgreSQL replication-related events.
//
// The goal is operational: help a DBA answer "how many times did my
// standby disconnect last night?" and "which queries were killed by a
// conflict with recovery?". We surface a handful of well-known markers
// (see replication marker constants) plus a small histogram per hour
// so spikes are obvious in the CLI.
type ReplicationMetrics struct {
	// Markers tracks the count for each pattern key (see replMarker* constants).
	Markers map[string]int
	// Events lists every replication event we captured, in arrival order.
	Events []ReplicationEvent
	// HourCounts buckets the total replication events per hour-of-day
	// ("00", "01", ..., "23") so the CLI can render a compact histogram.
	HourCounts map[string]int
	// LastTermination is the timestamp of the most recent replication
	// termination event (any of: terminated by primary, walsender timeout,
	// could not receive data). Zero if none observed.
	LastTermination time.Time
	// PeakHourLabel is the hour (e.g. "04:00-05:00") with the most
	// stream reconnects. Empty if no reconnects were captured.
	PeakHourLabel string
	PeakHourCount int
	// ConflictQueries holds per-query stats for queries terminated/
	// cancelled by a conflict with recovery.
	ConflictQueries map[string]*ReplicationConflictQueryStat
	// HasAny reports whether the analyzer saw at least one marker.
	HasAny bool
}

// ReplicationEvent is a single replication-related log entry.
type ReplicationEvent struct {
	Timestamp time.Time
	Marker    string // pattern key (see replMarker* constants)
	Severity  string // LOG / WARNING / ERROR / FATAL
}

// ReplicationConflictQueryStat aggregates per-query stats for queries
// terminated/cancelled by a conflict with recovery.
type ReplicationConflictQueryStat struct {
	ID              string
	FullHash        string
	NormalizedQuery string
	RawQuery        string
	Count           int
}

// ============================================================================
// Marker keys (stable identifiers used in JSON output)
// ============================================================================

const (
	// LOG-severity informational markers.
	replMarkerStreamStarted    = "stream_started"    // "started streaming WAL from primary at ..."
	replMarkerRecoveryPaused   = "recovery_paused"   // "recovery has paused"
	replMarkerRecoveryResuming = "recovery_resuming" // "recovery is resuming"

	// Termination markers (LOG or FATAL/ERROR).
	replMarkerWALReceiveFailed = "wal_receive_failed" // "could not receive data from WAL stream"
	replMarkerReplicationTerm  = "replication_term"  // "replication terminated by primary server"
	replMarkerWalsenderTimeout = "walsender_timeout" // "terminating walsender process due to replication timeout"
	replMarkerUnexpectedEOF    = "unexpected_eof"    // "unexpected EOF on standby connection"

	// Recovery-conflict markers (WARNING or ERROR/FATAL).
	replMarkerConflictTerminate = "conflict_terminate" // "terminating connection due to conflict with recovery"
	replMarkerConflictCancel    = "conflict_cancel"    // "canceling statement due to conflict with recovery"

	// Slot-management markers.
	replMarkerSlotInvalidated = "slot_invalidated" // "replication slot \"X\" has been invalidated"
)

// Note: we deliberately skip a few markers that turned out noisy or
// hard to interpret without context:
//   - "received SIGTERM from postmaster" (any backend can emit this,
//     not just walsenders; including it would double-count shutdowns).
//   - "replication command received" (informational, fires per command
//     during normal streaming — would bury the signal under noise).

// replMarkerPattern lists the substring → marker-key mapping. Order
// matters: longer / more specific patterns first so we don't bucket a
// "canceling statement due to conflict with recovery" under a generic
// "conflict with recovery" key.
var replMarkerPatterns = []struct {
	substr string
	key    string
}{
	// Stream lifecycle (LOG).
	{"started streaming WAL from primary", replMarkerStreamStarted},
	{"recovery has paused", replMarkerRecoveryPaused},
	{"recovery is resuming", replMarkerRecoveryResuming},

	// Termination patterns.
	{"could not receive data from WAL stream", replMarkerWALReceiveFailed},
	{"replication terminated by primary server", replMarkerReplicationTerm},
	{"terminating walsender process due to replication timeout", replMarkerWalsenderTimeout},
	{"unexpected EOF on standby connection", replMarkerUnexpectedEOF},

	// Recovery conflicts.
	{"terminating connection due to conflict with recovery", replMarkerConflictTerminate},
	{"canceling statement due to conflict with recovery", replMarkerConflictCancel},

	// Slot management.
	{"has been invalidated", replMarkerSlotInvalidated},
}

// terminationMarkers is the subset of marker keys that represent a
// "termination" event for the purposes of LastTermination tracking.
var terminationMarkers = map[string]bool{
	replMarkerWALReceiveFailed: true,
	replMarkerReplicationTerm:  true,
	replMarkerWalsenderTimeout: true,
	replMarkerUnexpectedEOF:    true,
}

// ============================================================================
// Streaming replication analyzer
// ============================================================================

// ReplicationAnalyzer processes replication-related events from log
// entries in streaming mode.
//
// Usage:
//
//	analyzer := NewReplicationAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type ReplicationAnalyzer struct {
	markers         map[string]int
	events          []ReplicationEvent
	hourCounts      map[string]int
	lastTermination time.Time

	// pendingConflictPID tracks PIDs that just had a conflict event so
	// the next STATEMENT continuation for that PID gets attributed to
	// the conflict query stats.
	pendingConflictPID map[string]bool

	conflictQueries map[string]*ReplicationConflictQueryStat

	hasAny bool
}

// NewReplicationAnalyzer creates a new replication event analyzer.
func NewReplicationAnalyzer() *ReplicationAnalyzer {
	return &ReplicationAnalyzer{
		markers:            make(map[string]int, 10),
		events:             make([]ReplicationEvent, 0, 32),
		hourCounts:         make(map[string]int, 24),
		pendingConflictPID: make(map[string]bool, 8),
		conflictQueries:    make(map[string]*ReplicationConflictQueryStat, 8),
	}
}

// Process analyzes a single log entry for replication markers.
//
// We accept both fresh entries and continuation lines: the latter lets
// us pick up a STATEMENT: that immediately follows a "conflict with
// recovery" ERROR, and resolve it to a query_id.
func (a *ReplicationAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message
	if len(msg) < 12 {
		return
	}

	// Cheap pre-filter: every replication marker contains at least
	// one of these substrings. Skipping early on lines that can't
	// carry a marker keeps the hot path linear on huge logs.
	if !replPrefilter(msg) {
		// Still handle the STATEMENT continuation for a pending conflict PID.
		if entry.IsContinuation && entry.PID != "" && a.pendingConflictPID[entry.PID] {
			a.captureConflictStatement(entry, msg)
		}
		return
	}

	// STATEMENT continuation for a conflict — record the query, then
	// fall through so a non-conflict marker on the same line (rare) is
	// not missed.
	if entry.IsContinuation && entry.PID != "" && a.pendingConflictPID[entry.PID] {
		a.captureConflictStatement(entry, msg)
	}

	// Find first matching marker. Order is significant (longest match first).
	for _, p := range replMarkerPatterns {
		if !strings.Contains(msg, p.substr) {
			continue
		}
		// Slot invalidation is identified by a more specific test: the
		// "has been invalidated" substring is broad enough that we
		// require "replication slot" on the same line to avoid
		// false-positives on unrelated logical decoding logs.
		if p.key == replMarkerSlotInvalidated && !strings.Contains(msg, "replication slot") {
			continue
		}

		a.recordMarker(entry, p.key)
		// One marker per line: PostgreSQL never emits two of these on
		// the same log entry.
		return
	}
}

// replPrefilter is a cheap sniff over the message: at least one of the
// short substrings below must be present for any replication marker
// to match. Hand-picked from the actual marker patterns.
func replPrefilter(msg string) bool {
	// "WAL", "walsender", "recovery", "replication", "standby"
	return strings.Contains(msg, "WAL") ||
		strings.Contains(msg, "walsender") ||
		strings.Contains(msg, "recovery") ||
		strings.Contains(msg, "replication") ||
		strings.Contains(msg, "standby")
}

// recordMarker stores a marker hit and updates aggregates.
func (a *ReplicationAnalyzer) recordMarker(entry *parser.LogEntry, key string) {
	a.hasAny = true
	a.markers[key]++

	severity := extractSeverityFromMessage(entry.Message)
	a.events = append(a.events, ReplicationEvent{
		Timestamp: entry.Timestamp,
		Marker:    key,
		Severity:  severity,
	})

	if !entry.Timestamp.IsZero() {
		hour := entry.Timestamp.Format("15")
		a.hourCounts[hour]++
	}

	if terminationMarkers[key] {
		if !entry.Timestamp.IsZero() && entry.Timestamp.After(a.lastTermination) {
			a.lastTermination = entry.Timestamp
		}
	}

	// Mark this PID as pending so a follow-up STATEMENT line resolves
	// to a conflict query stat. The flag is cleared once we capture
	// (or after the next non-continuation event for the PID).
	if key == replMarkerConflictTerminate || key == replMarkerConflictCancel {
		if entry.PID != "" {
			a.pendingConflictPID[entry.PID] = true
		}
	}
}

// captureConflictStatement extracts the SQL text from a STATEMENT
// continuation following a conflict event and aggregates it into
// ConflictQueries. Mirrors the helper used by the lock analyzer.
func (a *ReplicationAnalyzer) captureConflictStatement(entry *parser.LogEntry, msg string) {
	var query string
	if idx := strings.Index(msg, "STATEMENT:"); idx != -1 {
		query = strings.TrimSpace(msg[idx+10:])
	} else if idx := strings.Index(msg, "statement:"); idx != -1 {
		query = strings.TrimSpace(msg[idx+10:])
	}
	if query == "" {
		return
	}
	// Clear pending flag — one capture per conflict event.
	delete(a.pendingConflictPID, entry.PID)

	query = normalizeWhitespace(query)
	normalized := normalizeQuery(query)
	shortID, fullHash := GenerateQueryID(query, normalized)

	stat, ok := a.conflictQueries[fullHash]
	if !ok {
		stat = &ReplicationConflictQueryStat{
			ID:              shortID,
			FullHash:        fullHash,
			NormalizedQuery: normalized,
			RawQuery:        query,
		}
		a.conflictQueries[fullHash] = stat
	} else if query < stat.RawQuery {
		// Deterministic raw_query: pick the alphabetically smallest.
		stat.RawQuery = query
	}
	stat.Count++
}

// extractSeverityFromMessage returns the PostgreSQL severity prefix
// (LOG, WARNING, ERROR, FATAL, PANIC) from a message that carries one,
// or "" if not found.
func extractSeverityFromMessage(msg string) string {
	for _, sev := range []string{"FATAL", "ERROR", "WARNING", "PANIC", "LOG"} {
		if strings.Contains(msg, sev+":") {
			return sev
		}
	}
	return ""
}

// Finalize computes derived stats (peak hour, longest hour label) and
// returns the aggregated metrics. Idempotent: safe to call multiple times.
func (a *ReplicationAnalyzer) Finalize() ReplicationMetrics {
	if !a.hasAny {
		return ReplicationMetrics{
			Markers:         make(map[string]int),
			HourCounts:      make(map[string]int),
			ConflictQueries: make(map[string]*ReplicationConflictQueryStat),
		}
	}

	// Find peak hour by stream reconnects (most operationally relevant).
	// Fall back to peak hour of all events if no stream starts were captured.
	peakHour, peakCount := "", 0
	reconnectHours := make(map[string]int)
	for _, ev := range a.events {
		if ev.Marker == replMarkerStreamStarted && !ev.Timestamp.IsZero() {
			h := ev.Timestamp.Format("15")
			reconnectHours[h]++
		}
	}
	target := reconnectHours
	if len(target) == 0 {
		target = a.hourCounts
	}
	for h, c := range target {
		if c > peakCount {
			peakHour, peakCount = h, c
		}
	}

	peakLabel := ""
	if peakHour != "" {
		// "04:00-05:00" — easier to read than just "04".
		nextHour := "00"
		if h := parseHour(peakHour); h >= 0 && h < 23 {
			nextHour = padHour(h + 1)
		}
		peakLabel = peakHour + ":00-" + nextHour + ":00"
	}

	return ReplicationMetrics{
		Markers:         a.markers,
		Events:          a.events,
		HourCounts:      a.hourCounts,
		LastTermination: a.lastTermination,
		PeakHourLabel:   peakLabel,
		PeakHourCount:   peakCount,
		ConflictQueries: a.conflictQueries,
		HasAny:          true,
	}
}

// parseHour parses a 2-digit hour string ("00".."23") into an int, or
// returns -1 on error.
func parseHour(s string) int {
	if len(s) != 2 {
		return -1
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	if h < 0 || h > 23 {
		return -1
	}
	return h
}

// padHour zero-pads an hour int into a 2-digit string.
func padHour(h int) string {
	if h < 10 {
		return "0" + string('0'+rune(h))
	}
	return string('0'+rune(h/10)) + string('0'+rune(h%10))
}
