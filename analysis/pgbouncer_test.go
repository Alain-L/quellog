package analysis

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// statsLine builds the message part of a "LOG stats:" entry the way the
// stderr parser hands it over (severity word first).
func statsLine(xacts, queries, in, out int, xactUs, queryUs, waitUs string) string {
	return "LOG stats: " + strconv.Itoa(xacts) + " xacts/s, " + strconv.Itoa(queries) +
		" queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in " +
		strconv.Itoa(in) + " B/s, out " + strconv.Itoa(out) + " B/s, xact " + xactUs +
		" us, query " + queryUs + " us, wait " + waitUs + " us"
}

func feed(a *PgBouncerAnalyzer, base time.Time, msgs ...string) {
	for i, m := range msgs {
		e := parser.LogEntry{Timestamp: base.Add(time.Duration(i) * time.Minute), Message: m}
		a.Process(&e)
	}
}

func almost(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// TestPgBouncerWeightedAverages pins the central defect: pgBouncer logs
// "0 us" as a "no operation in this period" sentinel, so a quiet period
// must not drag the reported latency down. Ground truth for this pair of
// snapshots is simply the first snapshot's own means (1990 / 261 / 331),
// because the second one describes zero operations.
func TestPgBouncerWeightedAverages(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 41, 13, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		statsLine(3, 21, 1291, 565, "1990", "261", "331"),
		statsLine(0, 0, 0, 0, "0", "0", "0"),
	)
	m := a.Finalize()

	almost(t, "AvgXactMicros", m.AvgXactMicros, 1990)
	almost(t, "AvgQueryMicros", m.AvgQueryMicros, 261)
	almost(t, "AvgWaitMicros", m.AvgWaitMicros, 331)

	// Rates are genuine per-second rates: an idle period really does
	// contribute a zero, so their plain mean stays untouched.
	almost(t, "AvgXactsPerSec", m.AvgXactsPerSec, 1.5)
	almost(t, "AvgQueriesPerSec", m.AvgQueriesPerSec, 10.5)
}

// TestPgBouncerWeightedByCount checks the weighting itself: two
// snapshots with very different volumes must not weigh the same.
func TestPgBouncerWeightedByCount(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		statsLine(1, 1, 0, 0, "1000", "1000", "1000"),
		statsLine(9, 9, 0, 0, "2000", "2000", "2000"),
	)
	m := a.Finalize()

	// (1000*1 + 2000*9) / 10 = 1900, not the unweighted 1500.
	almost(t, "AvgXactMicros", m.AvgXactMicros, 1900)
	almost(t, "AvgQueryMicros", m.AvgQueryMicros, 1900)
	almost(t, "AvgWaitMicros", m.AvgWaitMicros, 1900)
}

// TestPgBouncerAllSnapshotsEmpty guards the division by zero: when every
// snapshot is a sentinel there is no operation to average over.
func TestPgBouncerAllSnapshotsEmpty(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		statsLine(0, 0, 0, 0, "0", "0", "0"),
		statsLine(0, 0, 0, 0, "0", "0", "0"),
	)
	m := a.Finalize()

	if m.StatsSampleCount != 2 {
		t.Fatalf("StatsSampleCount = %d, want 2", m.StatsSampleCount)
	}
	for name, got := range map[string]float64{
		"AvgXactMicros": m.AvgXactMicros, "AvgQueryMicros": m.AvgQueryMicros,
		"AvgWaitMicros": m.AvgWaitMicros,
	} {
		if got != 0 || math.IsNaN(got) {
			t.Errorf("%s = %v, want 0", name, got)
		}
	}
}

// TestPgBouncerTruncatedRateStillCounts covers pgBouncer's integer
// truncation of the per-second rates: a period with a single transaction
// can log "0 xacts/s" next to a real "xact 620 us". Dropping such a
// snapshot for a zero weight would discard a genuine measurement.
func TestPgBouncerTruncatedRateStillCounts(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base, statsLine(0, 0, 34, 69, "620", "620", "0"))
	m := a.Finalize()

	almost(t, "AvgXactMicros", m.AvgXactMicros, 620)
	almost(t, "AvgQueryMicros", m.AvgQueryMicros, 620)
	almost(t, "AvgWaitMicros", m.AvgWaitMicros, 0)
}

