// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// ServerTimelineEvent is one entry in the server lifecycle timeline.
// Kind is a short tag (e.g. "start", "shutdown", "sighup", "crash",
// "recovery"); Detail carries the human-readable specifics ("signal 11",
// "fast", "3 parameter changes", ...) so a single row reads on its own.
type ServerTimelineEvent struct {
	Timestamp time.Time
	Kind      string
	Detail    string
	seq       int64 // stream position (unexported: not serialized), for stable cross-shard merge
}

// ServerParameterChange records one "parameter X changed to Y" line.
// Old is empty when PG just announces a new value without restating the
// previous one — which is the common case for SIGHUP reloads (PG does
// not log the previous value, only the new one). The field is kept so
// future consumers can populate it from a config diff if they want.
type ServerParameterChange struct {
	Parameter string
	Old       string
	New       string
	Timestamp time.Time
	seq       int64 // stream position (unexported: not serialized), for stable cross-shard merge
}

// ServerMetrics aggregates the PostgreSQL server lifecycle events:
// starts, reloads, shutdowns, crashes, auxiliary-process exits, plus
// the per-reload parameter changes. The goal is to give a DBA the
// "what happened to my server?" timeline they would otherwise rebuild
// by grep'ing the logs at post-mortem.
type ServerMetrics struct {
	StartCount int
	StartTimes []time.Time

	ReloadCount int
	ReloadTimes []time.Time

	// Shutdown subtypes — fast (default in 99% of cases), immediate
	// (-m immediate), smart (graceful, waits for connections).
	ShutdownFastCount      int
	ShutdownImmediateCount int
	ShutdownSmartCount     int
	ShutdownTimes          []time.Time

	// ShutDownCompletedCount counts "database system is shut down"
	// messages — the post-condition of any successful shutdown.
	ShutDownCompletedCount int

	// CrashRecoveryCount counts "database system was not properly
	// shut down; automatic recovery in progress" — the signal that
	// the previous shutdown was not clean.
	CrashRecoveryCount int
	CrashRecoveryTimes []time.Time

	// InterruptedCount counts "database system was interrupted; last
	// known up at ..." — usually precedes CrashRecovery.
	InterruptedCount int

	// BackendCrashCount counts "server process (PID N) was terminated
	// by signal X" lines. SignalCounts maps signal number → occurrences
	// (signal 9 = SIGKILL, 11 = SIGSEGV, 6 = SIGABRT, ...).
	BackendCrashCount int
	BackendCrashTimes []time.Time
	SignalCounts      map[string]int

	// AuxProcessExitCount lumps together archiver/wal-writer/bgwriter/
	// checkpointer "exited with exit code N" lines — same shape, same
	// operational meaning ("a service helper crashed and was respawned").
	AuxProcessExitCount int

	// ParameterChanges records every "parameter X changed to Y" line,
	// in chronological order. They are kept flat (not bucketed per
	// reload) because PG does not tag a SIGHUP id; the renderers pair
	// them with the closest preceding reload by timestamp.
	ParameterChanges []ServerParameterChange

	// Timeline is a compact, capped log of lifecycle events suitable
	// for rendering "what happened, in order". Capped at
	// serverTimelineCap to avoid unbounded growth on multi-day logs
	// with frequent SIGHUPs.
	Timeline []ServerTimelineEvent
}

// serverTimelineCap is the maximum number of events kept in the
// rendered timeline. Real-world logs we have seen carry at most a
// handful of lifecycle events per day; 50 covers a typical week and
// keeps memory + render bounded on pathological inputs.
const serverTimelineCap = 50

// Server log message patterns. The matcher does cheap prefix dispatch
// against a stripped body so the hot path is short.
//
// Markers we INTENTIONALLY skip in this MVP:
//
//   - "57P03: the database system is in recovery mode" — emitted to
//     every rejected client connection during crash recovery; on
//     real logs that is hundreds of repeats for a single crash, so
//     counting them at the server level would be noise. The
//     interesting signal is the parent "system was interrupted" /
//     "not properly shut down", which we do capture.
//
//   - "parameter X cannot be changed without restarting the server"
//     (SQLSTATE 55P02) — a config-file warning, not a parameter
//     change. We focus on actual changes here.
//
//   - "received SIGUSR1/SIGUSR2" — internal coordination signals,
//     not actionable for a DBA.
const (
	parameterChangedPrefix = "parameter \""
	terminatedBySignal     = " was terminated by signal "
	exitedWithExitCode     = " exited with exit code "
)

