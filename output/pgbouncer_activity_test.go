//go:build !js

package output

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// TestRankedKept pins the cut of a ranked table, the PostgreSQL text
// report's and the pooler's, in text and Markdown alike: ten rows at
// most, and from the fifth on the table stops once 80% is covered.
func TestRankedKept(t *testing.T) {
	for _, c := range []struct {
		counts []int
		want   int
	}{
		{[]int{70, 5, 5, 5, 5, 5, 5}, 5},               // 80% at the 3rd row: not before the 5th
		{[]int{20, 20, 20, 10, 10, 10, 10}, 5},         // exactly 80% at the 5th row stops
		{[]int{30, 20, 10, 9, 5, 5, 5, 5, 5, 3, 3}, 7}, // 74% and 79% go on, 84% at the 7th row stops
		{[]int{5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5}, 10},
		{[]int{60, 40}, 2},
	} {
		total := 0
		for _, n := range c.counts {
			total += n
		}
		if got := rankedKept(c.counts, total); got != c.want {
			t.Errorf("rankedKept(%v) = %d, want %d", c.counts, got, c.want)
		}
	}
}

// activitySeries is n snapshots, step apart from t0, each with q queries/s.
func activitySeries(t0 time.Time, n int, step time.Duration, q int) []analysis.PgBouncerStatsPoint {
	out := make([]analysis.PgBouncerStatsPoint, n)
	for i := range out {
		out[i] = analysis.PgBouncerStatsPoint{Time: t0.Add(time.Duration(i+1) * step), Queries: q}
	}
	return out
}

// TestPgbActivityCap pins the row cap on the rows shown, event rows and
// folded idle runs included: one row per snapshot while they fit, else
// the smallest round bucket that does, whole days beyond one.
func TestPgbActivityCap(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	m := analysis.PgBouncerMetrics{StatsSeries: activitySeries(t0, 12, 10*time.Second, 5)}
	if a, _ := pgbBuildActivity(m, 12); a.Bucket != 0 || len(a.Rows) != 12 {
		t.Errorf("12 snapshots in 12 rows: bucket %v, %d rows", a.Bucket, len(a.Rows))
	}
	// A start before the first snapshot is a row of its own: 13 rows.
	m.Events = []analysis.PgBouncerEvent{{Time: t0.Add(-time.Minute), Kind: "start"}}
	if a, _ := pgbBuildActivity(m, 12); a.Bucket == 0 || len(a.Rows) > 12 {
		t.Errorf("12 snapshots and a start in 12 rows: bucket %v, %d rows", a.Bucket, len(a.Rows))
	}
	if a, _ := pgbBuildActivity(m, 13); a.Bucket != 0 || len(a.Rows) != 13 {
		t.Errorf("12 snapshots and a start in 13 rows: bucket %v, %d rows", a.Bucket, len(a.Rows))
	}
	// Twenty days, an hour apart: whole days per row.
	m = analysis.PgBouncerMetrics{StatsSeries: activitySeries(t0, 20*24, time.Hour, 5)}
	a, _ := pgbBuildActivity(m, 12)
	if len(a.Rows) > 12 || a.Bucket%(24*time.Hour) != 0 {
		t.Errorf("twenty days in 12 rows: bucket %v, %d rows", a.Bucket, len(a.Rows))
	}
}

