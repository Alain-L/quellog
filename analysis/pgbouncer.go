package analysis

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// PgBouncerStatsPoint is one "LOG stats:" telemetry snapshot. pgBouncer
// emits one per stats_period (default 60s, often 1s in verbose setups),
// each reporting per-second averages over that interval. Finalize sorts
// the series by time for the time-series charts.
type PgBouncerStatsPoint struct {
	Time        time.Time
	Xacts       int
	Queries     int
	InBytes     int
	OutBytes    int
	XactMicros  int64
	QueryMicros int64
	WaitMicros  int64
}

// PgBouncerMetrics is the full set of pgBouncer analysis results. It is
// intentionally separate from AggregatedMetrics: pgBouncer logs carry no
// SQL, query IDs, or checkpoint/vacuum events, so reusing the PostgreSQL
// metrics would mean a report full of empty sections.
type PgBouncerMetrics struct {
	// Name identifies the pooler: its first listen address, or its first
	// file when the log names none. Files are the inputs it was read from,
	// Listen its "listening on" addresses, Version the last release it
	// started as.
	Name    string
	Files   []string
	Listen  []string
	Version string

	MinTimestamp time.Time
	MaxTimestamp time.Time

	// Events is the pooler's own history: starts, stops and the admin
	// commands that changed what it serves, in time order.
	Events []PgBouncerEvent
	// Lines is the number of records the analyzer understood. LinesRead
	// is what the parser scanned and LinesUnrecognized what it could not
	// decode, so a report can show a loss instead of always stating that
	// it parsed everything it parsed. LinesRead may exceed the two of
	// them combined: a time filter drops decoded records too.
	Lines             int
	LinesRead         int
	LinesUnrecognized int
	// DebugLines is how many of Lines are DEBUG or NOISE records. Under
	// verbose=2 they are the overwhelming majority, so the headline
	// count means little until it is split from the operational signal.
	DebugLines int

	// Traffic: aggregates over the "LOG stats:" snapshots.
	StatsSampleCount  int
	AvgXactsPerSec    float64
	MaxXactsPerSec    int
	AvgQueriesPerSec  float64
	MaxQueriesPerSec  int
	AvgInBytesPerSec  float64
	MaxInBytesPerSec  int
	AvgOutBytesPerSec float64
	MaxOutBytesPerSec int
	AvgXactMicros     float64
	MaxXactMicros     int64
	AvgQueryMicros    float64
	MaxQueryMicros    int64
	AvgWaitMicros     float64
	MaxWaitMicros     int64
	StatsSeries       []PgBouncerStatsPoint

	// When each maximum above was logged: the first snapshot reaching it.
	MaxXactsTimestamp   time.Time
	MaxQueriesTimestamp time.Time
	MaxInTimestamp      time.Time
	MaxOutTimestamp     time.Time
	MaxXactTimestamp    time.Time
	MaxQueryTimestamp   time.Time
	MaxWaitTimestamp    time.Time

	// WaitEpisodes are the stretches during which clients queued: runs of
	// consecutive snapshots whose wait reaches waitEpisodeFloorUs.
	WaitEpisodes []PgBouncerWaitEpisode

	// Connections and sessions.
	LoginAttempts       int
	ServerConnections   int
	Sessions            int
	TotalSessionSeconds int64
	SessionDistribution map[string]int
	// SessionReasons splits the completed sessions by the reason the
	// pooler closed them: a client that left, and one the pooler killed
	// in its queue, are different populations with different ages.
	SessionReasons []PgBouncerCloseReason

	// Dimensional breakdowns. "Connections" are client login attempts;
	// "sessions" are completed (closed, age-stamped) client sessions.
	ConnectionsPerDatabase map[string]int
	ConnectionsPerUser     map[string]int
	ConnectionsPerHost     map[string]int
	SessionsPerDatabase    map[string]int
	SessionsPerUser        map[string]int
	SessionsPerHost        map[string]int

	// Pool behaviour and errors.
	ReservePoolActivations int
	Errors                 []PgBouncerError
	TotalErrors            int
}

// PgBouncerEvent is one step of a pooler's life. Kind is "start", "shutdown",
// "reload", or the lowercased admin command (pause, resume, suspend,
// kill, ...); Detail is the release for a start, and otherwise the
// signal or the console command as logged ("PAUSE 'app'").
type PgBouncerEvent struct {
	Time   time.Time
	Kind   string
	Detail string
}

// PgBouncerWaitEpisode is one stretch of queueing. Start is the snapshot
// before the first one over the floor, since a stats line describes the
// period that ended with it; Peak is the highest wait of the stretch.
type PgBouncerWaitEpisode struct {
	Start    time.Time
	End      time.Time
	PeakUs   int64
	PeakTime time.Time
}

// waitEpisodeFloorUs is the wait that opens an episode: 100 ms per server
// assignment, or before 1.23 a tenth of a client queued on average. On the
// lab sagas normal traffic stays under 4 ms and every saturation sits above
// 190 ms, so the floor falls well inside the gap.
const waitEpisodeFloorUs = 100_000

