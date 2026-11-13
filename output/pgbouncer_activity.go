//go:build !js

package output

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alain-L/quellog/analysis"
)

// The pooler's activity over time, shared by the text and Markdown
// reports: one row per stats snapshot, or per round time bucket when the
// snapshots outnumber the rows a view can hold, with the pooler's events
// on the row they fall in. It is the text counterpart of the HTML bar
// charts, and replaces the separate timeline and queueing stretches.

// Rows an activity table holds: the default view stays short, the
// detailed one keeps more of the series.
const (
	pgbActivityRows       = 12
	pgbActivityDetailRows = 20
)

// pgbActivityRow is one row of the activity table. A stats row covers
// [From, To]; an event row (no snapshot) holds events that fall outside
// every stats row.
type pgbActivityRow struct {
	At        time.Time // the time printed: where the row's period starts, or an event's
	From, To  time.Time
	Snapshots int
	Queries   float64 // mean queries per second over the row
	QueryUs   int64   // worst snapshot of the row
	WaitUs    int64   // worst snapshot of the row
	Events    []analysis.PgBouncerEvent
	IdleUntil time.Time // set when the row stands for a run of rows without traffic
}

// pgbActivity is the table and how it was cut.
type pgbActivity struct {
	Period time.Duration // the stats_period the series shows
	Mixed  bool          // runs of the pooler with different periods
	Short  bool          // a run whose period is under a minute
	Bucket time.Duration // 0: one row per snapshot
	Rows   []pgbActivityRow
}

// pgbBuckets are the round bucket sizes the table may group snapshots by.
var pgbBuckets = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
	15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// pgbBuildActivity cuts the stats series into at most maxRows rows,
// counting the rows of the events that fall outside the stats: one row
// per snapshot when they fit, else the smallest round bucket that does.
// It returns false when there are fewer than two snapshots: one point has
// no extent, and the report keeps its timeline and stretches then.
func pgbBuildActivity(m analysis.PgBouncerMetrics, maxRows int) (pgbActivity, bool) {
	period := m.StatsPeriod()
	if len(m.StatsSeries) < 2 || period <= 0 {
		return pgbActivity{}, false
	}
	starts := m.SnapshotStarts()
	a := pgbActivity{Period: period, Short: period < time.Minute}
	runs := pgbRunPeriods(m.StatsSeries, starts)
	for _, r := range runs {
		a.Mixed = a.Mixed || r != runs[0]
		a.Short = a.Short || r < time.Minute
	}
	rowsFor := func(bucket time.Duration) []pgbActivityRow {
		return pgbActivityRowsFor(m, starts, bucket, pgbRowLayout(m, period, bucket, a.Short))
	}
	a.Rows = rowsFor(0)
	if len(a.Rows) <= maxRows {
		return a, true
	}
	for _, size := range pgbBuckets {
		if size <= period {
			continue
		}
		a.Bucket, a.Rows = size, rowsFor(size)
		if len(a.Rows) <= maxRows {
			return a, true
		}
	}
	// A capture of weeks: whole days per row, as many as it takes.
	for size := 2 * 24 * time.Hour; len(a.Rows) > maxRows; size += 24 * time.Hour {
		a.Bucket, a.Rows = size, rowsFor(size)
	}
	return a, true
}

// pgbRunPeriods is the period of each run of the pooler: the median of
// the spans of the snapshots that continue the one before, rounded to the
// second, so a missing stats line or a late one does not change it.
func pgbRunPeriods(series []analysis.PgBouncerStatsPoint, starts []time.Time) (periods []time.Duration) {
	var spans []time.Duration
	flush := func() {
		if len(spans) > 0 {
			slices.Sort(spans)
			periods = append(periods, spans[len(spans)/2])
			spans = spans[:0]
		}
	}
	for i := 1; i < len(series); i++ {
		if !starts[i].Equal(series[i-1].Time) {
			flush()
			continue
		}
		spans = append(spans, series[i].Time.Sub(starts[i]).Round(time.Second))
	}
	flush()
	return periods
}

