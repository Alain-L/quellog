//go:build !js

package output

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alain-L/quellog/analysis"
)

// sessionBucketOrder is the fixed display order for the session-time
// histogram, matching the PostgreSQL connection report's buckets.
var sessionBucketOrder = []string{"< 1s", "1s - 1min", "1min - 30min", "30min - 2h", "2h - 5h", "> 5h"}

// PrintPgBouncer renders one report per pooler, each titled by the
// pooler's name when there are several. The default view is synthetic;
// detail (--pgbouncer-detail, --full) adds every breakdown.
func PrintPgBouncer(poolers []analysis.PgBouncerMetrics, detail bool) {
	for _, m := range poolers {
		title := "PGBOUNCER"
		if len(poolers) > 1 {
			title += " " + m.Name
		}
		printPgBouncer(m, title, detail)
	}
}

// pgbDetailHint points the default view to the detailed one.
const pgbDetailHint = "Details: --pgbouncer-detail"

// printPgBouncer renders one pooler's report in 80 columns: a block of
// "label : value" rows, the warnings and errors, and the activity table;
// the detailed view adds the highest snapshot averages, every message
// with its --event-detail ID, and the breakdowns.
func printPgBouncer(m analysis.PgBouncerMetrics, title string, detail bool) {
	clock := pgbClock(m)
	fmt.Println(ansiBold + "\n" + title + "\n" + ansiReset)
	for _, r := range pgbSummaryRows(m, detail) {
		for i, part := range pgbWrap(r[1], pgbSummaryValueWidth) {
			if i == 0 {
				fmt.Printf("  %-25s : %s\n", r[0], part)
			} else {
				fmt.Printf("  %-25s   %s\n", "", part)
			}
		}
	}

	// With one snapshot the highest is the average: nothing to add.
	if detail && m.StatsSampleCount > 1 {
		fmt.Println("\n  Max snapshot averages")
		for _, r := range pgbMaxRows(m) {
			if r[0] == "" {
				fmt.Printf("    %-23s   %s\n", "", r[1])
			} else {
				fmt.Printf("    %-23s : %s\n", r[0], r[1])
			}
		}
	}

	printPgbMessages(m, detail, clock)

	rows := pgbActivityRows
	if detail {
		rows = pgbActivityDetailRows
	}
	if a, ok := pgbBuildActivity(m, rows); ok {
		printPgbActivity(m, a)
	} else if detail {
		// Without a table to carry them, the events and the queueing
		// stretches keep their own lists.
		if len(m.Events) > 0 {
			printServerTimeline(pgbTimeline(m.Events))
		}
		printPgbEpisodes(m, clock)
	}

	if !detail {
		fmt.Println("\n  " + ansiMutedItalic + pgbDetailHint + ansiReset)
		return
	}
	if m.Sessions > 0 {
		// The histogram's empty buckets say nothing the others do not.
		var order []string
		for _, b := range sessionBucketOrder {
			if m.SessionDistribution[b] > 0 {
				order = append(order, b)
			}
		}
		fmt.Println()
		PrintHistogram(m.SessionDistribution, "Session time", "", 1, order)
		printPgbCloseReasons(m.SessionReasons, m.Sessions)
	}
	if m.LoginAttempts > 0 {
		if len(m.ConnectionsPerDatabase) > 0 {
			printRankedCountTable("Connections per database:", m.ConnectionsPerDatabase, m.LoginAttempts, true)
		}
		if len(m.ConnectionsPerUser) > 0 {
			printRankedCountTable("Connections per user:", m.ConnectionsPerUser, m.LoginAttempts, true)
		}
		if len(m.ConnectionsPerHost) > 0 {
			printRankedCountTable("Connections per host:", m.ConnectionsPerHost, m.LoginAttempts, true)
		}
	}
}