// waitEpisodes finds the queueing stretches of a time-sorted series.
func waitEpisodes(series []PgBouncerStatsPoint) []PgBouncerWaitEpisode {
	var out []PgBouncerWaitEpisode
	var cur *PgBouncerWaitEpisode
	for i, pt := range series {
		if pt.WaitMicros < waitEpisodeFloorUs {
			cur = nil
			continue
		}
		if cur == nil {
			start := pt.Time
			if i > 0 {
				start = series[i-1].Time
			}
			out = append(out, PgBouncerWaitEpisode{Start: start})
			cur = &out[len(out)-1]
		}
		cur.End = pt.Time
		if pt.WaitMicros > cur.PeakUs {
			cur.PeakUs, cur.PeakTime = pt.WaitMicros, pt.Time
		}
	}
	return out
}

// PgBouncerError is one normalized warning or error and when it occurred.
type PgBouncerError struct {
	Message string
	Count   int
	First   time.Time
	Last    time.Time
}

// PgBouncerCloseReason counts the client sessions closed for one reason.
type PgBouncerCloseReason struct {
	Reason       string
	Count        int
	TotalSeconds int64
}

// LinesFiltered is the number of decoded records the filters dropped:
// every line the parser read that neither failed to decode nor reached
// the report. It is derived, not counted, so no key is added to the JSON
// document. Without it the summary of a filtered run reads as a loss --
// "read 373 / entries 1" with nothing accounting for the other 372.
func (m PgBouncerMetrics) LinesFiltered() int {
	if n := m.LinesRead - m.LinesUnrecognized - m.Lines; n > 0 {
		return n
	}
	return 0
}

// statsCoverageFloor is the share of the reported window the "stats:"
// snapshots must account for before the traffic figures are presented
// without a caveat. It is set low on purpose: the point is to catch a
// stats_period so long that the telemetry describes a different window
// than the log does, not to comment on the sampling of a pooler that is
// merely idle. A capture of a correctly configured pooler covers
// essentially all of its window -- the real captures behind the
// regression fixtures sit between 0.90 and 1.00 -- so half leaves a
// wide margin below the legitimate cases.
const statsCoverageFloor = 2 // covered*statsCoverageFloor < window