// ServerAnalyzer processes server-lifecycle events in streaming mode.
//
// Usage:
//
//	analyzer := NewServerAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type ServerAnalyzer struct {
	m ServerMetrics
	// curSeq is the stream position of the entry currently being processed,
	// stamped onto each timeline/parameter event so the cross-shard merge
	// can restore exact single-pass order on equal timestamps.
	curSeq int64
}

// NewServerAnalyzer creates a new server-lifecycle analyzer.
func NewServerAnalyzer() *ServerAnalyzer {
	return &ServerAnalyzer{
		m: ServerMetrics{
			SignalCounts: make(map[string]int),
		},
	}
}

// Process examines a single log entry for server-lifecycle markers.
// Continuation lines (DETAIL/HINT/STATEMENT) are skipped — none of the
// markers we care about ever appears in a continuation.
func (a *ServerAnalyzer) Process(entry *parser.LogEntry) {
	if entry.IsContinuation {
		return
	}
	a.curSeq = entry.Seq
	msg := entry.Message
	if len(msg) < 12 {
		return
	}

	// Strip the prefix up to "LOG:" / "WARNING:" so substring scans hit
	// the marker right away regardless of log_line_prefix shape.
	body := stripSeverityPrefix(msg)

	switch {
	case strings.HasPrefix(body, "database system is ready"):
		a.recordStart(entry.Timestamp)
	case strings.HasPrefix(body, "database system is shut down"):
		a.m.ShutDownCompletedCount++
	case strings.HasPrefix(body, "database system was interrupted"):
		a.m.InterruptedCount++
		a.pushTimeline(entry.Timestamp, "interrupted", "system was interrupted")
	case strings.HasPrefix(body, "database system was not properly shut down"):
		a.m.CrashRecoveryCount++
		a.m.CrashRecoveryTimes = append(a.m.CrashRecoveryTimes, entry.Timestamp)
		a.pushTimeline(entry.Timestamp, "recovery", "not properly shut down — automatic recovery")
	case strings.HasPrefix(body, "received SIGHUP"):
		a.recordReload(entry.Timestamp)
	case strings.HasPrefix(body, "received fast shutdown"):
		a.recordShutdown(entry.Timestamp, "fast")
	case strings.HasPrefix(body, "received immediate shutdown"):
		a.recordShutdown(entry.Timestamp, "immediate")
	case strings.HasPrefix(body, "received smart shutdown"):
		a.recordShutdown(entry.Timestamp, "smart")
	case strings.HasPrefix(body, parameterChangedPrefix):
		a.recordParameterChange(entry.Timestamp, body)
	case strings.HasPrefix(body, "server process (PID "):
		a.recordBackendCrash(entry.Timestamp, body)
	case strings.HasPrefix(body, "archiver process (PID "),
		strings.HasPrefix(body, "WAL writer process (PID "),
		strings.HasPrefix(body, "background writer process (PID "),
		strings.HasPrefix(body, "checkpointer process (PID "):
		if strings.Contains(body, exitedWithExitCode) {
			a.m.AuxProcessExitCount++
		}
	}
}

// stripSeverityPrefix returns the message body starting right after the
// "LOG:" / "WARNING:" marker (and any leading SQLSTATE code like
// "00000:" that verbose logs prepend). Falls back to the original
// message when no marker is found — defensive only, every real PG line
// carries one.
func stripSeverityPrefix(msg string) string {
	for _, sev := range []string{" LOG:  ", " LOG: ", " WARNING:  ", " WARNING: "} {
		if idx := strings.Index(msg, sev); idx >= 0 {
			rest := msg[idx+len(sev):]
			// Strip optional SQLSTATE code "00000: " that
			// log_error_verbosity = verbose prepends.
			if len(rest) >= 7 && rest[5] == ':' && rest[6] == ' ' {
				if isSQLSTATEPrefix(rest[:5]) {
					return rest[7:]
				}
			}
			return rest
		}
	}
	return msg
}