// TestPgbActivityRows pins what a row holds: the worst query time and
// wait of its snapshots, the events of (From, To], idle runs folded, and
// buckets on the log's own clock.
func TestPgbActivityRows(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	m := analysis.PgBouncerMetrics{StatsSeries: []analysis.PgBouncerStatsPoint{
		{Time: at(10), Queries: 4, QueryMicros: 100},
		{Time: at(20), Queries: 0},
		{Time: at(30), Queries: 0},
		{Time: at(40), Queries: 8, QueryMicros: 900, WaitMicros: 200_000},
		{Time: at(50), Queries: 0},
	}, Events: []analysis.PgBouncerEvent{
		{Time: at(40), Kind: "reload"}, // at a snapshot's end: its row
		{Time: at(40).Add(time.Millisecond), Kind: "pause"},
		{Time: at(50), Kind: "resume"},
	}}
	a, _ := pgbBuildActivity(m, 20)
	var got []string
	for _, r := range a.Rows {
		got = append(got, fmt.Sprintf("%s q=%.0f qt=%d w=%d ev=%s idle=%v", r.At.Format("15:04:05"), r.Queries, r.QueryUs, r.WaitUs,
			pgbEventsShort(r.Events), !r.IdleUntil.IsZero()))
	}
	// A row is printed at the start of the period it covers, as a bucket
	// row is.
	want := []string{
		"09:00:00 q=4 qt=100 w=0 ev= idle=false",
		"09:00:10 q=0 qt=0 w=0 ev= idle=true",
		"09:00:30 q=8 qt=900 w=200000 ev=reload idle=false",
		"09:00:40 q=0 qt=0 w=0 ev=pause 10s idle=false",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Runs of 60 s and 15 s merged: the rows read to the second, the
	// title names no period, an event at the start of a row's period is
	// on that row, and the 15 s run starts at its own start.
	m = analysis.PgBouncerMetrics{StatsSeries: []analysis.PgBouncerStatsPoint{
		{Time: at(73), Queries: 5}, {Time: at(133), Queries: 5}, {Time: at(193), Queries: 5},
		{Time: at(213), Queries: 5}, {Time: at(228), Queries: 5},
	}, Events: []analysis.PgBouncerEvent{
		{Time: at(13), Kind: "start"}, {Time: at(194), Kind: "shutdown"}, {Time: at(198), Kind: "start"},
	}}
	a, _ = pgbBuildActivity(m, 20)
	got = got[:0]
	for _, r := range a.Rows {
		got = append(got, r.At.Format(a.clock(m))+" "+pgbEventsShort(r.Events))
	}
	want = []string{"09:00:13 start", "09:01:13 ", "09:02:13 ", "09:03:14 shutdown", "09:03:18 start", "09:03:33 "}
	if strings.Join(got, "|") != strings.Join(want, "|") || a.title() != "per snapshot" {
		t.Errorf("rows = %q, title %q; want %q, per snapshot", got, a.title(), want)
	}

	// A day bucket runs from the log's midnight, not UTC's.
	cest := time.FixedZone("CEST", 2*3600)
	m = analysis.PgBouncerMetrics{StatsSeries: activitySeries(time.Date(2026, 9, 1, 0, 0, 0, 0, cest), 20*24, time.Hour, 5)}
	a, _ = pgbBuildActivity(m, 21)
	if a.Bucket != 24*time.Hour || a.Rows[0].At.Format("15:04") != "00:00" {
		t.Errorf("day buckets start at %s (bucket %v), want the log's midnight", a.Rows[0].At.Format("15:04"), a.Bucket)
	}
}

// TestPgbActivityKeepsItsPeriod: one missing stats line in a run keeps
// the period in the title; a maximum is dated at the start of its snapshot's period, as rows.
func TestPgbActivityKeepsItsPeriod(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	m := analysis.PgBouncerMetrics{}
	for s := 10; s <= 120; s += 10 {
		if s != 60 { // the line of 09:01:00 is missing
			m.StatsSeries = append(m.StatsSeries, analysis.PgBouncerStatsPoint{Time: at(s), Queries: 5, WaitMicros: int64(s)})
		}
	}
	m.MaxWaitMicros, m.MaxWaitTimestamp = 120, at(120)
	a, _ := pgbBuildActivity(m, 20)
	if a.title() != "per snapshot (10s)" {
		t.Errorf("one missing line: title %q, want per snapshot (10s)", a.title())
	}
	if rows := pgbMaxRows(m); !strings.HasSuffix(rows[len(rows)-1][1], "at 09:01:50") {
		t.Errorf("the maximum is dated %q, want at 09:01:50, where its row starts", rows[len(rows)-1][1])
	}

}

// TestPgbEventsShort pins the short event names of the activity table.
func TestPgbEventsShort(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	ev := func(s int, kind string) analysis.PgBouncerEvent {
		return analysis.PgBouncerEvent{Time: t0.Add(time.Duration(s) * time.Second), Kind: kind}
	}
	for _, c := range []struct {
		evs  []analysis.PgBouncerEvent
		want string
	}{
		{[]analysis.PgBouncerEvent{ev(0, "pause"), ev(21, "resume")}, "pause 21s"},
		{[]analysis.PgBouncerEvent{ev(0, "suspend"), ev(3, "resume")}, "suspend 3s"},
		{[]analysis.PgBouncerEvent{ev(0, "shutdown"), ev(1, "start")}, "restart"},
		{[]analysis.PgBouncerEvent{ev(0, "reload"), ev(5, "pause")}, "pause, reload"},
		{[]analysis.PgBouncerEvent{ev(0, "resume"), ev(5, "shutdown")}, "shutdown, resume"},
		// Repeats are counted, and the pooler going down comes first.
		{[]analysis.PgBouncerEvent{ev(0, "reload"), ev(9, "reload"), ev(20, "shutdown"), ev(23, "start"), ev(40, "reload")}, "restart, reload ×3"},
	} {
		if got := pgbEventsShort(c.evs); got != c.want {
			t.Errorf("pgbEventsShort = %q, want %q", got, c.want)
		}
	}
}

// TestPgbMessagesBySeverity pins the message list: an error comes before
// more frequent warnings, and a view lists five by default.
func TestPgbMessagesBySeverity(t *testing.T) {
	m := analysis.PgBouncerMetrics{}
	for i := range 6 {
		m.Errors = append(m.Errors, analysis.PgBouncerError{Level: "WARNING", Message: fmt.Sprintf("w%d", i), Count: 10 - i})
	}
	m.Errors = append(m.Errors, analysis.PgBouncerError{Level: "ERROR", Message: "e", Count: 1})
	list, n := pgbMessages(m, false)
	if n != 5 || len(list) != 7 || list[0].Message != "e" {
		t.Errorf("default view: %d shown of %d, first %q; want 5 of 7, the error first", n, len(list), list[0].Message)
	}
	if _, n := pgbMessages(m, true); n != 7 {
		t.Errorf("detailed view: %d shown, want 7", n)
	}
}