// StatsPeriod returns pgBouncer's stats_period as the log lets it be
// inferred: the median interval between consecutive "stats:" snapshots.
// pgBouncer never logs the setting, and the cadence of those lines is
// its only trace. The median rather than the mean so a pooler restart
// or a rotation gap in the middle of the capture does not inflate it.
// Returns 0 when fewer than two snapshots leave nothing to measure.
func (m PgBouncerMetrics) StatsPeriod() time.Duration {
	if len(m.StatsSeries) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(m.StatsSeries)-1)
	for i := 1; i < len(m.StatsSeries); i++ {
		// Finalize sorts the series by time, so a non-positive gap is
		// two snapshots sharing a timestamp, not disorder.
		if d := m.StatsSeries[i].Time.Sub(m.StatsSeries[i-1].Time); d > 0 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

// StatsCoverage returns how much of the window the telemetry accounts
// for: one stats_period per snapshot, each snapshot being an average
// over the period that preceded it. Returns 0 when the period could not
// be inferred, which means "cannot be established" rather than "covers
// nothing" -- callers must not read it as a duration of zero.
func (m PgBouncerMetrics) StatsCoverage() time.Duration {
	period := m.StatsPeriod()
	if period <= 0 {
		return 0
	}
	return period * time.Duration(m.StatsSampleCount)
}

// StatsUnderCovered reports whether the traffic and timing figures rest
// on telemetry that leaves most of the window unmeasured. pgBouncer logs
// nothing between two "stats:" lines, so with stats_period far longer
// than the capture the report can state a rate of zero for a window in
// which hundreds of sessions ran: the figure is then a measurement of
// the sampled interval published as one of the log.
//
// It is false for a window with no snapshot at all: that is a section
// with no data, not a figure to distrust, and it gets its own message.
func (m PgBouncerMetrics) StatsUnderCovered() bool {
	if m.StatsSampleCount == 0 {
		return false
	}
	window := m.MaxTimestamp.Sub(m.MinTimestamp)
	if window <= 0 {
		return false
	}
	covered := m.StatsCoverage()
	if covered <= 0 {
		// One snapshot: its extent is unknown, so the share of the
		// window it accounts for cannot be established either.
		return true
	}
	return covered*statsCoverageFloor < window
}

// ClientLinesMissing reports whether the pooler demonstrably handled
// clients over this window while logging none of them. It is the only
// way to tell the two readings of an empty Connections section apart:
// log_connections = 0 silences the "login attempt" and "closing
// because" lines while leaving the telemetry, the pool events and the
// errors untouched, so traffic without a single client line means the
// log is missing them -- whereas a pooler that nobody used produces the
// same empty section with nothing to contradict it.
//
// Only the snapshot counters prove it: a transaction or a query is work
// done for a client, and the "stats:" line carrying them is written
// whatever log_connections is set to. A server connection opened and an
// error are deliberately not accepted as proof, although they are just
// as visible -- min_pool_size opens backends with nobody connected, and
// a backend refusing the pooler errors out before any client exists, so
// either one alone describes a pooler that is idle rather than silenced.
func (m PgBouncerMetrics) ClientLinesMissing() bool {
	if m.LoginAttempts > 0 || m.Sessions > 0 {
		return false
	}
	return m.MaxXactsPerSec > 0 || m.MaxQueriesPerSec > 0
}

// SessionsEndedByClient counts the completed sessions the client ended
// itself ("client close request", "client unexpected eof", ...); the rest
// were ended by the pooler: refused at login, killed in the queue or when
// idle, cut by a PAUSE or a server failure.
func (m PgBouncerMetrics) SessionsEndedByClient() int {
	n := 0
	for _, r := range m.SessionReasons {
		if strings.HasPrefix(r.Reason, "client ") {
			n += r.Count
		}
	}
	return n
}

// WaitIsQueueDepth reports whether the pooler logged "wait" with its
// meaning before pgBouncer 1.23: the clients' accumulated queueing time
// per second of wall time, a queue depth rather than a duration. 1.23
// made it a mean per server assignment. False when the release is not in
// the log.
func (m PgBouncerMetrics) WaitIsQueueDepth() bool {
	major, rest, _ := strings.Cut(m.Version, ".")
	minor, _, _ := strings.Cut(rest, ".")
	x, err1 := strconv.Atoi(major)
	y, err2 := strconv.Atoi(minor)
	return err1 == nil && err2 == nil && (x < 1 || x == 1 && y < 23)
}

// AvgSessionSeconds returns the mean completed-session age in seconds.
func (m PgBouncerMetrics) AvgSessionSeconds() float64 {
	if m.Sessions == 0 {
		return 0
	}
	return float64(m.TotalSessionSeconds) / float64(m.Sessions)
}

// PgBouncerAnalyzer accumulates pgBouncer metrics from a LogEntry stream.
// pgBouncer volumes are modest (telemetry is one line per interval, not
// per query), so a single-pass analyzer is sufficient — no PID sharding.
type PgBouncerAnalyzer struct {
	m PgBouncerMetrics

	sumXacts, sumQueries int64
	sumIn, sumOut        int64
	sumWait              int64 // plain sum, for a pre-1.23 wait (see WaitIsQueueDepth)

	// Weighted accumulators for the three per-operation averages. See
	// accumulateWeighted for why a plain mean of the logged means is
	// wrong.
	sumXactUs, sumXactW   int64
	sumQueryUs, sumQueryW int64
	sumWaitUs, sumWaitW   int64

	errors  map[string]*PgBouncerError
	reasons map[string]*PgBouncerCloseReason

	// Identity, taken from every record the file holds, filtered or not,
	// so grouping files into poolers does not depend on the time window.
	pids        map[string]bool
	first, last time.Time
	versionAt   time.Time
}

// NewPgBouncerAnalyzer returns an analyzer with its maps initialized.
func NewPgBouncerAnalyzer() *PgBouncerAnalyzer {
	return &PgBouncerAnalyzer{
		m: PgBouncerMetrics{
			SessionDistribution:    map[string]int{},
			ConnectionsPerDatabase: map[string]int{},
			ConnectionsPerUser:     map[string]int{},
			ConnectionsPerHost:     map[string]int{},
			SessionsPerDatabase:    map[string]int{},
			SessionsPerUser:        map[string]int{},
			SessionsPerHost:        map[string]int{},
		},
		errors:  map[string]*PgBouncerError{},
		reasons: map[string]*PgBouncerCloseReason{},
		pids:    map[string]bool{},
	}
}

// Observe records what identifies the pooler behind a record: its PID,
// its listen addresses, its release, and the span of the file. It must
// see every record, including those a time filter then drops.
func (a *PgBouncerAnalyzer) Observe(entry *parser.LogEntry) {
	if entry.PID != "" {
		a.pids[entry.PID] = true
	}
	if t := entry.Timestamp; !t.IsZero() {
		if a.first.IsZero() || t.Before(a.first) {
			a.first = t
		}
		if t.After(a.last) {
			a.last = t
		}
	}
	rest, ok := strings.CutPrefix(entry.Message, "LOG ")
	if !ok {
		return
	}
	if addr, ok := strings.CutPrefix(rest, "listening on "); ok && !slices.Contains(a.m.Listen, addr) {
		a.m.Listen = append(a.m.Listen, addr)
	}
	if v, ok := pgbRelease(rest); ok && !entry.Timestamp.Before(a.versionAt) {
		a.m.Version, a.versionAt = v, entry.Timestamp
	}
}

// pgbRelease extracts "1.25.2" from "process up: PgBouncer 1.25.2, ...".
func pgbRelease(rest string) (string, bool) {
	v, ok := strings.CutPrefix(rest, "process up: PgBouncer ")
	if !ok {
		return "", false
	}
	if i := strings.IndexByte(v, ','); i != -1 {
		v = v[:i]
	}
	return v, true
}

// pgbSignals maps the signals pgBouncer logs on receipt to the event
// they cause (see its SIGNALS documentation).
var pgbSignals = map[string]string{
	"SIGINT": "shutdown", "SIGTERM": "shutdown", "SIGQUIT": "shutdown",
	"SIGHUP": "reload", "SIGUSR1": "pause", "SIGUSR2": "resume",
}

// lifecycleEvent recognizes the LOG lines that mark a step of the
// pooler's life: its start banner, a signal, or an admin console command
// ("PAUSE 'db' command issued").
func lifecycleEvent(rest string) (kind, detail string, ok bool) {
	if v, ok := pgbRelease(rest); ok {
		return "start", "PgBouncer " + v, true
	}
	if sig, ok := strings.CutPrefix(rest, "got "); ok {
		sig, effect, _ := strings.Cut(sig, ",")
		// "got SIGUSR1, but already paused/suspended": the signal was
		// received and changed nothing.
		if strings.HasPrefix(strings.TrimSpace(effect), "but ") {
			return "", "", false
		}
		if kind, ok := pgbSignals[sig]; ok {
			return kind, sig, true
		}
		return "", "", false
	}
	cmd, ok := strings.CutSuffix(rest, " command issued")
	if !ok || cmd == "" {
		return "", "", false
	}
	verb, _, _ := strings.Cut(cmd, " ")
	if verb != strings.ToUpper(verb) {
		return "", "", false
	}
	if verb == "SHUTDOWN" {
		return "shutdown", cmd, true
	}
	return strings.ToLower(verb), cmd, true
}

// Process classifies one pgBouncer log entry. Message begins at the
// severity word (e.g. "LOG stats: ...", "WARNING C-0x..: pooler ...").
func (a *PgBouncerAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message
	if msg == "" {
		return
	}
	a.m.Lines++
	a.trackTime(entry.Timestamp)

	severity, rest := splitSeverity(msg)
	if severity == "DEBUG" || severity == "NOISE" {
		a.m.DebugLines++
	}

	// "LOG stats:" telemetry snapshot.
	if severity == "LOG" && strings.HasPrefix(rest, "stats:") {
		a.processStats(entry.Timestamp, rest[len("stats:"):])
		return
	}
	if severity == "LOG" {
		if kind, detail, ok := lifecycleEvent(rest); ok {
			a.m.Events = append(a.m.Events, PgBouncerEvent{Time: entry.Timestamp, Kind: kind, Detail: detail})
		}
	}

	// Socket-scoped events carry "C-0x../S-0x..: <peer> <verb>".
	if peer, verb, ok := splitSocketEvent(rest); ok {
		switch {
		case strings.HasPrefix(verb, "login attempt:"):
			a.m.LoginAttempts++
			db, user, host := parsePeer(peer)
			// The socket prefix names the pool the client is bound
			// to, which is still "(nodb)" when the login is about
			// to be rejected; the verb's own "db=" is the name the
			// client asked for. The peer stays the fallback for
			// releases that log no "db=" field at all.
			if v, ok := verbField(verb, "db="); ok {
				db = v
			}
			inc(a.m.ConnectionsPerDatabase, db)
			inc(a.m.ConnectionsPerUser, user)
			inc(a.m.ConnectionsPerHost, host)
		case strings.HasPrefix(verb, "new connection to server"):
			a.m.ServerConnections++
		case strings.HasPrefix(verb, "closing because:"):
			// A server socket closing is the pool recycling a
			// backend, not a client session, and its peer is
			// PostgreSQL rather than the client.
			if strings.HasPrefix(rest, "C-0x") {
				a.processClosing(peer, verb)
			}
		case strings.Contains(verb, "taking connection from reserve_pool"):
			a.m.ReservePoolActivations++
		}
	}

	// Warnings and errors, regardless of socket scope.
	if severity == "WARNING" || severity == "ERROR" || severity == "FATAL" {
		a.m.TotalErrors++
		a.noteError(normalizeError(rest), entry.Timestamp)
	}
}

func (a *PgBouncerAnalyzer) trackTime(t time.Time) {
	if t.IsZero() {
		return
	}
	if a.m.MinTimestamp.IsZero() || t.Before(a.m.MinTimestamp) {
		a.m.MinTimestamp = t
	}
	if t.After(a.m.MaxTimestamp) {
		a.m.MaxTimestamp = t
	}
}

// processStats parses the comma-separated "<n> <unit>" fragments of a
// stats line. Fields are matched by keyword so older pgBouncer releases
// (without client/server parses or binds) still parse.
func (a *PgBouncerAnalyzer) processStats(t time.Time, body string) {
	pt := PgBouncerStatsPoint{Time: t}
	for _, frag := range strings.Split(body, ",") {
		frag = strings.TrimSpace(frag)
		switch {
		case strings.HasSuffix(frag, "xacts/s"):
			pt.Xacts = leadingInt(frag)
		case strings.HasSuffix(frag, "queries/s"):
			pt.Queries = leadingInt(frag)
		case strings.HasPrefix(frag, "in "):
			pt.InBytes = trailingIntBefore(frag, "B/s")
		case strings.HasPrefix(frag, "out "):
			pt.OutBytes = trailingIntBefore(frag, "B/s")
		case strings.HasPrefix(frag, "xact "):
			pt.XactMicros = int64(trailingIntBefore(frag, "us"))
		case strings.HasPrefix(frag, "query "):
			pt.QueryMicros = int64(trailingIntBefore(frag, "us"))
		case strings.HasPrefix(frag, "wait "):
			pt.WaitMicros = int64(trailingIntBefore(frag, "us"))
		}
	}

	a.m.StatsSampleCount++
	a.sumXacts += int64(pt.Xacts)
	a.sumQueries += int64(pt.Queries)
	a.sumIn += int64(pt.InBytes)
	a.sumOut += int64(pt.OutBytes)
	a.sumWait += pt.WaitMicros
	accumulateWeighted(&a.sumXactUs, &a.sumXactW, pt.XactMicros, pt.Xacts)
	accumulateWeighted(&a.sumQueryUs, &a.sumQueryW, pt.QueryMicros, pt.Queries)
	// "wait" is averaged over server assignments, a counter pgBouncer
	// never puts in the log line. Under transaction pooling, the common
	// case, it is one per transaction (SHOW STATS on the P1 saga: 343050
	// assignments, 343072 transactions, 2399524 queries), so xacts/s is
	// the weight. See accumulateWeighted.
	accumulateWeighted(&a.sumWaitUs, &a.sumWaitW, pt.WaitMicros, pt.Xacts)

	if pt.Xacts > a.m.MaxXactsPerSec {
		a.m.MaxXactsPerSec, a.m.MaxXactsTimestamp = pt.Xacts, t
	}
	if pt.Queries > a.m.MaxQueriesPerSec {
		a.m.MaxQueriesPerSec, a.m.MaxQueriesTimestamp = pt.Queries, t
	}
	if pt.InBytes > a.m.MaxInBytesPerSec {
		a.m.MaxInBytesPerSec, a.m.MaxInTimestamp = pt.InBytes, t
	}
	if pt.OutBytes > a.m.MaxOutBytesPerSec {
		a.m.MaxOutBytesPerSec, a.m.MaxOutTimestamp = pt.OutBytes, t
	}
	if pt.XactMicros > a.m.MaxXactMicros {
		a.m.MaxXactMicros, a.m.MaxXactTimestamp = pt.XactMicros, t
	}
	if pt.QueryMicros > a.m.MaxQueryMicros {
		a.m.MaxQueryMicros, a.m.MaxQueryTimestamp = pt.QueryMicros, t
	}
	if pt.WaitMicros > a.m.MaxWaitMicros {
		a.m.MaxWaitMicros, a.m.MaxWaitTimestamp = pt.WaitMicros, t
	}
	a.m.StatsSeries = append(a.m.StatsSeries, pt)
}

// processClosing records one completed client session. It must be
// called only for client (C-0x) closings: a server closing is the pool
// recycling a backend connection, its age measures that backend's
// lifetime rather than a user-facing session, and its peer is the
// PostgreSQL server, so counting it would credit a session to the
// database host. Process enforces that; this function does not
// re-check.
func (a *PgBouncerAnalyzer) processClosing(peer, verb string) {
	age, ok := parseAge(verb)
	if !ok {
		return
	}
	a.m.Sessions++
	a.m.TotalSessionSeconds += int64(age.Seconds())
	reason := strings.TrimPrefix(verb, "closing because:")
	if i := strings.Index(reason, "(age="); i != -1 {
		reason = reason[:i]
	}
	reason = strings.TrimSpace(reason)
	r := a.reasons[reason]
	if r == nil {
		r = &PgBouncerCloseReason{Reason: reason}
		a.reasons[reason] = r
	}
	r.Count++
	r.TotalSeconds += int64(age.Seconds())
	a.m.SessionDistribution[sessionBucket(age)]++
	db, user, host := parsePeer(peer)
	inc(a.m.SessionsPerDatabase, db)
	inc(a.m.SessionsPerUser, user)
	inc(a.m.SessionsPerHost, host)
}

// noteError counts one occurrence of a normalized warning or error.
func (a *PgBouncerAnalyzer) noteError(msg string, t time.Time) {
	e := a.errors[msg]
	if e == nil {
		e = &PgBouncerError{Message: msg, First: t}
		a.errors[msg] = e
	}
	e.Count++
	if t.Before(e.First) {
		e.First = t
	}
	if t.After(e.Last) {
		e.Last = t
	}
}

// accumulateWeighted folds one snapshot into a weighted average of a
// per-operation duration.
//
// pgBouncer's calc_average (src/stats.c) divides each accumulated time
// by its own counter -- transactions for "xact", queries for "query",
// server assignments for "wait" -- and leaves the mean at 0 when that
// counter is zero for the period. Two consequences:
//
//  1. A "0 us" field is a "no operation in this period" sentinel only
//     when its rate is zero too. Skipping it drops it from numerator and
//     denominator alike, which is what a weighted mean does. Skipping
//     every zero instead would discard the periods that served traffic
//     and measured nothing, and pull the mean up to the busiest sample.
//  2. The snapshots must be weighted by their operation count, otherwise
//     a period that summarises three transactions counts as much as one
//     that summarises three thousand. Every snapshot covers the same
//     stats_period, so the logged per-second rate is proportional to the
//     count and works as the weight.
//
// The weight is floored at 1 because pgBouncer truncates the rate to an
// integer: a period with a single transaction logs "0 xacts/s" next to a
// perfectly real "xact 620 us", and a zero weight would silently discard
// that measurement.
func accumulateWeighted(sumUs, sumWeight *int64, us int64, rate int) {
	// A zero is only a sentinel when nothing ran: pgBouncer leaves the
	// average at 0 when its counter is zero for the period. When the
	// matching rate says work happened, a zero is a measurement -- an
	// unsaturated pool really does wait zero -- and dropping it inflated
	// the mean towards the busiest snapshot.
	if us <= 0 && rate <= 0 {
		return
	}
	weight := int64(rate)
	if weight < 1 {
		weight = 1
	}
	*sumUs += us * weight
	*sumWeight += weight
}

// weightedMean divides an accumulated numerator by its weight, returning
// 0 when no snapshot reported a single operation.
func weightedMean(sumUs, sumWeight int64) float64 {
	if sumWeight <= 0 {
		return 0
	}
	return float64(sumUs) / float64(sumWeight)
}

// Finalize computes the averages, orders the time series and returns the
// metrics.
func (a *PgBouncerAnalyzer) Finalize() PgBouncerMetrics {
	if n := float64(a.m.StatsSampleCount); n > 0 {
		// Rates, unlike the per-operation means above, are true
		// per-second figures: an idle period really did carry zero
		// transactions, so it belongs in a plain mean.
		a.m.AvgXactsPerSec = float64(a.sumXacts) / n
		a.m.AvgQueriesPerSec = float64(a.sumQueries) / n
		a.m.AvgInBytesPerSec = float64(a.sumIn) / n
		a.m.AvgOutBytesPerSec = float64(a.sumOut) / n
	}
	a.m.AvgXactMicros = weightedMean(a.sumXactUs, a.sumXactW)
	a.m.AvgQueryMicros = weightedMean(a.sumQueryUs, a.sumQueryW)
	a.m.AvgWaitMicros = weightedMean(a.sumWaitUs, a.sumWaitW)
	if a.m.WaitIsQueueDepth() && a.m.StatsSampleCount > 0 {
		// A queue depth is a per-second quantity like the rates: an idle
		// period really had nobody waiting, so it takes a plain mean.
		a.m.AvgWaitMicros = float64(a.sumWait) / float64(a.m.StatsSampleCount)
	}

	// Snapshots arrive in stream order, which a multi-file fan-in can
	// interleave across files. The charts plot this series on a time
	// axis and need it increasing; the sort is stable so equal
	// timestamps keep their arrival order and the output stays
	// byte-reproducible.
	sort.SliceStable(a.m.StatsSeries, func(i, j int) bool {
		return a.m.StatsSeries[i].Time.Before(a.m.StatsSeries[j].Time)
	})
	a.m.WaitEpisodes = waitEpisodes(a.m.StatsSeries)
	sort.SliceStable(a.m.Events, func(i, j int) bool {
		return a.m.Events[i].Time.Before(a.m.Events[j].Time)
	})

	a.m.Errors = a.m.Errors[:0]
	for _, e := range a.errors {
		a.m.Errors = append(a.m.Errors, *e)
	}
	sort.Slice(a.m.Errors, func(i, j int) bool {
		x, y := a.m.Errors[i], a.m.Errors[j]
		return x.Count > y.Count || x.Count == y.Count && x.Message < y.Message
	})
	a.m.SessionReasons = a.m.SessionReasons[:0]
	for _, r := range a.reasons {
		a.m.SessionReasons = append(a.m.SessionReasons, *r)
	}
	sort.Slice(a.m.SessionReasons, func(i, j int) bool {
		x, y := a.m.SessionReasons[i], a.m.SessionReasons[j]
		return x.Count > y.Count || x.Count == y.Count && x.Reason < y.Reason
	})
	return a.m
}

// Consume drains one file's records: every record identifies the pooler,
// and the ones inside the time bounds feed the report. ctx cancellation
// stops consumption and drains the remainder.
func (a *PgBouncerAnalyzer) Consume(ctx context.Context, in <-chan []parser.LogEntry, bounds parser.LogFilters) {
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case batch, ok := <-in:
			if !ok {
				break loop
			}
			for i := range batch {
				a.Observe(&batch[i])
				if bounds.IsEmpty() || parser.PassesFilters(batch[i], bounds) {
					a.Process(&batch[i])
				}
			}
			parser.PutBatch(batch)
		}
	}
	if ctx.Err() != nil {
		for range in {
		}
	}
}