// TestPgBouncerStatsSeriesSorted covers the chart axis: uPlot requires a
// strictly increasing X, and the multi-file fan-in can hand snapshots
// over out of order.
func TestPgBouncerStatsSeriesSorted(t *testing.T) {
	a := NewPgBouncerAnalyzer()
	for _, min := range []int{58, 59, 41, 42} {
		ts := time.Date(2026, 9, 20, 13, min, 0, 0, time.UTC)
		e := parser.LogEntry{Timestamp: ts, Message: statsLine(1, 1, 0, 0, "1", "1", "1")}
		a.Process(&e)
	}
	m := a.Finalize()

	if len(m.StatsSeries) != 4 {
		t.Fatalf("series length = %d, want 4", len(m.StatsSeries))
	}
	for i := 1; i < len(m.StatsSeries); i++ {
		if m.StatsSeries[i].Time.Before(m.StatsSeries[i-1].Time) {
			t.Fatalf("series is not monotonic at %d: %v then %v",
				i, m.StatsSeries[i-1].Time, m.StatsSeries[i].Time)
		}
	}
}

// TestPgBouncerStatsSeriesSortStable pins determinism: equal timestamps
// must keep their arrival order so two runs agree byte for byte.
func TestPgBouncerStatsSeriesSortStable(t *testing.T) {
	ts := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	for _, q := range []int{7, 3, 5, 1} {
		e := parser.LogEntry{Timestamp: ts, Message: statsLine(1, q, 0, 0, "1", "1", "1")}
		a.Process(&e)
	}
	m := a.Finalize()

	want := []int{7, 3, 5, 1}
	for i, w := range want {
		if m.StatsSeries[i].Queries != w {
			t.Fatalf("series[%d].Queries = %d, want %d (sort is not stable)",
				i, m.StatsSeries[i].Queries, w)
		}
	}
}

// TestPgBouncerOutOfRangeInteger covers a truncated or interleaved line:
// strconv.Atoi returns math.MaxInt64 together with ErrRange, and keeping
// that value overflows the sums into a negative average.
func TestPgBouncerOutOfRangeInteger(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		statsLine(10, 10, 100, 100, "1000", "1000", "1000"),
		"LOG stats: 99999999999999999999 xacts/s, 99999999999999999999 queries/s, "+
			"in 99999999999999999999 B/s, out 99999999999999999999 B/s, "+
			"xact 99999999999999999999 us, query 99999999999999999999 us, "+
			"wait 99999999999999999999 us",
	)
	m := a.Finalize()

	for name, got := range map[string]float64{
		"AvgXactMicros": m.AvgXactMicros, "AvgQueryMicros": m.AvgQueryMicros,
		"AvgWaitMicros": m.AvgWaitMicros, "AvgXactsPerSec": m.AvgXactsPerSec,
		"AvgQueriesPerSec": m.AvgQueriesPerSec, "AvgInBytesPerSec": m.AvgInBytesPerSec,
		"AvgOutBytesPerSec": m.AvgOutBytesPerSec,
	} {
		if got < 0 || math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("%s = %v, want a finite non-negative value", name, got)
		}
	}
	// The unreadable fields are treated as absent, so the readable
	// snapshot is the only one that carries weight.
	almost(t, "AvgXactMicros", m.AvgXactMicros, 1000)
	if m.MaxXactMicros != 1000 {
		t.Errorf("MaxXactMicros = %d, want 1000", m.MaxXactMicros)
	}
}

// TestPgBouncerNegativeAge rejects a corrupt "(age=-42s)": a session
// cannot last a negative time, and accepting it yields a negative total
// and a negative mean.
func TestPgBouncerNegativeAge(t *testing.T) {
	if d, ok := parseAge("closing because: client close request (age=-42s)"); ok {
		t.Errorf("parseAge accepted a negative age: %v", d)
	}
	if d, ok := parseAge("closing because: client close request (age=42s)"); !ok || d != 42*time.Second {
		t.Errorf("parseAge(42s) = %v, %v; want 42s, true", d, ok)
	}
	if d, ok := parseAge("closing because: client close request (age=0s)"); !ok || d != 0 {
		t.Errorf("parseAge(0s) = %v, %v; want 0s, true", d, ok)
	}

	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		"LOG C-0x1: db/user@10.0.0.1:5000 closing because: client close request (age=-42s)",
		"LOG C-0x2: db/user@10.0.0.1:5001 closing because: client close request (age=10s)",
	)
	m := a.Finalize()

	if m.TotalSessionSeconds < 0 || m.AvgSessionSeconds() < 0 {
		t.Errorf("negative session time: total=%d avg=%v",
			m.TotalSessionSeconds, m.AvgSessionSeconds())
	}
	if m.Sessions != 1 || m.TotalSessionSeconds != 10 {
		t.Errorf("sessions=%d total=%d, want 1 and 10", m.Sessions, m.TotalSessionSeconds)
	}
}