// isSQLSTATEPrefix returns true when s looks like a 5-char SQLSTATE
// code (all upper/digit). Used to skip "00000: " in verbose logs.
func isSQLSTATEPrefix(s string) bool {
	if len(s) != 5 {
		return false
	}
	for i := 0; i < 5; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func (a *ServerAnalyzer) recordStart(t time.Time) {
	a.m.StartCount++
	a.m.StartTimes = append(a.m.StartTimes, t)
	a.pushTimeline(t, "start", "database system is ready to accept connections")
}

func (a *ServerAnalyzer) recordReload(t time.Time) {
	a.m.ReloadCount++
	a.m.ReloadTimes = append(a.m.ReloadTimes, t)
	a.pushTimeline(t, "sighup", "reloading configuration files")
}

func (a *ServerAnalyzer) recordShutdown(t time.Time, kind string) {
	switch kind {
	case "fast":
		a.m.ShutdownFastCount++
	case "immediate":
		a.m.ShutdownImmediateCount++
	case "smart":
		a.m.ShutdownSmartCount++
	}
	a.m.ShutdownTimes = append(a.m.ShutdownTimes, t)
	a.pushTimeline(t, "shutdown", kind+" shutdown request")
}

func (a *ServerAnalyzer) recordParameterChange(t time.Time, body string) {
	// body looks like: parameter "work_mem" changed to "16MB"
	// Defensive parsing — be quiet on malformed inputs.
	rest := body[len(parameterChangedPrefix):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return
	}
	param := rest[:end]
	rest = rest[end+1:]
	idx := strings.Index(rest, "changed to \"")
	if idx < 0 {
		return
	}
	newRest := rest[idx+len("changed to \""):]
	endNew := strings.IndexByte(newRest, '"')
	if endNew < 0 {
		return
	}
	newVal := newRest[:endNew]
	a.m.ParameterChanges = append(a.m.ParameterChanges, ServerParameterChange{
		Parameter: param,
		New:       newVal,
		Timestamp: t,
		seq:       a.curSeq,
	})
}

func (a *ServerAnalyzer) recordBackendCrash(t time.Time, body string) {
	// body: server process (PID 12345) was terminated by signal 11: Segmentation fault
	idx := strings.Index(body, terminatedBySignal)
	if idx < 0 {
		// Anything else is a parsing edge case (or a future PG marker)
		// — count it but skip signal classification.
		a.m.BackendCrashCount++
		a.m.BackendCrashTimes = append(a.m.BackendCrashTimes, t)
		a.pushTimeline(t, "crash", "server process terminated")
		return
	}
	rest := body[idx+len(terminatedBySignal):]
	// Pull the signal number — digits up to ':' or end.
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		a.m.BackendCrashCount++
		a.m.BackendCrashTimes = append(a.m.BackendCrashTimes, t)
		a.pushTimeline(t, "crash", "server process terminated")
		return
	}
	sig := rest[:end]
	a.m.BackendCrashCount++
	a.m.BackendCrashTimes = append(a.m.BackendCrashTimes, t)
	a.m.SignalCounts[sig]++

	// Extract PID for the timeline detail — caller-friendly.
	pidStart := strings.Index(body, "PID ")
	pidStr := ""
	if pidStart >= 0 {
		pidRest := body[pidStart+4:]
		pe := strings.IndexByte(pidRest, ')')
		if pe > 0 {
			pidStr = pidRest[:pe]
		}
	}
	detail := "terminated by signal " + sig
	if pidStr != "" {
		detail = "PID " + pidStr + " " + detail
	}
	a.pushTimeline(t, "crash", detail)
}

// pushTimeline appends one event, dropping silently once the cap is
// reached. We bias toward keeping the earliest events because the
// "what happened first" question is what DBAs reach for; the alternative
// (sliding window) hides the start of incidents on noisy logs.
func (a *ServerAnalyzer) pushTimeline(t time.Time, kind, detail string) {
	if len(a.m.Timeline) >= serverTimelineCap {
		return
	}
	a.m.Timeline = append(a.m.Timeline, ServerTimelineEvent{
		Timestamp: t,
		Kind:      kind,
		Detail:    detail,
		seq:       a.curSeq,
	})
}

// Finalize returns the accumulated server-lifecycle metrics.
func (a *ServerAnalyzer) Finalize() ServerMetrics {
	return a.m
}

// HasAny reports whether any server-lifecycle event was captured. The
// renderers use this to decide whether to emit the SERVER section at
// all — an empty server section would be noise on the bulk of logs.
func (m ServerMetrics) HasAny() bool {
	return m.StartCount > 0 ||
		m.ReloadCount > 0 ||
		m.ShutdownFastCount > 0 ||
		m.ShutdownImmediateCount > 0 ||
		m.ShutdownSmartCount > 0 ||
		m.ShutDownCompletedCount > 0 ||
		m.CrashRecoveryCount > 0 ||
		m.InterruptedCount > 0 ||
		m.BackendCrashCount > 0 ||
		m.AuxProcessExitCount > 0 ||
		len(m.ParameterChanges) > 0
}