// SetSource names the file the analyzer read and what the parser saw of
// it: the lines it scanned and the ones it could not decode.
func (a *PgBouncerAnalyzer) SetSource(file string, read, unrecognized int) {
	a.m.Files = []string{file}
	a.m.LinesRead, a.m.LinesUnrecognized = read, unrecognized
}

// PgBouncerInstances groups per-file analyzers into poolers and returns
// one report per pooler, ordered by first record.
//
// A pooler's log spans several files when it is rotated, or restarted
// with a new file. Such files follow each other in time and share a PID
// (a rotation) or a listen address (a restart). Two poolers running at
// the same time are never merged, even on the same address: that is two
// hosts, or two containers, each with its own log. A file with no record
// in the time window is dropped.
func PgBouncerInstances(files []*PgBouncerAnalyzer) []PgBouncerMetrics {
	sort.SliceStable(files, func(i, j int) bool { return files[i].first.Before(files[j].first) })
	var groups []*PgBouncerAnalyzer
	for _, f := range files {
		var into *PgBouncerAnalyzer
		for _, g := range groups {
			if g.last.After(f.first) {
				continue // running at the same time: another pooler
			}
			if sharesKey(g.pids, f.pids) {
				into = g
				break
			}
			if into == nil && sharesListen(g.m.Listen, f.m.Listen) {
				into = g
			}
		}
		if into == nil {
			groups = append(groups, f)
			continue
		}
		into.merge(f)
	}

	var out []PgBouncerMetrics
	names := map[string]int{}
	for _, g := range groups {
		if g.m.Lines == 0 {
			continue
		}
		m := g.Finalize()
		if len(m.Files) > 0 {
			m.Name = m.Files[0]
		}
		if len(m.Listen) > 0 {
			m.Name = m.Listen[0]
		}
		names[m.Name]++
		out = append(out, m)
	}
	for i := range out {
		if names[out[i].Name] > 1 {
			out[i].Name += " (" + strings.Join(out[i].Files, ", ") + ")"
		}
	}
	return out
}