// TestPgBouncerFilteredLinesAreNamed pins the summary's line arithmetic
// under a time filter. Without LinesFiltered the report showed "read
// 373 / entries 1" and nothing else, which reads as 372 lost lines when
// every one of them was simply outside the requested window.
func TestPgBouncerFilteredLinesAreNamed(t *testing.T) {
	m := PgBouncerMetrics{LinesRead: 373, Lines: 1, LinesUnrecognized: 0}
	if got := m.LinesFiltered(); got != 372 {
		t.Errorf("LinesFiltered() = %d, want 372", got)
	}
	// A loss and a filter can coexist: 120 read, 20 undecodable, 100
	// decoded of which 40 survived the window.
	m = PgBouncerMetrics{LinesRead: 120, Lines: 40, LinesUnrecognized: 20}
	if got := m.LinesFiltered(); got != 60 {
		t.Errorf("LinesFiltered() = %d, want 60", got)
	}
	// Unfiltered runs must stay silent: nothing to report.
	m = PgBouncerMetrics{LinesRead: 440, Lines: 440, LinesUnrecognized: 0}
	if got := m.LinesFiltered(); got != 0 {
		t.Errorf("LinesFiltered() = %d, want 0", got)
	}
}

// TestPgBouncerLoginAttributedToRequestedDatabase pins which field of a
// "login attempt" line names the database. The socket prefix is a
// display name for the client's *bound* pool, so it reads "(nodb)"
// whenever pgBouncer rejected the login before binding one; the verb's
// own "db=" is the name the client asked for and is always present.
// Lines are copied verbatim from the audit material
// (pgb-01-transaction-saturation-errors.log).
func TestPgBouncerLoginAttributedToRequestedDatabase(t *testing.T) {
	base := time.Date(2026, 9, 20, 13, 58, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		"LOG C-0x55c871c95fa0: benchdb/bench@127.0.0.1:49372 login attempt: db=benchdb user=bench tls=no replication=no",
		"LOG C-0x55c871cfc500: (nodb)/nouserxyz@127.0.0.1:51184 login attempt: db=benchdb user=nouserxyz tls=no replication=no",
		"LOG C-0x55c871cfc500: (nodb)/bench@127.0.0.1:51194 login attempt: db=nosuchdb user=bench tls=no replication=no",
	)
	m := a.Finalize()

	wantMaps(t, "ConnectionsPerDatabase", m.ConnectionsPerDatabase,
		map[string]int{"benchdb": 2, "nosuchdb": 1})
	// The user and the host have no such placeholder problem: "user="
	// and the peer's own address agree on every line of the corpus.
	wantMaps(t, "ConnectionsPerUser", m.ConnectionsPerUser,
		map[string]int{"bench": 2, "nouserxyz": 1})
	wantMaps(t, "ConnectionsPerHost", m.ConnectionsPerHost,
		map[string]int{"127.0.0.1": 3})
}

// TestPgBouncerServerClosingIsNotASession pins the difference between a
// client socket ending a session and the pool recycling a backend. Only
// C-0x closings are user-facing sessions; an S-0x closing carries the
// *PostgreSQL server* as its peer, so counting it also mis-attributes a
// session to the backend's address. Lines are copied verbatim from
// testdata/regressions/pgbouncer/pgbouncer.log and from the audit
// material (pgb-01-transaction-saturation-errors.log).
func TestPgBouncerServerClosingIsNotASession(t *testing.T) {
	base := time.Date(2026, 9, 9, 21, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		"LOG C-0x7f3b3ad2f6a8: analytics/batch@10.89.0.6:51518 closing because: client close request (age=1s)",
		"LOG S-0x7f3b3acaa5b0: app/app_user@10.89.0.2:5432 closing because: failed to answer authreq (age=0s)",
		"LOG S-0x55c871cde150: benchdb/bench@127.0.0.1:5544 closing because: pause mode (age=7s)",
		"LOG S-0x55c871cdd380: benchdb/bench@127.0.0.1:5544 closing because: server lifetime over (age=30s)",
	)
	m := a.Finalize()

	if m.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1 (three of the four closings are server sockets)", m.Sessions)
	}
	if m.TotalSessionSeconds != 1 {
		t.Errorf("TotalSessionSeconds = %d, want 1", m.TotalSessionSeconds)
	}
	almost(t, "AvgSessionSeconds", m.AvgSessionSeconds(), 1)
	wantMaps(t, "SessionDistribution", m.SessionDistribution,
		map[string]int{"1s - 1min": 1})
	wantMaps(t, "SessionsPerDatabase", m.SessionsPerDatabase,
		map[string]int{"analytics": 1})
	wantMaps(t, "SessionsPerUser", m.SessionsPerUser, map[string]int{"batch": 1})
	// 10.89.0.2 and 127.0.0.1:5544 are PostgreSQL, not clients.
	wantMaps(t, "SessionsPerHost", m.SessionsPerHost,
		map[string]int{"10.89.0.6": 1})
}