// printPgbMessages lists the warnings and errors under their totals: the
// most frequent by default, every one with its --event-detail ID in the
// detailed view. The message column narrows, and a message is cut, only
// as far as 80 columns require; its ID gives it in full.
func printPgbMessages(m analysis.PgBouncerMetrics, detail bool, clock string) {
	fmt.Println("\n  " + pgbMessagesTitle(m))
	list := pgbMessages(m, detail)
	shown := list[:min(len(list), pgbListMax)]
	if len(shown) == 0 {
		return
	}
	mw, cw, ww, idw := 0, 0, 0, 0
	for _, e := range shown {
		mw = max(mw, utf8.RuneCountInString(e.Message))
		cw = max(cw, len(strconv.Itoa(e.Count)))
		ww = max(ww, utf8.RuneCountInString(pgbWhen(e, clock)))
		if detail {
			idw = max(idw, len(e.ID)+2)
		}
	}
	mw = min(mw, 80-4-idw-2-cw-2-ww)
	for _, e := range shown {
		id := ""
		if detail {
			id = ansiMutedItalic + e.ID + ansiReset + "  "
		}
		fmt.Printf("    %s%s  %*d  %s\n", id, pgbPad(pgbFit(e.Message, mw), mw), cw, e.Count, pgbWhen(e, clock))
	}
	if len(list) > len(shown) {
		fmt.Printf("    \u2026 and %d more\n", len(list)-len(shown))
	}
}