func sharesKey(a, b map[string]bool) bool {
	for k := range b {
		if a[k] {
			return true
		}
	}
	return false
}

func sharesListen(a, b []string) bool {
	for _, x := range b {
		if slices.Contains(a, x) {
			return true
		}
	}
	return false
}

// merge folds the analyzer of a later file of the same pooler into a.
func (a *PgBouncerAnalyzer) merge(b *PgBouncerAnalyzer) {
	m, n := &a.m, &b.m
	m.Files = append(m.Files, n.Files...)
	for _, l := range n.Listen {
		if !slices.Contains(m.Listen, l) {
			m.Listen = append(m.Listen, l)
		}
	}
	if !b.versionAt.IsZero() && !b.versionAt.Before(a.versionAt) {
		m.Version, a.versionAt = n.Version, b.versionAt
	}
	for k := range b.pids {
		a.pids[k] = true
	}
	a.last = b.last
	if !n.MinTimestamp.IsZero() && (m.MinTimestamp.IsZero() || n.MinTimestamp.Before(m.MinTimestamp)) {
		m.MinTimestamp = n.MinTimestamp
	}
	if n.MaxTimestamp.After(m.MaxTimestamp) {
		m.MaxTimestamp = n.MaxTimestamp
	}
	m.Events = append(m.Events, n.Events...)
	m.Lines += n.Lines
	m.LinesRead += n.LinesRead
	m.LinesUnrecognized += n.LinesUnrecognized
	m.DebugLines += n.DebugLines

	m.StatsSampleCount += n.StatsSampleCount
	m.StatsSeries = append(m.StatsSeries, n.StatsSeries...)
	a.sumXacts += b.sumXacts
	a.sumQueries += b.sumQueries
	a.sumIn += b.sumIn
	a.sumOut += b.sumOut
	a.sumWait += b.sumWait
	a.sumXactUs, a.sumXactW = a.sumXactUs+b.sumXactUs, a.sumXactW+b.sumXactW
	a.sumQueryUs, a.sumQueryW = a.sumQueryUs+b.sumQueryUs, a.sumQueryW+b.sumQueryW
	a.sumWaitUs, a.sumWaitW = a.sumWaitUs+b.sumWaitUs, a.sumWaitW+b.sumWaitW
	peak(&m.MaxXactsPerSec, &m.MaxXactsTimestamp, n.MaxXactsPerSec, n.MaxXactsTimestamp)
	peak(&m.MaxQueriesPerSec, &m.MaxQueriesTimestamp, n.MaxQueriesPerSec, n.MaxQueriesTimestamp)
	peak(&m.MaxInBytesPerSec, &m.MaxInTimestamp, n.MaxInBytesPerSec, n.MaxInTimestamp)
	peak(&m.MaxOutBytesPerSec, &m.MaxOutTimestamp, n.MaxOutBytesPerSec, n.MaxOutTimestamp)
	peak(&m.MaxXactMicros, &m.MaxXactTimestamp, n.MaxXactMicros, n.MaxXactTimestamp)
	peak(&m.MaxQueryMicros, &m.MaxQueryTimestamp, n.MaxQueryMicros, n.MaxQueryTimestamp)
	peak(&m.MaxWaitMicros, &m.MaxWaitTimestamp, n.MaxWaitMicros, n.MaxWaitTimestamp)

	m.LoginAttempts += n.LoginAttempts
	m.ServerConnections += n.ServerConnections
	m.Sessions += n.Sessions
	m.TotalSessionSeconds += n.TotalSessionSeconds
	m.ReservePoolActivations += n.ReservePoolActivations
	m.TotalErrors += n.TotalErrors
	for _, pair := range [][2]map[string]int{
		{m.SessionDistribution, n.SessionDistribution},
		{m.ConnectionsPerDatabase, n.ConnectionsPerDatabase},
		{m.ConnectionsPerUser, n.ConnectionsPerUser},
		{m.ConnectionsPerHost, n.ConnectionsPerHost},
		{m.SessionsPerDatabase, n.SessionsPerDatabase},
		{m.SessionsPerUser, n.SessionsPerUser},
		{m.SessionsPerHost, n.SessionsPerHost},
	} {
		for k, v := range pair[1] {
			pair[0][k] += v
		}
	}
	for msg, e := range b.errors {
		if x := a.errors[msg]; x != nil {
			x.Count += e.Count
			if e.First.Before(x.First) {
				x.First = e.First
			}
			if e.Last.After(x.Last) {
				x.Last = e.Last
			}
		} else {
			a.errors[msg] = e
		}
	}
	for k, r := range b.reasons {
		if x := a.reasons[k]; x != nil {
			x.Count += r.Count
			x.TotalSeconds += r.TotalSeconds
		} else {
			a.reasons[k] = r
		}
	}
}

