package analysis

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// connectionRunSingle processes every entry through one analyzer.
func connectionRunSingle(entries []parser.LogEntry) ConnectionMetrics {
	a := NewConnectionAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// connectionRunSharded shards entries by PID across n analyzers, processes each
// shard, folds shards 1..n-1 into shard 0 via Merge, then finalizes shard 0.
// Returns the metrics and the number of shards that actually received entries.
func connectionRunSharded(entries []parser.LogEntry, n int) (ConnectionMetrics, int) {
	shards := make([]*ConnectionAnalyzer, n)
	for i := range shards {
		shards[i] = NewConnectionAnalyzer()
	}
	seen := make(map[int]struct{})
	for i := range entries {
		s := shardForPID(entries[i].PID, n)
		seen[s] = struct{}{}
		shards[s].Process(&entries[i])
	}
	for i := 1; i < n; i++ {
		shards[0].Merge(shards[i])
	}
	return shards[0].Finalize(), len(seen)
}

// connectionAssertParity compares two ConnectionMetrics for shard-merge parity.
//
// Most fields are bit-exact and compared directly. Two categories need care:
//
//   - Accumulated duration sums (TotalSessionTime, SessionCumulated and the
//     Sum inside every StreamingDurationStats) are summed in a different order
//     by the sharded path; float/duration addition is not associative, so they
//     are compared with a tolerance.
//   - The P² median estimate inside each StreamingDurationStats is a streaming-
//     quantile sketch whose markers depend on the full ordered history; two
//     partial sketches over disjoint value populations cannot be combined into
//     the single-pass sketch (see the NOT-shard-mergeable note on Merge). The
//     per-dimension maps are therefore compared field-by-field via .Stats()
//     with Count/Min/Max/Avg asserted and Median EXCLUDED.
//
// The ordered per-occurrence series (received timestamps, session events) are
// compared as MULTISETS (sorted before comparison): the raw single-pass log
// stream is not strictly monotonic — real captures interleave a few lines out
// of timestamp order — so the chronological merge-sort cannot reproduce the
// exact single-pass append order. What parity guarantees is that no event is
// dropped or duplicated and the merge is a valid time-ordered combination,
// which sorted-sequence equality proves. The peak (computed by a sweep-line
// that sorts internally) is order-independent and asserted exactly.
//
// NOTE ON THE PEAK: PeakConcurrentSessions / PeakConcurrentTimestamp are NOT
// stored per-shard analyzer state; Finalize recomputes them with a sweep-line
// over the *merged* sessionChunks (plus flushed orphans). Because Merge unions
// every session into shard 0 before Finalize, that sweep-line sees the full
// global session set and the peak is reproduced exactly — hence it IS asserted
// here, with no exclusion.
func connectionAssertParity(t *testing.T, label string, want, got ConnectionMetrics) {
	t.Helper()

	if want.ConnectionReceivedCount != got.ConnectionReceivedCount {
		t.Errorf("%s: ConnectionReceivedCount: want=%d got=%d", label, want.ConnectionReceivedCount, got.ConnectionReceivedCount)
	}
	if want.DisconnectionCount != got.DisconnectionCount {
		t.Errorf("%s: DisconnectionCount: want=%d got=%d", label, want.DisconnectionCount, got.DisconnectionCount)
	}
	if !connDurationAlmostEqual(want.TotalSessionTime, got.TotalSessionTime) {
		t.Errorf("%s: TotalSessionTime: want=%v got=%v", label, want.TotalSessionTime, got.TotalSessionTime)
	}
	if !connDurationAlmostEqual(want.SessionCumulated, got.SessionCumulated) {
		t.Errorf("%s: SessionCumulated: want=%v got=%v", label, want.SessionCumulated, got.SessionCumulated)
	}

	// Top-level session stats: Count/Min/Max exact, Avg and Median tolerant.
	connAssertDurationStats(t, label+" SessionStats", want.SessionStats, got.SessionStats)

	// Peak: must be reproduced exactly (recomputed from merged sessions).
	if want.PeakConcurrentSessions != got.PeakConcurrentSessions {
		t.Errorf("%s: PeakConcurrentSessions: want=%d got=%d", label, want.PeakConcurrentSessions, got.PeakConcurrentSessions)
	}
	if !want.PeakConcurrentTimestamp.Equal(got.PeakConcurrentTimestamp) {
		t.Errorf("%s: PeakConcurrentTimestamp: want=%v got=%v", label, want.PeakConcurrentTimestamp, got.PeakConcurrentTimestamp)
	}

	// Session-duration distribution: bit-exact (integer counts).
	if !reflect.DeepEqual(want.SessionDistribution, got.SessionDistribution) {
		t.Errorf("%s: SessionDistribution mismatch:\n want=%v\n got =%v", label, want.SessionDistribution, got.SessionDistribution)
	}

	// Per-dimension streaming stats maps.
	connAssertStatsMap(t, label+" SessionsByUser", want.SessionsByUser, got.SessionsByUser)
	connAssertStatsMap(t, label+" SessionsByDatabase", want.SessionsByDatabase, got.SessionsByDatabase)
	connAssertStatsMap(t, label+" SessionsByHost", want.SessionsByHost, got.SessionsByHost)

	// Ordered iterators compared as multisets (sorted): the single-pass stream
	// is not strictly monotonic, so the merge-sorted order need not equal the
	// single-pass append order — only the set of events must be identical.
	connAssertConnections(t, label, want, got)
	connAssertSessionEvents(t, label, want, got)
}

func connAssertConnections(t *testing.T, label string, want, got ConnectionMetrics) {
	t.Helper()
	var w, g []int64
	want.IterateConnections(func(tm time.Time) bool { w = append(w, tm.UnixNano()); return true })
	got.IterateConnections(func(tm time.Time) bool { g = append(g, tm.UnixNano()); return true })
	if len(w) != len(g) {
		t.Errorf("%s: connections count: want=%d got=%d", label, len(w), len(g))
		return
	}
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
	for i := range w {
		if w[i] != g[i] {
			t.Errorf("%s: connection multiset diff at sorted[%d]: want=%v got=%v",
				label, i, time.Unix(0, w[i]).UTC(), time.Unix(0, g[i]).UTC())
			return
		}
	}
}

func connAssertSessionEvents(t *testing.T, label string, want, got ConnectionMetrics) {
	t.Helper()
	type se struct{ start, end int64 }
	collect := func(m ConnectionMetrics) []se {
		var out []se
		m.IterateSessionEvents(func(s SessionEvent) bool {
			out = append(out, se{s.StartTime.UnixNano(), s.EndTime.UnixNano()})
			return true
		})
		sort.Slice(out, func(i, j int) bool {
			if out[i].end != out[j].end {
				return out[i].end < out[j].end
			}
			return out[i].start < out[j].start
		})
		return out
	}
	w, g := collect(want), collect(got)
	if len(w) != len(g) {
		t.Errorf("%s: session events count: want=%d got=%d", label, len(w), len(g))
		return
	}
	for i := range w {
		if w[i] != g[i] {
			t.Errorf("%s: session multiset diff at sorted[%d]: want=%v..%v got=%v..%v", label, i,
				time.Unix(0, w[i].start).UTC(), time.Unix(0, w[i].end).UTC(),
				time.Unix(0, g[i].start).UTC(), time.Unix(0, g[i].end).UTC())
			return
		}
	}
}

func connAssertStatsMap(t *testing.T, label string, want, got map[string]*StreamingDurationStats) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s: key count: want=%d got=%d (want keys=%v got keys=%v)",
			label, len(want), len(got), connMapKeys(want), connMapKeys(got))
		return
	}
	for k, ws := range want {
		gs, ok := got[k]
		if !ok {
			t.Errorf("%s: key %q missing in sharded result", label, k)
			continue
		}
		connAssertDurationStats(t, label+" ["+k+"]", ws.Stats(), gs.Stats())
	}
}

