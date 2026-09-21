package analysis

import (
	"fmt"
	"math"
	"strconv"
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