// peak keeps the higher of two maxima and when it was logged; on a tie
// the earlier one.
func peak[T int | int64](v *T, at *time.Time, w T, wAt time.Time) {
	if w > *v || w == *v && w > 0 && wAt.Before(*at) {
		*v, *at = w, wAt
	}
}

// ---- parsing helpers ----

// splitSeverity splits "LOG stats: ..." into ("LOG", "stats: ...").
func splitSeverity(msg string) (severity, rest string) {
	if i := strings.IndexByte(msg, ' '); i != -1 {
		return msg[:i], msg[i+1:]
	}
	return msg, ""
}

// splitSocketEvent extracts the peer and the verb from a socket-scoped
// message "C-0x..: <peer> <verb...>". Returns ok=false when rest is not
// socket-scoped.
func splitSocketEvent(rest string) (peer, verb string, ok bool) {
	if !strings.HasPrefix(rest, "C-0x") && !strings.HasPrefix(rest, "S-0x") {
		return "", "", false
	}
	colon := strings.IndexByte(rest, ':')
	if colon == -1 {
		return "", "", false
	}
	after := strings.TrimSpace(rest[colon+1:])
	// The peer is "db/user@host:port". A client picks its own database
	// and user names before authenticating, so either may hold a space;
	// the host and port never do, so the peer ends at the first space
	// past the '@'.
	from := 0
	if at := strings.IndexByte(after, '@'); at != -1 {
		from = at
	}
	sp := strings.IndexByte(after[from:], ' ')
	if sp == -1 {
		return after, "", true
	}
	sp += from
	return after[:sp], strings.TrimSpace(after[sp+1:]), true
}