// connAssertDurationStats compares two DurationStats: Count/Min/Max exact,
// Avg and Median under tolerance (Avg derives from an accumulated Sum; Median
// is the P² estimate).
func connAssertDurationStats(t *testing.T, label string, want, got DurationStats) {
	t.Helper()
	if want.Count != got.Count {
		t.Errorf("%s: Count: want=%d got=%d", label, want.Count, got.Count)
	}
	if want.Min != got.Min {
		t.Errorf("%s: Min: want=%v got=%v", label, want.Min, got.Min)
	}
	if want.Max != got.Max {
		t.Errorf("%s: Max: want=%v got=%v", label, want.Max, got.Max)
	}
	if !connDurationAlmostEqual(want.Avg, got.Avg) {
		t.Errorf("%s: Avg: want=%v got=%v", label, want.Avg, got.Avg)
	}
	// Median is the P² streaming-quantile estimate — NOT shard-mergeable
	// (see Merge's doc-comment). Intentionally not asserted.
}

// connDurationAlmostEqual compares two durations with the shared relative
// float tolerance used for accumulated sums.
func connDurationAlmostEqual(a, b time.Duration) bool {
	return almostEqual(float64(a), float64(b))
}

func connMapKeys(m map[string]*StreamingDurationStats) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestConnectionShardMergeParity_Synthetic exercises the hard cases: many
// backends interleaved (so PIDs spread across shards), full connect→disconnect
// lifecycles with parseable session_time (feeding the duration stats and the
// per-user/db/host maps), bare disconnects (session-time-less), and some
// connections still open at end of log (orphans flushed at Finalize).
func TestConnectionShardMergeParity_Synthetic(t *testing.T) {
	base := time.Date(2025, 11, 30, 21, 0, 0, 0, time.UTC)

	type line struct {
		pid string
		msg string
	}
	var lines []line

	pids := []string{"101", "102", "103", "104", "105", "106", "107", "108", "109", "110", "111", "112"}
	users := []string{"alice", "bob", "carol"}
	dbs := []string{"app", "reports"}
	hosts := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}

	// Round 1: every backend connects.
	for _, pid := range pids {
		lines = append(lines, line{pid, "LOG:  connection received: host=10.0.0.1 port=5432"})
	}
	// Round 2: most backends disconnect with a parseable session time, varied
	// durations so min/max/median/distribution are non-degenerate. Leave the
	// last two PIDs connected (orphans).
	for i, pid := range pids {
		if i >= len(pids)-2 {
			continue // keep these two open
		}
		secs := (i%5)*7 + 3 // 3,10,17,24,31,3,...
		dur := connFmtSessionTime(time.Duration(secs) * time.Second)
		u := users[i%len(users)]
		db := dbs[i%len(dbs)]
		h := hosts[i%len(hosts)]
		lines = append(lines, line{pid,
			"LOG:  disconnection: session time: " + dur +
				" user=" + u + " database=" + db + " host=" + h})
	}
	// A bare disconnect with no parseable session time, for a PID that did
	// connect — exercises the receivedAt-fallback session path.
	lines = append(lines, line{"103", "LOG:  disconnection: connection closed unexpectedly"})
	// One long session to push the "> 5h" bucket and stretch the peak window.
	lines = append(lines, line{"201", "LOG:  connection received: host=10.0.0.9 port=6000"})
	lines = append(lines, line{"201",
		"LOG:  disconnection: session time: 6:00:00.000 user=dave database=app host=10.0.0.9"})

	entries := make([]parser.LogEntry, 0, len(lines))
	for i, l := range lines {
		full := "[" + l.pid + "] " + l.msg
		ts := base.Add(time.Duration(i) * time.Minute)
		entries = append(entries, parser.NewLogEntry(ts, full, false))
	}
	for i := range entries {
		if entries[i].PID == "" {
			t.Fatalf("entry %d: PID not extracted from %q", i, entries[i].Message)
		}
	}

	single := connectionRunSingle(entries)
	if single.ConnectionReceivedCount == 0 || single.DisconnectionCount == 0 {
		t.Fatalf("degenerate fixture: %+v", single)
	}
	if single.PeakConcurrentSessions < 2 {
		t.Fatalf("expected a concurrent-session peak >= 2, got %d", single.PeakConcurrentSessions)
	}

	for _, n := range []int{2, 3, 4} {
		sharded, spread := connectionRunSharded(entries, n)
		if spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", n, spread)
		}
		connectionAssertParity(t, "synthetic n="+itoa(n), single, sharded)
	}
}