// wantMaps compares a breakdown against its expected contents, naming
// every extra and every missing bucket.
func wantMaps(t *testing.T, name string, got, want map[string]int) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s[%q] = %d, want %d", name, k, got[k], v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s has unexpected bucket %q = %d", name, k, v)
		}
	}
}

// TestPgBouncerIdlePoolerWithServerErrorIsNotAccused pins the boundary
// of the "client lines are missing" verdict. A server connection and a
// pooler error are not proof that clients were served: min_pool_size
// opens backends with nobody connected, and a backend that refuses the
// pooler errors out before any client exists. Reading either as traffic
// told an idle pooler that its log_connections was silencing lines that
// were never there to log.
func TestPgBouncerIdlePoolerWithServerErrorIsNotAccused(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    PgBouncerMetrics
		want bool
	}{
		{"idle", PgBouncerMetrics{}, false},
		{"server connections only (min_pool_size)",
			PgBouncerMetrics{ServerConnections: 4}, false},
		{"pooler errors only (backend unreachable)",
			PgBouncerMetrics{TotalErrors: 7}, false},
		{"both, still no client",
			PgBouncerMetrics{ServerConnections: 4, TotalErrors: 7}, false},
		{"queries prove a client", PgBouncerMetrics{MaxQueriesPerSec: 48}, true},
		{"transactions prove a client", PgBouncerMetrics{MaxXactsPerSec: 12}, true},
		{"client lines present", PgBouncerMetrics{MaxQueriesPerSec: 48, LoginAttempts: 3}, false},
	} {
		if got := tc.m.ClientLinesMissing(); got != tc.want {
			t.Errorf("%s: ClientLinesMissing() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPgBouncerZeroIsAMeasurementWhenWorkHappened is a refutation test:
// it separates the rule this code implements from the neighbouring rule
// it used to implement, which no earlier test could tell apart.
//
// pgBouncer leaves an average at 0 when its counter is zero for the
// period. Reading that backwards -- skipping every snapshot whose value
// is 0 -- also throws away the periods that did work and measured no
// wait, which is what an unsaturated pool looks like. The two rules
// agree on a wholly idle snapshot and disagree here, so this is the
// case that pins which one is in force.
//
// Nine periods serve 100 queries/s each and queue nobody; the tenth
// queues for five seconds. The mean wait is 0.5 s. Skipping the zeros
// leaves only the tenth and reports 5 s -- the mean equal to the max.
func TestPgBouncerZeroIsAMeasurementWhenWorkHappened(t *testing.T) {
	a := NewPgBouncerAnalyzer()
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for i := range 10 {
		wait := 0
		if i == 9 {
			wait = 5_000_000
		}
		a.processStats(base.Add(time.Duration(i)*time.Minute), fmt.Sprintf(
			"stats: 100 xacts/s, 100 queries/s, 0 client parses/s, 0 server parses/s, "+
				"0 binds/s, in 900 B/s, out 800 B/s, xact 1000 us, query 500 us, wait %d us", wait))
	}
	m := a.Finalize()
	if got := m.AvgWaitMicros; got != 500_000 {
		t.Errorf("AvgWaitMicros = %v, want 500000 (skipping the zero waits gives 5e6)", got)
	}
	// A wholly idle snapshot stays a sentinel: nothing ran, so there is
	// nothing to average in. Both rules agree here -- this guards the
	// fix from over-correcting.
	b := NewPgBouncerAnalyzer()
	b.processStats(base, "stats: 3 xacts/s, 21 queries/s, 0 client parses/s, 0 server parses/s, "+
		"0 binds/s, in 1291 B/s, out 565 B/s, xact 1990 us, query 261 us, wait 331 us")
	b.processStats(base.Add(time.Minute), "stats: 0 xacts/s, 0 queries/s, 0 client parses/s, "+
		"0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us")
	if got := b.Finalize().AvgXactMicros; got != 1990 {
		t.Errorf("AvgXactMicros = %v, want 1990 (the idle snapshot must stay excluded)", got)
	}
}

// TestPgBouncerPeerWithSpacesAndAtSigns is a refutation test for the two
// peer delimiters. A client picks its own database and user names in the
// startup packet, before authenticating, so both may hold a space (a
// quoted identifier) or an '@' (Azure requires user@server). Cutting the
// peer at the first space dropped the whole event without a trace, and
// cutting the host at the first '@' split an Azure user in two.
//
// The host and port never hold a space, so the peer ends at the first
// space after the '@' -- that is the rule in force. The neighbouring
// rules, "first space" and "first @", agree on every conventional name
// and disagree on these.
func TestPgBouncerPeerWithSpacesAndAtSigns(t *testing.T) {
	for _, tc := range []struct{ name, peer, db, user, host string }{
		{"plain", "app/bob@10.0.0.1:5432", "app", "bob", "10.0.0.1"},
		{"space in database", "my db/bob@10.0.0.1:5432", "my db", "bob", "10.0.0.1"},
		{"azure user@server", "app/bob@srv01@10.0.0.1:5432", "app", "bob@srv01", "10.0.0.1"},
	} {
		db, user, host := parsePeer(tc.peer)
		if db != tc.db || user != tc.user || host != tc.host {
			t.Errorf("%s: parsePeer(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.name, tc.peer, db, user, host, tc.db, tc.user, tc.host)
		}
	}
	// The event must survive the split, not vanish: a peer holding a
	// space used to take its whole line with it.
	peer, verb, ok := splitSocketEvent("C-0x1: my db/bob@10.0.0.1:5432 login attempt: db=my db user=bob")
	if !ok || peer != "my db/bob@10.0.0.1:5432" || !strings.HasPrefix(verb, "login attempt:") {
		t.Errorf("splitSocketEvent = (%q, %q, %v), want the full peer and the verb", peer, verb, ok)
	}
}

// TestPgBouncerLifecycleEvents is a table of the LOG lines that mark a
// step of the pooler's life, next to lines that look alike and are not.
func TestPgBouncerLifecycleEvents(t *testing.T) {
	for _, c := range []struct{ rest, kind, detail string }{
		{"process up: PgBouncer 1.25.2, libevent 2.1.13-stable (epoll), adns: c-ares 1.34.5", "start", "PgBouncer 1.25.2"},
		{"got SIGINT, shutting down, waiting for all servers connections to be released", "shutdown", "SIGINT"},
		{"got SIGTERM, fast exit", "shutdown", "SIGTERM"},
		{"got SIGHUP, re-reading config", "reload", "SIGHUP"},
		{"RELOAD command issued", "reload", "RELOAD"},
		{"PAUSE 'bench' command issued", "pause", "PAUSE 'bench'"},
		{"RESUME command issued", "resume", "RESUME"},
		{"SHUTDOWN command issued", "shutdown", "SHUTDOWN"},
		{"server connections dropped, exiting", "", ""},
		{"listening on 127.0.0.1:6432", "", ""},
		{"got SIGCHLD", "", ""},
		{"no such command issued", "", ""},
	} {
		kind, detail, ok := lifecycleEvent(c.rest)
		if kind != c.kind || detail != c.detail || ok != (c.kind != "") {
			t.Errorf("lifecycleEvent(%q) = %q, %q, %v; want %q, %q", c.rest, kind, detail, ok, c.kind, c.detail)
		}
	}
}

// TestPgBouncerErrorsAndReasonsKeepTheirTimes checks that each error keeps
// its first and last occurrence, each close reason its own count and
// ages, and each maximum the time of the snapshot that reached it.
func TestPgBouncerErrorsAndReasonsKeepTheirTimes(t *testing.T) {
	base := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	a := NewPgBouncerAnalyzer()
	feed(a, base,
		"WARNING C-0x1: app/u@h:1 pooler error: query_wait_timeout",
		statsLine(5, 5, 1, 1, "10", "10", "900"),
		"LOG C-0x1: app/u@h:1 closing because: query_wait_timeout (age=3s)",
		"LOG C-0x2: app/u@h:2 closing because: client close request (age=40s)",
		"WARNING C-0x3: app/u@h:3 pooler error: query_wait_timeout",
		"LOG C-0x3: app/u@h:3 closing because: query_wait_timeout (age=5s)",
		statsLine(5, 5, 1, 1, "10", "10", "300"),
	)
	m := a.Finalize()
	if len(m.Errors) != 1 || m.Errors[0].Count != 2 || !m.Errors[0].First.Equal(base) || !m.Errors[0].Last.Equal(base.Add(4*time.Minute)) {
		t.Errorf("errors = %+v, want one error seen twice from minute 0 to minute 4", m.Errors)
	}
	want := []PgBouncerCloseReason{{"query_wait_timeout", 2, 8}, {"client close request", 1, 40}}
	if fmt.Sprint(m.SessionReasons) != fmt.Sprint(want) {
		t.Errorf("session reasons = %+v, want %+v", m.SessionReasons, want)
	}
	if !m.MaxWaitTimestamp.Equal(base.Add(time.Minute)) {
		t.Errorf("max wait at %v, want the first snapshot at %v", m.MaxWaitTimestamp, base.Add(time.Minute))
	}
}

// pgbFile builds the analyzer of one file: records from pid, every
// minute from start, with an optional listen line first.
func pgbFile(name, pid, listen string, start time.Time, minutes int) *PgBouncerAnalyzer {
	a := NewPgBouncerAnalyzer()
	msgs := []string{}
	if listen != "" {
		msgs = append(msgs, "LOG listening on "+listen)
	}
	for len(msgs) < minutes {
		msgs = append(msgs, statsLine(1, 1, 1, 1, "1", "1", "1"))
	}
	for i, m := range msgs {
		e := parser.LogEntry{Timestamp: start.Add(time.Duration(i) * time.Minute), Message: m, PID: pid}
		a.Observe(&e)
		a.Process(&e)
	}
	a.SetSource(name, len(msgs), 0)
	return a
}

// TestPgBouncerInstances separates the rule that groups files into
// poolers from its neighbours: grouping by address alone merges two
// hosts, grouping by PID alone merges two containers, and ignoring time
// splits a rotated or restarted pooler.
func TestPgBouncerInstances(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	later := t0.Add(time.Hour)
	for _, c := range []struct {
		name  string
		files []*PgBouncerAnalyzer
		want  []string
	}{
		{"rotation: same PID, one after the other", []*PgBouncerAnalyzer{
			pgbFile("a.log.1", "10", "127.0.0.1:6432", t0, 5),
			pgbFile("a.log", "10", "", later, 5),
		}, []string{"127.0.0.1:6432 [a.log.1 a.log]"}},
		{"restart into a new file: same address, one after the other", []*PgBouncerAnalyzer{
			pgbFile("a.log.1", "10", "127.0.0.1:6432", t0, 5),
			pgbFile("a.log", "11", "127.0.0.1:6432", later, 5),
		}, []string{"127.0.0.1:6432 [a.log.1 a.log]"}},
		{"two hosts on the same address at the same time", []*PgBouncerAnalyzer{
			pgbFile("h1.log", "10", "0.0.0.0:6432", t0, 5),
			pgbFile("h2.log", "20", "0.0.0.0:6432", t0, 5),
		}, []string{"0.0.0.0:6432 (h1.log) [h1.log]", "0.0.0.0:6432 (h2.log) [h2.log]"}},
		{"two containers, both PID 1, at the same time", []*PgBouncerAnalyzer{
			pgbFile("pod1.log", "1", "", t0, 5),
			pgbFile("pod2.log", "1", "", t0, 5),
		}, []string{"pod1.log [pod1.log]", "pod2.log [pod2.log]"}},
		{"nothing shared, one after the other", []*PgBouncerAnalyzer{
			pgbFile("x.log", "10", "", t0, 5),
			pgbFile("y.log", "20", "", later, 5),
		}, []string{"x.log [x.log]", "y.log [y.log]"}},
	} {
		var got []string
		for _, m := range PgBouncerInstances(c.files) {
			got = append(got, fmt.Sprintf("%s %v", m.Name, m.Files))
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPgBouncerWaitIsQueueDepth pins the release that changed what the
// wait field means: 1.23.0, not a neighbour.
func TestPgBouncerWaitIsQueueDepth(t *testing.T) {
	for v, want := range map[string]bool{
		"1.18.0": true, "1.22.1": true, "1.23.0": false, "1.25.2": false, "2.0": false, "": false, "x.y": false,
	} {
		if got := (PgBouncerMetrics{Version: v}).WaitIsQueueDepth(); got != want {
			t.Errorf("WaitIsQueueDepth(%q) = %v, want %v", v, got, want)
		}
	}
}