// parsePeer splits "db/user@host:port" into its parts. Missing pieces
// fall back to "?" so breakdowns stay total-preserving.
func parsePeer(peer string) (db, user, host string) {
	db, user, host = "?", "?", "?"
	slash := strings.IndexByte(peer, '/')
	// The last '@', not the first: Azure Database for PostgreSQL
	// requires user@server logins, so the user itself carries one.
	at := strings.LastIndexByte(peer, '@')
	if slash == -1 || at == -1 || at < slash {
		return
	}
	db = peer[:slash]
	user = peer[slash+1 : at]
	host = peer[at+1:]
	if c := strings.LastIndexByte(host, ':'); c != -1 {
		host = host[:c]
	}
	return
}

// verbField returns the value of a "key=value" field carried by the
// verb, e.g. verbField("login attempt: db=app user=bob tls=no", "db=")
// is ("app", true). ok=false when the field is absent, so callers can
// fall back to another source.
//
// A value runs to the next "key=" pair rather than to the next space:
// the fields are unquoted, and a client picks its own database and user
// names before authenticating, so either may hold a space.
func verbField(verb, key string) (string, bool) {
	for i := 0; ; {
		j := strings.Index(verb[i:], key)
		if j == -1 {
			return "", false
		}
		j += i
		if j == 0 || verb[j-1] == ' ' {
			v := strings.TrimSpace(verb[j+len(key):])
			if end := nextFieldStart(v); end != -1 {
				v = v[:end]
			}
			if v != "" {
				return v, true
			}
			return "", false
		}
		i = j + len(key)
	}
}