// pgbActivityRowsFor builds the rows for one bucket size (0: one row per
// snapshot, over the period it averages; else each snapshot in the bucket
// it was logged in), the events on the row they fall in, rows printed
// at the same time (layout) as one, and a run of rows with no traffic
// folded into one.
func pgbActivityRowsFor(m analysis.PgBouncerMetrics, starts []time.Time, bucket time.Duration, layout string) (out []pgbActivityRow) {
	// Buckets fall on the log's own clock: a day runs from its midnight.
	key := func(t time.Time) int64 {
		s := int64(bucket / time.Second)
		_, off := t.Zone()
		return (t.Unix()+int64(off)-1)/s*s - int64(off)
	}
	byKey := map[int64]*pgbActivityRow{}
	var rows []*pgbActivityRow
	for i, p := range m.StatsSeries {
		var r *pgbActivityRow
		if bucket == 0 {
			r = &pgbActivityRow{At: starts[i], From: starts[i], To: p.Time}
			rows = append(rows, r)
		} else {
			k := key(p.Time)
			if r = byKey[k]; r == nil {
				from := time.Unix(k, 0).In(p.Time.Location())
				r = &pgbActivityRow{At: from, From: from, To: from.Add(bucket)}
				byKey[k] = r
				rows = append(rows, r)
			}
		}
		r.Snapshots++
		r.Queries += float64(p.Queries)
		r.QueryUs = max(r.QueryUs, p.QueryMicros)
		r.WaitUs = max(r.WaitUs, p.WaitMicros)
	}
	for _, r := range rows {
		r.Queries /= float64(r.Snapshots)
	}

	// Each event on the row it falls in, or on a row of its own.
	for _, e := range m.Events {
		var home *pgbActivityRow
		for _, r := range rows {
			if r.Snapshots > 0 && e.Time.After(r.From) && !e.Time.After(r.To) {
				home = r
				break
			}
		}
		if home == nil {
			home = &pgbActivityRow{At: e.Time, From: e.Time, To: e.Time}
			rows = append(rows, home)
		}
		home.Events = append(home.Events, e)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.Before(rows[j].At)
		}
		return rows[i].Snapshots == 0 && rows[j].Snapshots > 0
	})

	// Rows printed at the same time are one: an event in the minute a
	// snapshot's period starts, a shutdown and the start after it.
	var merged []*pgbActivityRow
	for _, r := range rows {
		if n := len(merged); n > 0 {
			prev := merged[n-1]
			if prev.At.Format(layout) == r.At.Format(layout) && (prev.Snapshots == 0 || r.Snapshots == 0) {
				if prev.Snapshots == 0 {
					r.Events = append(slices.Clone(prev.Events), r.Events...)
					merged[n-1] = r
				} else {
					prev.Events = append(prev.Events, r.Events...)
				}
				continue
			}
		}
		merged = append(merged, r)
	}
	rows = merged

	// A run of rows with no traffic, no timing and no event says one
	// thing: it is printed once.
	idle := func(r *pgbActivityRow) bool {
		return r.Snapshots > 0 && r.Queries < 0.05 && r.QueryUs == 0 && r.WaitUs == 0 && len(r.Events) == 0
	}
	for i := 0; i < len(rows); i++ {
		j := i
		for j+1 < len(rows) && idle(rows[i]) && idle(rows[j+1]) {
			j++
		}
		r := *rows[i]
		if j > i {
			r.IdleUntil = rows[j].To
		}
		out = append(out, r)
		i = j
	}
	return out
}

