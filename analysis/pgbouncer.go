package analysis

import (
	"context"
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
	Time        time.Time `json:"time"`
	Xacts       int       `json:"xacts_per_sec"`
	Queries     int       `json:"queries_per_sec"`
	InBytes     int       `json:"in_bytes_per_sec"`
	OutBytes    int       `json:"out_bytes_per_sec"`
	XactMicros  int64     `json:"xact_us"`
	QueryMicros int64     `json:"query_us"`
	WaitMicros  int64     `json:"wait_us"`
}

// PgBouncerMetrics is the full set of pgBouncer analysis results. It is
// intentionally separate from AggregatedMetrics: pgBouncer logs carry no
// SQL, query IDs, or checkpoint/vacuum events, so reusing the PostgreSQL
// metrics would mean a report full of empty sections.
type PgBouncerMetrics struct {
	MinTimestamp time.Time `json:"min_timestamp"`
	MaxTimestamp time.Time `json:"max_timestamp"`
	// Lines is the number of records the analyzer understood. LinesRead
	// is what the parser scanned and LinesUnrecognized what it could not
	// decode, so a report can show a loss instead of always stating that
	// it parsed everything it parsed. LinesRead may exceed the two of
	// them combined: a time filter drops decoded records too.
	Lines             int `json:"lines"`
	LinesRead         int `json:"lines_read"`
	LinesUnrecognized int `json:"lines_unrecognized"`
	// DebugLines is how many of Lines are DEBUG or NOISE records. Under
	// verbose=2 they are the overwhelming majority, so the headline
	// count means little until it is split from the operational signal.
	DebugLines int `json:"debug_lines"`

	// Traffic: aggregates over the "LOG stats:" snapshots.
	StatsSampleCount  int                   `json:"stats_sample_count"`
	AvgXactsPerSec    float64               `json:"avg_xacts_per_sec"`
	MaxXactsPerSec    int                   `json:"max_xacts_per_sec"`
	AvgQueriesPerSec  float64               `json:"avg_queries_per_sec"`
	MaxQueriesPerSec  int                   `json:"max_queries_per_sec"`
	AvgInBytesPerSec  float64               `json:"avg_in_bytes_per_sec"`
	MaxInBytesPerSec  int                   `json:"max_in_bytes_per_sec"`
	AvgOutBytesPerSec float64               `json:"avg_out_bytes_per_sec"`
	MaxOutBytesPerSec int                   `json:"max_out_bytes_per_sec"`
	AvgXactMicros     float64               `json:"avg_xact_us"`
	MaxXactMicros     int64                 `json:"max_xact_us"`
	AvgQueryMicros    float64               `json:"avg_query_us"`
	MaxQueryMicros    int64                 `json:"max_query_us"`
	AvgWaitMicros     float64               `json:"avg_wait_us"`
	MaxWaitMicros     int64                 `json:"max_wait_us"`
	StatsSeries       []PgBouncerStatsPoint `json:"stats_series,omitempty"`

	// Connections and sessions.
	LoginAttempts       int            `json:"login_attempts"`
	ServerConnections   int            `json:"server_connections"`
	Sessions            int            `json:"sessions"`
	TotalSessionSeconds int64          `json:"total_session_seconds"`
	SessionDistribution map[string]int `json:"session_distribution,omitempty"`

	// Dimensional breakdowns. "Connections" are client login attempts;
	// "sessions" are completed (closed, age-stamped) client sessions.
	ConnectionsPerDatabase map[string]int `json:"connections_per_database,omitempty"`
	ConnectionsPerUser     map[string]int `json:"connections_per_user,omitempty"`
	ConnectionsPerHost     map[string]int `json:"connections_per_host,omitempty"`
	SessionsPerDatabase    map[string]int `json:"sessions_per_database,omitempty"`
	SessionsPerUser        map[string]int `json:"sessions_per_user,omitempty"`
	SessionsPerHost        map[string]int `json:"sessions_per_host,omitempty"`

	// Pool behaviour and errors.
	ReservePoolActivations int            `json:"reserve_pool_activations"`
	Errors                 map[string]int `json:"errors,omitempty"`
	TotalErrors            int            `json:"total_errors"`
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

	// Weighted accumulators for the three per-operation averages. See
	// accumulateWeighted for why a plain mean of the logged means is
	// wrong.
	sumXactUs, sumXactW   int64
	sumQueryUs, sumQueryW int64
	sumWaitUs, sumWaitW   int64
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
			Errors:                 map[string]int{},
		},
	}
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
		inc(a.m.Errors, normalizeError(rest))
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
	accumulateWeighted(&a.sumXactUs, &a.sumXactW, pt.XactMicros, pt.Xacts)
	accumulateWeighted(&a.sumQueryUs, &a.sumQueryW, pt.QueryMicros, pt.Queries)
	// "wait" is averaged over server assignments, a counter pgBouncer
	// never puts in the log line; queries/s is the closest proxy we do
	// have. See accumulateWeighted.
	accumulateWeighted(&a.sumWaitUs, &a.sumWaitW, pt.WaitMicros, pt.Queries)

	if pt.Xacts > a.m.MaxXactsPerSec {
		a.m.MaxXactsPerSec = pt.Xacts
	}
	if pt.Queries > a.m.MaxQueriesPerSec {
		a.m.MaxQueriesPerSec = pt.Queries
	}
	if pt.InBytes > a.m.MaxInBytesPerSec {
		a.m.MaxInBytesPerSec = pt.InBytes
	}
	if pt.OutBytes > a.m.MaxOutBytesPerSec {
		a.m.MaxOutBytesPerSec = pt.OutBytes
	}
	if pt.XactMicros > a.m.MaxXactMicros {
		a.m.MaxXactMicros = pt.XactMicros
	}
	if pt.QueryMicros > a.m.MaxQueryMicros {
		a.m.MaxQueryMicros = pt.QueryMicros
	}
	if pt.WaitMicros > a.m.MaxWaitMicros {
		a.m.MaxWaitMicros = pt.WaitMicros
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
	a.m.SessionDistribution[sessionBucket(age)]++
	db, user, host := parsePeer(peer)
	inc(a.m.SessionsPerDatabase, db)
	inc(a.m.SessionsPerUser, user)
	inc(a.m.SessionsPerHost, host)
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

	// Snapshots arrive in stream order, which a multi-file fan-in can
	// interleave across files. The charts plot this series on a time
	// axis and need it increasing; the sort is stable so equal
	// timestamps keep their arrival order and the output stays
	// byte-reproducible.
	sort.SliceStable(a.m.StatsSeries, func(i, j int) bool {
		return a.m.StatsSeries[i].Time.Before(a.m.StatsSeries[j].Time)
	})
	return a.m
}

// AggregatePgBouncerMetrics drains a LogEntry stream through a single
// PgBouncerAnalyzer. Mirrors AggregateMetricsWithWorkers' contract: ctx
// cancellation stops consumption and drains the remainder.
func AggregatePgBouncerMetrics(ctx context.Context, in <-chan []parser.LogEntry) PgBouncerMetrics {
	a := NewPgBouncerAnalyzer()
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
				a.Process(&batch[i])
			}
			parser.PutBatch(batch)
		}
	}
	if ctx.Err() != nil {
		for range in {
		}
	}
	return a.Finalize()
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
	// the host and port never do. Look for the end of the peer past the
	// '@' rather than at the first space, which used to cut a quoted
	// name in half and drop the whole event.
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
	if peer, verb, ok := splitSocketEvent(rest); ok {
		_ = peer
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