// TestConnectionShardMergeParity_Fixtures proves parity on real parsed corpora
// across several shard counts. Skips gracefully when no fixture carries
// connection lines.
func TestConnectionShardMergeParity_Fixtures(t *testing.T) {
	candidates := []string{
		"../test/testdata/stderr.log",
		"../test/testdata/syslog.log",
		"../test/testdata/test_summary.log",
		"../test/testdata/sql_extended.log",
	}

	ranAny := false
	for _, path := range candidates {
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := connectionRunSingle(entries)
		if single.ConnectionReceivedCount == 0 && single.DisconnectionCount == 0 {
			continue
		}
		ranAny = true

		spread := false
		for _, n := range []int{2, 4, 8} {
			sharded, sp := connectionRunSharded(entries, n)
			if sp > 1 {
				spread = true
			}
			connectionAssertParity(t, path+" n="+itoa(n), single, sharded)
		}
		_ = spread
	}

	if !ranAny {
		t.Skip("no fixture with connection metrics found")
	}
}

// connFmtSessionTime renders a duration in PostgreSQL's "H:MM:SS.mmm" session
// time format, the shape extractSessionTime parses back.
func connFmtSessionTime(d time.Duration) string {
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	d -= s * time.Second
	ms := d / time.Millisecond
	return itoa(int(h)) + ":" + conn2(int(m)) + ":" + conn2(int(s)) + "." + conn3(int(ms))
}

func conn2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func conn3(n int) string {
	switch {
	case n < 10:
		return "00" + itoa(n)
	case n < 100:
		return "0" + itoa(n)
	default:
		return itoa(n)
	}
}
