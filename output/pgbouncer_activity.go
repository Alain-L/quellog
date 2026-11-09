//go:build !js

package output

import (
	"fmt"
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
	At        time.Time // the time printed: a snapshot's own, a bucket's start, an event's
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
	Bucket time.Duration // 0: one row per snapshot
	Rows   []pgbActivityRow
}

// pgbBuckets are the round bucket sizes the table may group snapshots by.
var pgbBuckets = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
	15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// pgbBuildActivity cuts the stats series into at most maxRows rows. It
// returns false when there are fewer than two snapshots: one point has no
// extent, and the report keeps its timeline and stretches then.
func pgbBuildActivity(m analysis.PgBouncerMetrics, maxRows int) (pgbActivity, bool) {
	pts := m.StatsSeries
	period := m.StatsPeriod()
	if len(pts) < 2 || period <= 0 {
		return pgbActivity{}, false
	}
	a := pgbActivity{Period: period}
	key := func(t time.Time, size time.Duration) int64 {
		s := int64(size / time.Second)
		return (t.Unix() - 1) / s * s
	}
	if len(pts) > maxRows {
		a.Bucket = pgbBuckets[len(pgbBuckets)-1]
		for _, size := range pgbBuckets {
			if size <= period {
				continue
			}
			seen := map[int64]bool{}
			for _, p := range pts {
				seen[key(p.Time, size)] = true
			}
			if len(seen) <= maxRows {
				a.Bucket = size
				break
			}
		}
	}

	byKey := map[int64]*pgbActivityRow{}
	var rows []*pgbActivityRow
	for _, p := range pts {
		var r *pgbActivityRow
		if a.Bucket == 0 {
			r = &pgbActivityRow{At: p.Time, From: p.Time.Add(-period), To: p.Time}
			rows = append(rows, r)
		} else {
			k := key(p.Time, a.Bucket)
			if r = byKey[k]; r == nil {
				from := time.Unix(k, 0).In(p.Time.Location())
				r = &pgbActivityRow{At: from, From: from, To: from.Add(a.Bucket)}
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
		a.Rows = append(a.Rows, r)
		i = j
	}
	return a, true
}

// title names how the table is cut: "per snapshot (10s)", "by minute".
func (a pgbActivity) title() string {
	switch a.Bucket {
	case 0:
		return "per snapshot (" + formatSessionDuration(a.Period) + ")"
	case time.Minute:
		return "by minute"
	case time.Hour:
		return "by hour"
	case 24 * time.Hour:
		return "by day"
	}
	return "per " + formatSessionDuration(a.Bucket)
}

// clock is the layout of the time column: seconds when a row is shorter
// than a minute, and the date when the window spans more than one day.
func (a pgbActivity) clock(m analysis.PgBouncerMetrics) string {
	layout := "15:04"
	if step := max(a.Bucket, a.Period); step < time.Minute {
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
// event. The exact times are in the JSON export's events.
func pgbEventsShort(evs []analysis.PgBouncerEvent) string {
	var out []string
	for i := 0; i < len(evs); i++ {
		e := evs[i]
		if i+1 < len(evs) {
			next := evs[i+1]
			if (e.Kind == "pause" || e.Kind == "suspend") && next.Kind == "resume" {
				out = append(out, e.Kind+" "+formatSessionDuration(next.Time.Sub(e.Time)))
				i++
				continue
			}
			if e.Kind == "shutdown" && next.Kind == "start" {
				out = append(out, "restart")
				i++
				continue
			}
		}
		out = append(out, e.Kind)
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
func pgbMaxRows(m analysis.PgBouncerMetrics) [][2]string {
	clock := pgbClock(m)
	var rows [][2]string
	pair := func(label, a, b string, ta, tb time.Time) {
		if ta.Equal(tb) {
			if v := a + ", " + b + pgbAt(ta, clock); utf8.RuneCountInString(v) <= pgbMaxValueWidth {
				rows = append(rows, [2]string{label, v})
				return
			}
		}
		rows = append(rows, [2]string{label, a + pgbAt(ta, clock)}, [2]string{"", b + pgbAt(tb, clock)})
	}
	pair("Throughput", fmt.Sprintf("%d transactions/s", m.MaxXactsPerSec), fmt.Sprintf("%d queries/s", m.MaxQueriesPerSec),
		m.MaxXactsTimestamp, m.MaxQueriesTimestamp)
	pair("Network", bytesPerSec(float64(m.MaxInBytesPerSec))+" in", bytesPerSec(float64(m.MaxOutBytesPerSec))+" out",
		m.MaxInTimestamp, m.MaxOutTimestamp)
	pair("Time", "transaction "+microsI(m.MaxXactMicros), "query "+microsI(m.MaxQueryMicros),
		m.MaxXactTimestamp, m.MaxQueryTimestamp)
	if m.WaitIsQueueDepth() {
		rows = append(rows, [2]string{"Clients queued", queueDepth(float64(m.MaxWaitMicros)) + pgbAt(m.MaxWaitTimestamp, clock)})
	} else {
		rows = append(rows, [2]string{"Pool wait", microsI(m.MaxWaitMicros) + pgbAt(m.MaxWaitTimestamp, clock)})
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

// pgbMessages are the messages a view lists: the five most frequent by
// default, every one by level otherwise.
func pgbMessages(m analysis.PgBouncerMetrics, detail bool) []analysis.PgBouncerError {
	if !detail {
		return m.Errors[:min(5, len(m.Errors))]
	}
	var out []analysis.PgBouncerError
	for _, l := range pgbLevels(m.Errors) {
		out = append(out, l.errs...)
	}
	return out
}

// pgbFit cuts s to n runes, the last one an ellipsis.
func pgbFit(s string, n int) string {
	if n < 1 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
