// Package analysis aggregates parsed log entries into reporting metrics.
package analysis

import (
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// pgbrUnknownStanza holds the entries seen before any line of the source
// names a stanza. The first stanza named takes them over; a source that
// never names one renames the bucket after its file name when that name
// carries a stanza (see endSource). A report left with it has Stanza "".
const pgbrUnknownStanza = ""

// PgBackRestUnnamedStanza is how outputs name a report whose lines named
// no stanza (Stanza "").
const PgBackRestUnnamedStanza = "(from PostgreSQL log)"

// PgBackRestStanzaName is stanza, or PgBackRestUnnamedStanza for "".
func PgBackRestStanzaName(stanza string) string {
	if stanza == "" {
		return PgBackRestUnnamedStanza
	}
	return stanza
}

// --- Result types (no json tags: the JSON/YAML view is built in
// output/pgbackrest_json.go, as for the rest of quellog's analyzers). ---

// PgBackRestBackup is one backup command run.
type PgBackRestBackup struct {
	Label         string
	Type          string // full, diff, incr — after any "changed to full"
	ChangedToFull bool
	// RequestedType is the type that was actually requested (--type=,
	// or pgBackRest's own default when that option was not given) when
	// ChangedToFull is true — the origin Type itself no longer carries,
	// having been normalized to "full". Empty when ChangedToFull is
	// false, or when the request could not be determined.
	RequestedType string
	Repo          string
	Start         time.Time
	End           time.Time
	Duration      time.Duration
	HasDuration   bool
	SizeRaw       string // as pgBackRest wrote it ("22MB", "8.3KB")
	SizeBytes     int64  // SizeRaw in bytes (1024-based units), when HasSize
	HasSize       bool
	FileTotal     int
	Succeeded     bool
	ErrorCode     int
	ErrorLine     string
	// ErrorDetail holds the lines written under ErrorLine: the cause
	// ("FileMissingError: ...") and the HINTs.
	ErrorDetail []string
	ExecID      string
	// Crashed is true when this run never wrote its own "command end"
	// line at all (the process was killed or crashed) — reported as
	// such, never as a success. End is then zero: the log does not say
	// when the run stopped, only when it wrote its last line (LastLine).
	Crashed bool
	// LastLine is when a run with no "command end" (crashed or in
	// progress) wrote its last line, see pgbrRun.lastSeen.
	LastLine time.Time
	// Signal names the signal a crashed run's log says terminated it
	// ("SIGTERM", from pgBackRest's own "terminated on signal" line), ""
	// when the log says nothing more than the missing "command end".
	Signal string
	// InProgress is true when the run had not ended by the --end bound
	// while lines after it were left out: its outcome is past the window.
	InProgress bool
	// WaitingFor names the checkpoint a crashed run was waiting for when
	// its last line said so ("next regular checkpoint"), "" otherwise.
	WaitingFor string
}

// PgBackRestCommandStat rolls up every run of one command (begin/end
// pairs) regardless of outcome.
type PgBackRestCommandStat struct {
	Command       string
	Runs          int
	Succeeded     int
	Failed        int
	TotalDuration time.Duration
	MaxDuration   time.Duration
}

// PgBackRestFailureStretch is one run of consecutive archive-push/get
// failures for the same WAL segment.
type PgBackRestFailureStretch struct {
	Segment   string
	Code      int
	FirstLine string
	First     time.Time
	Last      time.Time
	Attempts  int
	// Recovery says whether a successful push of this segment or a later
	// one followed (archive-push, see PgBackRestRecovery*; RecoveredBy is
	// that first push when it did), or whether archive-get later found a
	// WAL segment for the same data directory (archive-get).
	Recovery    string
	RecoveredBy *PgBackRestPush
	// Path is an archive-get stretch's data directory (--pg1-path): its
	// stretches are kept per directory, a restored instance or a standby,
	// and Segment is the first WAL segment it asked for.
	Path string
}

// Recovery values of an archive-push failure stretch.
const (
	PgBackRestRecovered       = "recovered"
	PgBackRestStillFailing    = "still_failing" // successful pushes are logged, none followed
	PgBackRestNoLaterPush     = "no_later_push" // none followed for this stanza, another stanza pushed the same segment afterwards
	PgBackRestRecoveryUnknown = "unknown"       // successful pushes are not logged, or --end cut the input
)

// PgBackRestPush is one successful WAL push. Mode is "sync" (archive-push
// pushed it), "async" (through the async worker: the worker's batch, by
// its last segment, or the synchronous call's "pushed ... asynchronously")
// or "postgresql" (PostgreSQL's DEBUG1 "archived write-ahead log file").
type PgBackRestPush struct {
	Segment string
	Time    time.Time
	Mode    string
	// batchFailed are the segments the async batch behind this push
	// could not push: the push says nothing of them.
	batchFailed []string
}

// PgBackRestArchiveStats covers one direction (push or get) of WAL
// archiving. The sync counters are the command PostgreSQL runs, the async
// ones the archive-async worker: they are never added together, so that
// Failed compares with PostgreSQL's own count (PGArchiveFailures).
type PgBackRestArchiveStats struct {
	Pushed        int // archive-push: synchronous success
	PushedAsync   int // archive-push: pushed through the async worker, from its own log or the sync call's "pushed ... asynchronously"
	Found         int // archive-get: synchronous success
	FoundAsync    int // archive-get: async worker success
	NotFound      int // archive-get: WAL not in the archive (end of recovery — expected)
	NotFoundAsync int
	DroppedEvents []PgBackRestDropEvent // queue-max exceeded, WAL never pushed
	Failed        int                   // ERROR during a synchronous push/get attempt
	FailedAsync   int                   // ERROR during an async-worker push/get attempt
	Timeouts      int                   // [082] ArchiveTimeoutError
	Failures      []PgBackRestFailureStretch
	// LastFound is when archive-get last found a segment (archive-get only).
	LastFound time.Time
	// LastPush is the latest successful push (archive-push only), nil
	// when none was logged.
	LastPush *PgBackRestPush
	// Logged is true when the synchronous command's own begin/end lines
	// were seen, AsyncLogged the async worker's. Without them (pgBackRest's
	// default console level in a PostgreSQL log) the sync counters above
	// cannot be counted and stay at zero whatever happened: they are "not
	// logged", and PostgreSQL's own failure count is the one to read.
	Logged      bool
	AsyncLogged bool
}

// PgBackRestDropEvent is one "dropped WAL file" WARN observation, kept
// individually (not just counted) so that merging the dedicated
// archive-push:async log with the synchronous observation of the SAME
// drop embedded in the PostgreSQL log (both log it, from two different
// vantage points on one real event — see dedupDropEvents) can recognize
// the pair as one drop, not two.
type PgBackRestDropEvent struct {
	Segment string
	Time    time.Time
}

// PgBackRestExpireRun is one expire command's results.
type PgBackRestExpireRun struct {
	Time           time.Time
	Repo           string
	Expired        []string // backup labels actually removed ("remove expired backup X")
	ArchiveRemoved int
	Warnings       []string
	Succeeded      bool
	ErrorCode      int      // the "aborted with exception [NNN]" code, 0 when none
	ErrorLine      string   // the run's last ERROR text, when it failed
	ErrorDetail    []string // the lines under ErrorLine, see PgBackRestBackup
	// SetLabel is the backup an expire run was asked to remove (--set=),
	// "" for a retention-driven run.
	SetLabel string
}

// PgBackRestRetentionWarning is one distinct warning of the expire runs,
// with the runs that wrote it.
type PgBackRestRetentionWarning struct {
	Text  string
	Count int
	Repos []string // distinct Repo of those runs, in first-seen order
	First time.Time
	Last  time.Time
}

// PgBackRestOperation is one entry in the "other operations" timeline
// (stanza-create/upgrade/delete, check, restore, verify, start/stop).
type PgBackRestOperation struct {
	Command     string
	Time        time.Time
	Summary     string
	Succeeded   bool
	HasOutcome  bool
	Duration    time.Duration
	HasDuration bool
	ErrorCode   int
	// Crashed is true when this run never wrote its own "command end"
	// line (see PgBackRestBackup.Crashed) — reported as such, never as a
	// success.
	Crashed bool
	Signal  string // see PgBackRestBackup.Signal
	// Path is a restore's data directory (--pg1-path): the archive-get
	// stretches and missing WAL of the same Path are its recovery's.
	Path string
}

// PgBackRestPathTimes is a data directory and the times of its finds.
type PgBackRestPathTimes struct {
	Path  string
	Times []time.Time
}

// PgBackRestMissingWAL is a WAL segment or .history file archive-get
// could not find for a data directory although its push was logged
// before: it left the repository, and that recovery ended there (or, for
// a .history file, took a timeline number already used).
type PgBackRestMissingWAL struct {
	Segment  string
	Path     string    // the data directory (--pg1-path) whose recovery asked for it
	Time     time.Time // its first "unable to find" for Path
	PushedAt time.Time // the first push of Segment logged before Time
}

// PgBackRestIssue is one error or warning, grouped by code, normalized
// first line and cause (see issueKey).
type PgBackRestIssue struct {
	Code    int
	HasCode bool
	// ID is the --event-detail handle: "er-"/"wa-" and 4 characters
	// hashed from the stanza and the grouping key (see PgBackRestIssueID).
	ID      string
	Message string // normalized first line (paths shortened, not erased; variable tokens placeholdered)
	Example string // verbatim first line, plus the repo line when the message carried one
	// RepoLine is the failing repository's "repoN: [...]" line of a
	// multi-repository error ([104]), also folded into Example.
	RepoLine string
	// Detail holds the lines written under the first occurrence (the
	// cause), HINT lines left out.
	Detail   []string
	Count    int
	First    time.Time
	Last     time.Time
	Commands []string // distinct command names observed with this issue
	// Times holds each dated occurrence, in time order: Count minus the
	// occurrences of a log-timestamp=n file.
	Times []time.Time
	// cmdCount counts the occurrences of each of Commands, so that one
	// taken back (dropIssue) leaves Commands exact.
	cmdCount map[string]int
}

// addTime records one occurrence at ts, when it is dated.
func (is *PgBackRestIssue) addTime(ts time.Time) {
	if !ts.IsZero() {
		is.Times = append(is.Times, ts)
	}
}

// PgBackRestIssueID is the --event-detail handle of an issue of stanza:
// the PostgreSQL events' prefixes (er- ERROR, wa- WARN) and hashing
// (GenerateEventID), on a key that names the source and the stanza.
func PgBackRestIssueID(severity, stanza string, is PgBackRestIssue) string {
	sev := "ERROR"
	if severity == "WARN" {
		sev = "WARNING"
	}
	return GenerateEventID(sev, "pgbackrest|"+stanza+"|"+issueKey(is.Code, is.HasCode, is.Message, is.Detail))
}

// PgBackRestPGFailureGroup is PostgreSQL's archive_command failures of
// one segment with one cause.
type PgBackRestPGFailureGroup struct {
	Segment string
	Cause   string // "exit N", "signal N" or "exit unknown"
	Count   int
	First   time.Time
	Last    time.Time
}

// PgBackRestPGArchiveFailure is one archive_command failure as seen from
// PostgreSQL's own log.
type PgBackRestPGArchiveFailure struct {
	Segment  string
	ExitCode int
	HasExit  bool
	Signal   int
	IsSignal bool
	Time     time.Time
}

// PgBackRestMetrics is the full report for one stanza.
type PgBackRestMetrics struct {
	Stanza string
	Files  []string
	// LinesRead counts the physical lines of the stanza's pgBackRest
	// entries, banners and blank lines included.
	LinesRead     int
	Versions      []string
	StartTime     time.Time
	EndTime       time.Time
	HasTimestamps bool // false when every entry came from a log-timestamp=n file
	SawBeginEnd   bool // false => log-level-file hides INFO: no command framing seen
	// BackupsLogged is true when the begin or end line of a command other
	// than archive-push/archive-get was seen, which only pgBackRest's own
	// logs hold: without one (a PostgreSQL log alone) backups are not
	// logged, rather than none.
	BackupsLogged bool
	// InputEnd is the last timestamp of the whole input, PostgreSQL lines
	// included: the backup ages are measured to it.
	InputEnd time.Time
	// Window is set when --begin/--end restricted the report.
	Window *PgBackRestWindow

	Backups []PgBackRestBackup
	// Last is keyed by backup type ("full", "diff", "incr").
	Last map[string]PgBackRestLast

	Commands []PgBackRestCommandStat

	ArchivePush PgBackRestArchiveStats
	ArchiveGet  PgBackRestArchiveStats

	ExpireRuns []PgBackRestExpireRun
	// RetentionWarnings groups the ExpireRuns' warnings by text, in order
	// of first run.
	RetentionWarnings []PgBackRestRetentionWarning

	Operations []PgBackRestOperation

	Errors   []PgBackRestIssue
	Warnings []PgBackRestIssue

	PGArchiveFailures []PgBackRestPGArchiveFailure
	// PGArchiveFailureGroups groups PGArchiveFailures by segment and
	// cause, in time order.
	PGArchiveFailureGroups []PgBackRestPGFailureGroup
	PGRetryWarnings        int
	// PGRetryTimes dates each of PGRetryWarnings, in time order.
	PGRetryTimes      []time.Time
	PGRestoreFailures int

	MissingInfo []string

	// RestoreTimes dates each segment archive-get found, per data
	// directory (--pg1-path), in order of first find: the HTML report's
	// WAL restored chart.
	RestoreTimes []PgBackRestPathTimes

	// MissingWAL lists the segments recovery asked for that the
	// repository no longer had, in time order (see PgBackRestMissingWAL).
	MissingWAL []PgBackRestMissingWAL

	// PushTimes dates each segment ArchivePush.Pushed and PushedAsync
	// count, in time order (an async batch's segments at its end): the
	// HTML report's cumulative WAL chart.
	PushTimes []time.Time
}

// PgBackRestLast is the latest successful backup of one type still
// present (nil when none is left), and the label of a more recent one
// this log's expire runs removed ("" otherwise).
type PgBackRestLast struct {
	Backup  *PgBackRestBackup
	Expired string
}

// PgBackRestWindow is the --begin/--end window a report was restricted
// to. CutBefore/CutAfter are true when pgBackRest lines before/after it
// were left out: an absent backup then means none in this window, not
// none at all.
type PgBackRestWindow struct {
	Begin, End          time.Time
	CutBefore, CutAfter bool
}

// LastOf returns the most recent successful, non-expired backup of the
// given type ("full", "diff", "incr") and its age at InputEnd, or
// ok=false if none exists.
func (m PgBackRestMetrics) LastOf(typ string) (b PgBackRestBackup, age time.Duration, ok bool) {
	ptr := m.Last[typ].Backup
	if ptr == nil {
		return b, 0, false
	}
	return *ptr, m.InputEnd.Sub(ptr.End), true
}

// --- Analyzer ---

// pgbrRun is one open command invocation awaiting its "command end" line.
type pgbrRun struct {
	command string
	execID  string
	repo    string
	typeOpt string
	// sizeType is the type the "backup size" line names: the one taken.
	sizeType      string
	start         time.Time
	backupLabel   string
	changedToFull bool
	// changedFromType is the backup type word pgBackRest's own
	// "changed to full" WARN names (observeWarn), the only place that
	// type is stated when typeOpt is empty (an implicit default type,
	// not given via --type=).
	changedFromType string
	sizeRaw         string
	fileTotal       int
	// segment is the WAL file named by the begin line's positional
	// argument ("[pg_wal/X]"), which the ERROR line rarely repeats.
	segment string
	// lastSeen is the timestamp of the last line written while this run
	// was the current one, lastText that line's text.
	lastSeen time.Time
	lastText string
	// pg1Path and target are a restore's --pg1-path and --target.
	pg1Path string
	target  string
	// signal is the signal a "terminated on signal" line named for it.
	signal string
	// pushLast and pushCount are the last segment and the number of
	// segments of an async worker's "push N WAL file(s)" line,
	// pushDropped the drops it logged.
	pushLast               string
	pushCount, pushDropped int
	// pushFailed are the segments it could not push.
	pushFailed []string
	// proc numbers the OS process that opened the run (one per "PROCESS
	// START" banner), so a later process's lines never land on it.
	proc int
	// repoWarn is the run's last line when it was a "repoN: [...]" WARN:
	// the cause of the bare ERROR or "[...Error]" WARN that may follow.
	repoWarn *pgbrRepoWarn
}

// pgbrRepoWarn is a "repoN: [...]" WARN as it was added to the warnings.
type pgbrRepoWarn struct {
	text, example, cmd string
	code               int
	hasCode            bool
	detail             []string
	ts                 time.Time
}

// pgbrPushRange is a batch a successful async worker run pushed: n
// segments from first to last, its drops left out.
type pgbrPushRange struct {
	first, last string
	n           int
	at          time.Time // the batch's end, when its segments count as pushed
}

// pgbrAsyncSeg is a segment the synchronous call logged as "pushed ...
// asynchronously", and when.
type pgbrAsyncSeg struct {
	seg string
	at  time.Time
}

// lastPushedSegment is the highest segment an async batch is known by
// name to have pushed, "" when no name is left. A batch's segments are
// consecutive, so the one before a failed segment is named by
// decrementing it, never across a log boundary (its size is unknown).
func (r *pgbrRun) lastPushedSegment() string {
	last := r.pushLast
	if last == "" {
		last = r.segment
	}
	for slices.Contains(r.pushFailed, last) {
		if last == r.segment || r.pushDropped > 0 {
			return ""
		}
		last = pgbrPrevSegment(last)
		if last == "" || last < r.segment {
			return ""
		}
	}
	return last
}

// pgbrPrevSegment is the segment before seg in the same log, "" when seg
// is not a WAL segment name or is the first of its log.
func pgbrPrevSegment(seg string) string {
	if len(seg) != 24 {
		return ""
	}
	n, err := strconv.ParseUint(seg[16:], 16, 32)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("%s%08X", seg[:16], n-1)
}

// pgbrStanza is the per-stanza working state for one PgBackRestAnalyzer.
type pgbrStanza struct {
	PgBackRestMetrics
	openRuns map[string][]*pgbrRun
	// runStack holds every open run, across commands, oldest first.
	runStack  []*pgbrRun
	cmdStat   map[string]*PgBackRestCommandStat
	errIdx    map[string]int
	warnIdx   map[string]int
	pushFail  map[string]*PgBackRestFailureStretch
	getFail   map[string]*PgBackRestFailureStretch
	curExpire *PgBackRestExpireRun
	versions  map[string]bool
	// touchedFiles lists the input files that fed this stanza.
	touchedFiles map[string]bool
	// lastDroppedSegment is the segment of the last "dropped WAL file"
	// WARN, until the "pushed ... asynchronously" line that follows it.
	lastDroppedSegment string
	// asyncPushedSegs are the segments the synchronous call logged as
	// "pushed ... asynchronously", asyncPushRanges the batches the async
	// worker pushed: two views of the same pushes (see asyncPushTimes).
	asyncPushedSegs []pgbrAsyncSeg
	asyncPushRanges []pgbrPushRange
	// syncPushAt dates each synchronous push ArchivePush.Pushed counts.
	syncPushAt []time.Time
	// getMisses are archive-get's "unable to find" of a WAL segment or a
	// .history file, by
	// data directory (Segment, Path, Time), and getFoundAt the last time
	// it found one for each data directory.
	getMisses  []PgBackRestMissingWAL
	getFoundAt map[string]time.Time
	// getFoundSeg is when archive-get last found each segment, by data
	// directory ("path\x00segment"): a miss it later found is no loss.
	getFoundSeg map[string]time.Time
	// getFoundTimes dates every find, per data directory (RestoreTimes).
	getFoundTimes map[string][]time.Time
	// archiveErrTimes holds, by issue key, when the archive commands
	// logged each ERROR, the synchronous call apart from the async worker
	// (see dedupRelayedErrors).
	archiveErrTimes map[string]*pgbrErrTimes
	// pushes are the successful pushes, for LastPush and the stretches'
	// recovery.
	pushes []PgBackRestPush
	// pendingSummary holds one line per command, the Summary of the
	// operation its "command end" closes.
	pendingSummary map[string]string
	// lastErrorFirstLine is the last ERROR's text, a failed backup's
	// ErrorLine, and lastErrorDetail the lines under it.
	lastErrorFirstLine string
	lastErrorDetail    []string
	// invocationHasActivity/invocationHasBeginEnd describe the current
	// invocation (between two banners): whether it logged anything, and
	// whether it logged its own begin/end.
	invocationHasActivity, invocationHasBeginEnd bool
	// missingBeginEndRuns counts the invocations that logged something but
	// no begin/end of their own.
	missingBeginEndRuns int
	// hasContent is true once a pgBackRest line or a PostgreSQL-side
	// archiving observation was attributed to this stanza.
	hasContent bool
	// sawBanner is true once a "PROCESS START" banner was attributed: a
	// pgBackRest log whose level hides everything else still shows.
	sawBanner bool
	// undatedSources names the sources of the header lines written under
	// log-timestamp=n ("" when the source is not known), datedSources the
	// sources of dated lines.
	undatedSources, datedSources map[string]bool
	// deferred* hold PostgreSQL-side observations that only count when the
	// run shows pgBackRest elsewhere (see observePGArchiveFailure).
	deferredFailures []PgBackRestPGArchiveFailure
	deferredRetries  []time.Time
	deferredRestores []time.Time
	deferredArchived []PgBackRestPush
}

// noteProcessStart closes the invocation a banner ends and starts the
// next one.
func (st *pgbrStanza) noteProcessStart() {
	st.closeInvocation()
	st.invocationHasActivity = false
	st.invocationHasBeginEnd = false
}

// closeInvocation counts the ending invocation as missing its begin/end
// when it logged something but not its own framing (log-level-file hid
// it), which SawBeginEnd cannot show once another invocation had it.
func (st *pgbrStanza) closeInvocation() {
	if st.invocationHasActivity && !st.invocationHasBeginEnd {
		st.missingBeginEndRuns++
	}
}

func newPgbrStanza(name string) *pgbrStanza {
	return &pgbrStanza{
		PgBackRestMetrics: PgBackRestMetrics{Stanza: name},
		openRuns:          make(map[string][]*pgbrRun),
		cmdStat:           make(map[string]*PgBackRestCommandStat),
		errIdx:            make(map[string]int),
		warnIdx:           make(map[string]int),
		pushFail:          make(map[string]*PgBackRestFailureStretch),
		getFail:           make(map[string]*PgBackRestFailureStretch),
		getFoundAt:        make(map[string]time.Time),
		getFoundSeg:       make(map[string]time.Time),
		getFoundTimes:     make(map[string][]time.Time),
		versions:          make(map[string]bool),
		pendingSummary:    make(map[string]string),
	}
}

// noteGlobalBounds widens the stanza's Start/End time window.
func (st *pgbrStanza) noteGlobalBounds(ts time.Time) {
	if ts.IsZero() {
		return
	}
	st.HasTimestamps = true
	widenSpan(&st.StartTime, &st.EndTime, ts)
}

// widenSpan widens the span [*first, *last] to take in ts; a zero ts
// changes nothing.
func widenSpan(first, last *time.Time, ts time.Time) {
	if ts.IsZero() {
		return
	}
	if first.IsZero() || ts.Before(*first) {
		*first = ts
	}
	if last.IsZero() || ts.After(*last) {
		*last = ts
	}
}

// mergeSpan widens [*first, *last] to take in the span [from, to].
func mergeSpan(first, last *time.Time, from, to time.Time) {
	widenSpan(first, last, from)
	widenSpan(first, last, to)
}

// noteTime is noteGlobalBounds for a line that is not begin/end framing:
// the line also shows the current run still alive.
func (st *pgbrStanza) noteTime(ts time.Time, proc int) {
	st.noteGlobalBounds(ts)
	if ts.IsZero() {
		return
	}
	if r := st.currentRun(proc); r != nil {
		r.lastSeen = ts
	}
}

// currentRun returns the run the next line belongs to: the most recently
// opened run still open, when the current OS process (proc) opened it.
// pgBackRest writes a log file one process at a time, so a run opened by
// an earlier process that never logged its end is not the current one.
func (st *pgbrStanza) currentRun(proc int) *pgbrRun {
	n := len(st.runStack)
	if n == 0 || st.runStack[n-1].proc != proc {
		return nil
	}
	return st.runStack[n-1]
}

// noteFile records that file fed st; a no-op for "".
func (st *pgbrStanza) noteFile(file string) {
	if file == "" {
		return
	}
	if st.touchedFiles == nil {
		st.touchedFiles = make(map[string]bool)
	}
	st.touchedFiles[file] = true
}

func (st *pgbrStanza) noteVersion(v string) {
	if v == "" || st.versions[v] {
		return
	}
	st.versions[v] = true
	st.Versions = append(st.Versions, v)
}

func (st *pgbrStanza) pushRun(r *pgbrRun) {
	r.lastSeen = r.start
	st.openRuns[r.command] = append(st.openRuns[r.command], r)
	st.runStack = append(st.runStack, r)
}

// popRun removes and returns the most recently opened open run of
// command: the log is written by one process at a time, so the last
// invocation begun is the first to end, even one that dies at once on a
// lock held by an older one. A run killed before its "command end" is
// never popped; closeAbandonedRuns reports it.
func (st *pgbrStanza) popRun(command string) *pgbrRun {
	q := st.openRuns[command]
	if len(q) == 0 {
		return nil
	}
	r := q[len(q)-1]
	st.openRuns[command] = q[:len(q)-1]
	st.removeFromRunStack(r)
	return r
}

// removeFromRunStack drops r from runStack, wherever it sits. The scan
// starts at the top, where the run that ends almost always is: killed
// runs pile up under it.
func (st *pgbrStanza) removeFromRunStack(r *pgbrRun) {
	for i := len(st.runStack) - 1; i >= 0; i-- {
		if st.runStack[i] == r {
			st.runStack = append(st.runStack[:i], st.runStack[i+1:]...)
			return
		}
	}
}

// closeAbandonedRuns reports every run still open at the end of the
// source as killed, dated by its last line. When lines past the --end
// bound were left out (cutAfter), a run with no signal line may have
// ended there: it is reported as still running at the window's end.
func (st *pgbrStanza) closeAbandonedRuns(cutAfter bool) {
	for _, run := range st.runStack {
		end := run.lastSeen
		if end.IsZero() {
			end = run.start
		}
		inProgress := cutAfter && run.signal == ""
		st.finishRun(run.command, run, runEnd{time: end, crashed: !inProgress, inProgress: inProgress})
	}
	st.runStack = nil
	st.openRuns = make(map[string][]*pgbrRun)
}

func (st *pgbrStanza) commandStat(command string) *PgBackRestCommandStat {
	cs := st.cmdStat[command]
	if cs == nil {
		cs = &PgBackRestCommandStat{Command: command}
		st.cmdStat[command] = cs
	}
	return cs
}

// PgBackRestAnalyzer accumulates the pgBackRest entries of the stream
// into per-stanza metrics: the lines the stderr parser carved out, the
// markers it and the input layers send, and the PostgreSQL entries that
// may be archive_command or restore_command failures.
type PgBackRestAnalyzer struct {
	stanzas    map[string]*pgbrStanza
	stanzaHint string // last --stanza= seen, "" after a banner
	// file is the input file being read, member the archive member, and
	// sourceNamed the last stanza named in the current one.
	file, member, sourceNamed string
	// pendingFail is a PostgreSQL archive failure awaiting its DETAIL line.
	pendingFail *pgbrPendingPGFailure
	// archiveCmd says what the last DETAIL showed archive_command to be:
	// 0 unknown, 1 pgBackRest, -1 another program.
	archiveCmd int8
	// evidence is true once this analyzer saw a pgBackRest line or a
	// failed archive_command naming pgbackrest.
	evidence bool
	// proc numbers the OS processes, one per "PROCESS START" banner.
	proc int
	// bannerInSource is true once the current source showed a banner:
	// only pgBackRest's own files have invocations (a PostgreSQL log
	// holds none, whatever the order of the inputs).
	bannerInSource bool
	// seen remembers each dated pgBackRest line by content (see
	// lineKey) and the source it was read from; src numbers the sources.
	seen map[uint64]pgbrSeenLine
	src  int
	// window holds the --begin/--end bounds, on the wall clock.
	window *PgBackRestWindow
	// inputEnd is the last timestamp of the input, see InputEnd.
	inputEnd time.Time
}

// pgbrWall projects t onto its own wall clock, in UTC: the space of
// pgBackRest's zoneless times, which are parsed as UTC.
func pgbrWall(t time.Time) time.Time {
	return parser.WallClock(t).UTC()
}

// noteInputTime records the timestamp of an entry of the input, for
// InputEnd, on the wall clock pgBackRest writes (see pgbrWall):
// PostgreSQL's zoned times and pgBackRest's zoneless ones then compare
// whatever the machine's zone.
func (a *PgBackRestAnalyzer) noteInputTime(ts time.Time) {
	ts = pgbrWall(ts)
	if ts.After(a.inputEnd) {
		a.inputEnd = ts
	}
}

// pgbrPendingPGFailure is a PostgreSQL "archive command failed" or
// "... terminated by signal" line waiting for the DETAIL line that names
// the command.
type pgbrPendingPGFailure struct {
	ts       time.Time
	exitCode int
	hasExit  bool
	signal   int
	isSignal bool
}

// PgBackRestInScope reports whether a run shows the pgBackRest report.
// pgBackRest has no database, user or application, so an attribute filter
// leaves it out, and so does a section option, which asks for that
// PostgreSQL section only (sections other than "all").
func PgBackRestInScope(f parser.LogFilters, sections []string) bool {
	if len(f.DbFilter) > 0 || len(f.UserFilter) > 0 || len(f.ExcludeUser) > 0 || len(f.AppFilter) > 0 {
		return false
	}
	return len(sections) == 1 && sections[0] == "all"
}

// PgBackRestLinesRead totals the lines read of the reports, the entry
// count of a run with no PostgreSQL entry.
func PgBackRestLinesRead(pbr []PgBackRestMetrics) int {
	n := 0
	for _, m := range pbr {
		n += m.LinesRead
	}
	return n
}

// NewPgBackRestAnalyzer creates an empty analyzer.
func NewPgBackRestAnalyzer() *PgBackRestAnalyzer {
	return &PgBackRestAnalyzer{stanzas: make(map[string]*pgbrStanza)}
}

// SetTimeWindow restricts the analyzer to the --begin/--end bounds (zero
// for none). Entries outside are left out and recorded as such, and
// incomplete runs and notes at the edges are not reported as facts.
func (a *PgBackRestAnalyzer) SetTimeWindow(begin, end time.Time) {
	if begin.IsZero() && end.IsZero() {
		a.window = nil
		return
	}
	a.window = &PgBackRestWindow{Begin: pgbrWall(begin), End: pgbrWall(end)}
}

// outsideWindow reports whether ts falls outside the window, recording
// which side was cut.
func (a *PgBackRestAnalyzer) outsideWindow(ts time.Time) bool {
	w := a.window
	if w == nil || ts.IsZero() {
		return false
	}
	wall := pgbrWall(ts)
	if !w.Begin.IsZero() && wall.Before(w.Begin) {
		w.CutBefore = true
		return true
	}
	if !w.End.IsZero() && wall.After(w.End) {
		w.CutAfter = true
		return true
	}
	return false
}

// stanzaFor returns the stanza an entry belongs to: the one it names, or
// the last one named, or the unknown bucket. The first stanza named takes
// over the unknown bucket.
func (a *PgBackRestAnalyzer) stanzaFor(explicit string) *pgbrStanza {
	key := explicit
	switch {
	case key != "":
		a.stanzaHint = key
		a.sourceNamed = key
		a.absorbUnknown(key)
	case a.stanzaHint != "":
		key = a.stanzaHint
	default:
		key = pgbrUnknownStanza
	}
	st := a.stanzas[key]
	if st == nil {
		st = newPgbrStanza(key)
		a.stanzas[key] = st
	}
	st.noteFile(a.file)
	return st
}

// absorbUnknown hands the unknown bucket to stanza key.
func (a *PgBackRestAnalyzer) absorbUnknown(key string) {
	if key == pgbrUnknownStanza {
		return
	}
	unk := a.stanzas[pgbrUnknownStanza]
	if unk == nil {
		return
	}
	delete(a.stanzas, pgbrUnknownStanza)
	if dst := a.stanzas[key]; dst != nil {
		dst.absorb(unk)
		return
	}
	unk.Stanza = key
	a.stanzas[key] = unk
}

// endSource closes the current source (an archive member or an input
// file): lines that named no stanza go to the one its name suggests, else
// to the last one it named.
func (a *PgBackRestAnalyzer) endSource() {
	name := a.member
	if name == "" {
		name = a.file
	}
	hint := parser.StanzaHintFromFilename(name)
	if hint == "" {
		hint = a.sourceNamed
	}
	if hint != "" {
		a.absorbUnknown(hint)
	}
	a.sourceNamed = ""
}

// sourceLabel names the current source for a note, "" when unknown
// (stdin, the browser).
func (a *PgBackRestAnalyzer) sourceLabel() string {
	if a.member != "" {
		return a.member
	}
	if a.file == "-" {
		return ""
	}
	return a.file
}

// absorb merges src, whose lines follow st's, into st.
func (st *pgbrStanza) absorb(src *pgbrStanza) {
	st.LinesRead += src.LinesRead
	st.Backups = append(st.Backups, src.Backups...)
	st.Operations = append(st.Operations, src.Operations...)
	st.ExpireRuns = append(st.ExpireRuns, src.ExpireRuns...)
	st.Errors = mergeIssues(st.Errors, src.Errors)
	st.errIdx = rebuildPgBackRestIssueIndex(st.Errors)
	st.Warnings = mergeIssues(st.Warnings, src.Warnings)
	st.warnIdx = rebuildPgBackRestIssueIndex(st.Warnings)
	st.PGArchiveFailures = append(st.PGArchiveFailures, src.PGArchiveFailures...)
	st.PGRetryWarnings += src.PGRetryWarnings
	st.PGRetryTimes = append(st.PGRetryTimes, src.PGRetryTimes...)
	st.PGRestoreFailures += src.PGRestoreFailures
	st.deferredFailures = append(st.deferredFailures, src.deferredFailures...)
	st.deferredRetries = append(st.deferredRetries, src.deferredRetries...)
	st.deferredRestores = append(st.deferredRestores, src.deferredRestores...)
	st.deferredArchived = append(st.deferredArchived, src.deferredArchived...)
	st.pushes = append(st.pushes, src.pushes...)
	addArchiveCounts(&st.ArchivePush, src.ArchivePush)
	addArchiveCounts(&st.ArchiveGet, src.ArchiveGet)
	mergeStretchMaps(st.pushFail, src.pushFail)
	mergeStretchMaps(st.getFail, src.getFail)
	for cmd, cs := range src.cmdStat {
		d := st.commandStat(cmd)
		d.Runs += cs.Runs
		d.Succeeded += cs.Succeeded
		d.Failed += cs.Failed
		d.TotalDuration += cs.TotalDuration
		d.MaxDuration = max(d.MaxDuration, cs.MaxDuration)
	}
	for cmd, q := range src.openRuns {
		st.openRuns[cmd] = append(st.openRuns[cmd], q...)
	}
	st.runStack = append(st.runStack, src.runStack...)
	if src.curExpire != nil {
		st.curExpire = src.curExpire
	}
	for _, v := range src.Versions {
		st.noteVersion(v)
	}
	for f := range src.touchedFiles {
		st.noteFile(f)
	}
	for f := range src.undatedSources {
		st.undatedSources = addToSet(st.undatedSources, f)
	}
	for f := range src.datedSources {
		st.datedSources = addToSet(st.datedSources, f)
	}
	for cmd, line := range src.pendingSummary {
		st.pendingSummary[cmd] = line
	}
	if src.lastErrorFirstLine != "" {
		st.lastErrorFirstLine, st.lastErrorDetail = src.lastErrorFirstLine, src.lastErrorDetail
	}
	st.asyncPushedSegs = append(st.asyncPushedSegs, src.asyncPushedSegs...)
	st.asyncPushRanges = append(st.asyncPushRanges, src.asyncPushRanges...)
	st.syncPushAt = append(st.syncPushAt, src.syncPushAt...)
	st.getMisses = append(st.getMisses, src.getMisses...)
	for path, t := range src.getFoundAt {
		if t.After(st.getFoundAt[path]) {
			st.getFoundAt[path] = t
		}
	}
	for path, ts := range src.getFoundTimes {
		st.getFoundTimes[path] = append(st.getFoundTimes[path], ts...)
	}
	for k, t := range src.getFoundSeg {
		if t.After(st.getFoundSeg[k]) {
			st.getFoundSeg[k] = t
		}
	}
	for key, t := range src.archiveErrTimes {
		d := st.errTimes(key)
		d.sync = append(d.sync, t.sync...)
		d.async = append(d.async, t.async...)
	}
	if src.lastDroppedSegment != "" {
		st.lastDroppedSegment = src.lastDroppedSegment
	}
	mergeSpan(&st.StartTime, &st.EndTime, src.StartTime, src.EndTime)
	st.HasTimestamps = st.HasTimestamps || src.HasTimestamps
	st.SawBeginEnd = st.SawBeginEnd || src.SawBeginEnd
	st.BackupsLogged = st.BackupsLogged || src.BackupsLogged
	st.hasContent = st.hasContent || src.hasContent
	st.sawBanner = st.sawBanner || src.sawBanner
	// src belongs to a later process: st's invocation is over.
	st.closeInvocation()
	st.invocationHasActivity = src.invocationHasActivity
	st.invocationHasBeginEnd = src.invocationHasBeginEnd
	st.missingBeginEndRuns += src.missingBeginEndRuns
}

// addArchiveCounts adds b's counters, drop events and flags to a.
func addArchiveCounts(a *PgBackRestArchiveStats, b PgBackRestArchiveStats) {
	a.Pushed += b.Pushed
	a.PushedAsync += b.PushedAsync
	a.Found += b.Found
	a.FoundAsync += b.FoundAsync
	a.NotFound += b.NotFound
	a.NotFoundAsync += b.NotFoundAsync
	a.DroppedEvents = append(a.DroppedEvents, b.DroppedEvents...)
	a.Failed += b.Failed
	a.FailedAsync += b.FailedAsync
	a.Timeouts += b.Timeouts
	if b.LastFound.After(a.LastFound) {
		a.LastFound = b.LastFound
	}
	a.Logged = a.Logged || b.Logged
	a.AsyncLogged = a.AsyncLogged || b.AsyncLogged
}

// mergeStretchMaps folds src's per-segment failure stretches into dst.
func mergeStretchMaps(dst, src map[string]*PgBackRestFailureStretch) {
	for key, s := range src {
		d := dst[key]
		if d == nil {
			dst[key] = s
			continue
		}
		d.Attempts += s.Attempts
		if s.Code != 0 {
			d.Code = s.Code
		}
		mergeSpan(&d.First, &d.Last, s.First, s.Last)
	}
}

// rebuildPgBackRestIssueIndex rebuilds the issueKey -> index map
// addIssue relies on, after the issue slice was replaced.
func rebuildPgBackRestIssueIndex(issues []PgBackRestIssue) map[string]int {
	idx := make(map[string]int, len(issues))
	for i, is := range issues {
		idx[issueKey(is.Code, is.HasCode, is.Message, is.Detail)] = i
	}
	return idx
}

// Process classifies one pgBackRest entry (Message "LEVEL: text" or
// "LEVEL: [code]: text", optional "\n"-joined continuation lines) or
// marker, and folds it into its stanza.
func (a *PgBackRestAnalyzer) Process(e *parser.LogEntry) {
	if info := e.PgBackRest; info != nil {
		a.processMarker(info)
		return
	}
	// pgBackRest writes a wall clock with no zone, parsed as UTC; an entry
	// dated by a PostgreSQL prefix is projected onto the same wall clock.
	ts := pgbrWall(e.Timestamp)
	if a.outsideWindow(ts) {
		return
	}
	a.noteInputTime(ts)
	level, code, hasCode, text, continuation := splitPgBackRestMessage(e.Message)
	if level == "" {
		return
	}
	a.evidence = true
	lineCount := 1 + strings.Count(e.Message, "\n") // HINT lines included
	key, keyed := lineKey(e, ts)
	if keyed {
		if prev, ok := a.seen[key]; ok && prev.src != a.src {
			a.processDuplicate(e, key, prev, level, code, hasCode, text, continuation, ts, lineCount)
			return
		}
		if _, ok := a.seen[key]; !ok {
			if a.seen == nil {
				a.seen = make(map[uint64]pgbrSeenLine)
			}
			a.seen[key] = pgbrSeenLine{src: a.src}
		}
	}

	if cmd, version, options, ok := parseCommandBegin(text); ok {
		stanza, _ := parseOption(options, "stanza")
		st := a.stanzaFor(stanza)
		a.noteContent(st, e, lineCount)
		st.noteVersion(version)
		st.noteFraming(cmd)
		execID, _ := parseOption(options, "exec-id")
		typeOpt, _ := parseOption(options, "type")
		repo := commandRepo(cmd, options)
		run := &pgbrRun{command: cmd, execID: execID, repo: repo, typeOpt: typeOpt, start: ts, segment: extractBeginSegment(options), proc: a.proc}
		switch baseCommand(cmd) {
		case "expire":
			st.curExpire = &PgBackRestExpireRun{Time: ts, Repo: repo}
			st.curExpire.SetLabel, _ = parseOption(options, "set")
		case "restore":
			run.pg1Path, _ = parseOption(options, "pg1-path")
			run.target, _ = parseOption(options, "target")
		case "archive-get":
			run.pg1Path, _ = parseOption(options, "pg1-path")
			if run.segment == "" {
				run.segment = pgbrHistoryArg(options)
			}
		}
		st.pushRun(run)
		st.noteGlobalBounds(ts)
		return
	}

	if cmd, success, ms, hasMs, errCode, hasErrCode, ok := parseCommandEnd(text); ok {
		st := a.stanzaFor("")
		a.noteContent(st, e, lineCount)
		st.noteFraming(cmd)
		run := st.popRun(cmd)
		// Not noteTime: the end line belongs to the run it closes, never
		// to a run left open underneath it.
		st.noteGlobalBounds(ts)
		st.finishRun(cmd, run, runEnd{time: ts, success: success, dur: time.Duration(ms) * time.Millisecond, hasDur: hasMs, errCode: errCode, hasErrCode: hasErrCode})
		if st.curExpire != nil && baseCommand(cmd) == "expire" {
			st.curExpire.Succeeded = success
			if hasErrCode {
				st.curExpire.ErrorCode = errCode
			}
			if !success {
				st.curExpire.ErrorLine, st.curExpire.ErrorDetail = st.lastErrorFirstLine, st.lastErrorDetail
			}
			st.ExpireRuns = append(st.ExpireRuns, *st.curExpire)
			st.curExpire = nil
		}
		return
	}

	st := a.stanzaFor("")
	a.noteContent(st, e, lineCount)
	st.noteTime(ts, a.proc)
	var repoWarn *pgbrRepoWarn
	if run := st.currentRun(a.proc); run != nil {
		run.lastText = text
		repoWarn, run.repoWarn = run.repoWarn, nil
	}

	switch level {
	case "ERROR":
		run := st.currentRun(a.proc)
		cmd := ""
		if run != nil {
			cmd = run.command
		}
		if repoWarn != nil && len(continuation) == 0 {
			// The run logged the cause as a "repoN: [...]" WARN and the
			// ERROR bare: one error, with that cause, not a warning more.
			st.dropIssue(&st.Warnings, &st.warnIdx, repoWarn.code, repoWarn.hasCode, repoWarn.text, repoWarn.detail, repoWarn.ts, repoWarn.cmd)
			continuation = append([]string{repoWarn.text}, repoWarn.detail...)
		}
		displayText, example, repoLine := annotateIssueText(text, continuation)
		st.lastErrorFirstLine, st.lastErrorDetail = displayText, continuation
		st.addIssue(&st.Errors, st.errIdx, code, hasCode, displayText, example, repoLine, continuation, ts, cmd)
		st.noteArchiveErrTime(cmd, code, hasCode, displayText, continuation, ts)
		st.observeArchiveFailure(cmd, run, text, code, ts)
		if keyed {
			a.seen[key] = pgbrSeenLine{src: a.seen[key].src, failureCounted: isArchiveCommand(cmd), cmdless: cmd == ""}
		}
	case "WARN":
		cmd := ""
		run := st.currentRun(a.proc)
		if run != nil {
			cmd = run.command
		}
		if repoWarn != nil && pgbrErrorTypeRe.MatchString(text) {
			// "[RepoInvalidError] ..." after the "repoN: [...]" WARN that
			// gave its cause: the same failure, already counted.
			break
		}
		displayText, example, repoLine := annotateIssueText(text, continuation)
		st.addIssue(&st.Warnings, st.warnIdx, code, hasCode, displayText, example, repoLine, continuation, ts, cmd)
		if keyed {
			a.seen[key] = pgbrSeenLine{src: a.seen[key].src, cmdless: cmd == ""}
		}
		if run != nil && pgbrRepoLineRe.MatchString(text) {
			run.repoWarn = &pgbrRepoWarn{text: displayText, example: example, cmd: cmd, code: code, hasCode: hasCode, detail: continuation, ts: ts}
		}
		st.observeWarn(text, ts)
		if baseCommand(cmd) == "expire" && st.curExpire != nil {
			st.curExpire.Warnings = append(st.curExpire.Warnings, text)
		}
	default: // INFO, DETAIL, DEBUG, TRACE, ASSERT
		st.observeInfo(text, ts)
	}
}

// pgbrSeenLine is where a pgBackRest line was first read, and whether
// its ERROR was counted as an archive-push/get failure there.
type pgbrSeenLine struct {
	src            int
	failureCounted bool
	// cmdless is true when its ERROR/WARN was counted with no command
	// known (a PostgreSQL log holds no command begin at the default
	// console level): the source that knows it completes it.
	cmdless bool
}

// lineKey identifies a dated pgBackRest line by its time, process and
// text. The same line read from two sources is one event: with
// --log-level-file in archive_command, archive-push writes each line to
// its own log file and, through its console output, to the PostgreSQL
// log.
func lineKey(e *parser.LogEntry, ts time.Time) (uint64, bool) {
	if ts.IsZero() {
		return 0, false
	}
	h := fnv.New64a()
	h.Write([]byte(strconv.FormatInt(ts.UnixNano(), 10)))
	h.Write([]byte{0})
	h.Write([]byte(e.PID))
	h.Write([]byte{0})
	h.Write([]byte(e.Message))
	return h.Sum64(), true
}

// processDuplicate handles a line already read from another source: it
// counts as read, nothing more, except an ERROR whose first copy had no
// archive run around it (the PostgreSQL log keeps only the console's
// warnings and errors) while this one has: the failure counts here.
func (a *PgBackRestAnalyzer) processDuplicate(e *parser.LogEntry, key uint64, prev pgbrSeenLine, level string, code int, hasCode bool, text string, continuation []string, ts time.Time, lineCount int) {
	stanza := ""
	if _, _, options, ok := parseCommandBegin(text); ok {
		stanza, _ = parseOption(options, "stanza")
	}
	st := a.stanzaFor(stanza)
	a.noteContent(st, e, lineCount)
	// A begin or end read first elsewhere still frames this source's
	// invocation (see closeInvocation).
	if cmd, _, _, ok := parseCommandBegin(text); ok {
		st.noteFraming(cmd)
	} else if cmd, _, _, _, _, _, ok := parseCommandEnd(text); ok {
		st.noteFraming(cmd)
	}
	if level == "ERROR" || level == "WARN" {
		a.completeDuplicate(st, prev, level, code, hasCode, text, continuation, ts)
	}
	if level != "ERROR" || prev.failureCounted {
		return
	}
	if run := st.currentRun(a.proc); run != nil && isArchiveCommand(run.command) {
		st.observeArchiveFailure(run.command, run, text, code, ts)
		a.seen[key] = pgbrSeenLine{src: prev.src, failureCounted: true}
	}
}

// completeDuplicate gives an ERROR/WARN first counted with no command
// known the command this source runs it under, and joins a bare ERROR to
// the "repoN: [...]" WARN before it, as when the line is read first here:
// the report does not depend on the order of the inputs.
func (a *PgBackRestAnalyzer) completeDuplicate(st *pgbrStanza, prev pgbrSeenLine, level string, code int, hasCode bool, text string, continuation []string, ts time.Time) {
	run := st.currentRun(a.proc)
	if run == nil {
		return
	}
	run.lastText = text
	repoWarn := run.repoWarn
	run.repoWarn = nil
	display, example, _ := annotateIssueText(text, continuation)
	switch level {
	case "WARN":
		if prev.cmdless {
			st.addIssueCommand(st.Warnings, st.warnIdx, code, hasCode, display, continuation, run.command)
		}
		if pgbrRepoLineRe.MatchString(text) {
			run.repoWarn = &pgbrRepoWarn{text: display, example: example, cmd: run.command, code: code, hasCode: hasCode, detail: continuation, ts: ts}
		}
	case "ERROR":
		if !prev.cmdless {
			return
		}
		if repoWarn != nil && len(continuation) == 0 {
			st.dropIssue(&st.Warnings, &st.warnIdx, repoWarn.code, repoWarn.hasCode, repoWarn.text, repoWarn.detail, repoWarn.ts, repoWarn.cmd)
			st.dropIssue(&st.Errors, &st.errIdx, code, hasCode, display, nil, ts, "")
			cont := append([]string{repoWarn.text}, repoWarn.detail...)
			d, ex, rl := annotateIssueText(text, cont)
			st.addIssue(&st.Errors, st.errIdx, code, hasCode, d, ex, rl, cont, ts, run.command)
			return
		}
		st.addIssueCommand(st.Errors, st.errIdx, code, hasCode, display, continuation, run.command)
	}
}

// isArchiveCommand reports whether cmd is archive-push or archive-get,
// async worker included.
func isArchiveCommand(cmd string) bool {
	switch baseCommand(cmd) {
	case "archive-push", "archive-get":
		return true
	}
	return false
}

// processMarker handles a marker entry: a source boundary, a banner, a
// signal line or blank lines.
func (a *PgBackRestAnalyzer) processMarker(info *parser.PgBackRestLineInfo) {
	switch {
	case info.Source != "":
		a.endSource()
		a.src++
		a.bannerInSource = false
		if info.SourceIsMember {
			a.member = info.Source
		} else {
			a.file, a.member = info.Source, ""
		}
	case info.ProcessStart:
		// A new process starts a new invocation, whose stanza its own
		// begin line will name.
		a.proc++
		a.bannerInSource = true
		if a.stanzaHint != "" {
			a.stanzas[a.stanzaHint].noteProcessStart()
			a.stanzaHint = ""
		}
		st := a.stanzaFor("")
		st.noteProcessStart()
		st.sawBanner = true
		st.LinesRead++
	case info.Signal != "":
		// The signal stops the process that wrote the current run.
		st := a.stanzaFor("")
		st.LinesRead++
		if r := st.currentRun(a.proc); r != nil {
			r.signal = info.Signal
		}
	case info.BlankLines > 0:
		a.stanzaFor("").LinesRead += info.BlankLines
	}
}

// noteContent records a pgBackRest entry of lineCount lines attributed
// to st.
func (a *PgBackRestAnalyzer) noteContent(st *pgbrStanza, e *parser.LogEntry, lineCount int) {
	st.LinesRead += lineCount
	if a.bannerInSource {
		st.invocationHasActivity = true
	}
	st.hasContent = true
	switch {
	case !e.Timestamp.IsZero():
		st.datedSources = addToSet(st.datedSources, a.sourceLabel())
	case e.PID != "":
		// A header without its timestamp: log-timestamp=n.
		st.undatedSources = addToSet(st.undatedSources, a.sourceLabel())
	}
}

// addToSet adds key to set, creating it when nil.
func addToSet(set map[string]bool, key string) map[string]bool {
	if set == nil {
		set = make(map[string]bool)
	}
	set[key] = true
	return set
}

// noteFraming records a command's own begin or end line.
func (st *pgbrStanza) noteFraming(cmd string) {
	st.SawBeginEnd = true
	st.invocationHasBeginEnd = true
	async := strings.HasSuffix(cmd, ":async")
	switch baseCommand(cmd) {
	case "archive-push":
		st.ArchivePush.Logged = st.ArchivePush.Logged || !async
		st.ArchivePush.AsyncLogged = st.ArchivePush.AsyncLogged || async
	case "archive-get":
		st.ArchiveGet.Logged = st.ArchiveGet.Logged || !async
		st.ArchiveGet.AsyncLogged = st.ArchiveGet.AsyncLogged || async
	default:
		st.BackupsLogged = true
	}
}

// pgbrNoTimestampNote names the sources of the header lines written
// under log-timestamp=n. It says "some lines" when a source is not known
// or also holds dated lines (a concatenation).
func pgbrNoTimestampNote(undated, dated map[string]bool) string {
	partial := false
	names := make([]string, 0, len(undated))
	for s := range undated {
		if s == "" || dated[s] {
			partial = true
		}
		if s != "" {
			names = append(names, ShortenPath(s))
		}
	}
	sort.Strings(names)
	const why = " no timestamps; dates and durations cannot be computed for "
	switch {
	case partial && len(names) == 0:
		return "log-timestamp=n: some lines carry" + why + "their runs"
	case partial:
		return "log-timestamp=n: some lines of " + strings.Join(names, ", ") + " carry" + why + "their runs"
	case len(names) == 1:
		return "log-timestamp=n: " + names[0] + " carries" + why + "its runs"
	}
	return "log-timestamp=n: " + strings.Join(names, ", ") + " carry" + why + "their runs"
}

// applyDeferred counts the PostgreSQL-side observations held back until
// the run showed pgBackRest elsewhere.
func (st *pgbrStanza) applyDeferred() {
	// PostgreSQL's successful archives are evidence only: no bounds, no
	// content.
	st.pushes = append(st.pushes, st.deferredArchived...)
	if len(st.deferredFailures)+len(st.deferredRetries)+len(st.deferredRestores) == 0 {
		return
	}
	for _, f := range st.deferredFailures {
		st.noteGlobalBounds(f.Time)
	}
	st.PGArchiveFailures = append(st.PGArchiveFailures, st.deferredFailures...)
	for _, ts := range st.deferredRetries {
		st.noteGlobalBounds(ts)
	}
	st.PGRetryWarnings += len(st.deferredRetries)
	st.PGRetryTimes = append(st.PGRetryTimes, st.deferredRetries...)
	for _, ts := range st.deferredRestores {
		st.noteGlobalBounds(ts)
	}
	st.PGRestoreFailures += len(st.deferredRestores)
	sortByTime(st.PGArchiveFailures, func(f PgBackRestPGArchiveFailure) time.Time { return f.Time })
	st.hasContent = true
}

// Finalize returns one report per stanza, sorted by name. The
// PostgreSQL-side observations held back count only when the run showed
// pgBackRest. Call it once, after the stream was consumed.
func (a *PgBackRestAnalyzer) Finalize() []PgBackRestMetrics {
	a.endSource()
	windowed := a.window != nil
	out := make([]PgBackRestMetrics, 0, len(a.stanzas))
	pushes := make([][]PgBackRestPush, 0, len(a.stanzas))
	for _, key := range slices.Sorted(maps.Keys(a.stanzas)) {
		st := a.stanzas[key]
		st.closeAbandonedRuns(windowed && a.window.CutAfter)
		if a.evidence {
			st.applyDeferred()
		}
		// A stanza without any pgBackRest content is a phantom: markers
		// alone, a window that filtered all its lines out, or
		// PostgreSQL-side observations that turned out not to be
		// pgBackRest's. Banners alone (unwindowed) say log-level-file
		// hides everything.
		if !st.hasContent && (windowed || !st.sawBanner) {
			continue
		}
		st.closeInvocation()
		m := st.PgBackRestMetrics
		m.Files = slices.Sorted(maps.Keys(st.touchedFiles))
		if a.window != nil {
			w := *a.window
			m.Window = &w
		}
		m.ArchivePush.Failures = stretchSlice(st.pushFail)
		m.ArchiveGet.Failures = stretchSlice(st.getFail)
		st.noteGetRecovery(&m.ArchiveGet, windowed && a.window.CutAfter)
		m.MissingWAL = st.missingWAL()
		m.RestoreTimes = st.restoreTimes()
		m.PGRetryTimes = slices.Clone(m.PGRetryTimes)
		slices.SortFunc(m.PGRetryTimes, time.Time.Compare)
		// The synchronous call and the async worker both log a drop.
		m.ArchivePush.DroppedEvents = dedupDropEvents(m.ArchivePush.DroppedEvents)
		m.PGArchiveFailureGroups = pgFailureGroups(m.PGArchiveFailures)
		m.InputEnd = a.inputEnd
		if m.EndTime.After(m.InputEnd) {
			m.InputEnd = m.EndTime
		}
		asyncAt := st.asyncPushTimes()
		m.ArchivePush.PushedAsync = len(asyncAt)
		m.PushTimes = append(slices.Clone(st.syncPushAt), asyncAt...)
		slices.SortFunc(m.PushTimes, time.Time.Compare)
		st.notePushRecovery(&m.ArchivePush, windowed && a.window.CutAfter)
		sort.Strings(m.Versions)
		m.Commands = make([]PgBackRestCommandStat, 0, len(st.cmdStat))
		for _, cs := range st.cmdStat {
			m.Commands = append(m.Commands, *cs)
		}
		sort.Slice(m.Commands, func(i, j int) bool { return m.Commands[i].Command < m.Commands[j].Command })
		if len(st.undatedSources) > 0 {
			m.MissingInfo = append(m.MissingInfo, pgbrNoTimestampNote(st.undatedSources, st.datedSources))
		}
		// A window can cut a run's begin/end away: the two notes below
		// only hold for a whole input.
		if !windowed && !m.SawBeginEnd {
			m.MissingInfo = append(m.MissingInfo, "no command begin/end lines seen: log-level-file is below info (or log-level-console below info for an embedded PostgreSQL log), so backup/archive run outcomes cannot be tracked here")
		} else if !windowed && st.missingBeginEndRuns > 0 {
			runs := fmt.Sprintf("%d invocations", st.missingBeginEndRuns)
			if st.missingBeginEndRuns == 1 {
				runs = "1 invocation"
			}
			m.MissingInfo = append(m.MissingInfo, fmt.Sprintf("%s logged activity (warnings/errors) but no command begin/end lines of their own: log-level-file hid their outcome, while an earlier/later invocation in the same file had it", runs))
		}
		// One order whatever the packaging (files, concatenation, tar).
		m.Errors = st.dedupRelayedErrors(m.Errors)
		sortPgBackRestIssues(m.Errors)
		sortPgBackRestIssues(m.Warnings)
		for i := range m.Errors {
			m.Errors[i].ID = PgBackRestIssueID("ERROR", m.Stanza, m.Errors[i])
		}
		for i := range m.Warnings {
			m.Warnings[i].ID = PgBackRestIssueID("WARN", m.Stanza, m.Warnings[i])
		}
		sortByTime(m.Operations, func(o PgBackRestOperation) time.Time { return o.Time })
		sortByTime(m.ExpireRuns, func(r PgBackRestExpireRun) time.Time { return r.Time })
		m.RetentionWarnings = retentionWarnings(m.ExpireRuns)
		recomputeLastBackups(&m)
		out = append(out, m)
		pushes = append(pushes, st.pushes)
	}
	markNoLaterPush(out, pushes)
	return out
}

// markNoLaterPush turns a still failing stretch into no_later_push when
// another stanza pushed the same segment after its last attempt:
// archive_command was switched to that stanza. pushes[i] are out[i]'s.
func markNoLaterPush(out []PgBackRestMetrics, pushes [][]PgBackRestPush) {
	for i := range out {
		for k := range out[i].ArchivePush.Failures {
			f := &out[i].ArchivePush.Failures[k]
			if f.Recovery != PgBackRestStillFailing || f.Segment == "" {
				continue
			}
			for j := range out {
				if j != i && slices.ContainsFunc(pushes[j], func(p PgBackRestPush) bool {
					return p.Segment == f.Segment && p.Time.After(f.Last)
				}) {
					f.Recovery = PgBackRestNoLaterPush
					break
				}
			}
		}
	}
}

// noteGetRecovery sets each archive-get stretch's Recovery: recovered
// when archive-get later found a segment for the same data directory.
func (st *pgbrStanza) noteGetRecovery(ag *PgBackRestArchiveStats, cutAfter bool) {
	for i := range ag.Failures {
		f := &ag.Failures[i]
		switch {
		case f.Last.IsZero() || cutAfter:
			f.Recovery = PgBackRestRecoveryUnknown
		case st.getFoundAt[f.Path].After(f.Last):
			f.Recovery = PgBackRestRecovered
		default:
			f.Recovery = PgBackRestStillFailing
		}
	}
}

// missingWAL keeps the archive-get misses of a segment whose push was
// logged before: once per segment and data directory, in time order. An
// async batch is known by its last segment only, so a segment pushed in
// the middle of one is not matched.
func (st *pgbrStanza) missingWAL() []PgBackRestMissingWAL {
	var out []PgBackRestMissingWAL
	seen := map[[2]string]bool{}
	misses := slices.Clone(st.getMisses)
	sortByTime(misses, func(m PgBackRestMissingWAL) time.Time { return m.Time })
	for _, miss := range misses {
		k := [2]string{miss.Segment, miss.Path}
		if seen[k] || miss.Time.IsZero() || st.getFoundSeg[miss.Path+"\x00"+miss.Segment].After(miss.Time) {
			continue
		}
		var pushed time.Time
		for _, p := range st.pushes {
			if p.Segment == miss.Segment && p.Time.Before(miss.Time) && (pushed.IsZero() || p.Time.Before(pushed)) {
				pushed = p.Time
			}
		}
		if pushed.IsZero() {
			continue // end of the archive, or a standby ahead of it: expected
		}
		seen[k] = true
		miss.PushedAt = pushed
		out = append(out, miss)
	}
	return out
}

// restoreTimes lists the finds per data directory, in order of first find.
func (st *pgbrStanza) restoreTimes() []PgBackRestPathTimes {
	var out []PgBackRestPathTimes
	for path, ts := range st.getFoundTimes {
		ts = slices.Clone(ts)
		slices.SortFunc(ts, time.Time.Compare)
		out = append(out, PgBackRestPathTimes{Path: path, Times: ts})
	}
	slices.SortFunc(out, func(a, b PgBackRestPathTimes) int {
		if c := a.Times[0].Compare(b.Times[0]); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// notePushRecovery sets LastPush and each push failure stretch's
// Recovery: recovered by the first successful push after its last
// attempt, of its segment or a later one (WAL names order by timeline,
// then position), never by the async batch that failed it. cutAfter is
// true when --end left later lines out.
func (st *pgbrStanza) notePushRecovery(ap *PgBackRestArchiveStats, cutAfter bool) {
	pushes := slices.Clone(st.pushes)
	slices.SortStableFunc(pushes, func(a, b PgBackRestPush) int { return a.Time.Compare(b.Time) })
	// The last WAL segment, not a later .backup or .history file.
	if n := len(pushes); n > 0 {
		last := pushes[n-1]
		for i := n - 1; i >= 0; i-- {
			if strings.IndexByte(pushes[i].Segment, '.') < 0 {
				last = pushes[i]
				break
			}
		}
		ap.LastPush = &last
	}
	known := (ap.Logged || ap.AsyncLogged || len(pushes) > 0) && !cutAfter
	for i := range ap.Failures {
		f := &ap.Failures[i]
		f.Recovery = PgBackRestRecoveryUnknown
		if f.Last.IsZero() {
			continue // log-timestamp=n: no order to read
		}
		if known {
			f.Recovery = PgBackRestStillFailing
		}
		j := sort.Search(len(pushes), func(j int) bool { return pushes[j].Time.After(f.Last) })
		for ; j < len(pushes); j++ {
			p := pushes[j]
			if slices.Contains(p.batchFailed, f.Segment) {
				continue
			}
			if f.Segment == "" || (len(p.Segment) == len(f.Segment) && p.Segment >= f.Segment) {
				f.Recovery, f.RecoveredBy = PgBackRestRecovered, &p
				break
			}
		}
	}
}

// asyncPushTimes dates each segment pushed through the async worker:
// those of its successful batches at the batch's end, plus those the
// synchronous call reported that no such batch covers (the worker's log
// was not read), at that line. Its length is ArchivePush.PushedAsync.
func (st *pgbrStanza) asyncPushTimes() []time.Time {
	var out []time.Time
	for _, r := range st.asyncPushRanges {
		for range r.n {
			out = append(out, r.at)
		}
	}
	if len(st.asyncPushedSegs) == 0 {
		return out
	}
	// A segment is covered by a batch whose first segment has its length
	// and whose bounds hold it. Per length, the batches are merged into
	// disjoint sorted spans, then each segment is binary-searched.
	spans := map[int][]pgbrPushRange{}
	for _, r := range st.asyncPushRanges {
		if r.first <= r.last {
			spans[len(r.first)] = append(spans[len(r.first)], r)
		}
	}
	for l, rs := range spans {
		sort.Slice(rs, func(i, j int) bool { return rs[i].first < rs[j].first })
		merged := rs[:1]
		for _, r := range rs[1:] {
			cur := &merged[len(merged)-1]
			if r.first <= cur.last {
				cur.last = max(cur.last, r.last)
			} else {
				merged = append(merged, r)
			}
		}
		spans[l] = merged
	}
	for _, p := range st.asyncPushedSegs {
		seg := p.seg
		rs := spans[len(seg)]
		// Last span starting at or before seg.
		i := sort.Search(len(rs), func(i int) bool { return rs[i].first > seg }) - 1
		if seg == "" || i < 0 || seg > rs[i].last {
			out = append(out, p.at)
		}
	}
	return out
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	out := append([]string(nil), a...)
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// pgbrDropEventTolerance is how close two "dropped WAL file" lines of the
// same segment must be to count as one drop seen from two logs (the async
// worker's and the PostgreSQL log): they are milliseconds apart in
// practice.
const pgbrDropEventTolerance = 5 * time.Minute

// dedupDropEvents returns events in time order, without those that
// name the same segment within pgbrDropEventTolerance of an earlier one
// in the list — see PgBackRestDropEvent.
func dedupDropEvents(events []PgBackRestDropEvent) []PgBackRestDropEvent {
	var dst []PgBackRestDropEvent
	// Kept events by segment: only those of the same segment can match.
	kept := map[string][]PgBackRestDropEvent{}
	for _, s := range events {
		dup := false
		for _, d := range kept[s.Segment] {
			if d.Segment == "" || d.Time.IsZero() || s.Time.IsZero() {
				continue
			}
			diff := d.Time.Sub(s.Time)
			if diff < 0 {
				diff = -diff
			}
			if diff <= pgbrDropEventTolerance {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, s)
			kept[s.Segment] = append(kept[s.Segment], s)
		}
	}
	sort.Slice(dst, func(i, j int) bool { return dst[i].Time.Before(dst[j].Time) })
	return dst
}

// mergeFailureStretches folds src's stretches into dst, merging a pair
// that names the same segment and whose windows overlap or touch: one
// outage seen from two logs, so Attempts is the max of the two, not their
// sum. A stretch without a segment never merges.
func mergeFailureStretches(dst, src []PgBackRestFailureStretch) []PgBackRestFailureStretch {
	// Indexes into dst by segment: only stretches of the same segment merge.
	bySeg := make(map[string][]int, len(dst))
	for i, d := range dst {
		bySeg[d.Segment] = append(bySeg[d.Segment], i)
	}
	for _, s := range src {
		merged := false
		for _, i := range bySeg[s.Segment] {
			d := &dst[i]
			if d.Segment == "" || !stretchesOverlap(*d, s) {
				continue
			}
			mergeSpan(&d.First, &d.Last, s.First, s.Last)
			if s.Attempts > d.Attempts {
				d.Attempts = s.Attempts
			}
			if d.Code == 0 {
				d.Code = s.Code
			}
			merged = true
			break
		}
		if !merged {
			bySeg[s.Segment] = append(bySeg[s.Segment], len(dst))
			dst = append(dst, s)
		}
	}
	sortStretches(dst)
	return dst
}

// stretchesOverlap reports whether two same-segment failure stretches
// describe overlapping (inclusive) time windows — the signal that they
// are one outage observed twice rather than two distinct ones for a
// segment that failed, recovered, and later failed again.
func stretchesOverlap(a, b PgBackRestFailureStretch) bool {
	if a.First.IsZero() || a.Last.IsZero() || b.First.IsZero() || b.Last.IsZero() {
		return false
	}
	return !a.Last.Before(b.First) && !b.Last.Before(a.First)
}

// sortByTime orders items chronologically, keeping stream order for
// equal times.
func sortByTime[T any](items []T, at func(T) time.Time) {
	sort.SliceStable(items, func(i, j int) bool { return at(items[i]).Before(at(items[j])) })
}

// sortPgBackRestIssues ranks an Errors/Warnings list by Count
// (descending, the most frequent first) then, for ties, by First
// occurrence (ascending) — the one order every packaging (single file,
// several files/arguments, tar/concatenated stream) must converge on,
// whether or not the list went through mergeIssues on its way here.
// Each issue's Commands is sorted too, for the same reason.
func sortPgBackRestIssues(issues []PgBackRestIssue) {
	for i := range issues {
		sort.Strings(issues[i].Commands)
		slices.SortFunc(issues[i].Times, time.Time.Compare)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Count != issues[j].Count {
			return issues[i].Count > issues[j].Count
		}
		return issues[i].First.Before(issues[j].First)
	})
}

func mergeIssues(a, b []PgBackRestIssue) []PgBackRestIssue {
	out := append([]PgBackRestIssue(nil), a...)
	idx := rebuildPgBackRestIssueIndex(out)
	for _, is := range b {
		k := issueKey(is.Code, is.HasCode, is.Message, is.Detail)
		if i, ok := idx[k]; ok {
			out[i].Count += is.Count
			out[i].Times = append(slices.Clone(out[i].Times), is.Times...)
			out[i].Commands = mergeStrings(out[i].Commands, is.Commands)
			out[i].cmdCount = maps.Clone(out[i].cmdCount)
			for c, n := range is.cmdCount {
				if out[i].cmdCount == nil {
					out[i].cmdCount = map[string]int{}
				}
				out[i].cmdCount[c] += n
			}
			mergeSpan(&out[i].First, &out[i].Last, is.First, is.Last)
		} else {
			idx[k] = len(out)
			out = append(out, is)
		}
	}
	sortPgBackRestIssues(out)
	return out
}

// issueKey groups issues by code, normalized message and cause: two
// failures sharing a message ("unable to find a valid repository") may
// have different causes ("[FileOpenError] ... Permission denied",
// "[FileMissingError] ...").
// pgbrErrTimes are the times one archive ERROR was logged by the
// synchronous call and by the async worker.
type pgbrErrTimes struct {
	sync, async []time.Time
}

// pgbrRelayWindow is how far apart the async worker's ERROR and the
// synchronous call's relay of it can be logged.
const pgbrRelayWindow = time.Second

func (st *pgbrStanza) errTimes(key string) *pgbrErrTimes {
	if st.archiveErrTimes == nil {
		st.archiveErrTimes = make(map[string]*pgbrErrTimes)
	}
	t := st.archiveErrTimes[key]
	if t == nil {
		t = &pgbrErrTimes{}
		st.archiveErrTimes[key] = t
	}
	return t
}

// noteArchiveErrTime records when an archive-push/get ERROR was logged,
// for dedupRelayedErrors.
func (st *pgbrStanza) noteArchiveErrTime(cmd string, code int, hasCode bool, firstLine string, detail []string, ts time.Time) {
	switch baseCommand(cmd) {
	case "archive-push", "archive-get":
	default:
		return
	}
	t := st.errTimes(issueKey(code, hasCode, normalizePgBackRestMessage(firstLine), detail))
	if strings.HasSuffix(cmd, ":async") {
		t.async = append(t.async, ts)
	} else {
		t.sync = append(t.sync, ts)
	}
}

// dedupRelayedErrors counts once an async worker ERROR that the
// synchronous call relayed to PostgreSQL: the call reads the worker's
// error and logs it again, within pgbrRelayWindow. Each relay pairs with
// one worker ERROR at most.
func (st *pgbrStanza) dedupRelayedErrors(errs []PgBackRestIssue) []PgBackRestIssue {
	if len(st.archiveErrTimes) == 0 {
		return errs
	}
	errs = slices.Clone(errs)
	for i := range errs {
		is := &errs[i]
		t := st.archiveErrTimes[issueKey(is.Code, is.HasCode, is.Message, is.Detail)]
		if t == nil || len(t.sync) == 0 || len(t.async) == 0 {
			continue
		}
		relayed := relayPairs(t.sync, t.async)
		is.Count -= len(relayed)
		is.Times = removeTimes(is.Times, relayed)
	}
	return errs
}

// relayPairs returns the async times that have a sync time within
// pgbrRelayWindow, each sync time used once (greedy, in time order).
func relayPairs(sync, async []time.Time) []time.Time {
	sync = slices.Clone(sync)
	async = slices.Clone(async)
	slices.SortFunc(sync, time.Time.Compare)
	slices.SortFunc(async, time.Time.Compare)
	var out []time.Time
	j := 0
	for _, a := range async {
		for j < len(sync) && sync[j].Before(a.Add(-pgbrRelayWindow)) {
			j++
		}
		if j < len(sync) && !sync[j].After(a.Add(pgbrRelayWindow)) {
			out = append(out, a)
			j++
		}
	}
	return out
}

// removeTimes removes one instance of each of drop from ts.
func removeTimes(ts, drop []time.Time) []time.Time {
	out := slices.Clone(ts)
	for _, d := range drop {
		if i := slices.IndexFunc(out, d.Equal); i >= 0 {
			out = slices.Delete(out, i, i+1)
		}
	}
	return out
}

func issueKey(code int, hasCode bool, msg string, detail []string) string {
	key := "|" + msg + "|" + issueCause(detail)
	if hasCode {
		return strconv.Itoa(code) + key
	}
	return key
}

// issueCause is the first line written under an issue that is not a
// HINT, normalized like the message.
func issueCause(detail []string) string {
	for _, l := range detail {
		if !strings.HasPrefix(l, "HINT: ") {
			return normalizePgBackRestMessage(l)
		}
	}
	return ""
}

// recomputeLastBackups orders the merged backup list (which may now
// interleave backups from several files/stanzas sharing the same
// stanza name) by start, then finds the most recent successful
// full/diff/incr backup, by end.
func recomputeLastBackups(m *PgBackRestMetrics) {
	sort.SliceStable(m.Backups, func(i, j int) bool { return m.Backups[i].Start.Before(m.Backups[j].Start) })
	byEnd := make([]int, len(m.Backups))
	for i := range byEnd {
		byEnd[i] = i
	}
	sort.SliceStable(byEnd, func(i, j int) bool { return m.Backups[byEnd[i]].End.Before(m.Backups[byEnd[j]].End) })
	expired := make(map[string]bool, 4)
	for _, er := range m.ExpireRuns {
		for _, label := range er.Expired {
			expired[label] = true
		}
	}
	m.Last = make(map[string]PgBackRestLast, 3)
	mostRecent := map[string]*PgBackRestBackup{}
	for _, i := range byEnd {
		b := &m.Backups[i]
		if !b.Succeeded {
			continue
		}
		mostRecent[b.Type] = b
		if b.Label != "" && expired[b.Label] {
			continue // superseded by its own expire run; not eligible
		}
		l := m.Last[b.Type]
		l.Backup = b
		m.Last[b.Type] = l
	}
	// Last never holds an expired label: the most recent one was expired
	// exactly when it is not Last.
	for typ, b := range mostRecent {
		if b.Label != "" && expired[b.Label] {
			l := m.Last[typ]
			l.Expired = b.Label
			m.Last[typ] = l
		}
	}
}

// retentionWarnings groups the expire runs' warnings by text.
func retentionWarnings(runs []PgBackRestExpireRun) []PgBackRestRetentionWarning {
	var groups []PgBackRestRetentionWarning
	idx := map[string]int{}
	for _, r := range runs {
		for _, text := range r.Warnings {
			i, ok := idx[text]
			if !ok {
				i = len(groups)
				idx[text] = i
				groups = append(groups, PgBackRestRetentionWarning{Text: text})
			}
			g := &groups[i]
			g.Count++
			if !slices.Contains(g.Repos, r.Repo) {
				g.Repos = append(g.Repos, r.Repo)
			}
			widenSpan(&g.First, &g.Last, r.Time)
		}
	}
	return groups
}

// pgFailureGroups groups PostgreSQL's archive_command failures by segment
// and cause, in time order.
func pgFailureGroups(fs []PgBackRestPGArchiveFailure) []PgBackRestPGFailureGroup {
	var groups []PgBackRestPGFailureGroup
	idx := map[string]int{}
	for _, f := range fs {
		cause := "exit unknown"
		switch {
		case f.IsSignal:
			cause = fmt.Sprintf("signal %d", f.Signal)
		case f.HasExit:
			cause = fmt.Sprintf("exit %d", f.ExitCode)
		}
		key := f.Segment + "|" + cause
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, PgBackRestPGFailureGroup{Segment: f.Segment, Cause: cause})
		}
		groups[i].Count++
		widenSpan(&groups[i].First, &groups[i].Last, f.Time)
	}
	sortByTime(groups, func(g PgBackRestPGFailureGroup) time.Time { return g.First })
	return groups
}