// pgbWrap breaks a value into lines of at most width runes, between
// words, so a summary row that explains a zero stays within 80 columns.
// The setting it names ("check log_connections = 1 in pgbouncer.ini") is
// never split.
func pgbWrap(s string, width int) []string {
	words := strings.Fields(s)
	if before, check, ok := strings.Cut(s, "check "); ok {
		words = append(strings.Fields(before), "check "+check)
	}
	var lines []string
	line := ""
	for _, w := range words {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

// pgbPad pads s with spaces to n runes.
func pgbPad(s string, n int) string {
	if d := n - utf8.RuneCountInString(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// printPgbActivity prints the activity table: the queries per second as
// a bar, the worst query time and wait of the row, and the events.
func printPgbActivity(m analysis.PgBouncerMetrics, a pgbActivity) {
	const barWidth = 20
	top := 0.0
	for _, r := range a.Rows {
		top = max(top, r.Queries)
	}
	unit := 1.0
	for _, mult := range []float64{1, 10, 100, 1000, 10000, 100000, 1000000} {
		found := false
		for _, u := range []float64{1, 2, 5} {
			if top/(u*mult) <= barWidth {
				unit, found = u*mult, true
				break
			}
		}
		if found {
			break
		}
	}
	unitText := fmt.Sprintf("%g", unit)
	if unit >= 1000 {
		unitText = fmt.Sprintf("%gk", unit/1000)
	}
	layout := a.clock(m)
	tw := len(layout)
	ww := len("22.9 clients")
	if !m.WaitIsQueueDepth() {
		ww = len("192.7 ms")
	}

	fmt.Printf("\n  Activity %s | \u25a0 = %s queries/s\n\n", a.title(), unitText)
	fmt.Printf("  %-*s  %-*s%8s  %-9s  %-*s  %s\n", tw, "", barWidth, "", "queries", "query", ww, pgbWaitHeader(m), "events")
	for _, r := range a.Rows {
		at := r.At.Format(layout)
		var line string
		switch {
		case !r.IdleUntil.IsZero():
			line = fmt.Sprintf("  %-*s  no traffic until %s", tw, at, r.IdleUntil.Format(layout))
		case r.Snapshots == 0:
			line = fmt.Sprintf("  %-*s  %-*s%8s  %-9s  %-*s  %s", tw, at, barWidth, "", "", "", ww, "", pgbEventsShort(r.Events))
		default:
			n := int(r.Queries/unit + 0.5)
			if n == 0 && r.Queries > 0 {
				n = 1
			}
			line = fmt.Sprintf("  %-*s  %s%8s  %-9s  %-*s  %s", tw, at, pgbPad(strings.Repeat("\u25a0", n), barWidth), pgbRate(r.Queries),
				pgbRowQuery(r.QueryUs), ww, pgbRowWait(m, r.WaitUs), pgbEventsShort(r.Events))
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	fmt.Println("  " + ansiMutedItalic + pgbActivityNote(m) + ansiReset)
}

// pgbSummaryValueWidth is what a summary value may take in 80 columns.
const pgbSummaryValueWidth = 80 - 2 - 25 - 3

// pgbSummaryRows are the "label : value" rows of a pooler's report, the
// same in text and Markdown, one per subject: the values that go together
// share a row, and part again when they would not fit 80 columns or when
// a zero carries the setting it depends on. Detail adds the source, the
// network and how the sessions ended.
func pgbSummaryRows(m analysis.PgBouncerMetrics, detail bool) [][2]string {
	var rows [][2]string
	row := func(label, value string) { rows = append(rows, [2]string{label, value}) }
	fits := func(v string) bool { return utf8.RuneCountInString(v) <= pgbSummaryValueWidth }

	pooler := m.Name
	if m.Version != "" {
		pooler += ", PgBouncer " + m.Version
	}
	row("Pooler", pooler)
	if detail {
		row("Files", pgbFileNames(m.Files))
	}
	row("Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	row("End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	if detail {
		row("Duration", m.MaxTimestamp.Sub(m.MinTimestamp).String())
		read := strconv.Itoa(m.LinesRead) + " read"
		if m.Lines != m.LinesRead {
			read += ", " + strconv.Itoa(m.Lines) + " entries"
		}
		row("Log lines", read)
		var aside []string
		if m.LinesUnrecognized > 0 {
			aside = append(aside, strconv.Itoa(m.LinesUnrecognized)+" unrecognized")
		}
		if n := m.LinesFiltered(); n > 0 {
			aside = append(aside, strconv.Itoa(n)+" filtered out")
		}
		if m.DebugLines > 0 {
			aside = append(aside, strconv.Itoa(m.DebugLines)+" debug/noise")
		}
		if len(aside) > 0 {
			row("Set aside", strings.Join(aside, ", "))
		}
		row("Stats snapshots", strconv.Itoa(m.StatsSampleCount))
	}

	// Traffic, timing and pool wait come from the "stats:" snapshots.
	if m.StatsSampleCount == 0 {
		row("Traffic", `no "stats:" line in this window, check stats_period in pgbouncer.ini`)
	} else {
		if m.StatsUnderCovered() {
			row("Stats coverage", pgbCoverageText(m))
		}
		if v := fmt.Sprintf("%.1f transactions/s, %.1f queries/s", m.AvgXactsPerSec, m.AvgQueriesPerSec); fits(v) {
			row("Throughput", v)
		} else {
			row("Transactions", fmt.Sprintf("%.1f /s", m.AvgXactsPerSec))
			row("Queries", fmt.Sprintf("%.1f /s", m.AvgQueriesPerSec))
		}
		if detail {
			row("Network", bytesPerSec(m.AvgInBytesPerSec)+" in, "+bytesPerSec(m.AvgOutBytesPerSec)+" out")
		}
		if v := "transaction " + micros(m.AvgXactMicros) + ", query " + micros(m.AvgQueryMicros); fits(v) {
			row("Avg time", v)
		} else {
			row("Avg transaction time", micros(m.AvgXactMicros))
			row("Avg query time", micros(m.AvgQueryMicros))
		}
		if m.WaitIsQueueDepth() {
			row("Avg clients queued", queueDepth(m.AvgWaitMicros))
		} else {
			row("Avg pool wait time", micros(m.AvgWaitMicros))
		}
		if detail && len(m.WaitEpisodes) > 0 {
			row("Wait stretches", pgbStretchesText(m))
		}
	}

	logins := pgbClientCount(m.LoginAttempts, m, pgbNoConnections)
	sessions := pgbClientCount(m.Sessions, m, pgbNoSessions)
	avg := ""
	if m.Sessions > 0 {
		avg = formatSessionDuration(time.Duration(m.AvgSessionSeconds() * float64(time.Second)))
	}
	if v := logins + " login attempts, " + sessions + " sessions of " + avg + " on average"; m.LoginAttempts > 0 && m.Sessions > 0 && fits(v) {
		row("Clients", v)
	} else {
		row("Login attempts", logins)
		if avg != "" {
			sessions += ", average " + avg
		}
		row("Completed sessions", sessions)
	}
	if detail && len(m.SessionReasons) > 0 {
		byClient := m.SessionsEndedByClient()
		row("Sessions ended by", fmt.Sprintf("the client %d, the pooler %d", byClient, m.Sessions-byClient))
	}
	servers := strconv.Itoa(m.ServerConnections) + " opened"
	if m.ReservePoolActivations > 0 {
		servers += ", " + pgbrPlural(m.ReservePoolActivations, "reserve pool activation")
	}
	row("Server connections", servers)
	return rows
}

// pgbLevel is the warnings and errors of one level, most frequent first.
type pgbLevel struct {
	level string
	total int
	errs  []analysis.PgBouncerError
}

// pgbLevels groups the messages by level, the most severe first.
func pgbLevels(es []analysis.PgBouncerError) []pgbLevel {
	var out []pgbLevel
	for _, level := range []string{"FATAL", "ERROR", "WARNING"} {
		l := pgbLevel{level: level}
		for _, e := range es {
			if e.Level == level {
				l.errs = append(l.errs, e)
				l.total += e.Count
			}
		}
		if len(l.errs) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// pgbWhen is "first → last", or the one time when they are the same.
func pgbWhen(e analysis.PgBouncerError, clock string) string {
	when := e.First.Format(clock)
	if last := e.Last.Format(clock); last != when {
		when += " → " + last
	}
	return when
}

// pgbFileNames lists the files a pooler was read from, by name.
func pgbFileNames(files []string) string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f)
	}
	return strings.Join(names, ", ")
}

// pgbStretchesText is the count of queueing stretches and the longest.
func pgbStretchesText(m analysis.PgBouncerMetrics) string {
	longest := m.WaitEpisodes[0]
	for _, e := range m.WaitEpisodes[1:] {
		if e.End.Sub(e.Start) > longest.End.Sub(longest.Start) {
			longest = e
		}
	}
	return fmt.Sprintf("%d, longest %s from %s", len(m.WaitEpisodes),
		formatSessionDuration(longest.End.Sub(longest.Start)), longest.Start.Format(pgbClock(m)))
}

// pgbClientCount is a client-side count, and when it is zero, the setting
// it depends on, and whether traffic shows over the same window: that is
// all the log can say, the clients may predate the window.
func pgbClientCount(n int, m analysis.PgBouncerMetrics, s pgbEmptySection) string {
	switch {
	case n > 0:
		return strconv.Itoa(n)
	case m.ClientLinesMissing():
		return "0 although traffic shows, " + s.check
	}
	return "0, " + s.check
}

// pgbListMax caps the close-reason and error lists. Both are short in
// practice, and unlike the breakdowns they are not cut at 80% of the
// total: the rare reason or error is the one worth reading.
const pgbListMax = 20

// printPgbCloseReasons lists the completed sessions by close reason, with
// the average age of each: a session the client ended and one the pooler
// killed in its queue are different populations.
func printPgbCloseReasons(rs []analysis.PgBouncerCloseReason, total int) {
	if len(rs) == 0 {
		return
	}
	w := 0
	for i, r := range rs {
		if i < pgbListMax {
			w = max(w, len(r.Reason))
		}
	}
	fmt.Println("  Sessions per close reason:") // the histogram above ends with a blank line
	for i, r := range rs {
		if i == pgbListMax {
			fmt.Printf("    \u2026 and %d more\n", len(rs)-i)
			break
		}
		avg := time.Duration(float64(r.TotalSeconds) / float64(r.Count) * float64(time.Second))
		fmt.Printf("    %-*s  %6d  %5.1f%%  avg %s\n", w, r.Reason, r.Count, float64(r.Count)/float64(total)*100, formatSessionDuration(avg))
	}
}

// pgbEpisodeTitle names the queueing stretches in the unit the section
// uses for the wait.
func pgbEpisodeTitle(m analysis.PgBouncerMetrics) string {
	if m.WaitIsQueueDepth() {
		return "Stretches with 0.1 client or more queued"
	}
	return "Stretches with waits of 100 ms or more"
}

// pgbWait renders a wait in the section's unit.
func pgbWait(m analysis.PgBouncerMetrics, us int64) string {
	if m.WaitIsQueueDepth() {
		return queueDepth(float64(us))
	}
	return microsI(us)
}

// printPgbEpisodes lists when clients queued: each stretch, how long it
// lasted and its worst snapshot, so a saturation reads as an episode with
// a start and an end rather than as one maximum.
func printPgbEpisodes(m analysis.PgBouncerMetrics, clock string) {
	if len(m.WaitEpisodes) == 0 {
		return
	}
	fmt.Println("\n  " + pgbEpisodeTitle(m) + ":")
	for i, e := range m.WaitEpisodes {
		if i == pgbListMax {
			fmt.Printf("    \u2026 and %d more\n", len(m.WaitEpisodes)-i)
			break
		}
		fmt.Printf("    %s \u2192 %s  %-8s peak %s at %s\n", e.Start.Format(clock), e.End.Format(clock),
			formatSessionDuration(e.End.Sub(e.Start)), pgbWait(m, e.PeakUs), e.PeakTime.Format(clock))
	}
}

// pgbTimeline adapts the pooler's events to the PostgreSQL timeline
// printer, so both read the same way.
func pgbTimeline(events []analysis.PgBouncerEvent) []analysis.ServerTimelineEvent {
	out := make([]analysis.ServerTimelineEvent, len(events))
	for i, e := range events {
		out[i] = analysis.ServerTimelineEvent{Timestamp: e.Time, Kind: e.Kind, Detail: pgbEventDetail(e)}
	}
	return out
}

// pgbEventDetail is an event's detail, empty when it only repeats the
// kind ("RELOAD" for a reload).
func pgbEventDetail(e analysis.PgBouncerEvent) string {
	if strings.EqualFold(e.Detail, e.Kind) {
		return ""
	}
	return e.Detail
}

// pgbClock is the layout for the pooler's times of day: the clock alone,
// as the PostgreSQL timeline prints it, unless the pooler's window spans
// more than one day.
func pgbClock(m analysis.PgBouncerMetrics) string {
	if m.MinTimestamp.Format(time.DateOnly) != m.MaxTimestamp.Format(time.DateOnly) {
		return time.DateTime
	}
	return time.TimeOnly
}

// pgbAt renders " at <time>" for a maximum, or nothing when none was set.
func pgbAt(t time.Time, clock string) string {
	if t.IsZero() {
		return ""
	}
	return " at " + t.Format(clock)
}

// queueDepth renders a pre-1.23 wait, in microseconds per second, as the
// number of clients it amounts to: before 1.23 pgBouncer's "wait" field
// adds up the time every client spent queued during each second.
func queueDepth(usPerSec float64) string {
	return fmt.Sprintf("%.1f clients", usPerSec/1_000_000)
}

// pgbCoverageText qualifies traffic figures whose snapshots account for
// less than half of the reported window: pgBouncer logs nothing between
// two snapshots, so the figures describe what was sampled, not the window.
func pgbCoverageText(m analysis.PgBouncerMetrics) string {
	window := m.MaxTimestamp.Sub(m.MinTimestamp).Round(time.Second).String()
	if covered := m.StatsCoverage(); covered > 0 {
		return fmt.Sprintf("%d snapshots cover %s of this %s window, check stats_period in pgbouncer.ini", m.StatsSampleCount, covered.Round(time.Second), window)
	}
	return "a single snapshot, of unknown extent, for this " + window + " window, check stats_period in pgbouncer.ini"
}

// pgbEmptySection names the pgbouncer.ini setting a client-side count
// depends on, said when traffic shows yet nothing was logged (see
// PgBouncerMetrics.ClientLinesMissing).
type pgbEmptySection struct{ check string }

var (
	pgbNoConnections = pgbEmptySection{check: "check log_connections = 1 in pgbouncer.ini"}
	pgbNoSessions    = pgbEmptySection{check: "check log_disconnections = 1 in pgbouncer.ini"}
)

// bytesPerSec formats a per-second byte rate, e.g. "1.7 kB/s".
func bytesPerSec(v float64) string {
	return FormatBytes(int64(v+0.5)) + "/s"
}

// micros formats a microsecond value with unit-appropriate precision.
// pgBouncer reports timings in microseconds; keeping µs/ms resolution is
// a deliberate advantage over the reference analyzer, which rounds to
// milliseconds. Examples: 842 → "842 µs", 3425 → "3.4 ms", 2.4e6 → "2.4 s".
func micros(us float64) string {
	switch {
	case us < 1000:
		return fmt.Sprintf("%.0f µs", us)
	case us < 1_000_000:
		return fmt.Sprintf("%.1f ms", us/1000)
	default:
		return fmt.Sprintf("%.2f s", us/1_000_000)
	}
}

func microsI(us int64) string {
	return micros(float64(us))
}