// nextFieldStart returns the offset of the space that begins the next
// "key=" pair in v, or -1 when v holds no further pair.
func nextFieldStart(v string) int {
	for i := 0; i < len(v); i++ {
		if v[i] != ' ' {
			continue
		}
		for j := i + 1; j < len(v); j++ {
			if v[j] == '=' {
				return i
			}
			if v[j] == ' ' {
				break
			}
		}
	}
	return -1
}

// parseAge extracts the "(age=Ns)" session duration.
func parseAge(verb string) (time.Duration, bool) {
	i := strings.Index(verb, "(age=")
	if i == -1 {
		return 0, false
	}
	j := strings.IndexByte(verb[i:], ')')
	if j == -1 {
		return 0, false
	}
	token := verb[i+len("(age=") : i+j] // e.g. "42s"
	token = strings.TrimSuffix(token, "s")
	secs, err := strconv.Atoi(strings.TrimSpace(token))
	if err != nil || secs < 0 {
		// A negative age is a corrupt line, never a session: keeping
		// it would drag the total and the mean below zero.
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// normalizeError strips the variable socket id / peer / age so the same
// failure collapses into one "Most Frequent Errors" bucket.
func normalizeError(rest string) string {
	// Drop a leading "C-0x..: <peer> " or "S-0x..: <peer> " scope.
	if _, verb, ok := splitSocketEvent(rest); ok {
		rest = verb
	}
	// Drop trailing "(age=Ns)".
	if i := strings.Index(rest, " (age="); i != -1 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// sessionBucket maps a duration to the same labels the PostgreSQL
// connection report uses, for an iso session-time histogram.
func sessionBucket(d time.Duration) string {
	switch {
	case d < time.Second:
		return "< 1s"
	case d < time.Minute:
		return "1s - 1min"
	case d < 30*time.Minute:
		return "1min - 30min"
	case d < 2*time.Hour:
		return "30min - 2h"
	case d < 5*time.Hour:
		return "2h - 5h"
	default:
		return "> 5h"
	}
}

func inc(m map[string]int, k string) {
	if k == "" {
		k = "?"
	}
	m[k]++
}

// leadingInt parses the integer at the start of a fragment like
// "17 xacts/s". An unreadable number -- a line cut short by a rotation,
// or two writers interleaved -- yields 0, i.e. the field is treated as
// absent. Atoi's value must be discarded on error: out of range it
// returns math.MaxInt64 alongside ErrRange, which overflows the running
// sums and turns the averages negative.
func leadingInt(frag string) int {
	sp := strings.IndexByte(frag, ' ')
	if sp == -1 {
		return 0
	}
	n, err := strconv.Atoi(frag[:sp])
	if err != nil {
		return 0
	}
	return n
}

// trailingIntBefore parses the integer that precedes the given unit
// suffix, e.g. trailingIntBefore("in 1742 B/s", "B/s") == 1742 and
// trailingIntBefore("xact 2384 us", "us") == 2384. Like leadingInt, an
// unreadable number yields 0 rather than Atoi's out-of-range sentinel.
func trailingIntBefore(frag, unit string) int {
	frag = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(frag), unit))
	sp := strings.LastIndexByte(frag, ' ')
	if sp == -1 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(frag[sp+1:]))
	if err != nil {
		return 0
	}
	return n
}