// title names how the table is cut: "per snapshot (10s)", "by minute";
// no period when the pooler's runs have different ones.
func (a pgbActivity) title() string {
	switch a.Bucket {
	case 0:
		if a.Mixed {
			return "per snapshot"
		}
		return "per snapshot (" + formatSessionDuration(a.Period) + ")"
	case time.Minute:
		return "by minute"
	case time.Hour:
		return "by hour"
	case 24 * time.Hour:
		return "by day"
	}
	if a.Bucket%(24*time.Hour) == 0 {
		return fmt.Sprintf("per %d days", a.Bucket/(24*time.Hour))
	}
	return "per " + formatSessionDuration(a.Bucket)
}

// clock is the layout of the time column (pgbRowLayout).
func (a pgbActivity) clock(m analysis.PgBouncerMetrics) string {
	return pgbRowLayout(m, a.Period, a.Bucket, a.Short)
}

// pgbRowLayout is the layout of the activity table's time column: seconds
// when a row is shorter than a minute -- a row per snapshot spans the
// period of its run, and short says a run's is -- and the date when the
// window spans more than one day.
func pgbRowLayout(m analysis.PgBouncerMetrics, period, bucket time.Duration, short bool) string {
	layout := "15:04"
	if max(bucket, period) < time.Minute || bucket == 0 && short {
		layout = "15:04:05"
	}
	if pgbClock(m) == time.DateTime {
		layout = "01-02 " + layout
	}
	return layout
}

// waitHeader names the wait column in the section's unit.
func pgbWaitHeader(m analysis.PgBouncerMetrics) string {
	if m.WaitIsQueueDepth() {
		return "queued"
	}
	return "wait"
}

// pgbRowWait is a row's worst wait, empty when it rounds to nothing.
func pgbRowWait(m analysis.PgBouncerMetrics, us int64) string {
	if us <= 0 {
		return ""
	}
	if v := pgbWait(m, us); v != "0.0 clients" && v != "0 µs" {
		return v
	}
	return ""
}

// pgbRowQuery is a row's worst query time, empty when nothing was timed.
func pgbRowQuery(us int64) string {
	if us <= 0 {
		return ""
	}
	return microsI(us)
}

// pgbRate is a short per-second rate for the table: "15.4k/s", "58/s".
func pgbRate(v float64) string {
	switch {
	case v == 0:
		return "0/s"
	case v >= 1000:
		return fmt.Sprintf("%.1fk/s", v/1000)
	case v >= 100:
		return fmt.Sprintf("%.0f/s", v)
	}
	return fmt.Sprintf("%.1f/s", v)
}

// pgbEventsShort names a row's events in a few words: a pause and its
// resume, or a shutdown and the next start, on the same row read as one
// event; a repeated one is counted ("reload ×2"). Restarts, shutdowns and
// starts come first, then pauses, then the rest, so the cut a narrow
// column makes never hides the pooler going down. The exact times are in
// the JSON export's events.
func pgbEventsShort(evs []analysis.PgBouncerEvent) string {
	var labels []string
	for i := 0; i < len(evs); i++ {
		e := evs[i]
		if i+1 < len(evs) {
			next := evs[i+1]
			if (e.Kind == "pause" || e.Kind == "suspend") && next.Kind == "resume" {
				labels = append(labels, e.Kind+" "+formatSessionDuration(next.Time.Sub(e.Time)))
				i++
				continue
			}
			if e.Kind == "shutdown" && next.Kind == "start" {
				labels = append(labels, "restart")
				i++
				continue
			}
		}
		labels = append(labels, e.Kind)
	}
	var order []string
	count := map[string]int{}
	for _, l := range labels {
		if count[l] == 0 {
			order = append(order, l)
		}
		count[l]++
	}
	rank := func(l string) int {
		switch kind, _, _ := strings.Cut(l, " "); kind {
		case "restart", "shutdown", "start":
			return 0
		case "pause", "suspend", "resume":
			return 1
		}
		return 2
	}
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i]) < rank(order[j]) })
	out := make([]string, len(order))
	for i, l := range order {
		out[i] = l
		if n := count[l]; n > 1 {
			out[i] = fmt.Sprintf("%s ×%d", l, n)
		}
	}
	return strings.Join(out, ", ")
}

