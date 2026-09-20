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
	Lines        int       `json:"lines"`

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
			inc(a.m.ConnectionsPerDatabase, db)
			inc(a.m.ConnectionsPerUser, user)
			inc(a.m.ConnectionsPerHost, host)
		case strings.HasPrefix(verb, "new connection to server"):
			a.m.ServerConnections++
		case strings.HasPrefix(verb, "closing because:"):
			a.processClosing(peer, verb)
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

// processClosing records a completed client session. Only client (C-0x)
// closings carry a meaningful user-facing session age; server (S-0x)
// closings are pool recycling and are not counted as sessions here — but
// the caller already filtered on socket prefix via the peer string, so
// we count every age-stamped closing the same way pgBadger does.
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
//  1. A "0 us" field is a "no operation in this period" sentinel, not a
//     measurement. Averaging it in would let an idle night halve the
//     reported latency. Skipping it drops it from both the numerator and
//     the denominator, which is exactly what a weighted mean does.
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
	if us <= 0 {
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
	sp := strings.IndexByte(after, ' ')
	if sp == -1 {
		return after, "", true
	}
	return after[:sp], strings.TrimSpace(after[sp+1:]), true
}

// parsePeer splits "db/user@host:port" into its parts. Missing pieces
// fall back to "?" so breakdowns stay total-preserving.
func parsePeer(peer string) (db, user, host string) {
	db, user, host = "?", "?", "?"
	slash := strings.IndexByte(peer, '/')
	at := strings.IndexByte(peer, '@')
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