// pgbActivityNote says what the timing columns hold.
func pgbActivityNote(m analysis.PgBouncerMetrics) string {
	if m.WaitIsQueueDepth() {
		return "query: query time, queued: clients queued, worst snapshot of each row"
	}
	return "query: query time, wait: pool wait, worst snapshot of each row"
}

// pgbMaxRows are the highest snapshot averages, two that share a time on
// one line when it fits; an empty label continues the line above.
//
// A maximum is dated, as the activity table's rows are, at the start of
// the period its snapshot averages; the JSON export keeps the stats
// line's own time.
func pgbMaxRows(m analysis.PgBouncerMetrics) [][2]string {
	clock := pgbClock(m)
	startOf := map[int64]time.Time{}
	for i, start := range m.SnapshotStarts() {
		startOf[m.StatsSeries[i].Time.UnixNano()] = start
	}
	at := func(t time.Time, clock string) string {
		if s, ok := startOf[t.UnixNano()]; ok {
			t = s
		}
		return pgbAt(t, clock)
	}
	var rows [][2]string
	pair := func(label, a, b string, ta, tb time.Time) {
		if ta.Equal(tb) {
			if v := a + ", " + b + at(ta, clock); utf8.RuneCountInString(v) <= pgbMaxValueWidth {
				rows = append(rows, [2]string{label, v})
				return
			}
		}
		rows = append(rows, [2]string{label, a + at(ta, clock)}, [2]string{"", b + at(tb, clock)})
	}
	pair("Throughput", fmt.Sprintf("%d transactions/s", m.MaxXactsPerSec), fmt.Sprintf("%d queries/s", m.MaxQueriesPerSec),
		m.MaxXactsTimestamp, m.MaxQueriesTimestamp)
	pair("Network", bytesPerSec(float64(m.MaxInBytesPerSec))+" in", bytesPerSec(float64(m.MaxOutBytesPerSec))+" out",
		m.MaxInTimestamp, m.MaxOutTimestamp)
	pair("Time", "transaction "+microsI(m.MaxXactMicros), "query "+microsI(m.MaxQueryMicros),
		m.MaxXactTimestamp, m.MaxQueryTimestamp)
	if m.WaitIsQueueDepth() {
		rows = append(rows, [2]string{"Clients queued", queueDepth(float64(m.MaxWaitMicros)) + at(m.MaxWaitTimestamp, clock)})
	} else {
		rows = append(rows, [2]string{"Pool wait", microsI(m.MaxWaitMicros) + at(m.MaxWaitTimestamp, clock)})
	}
	return rows
}

// pgbMaxValueWidth is what a value of the max block may take: 80 columns,
// less its indent and its 23-wide label.
const pgbMaxValueWidth = 80 - 4 - 23 - 3

// pgbMessagesTitle counts the messages by level: "0 errors, 33 warnings".
func pgbMessagesTitle(m analysis.PgBouncerMetrics) string {
	errs, warns := 0, 0
	for _, e := range m.Errors {
		if e.Level == "WARNING" {
			warns += e.Count
		} else {
			errs += e.Count
		}
	}
	return "Messages: " + pgbrPlural(errs, "error") + ", " + pgbrPlural(warns, "warning")
}

// pgbMessages are the messages by level, the most severe first, the
// most frequent first within a level, and how many a view lists: five by
// default, pgbListMax in the detailed view, the rest declared after them.
// An error is not hidden behind more frequent warnings.
func pgbMessages(m analysis.PgBouncerMetrics, detail bool) (list []analysis.PgBouncerError, shown int) {
	for _, l := range pgbLevels(m.Errors) {
		list = append(list, l.errs...)
	}
	shown = 5
	if detail {
		shown = pgbListMax
	}
	return list, min(shown, len(list))
}

// pgbFit cuts s to n runes, the last one an ellipsis.
func pgbFit(s string, n int) string {
	if n < 1 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
