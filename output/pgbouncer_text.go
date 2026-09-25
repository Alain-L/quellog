//go:build !js

package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// sessionBucketOrder is the fixed display order for the session-time
// histogram, matching the PostgreSQL connection report's buckets.
var sessionBucketOrder = []string{"< 1s", "1s - 1min", "1min - 30min", "30min - 2h", "2h - 5h", "> 5h"}

// PrintPgBouncer renders one report per pooler, each titled by the
// pooler's name when there are several.
func PrintPgBouncer(poolers []analysis.PgBouncerMetrics) {
	for _, m := range poolers {
		title := "PGBOUNCER SUMMARY"
		if len(poolers) > 1 {
			title += " (" + m.Name + ")"
		}
		printPgBouncer(m, title)
	}
}

// printPgBouncer renders one pooler's report to stdout in the same visual
// language as the PostgreSQL text report (bold headers, two-space
// indented "label : value" lines, shared histogram helper).
func printPgBouncer(m analysis.PgBouncerMetrics, title string) {
	bold := ansiBold
	reset := ansiReset
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)
	clock := pgbClock(m)

	fmt.Println(bold + "\n" + title + "\n" + reset)
	fmt.Printf("  %-25s : %s\n", "Pooler", m.Name)
	if m.Version != "" {
		fmt.Printf("  %-25s : PgBouncer %s\n", "Version", m.Version)
	}
	fmt.Printf("  %-25s : %s\n", "Files", strings.Join(m.Files, ", "))
	fmt.Printf("  %-25s : %s\n", "Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "Duration", duration)
	fmt.Printf("  %-25s : %d\n", "Log lines read", m.LinesRead)
	fmt.Printf("  %-25s : %d\n", "Log entries", m.Lines)
	if m.LinesUnrecognized > 0 {
		fmt.Printf("  %-25s : %d\n", "Unrecognized lines", m.LinesUnrecognized)
	}
	if n := m.LinesFiltered(); n > 0 {
		fmt.Printf("  %-25s : %d\n", "Filtered-out lines", n)
	}
	if m.DebugLines > 0 {
		fmt.Printf("  %-25s : %d\n", "Debug/noise entries", m.DebugLines)
	}
	fmt.Printf("  %-25s : %d\n", "Stats snapshots", m.StatsSampleCount)
	fmt.Printf("  %-25s : %d\n", "Login attempts", m.LoginAttempts)
	fmt.Printf("  %-25s : %d\n", "Server connections opened", m.ServerConnections)
	fmt.Printf("  %-25s : %d\n", "Completed sessions", m.Sessions)
	if m.ReservePoolActivations > 0 {
		fmt.Printf("  %-25s : %d\n", "Reserve pool activations", m.ReservePoolActivations)
	}
	fmt.Printf("  %-25s : %d\n", "Errors", m.TotalErrors)
	if len(m.Events) > 0 {
		printServerTimeline(pgbTimeline(m.Events))
	}

	// Traffic (from the per-second stats snapshots).
	if m.StatsSampleCount == 0 {
		fmt.Println(bold + "\nREQUEST THROUGHPUT\n" + reset)
		fmt.Println("  (No data available)")
		fmt.Println()
		printPgbNote(pgbNoStatsNote)
	}
	if m.StatsSampleCount > 0 {
		fmt.Println(bold + "\nREQUEST THROUGHPUT\n" + reset)
		if m.StatsUnderCovered() {
			printPgbNote(pgbCoverageNote(m))
			fmt.Println()
		}
		fmt.Printf("  %-25s : %.1f /s  (max snapshot avg %d /s%s)\n", "Transactions", m.AvgXactsPerSec, m.MaxXactsPerSec, pgbAt(m.MaxXactsTimestamp, clock))
		fmt.Printf("  %-25s : %.1f /s  (max snapshot avg %d /s%s)\n", "Queries", m.AvgQueriesPerSec, m.MaxQueriesPerSec, pgbAt(m.MaxQueriesTimestamp, clock))
		fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Network in", bytesPerSec(m.AvgInBytesPerSec), bytesPerSec(float64(m.MaxInBytesPerSec)), pgbAt(m.MaxInTimestamp, clock))
		fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Network out", bytesPerSec(m.AvgOutBytesPerSec), bytesPerSec(float64(m.MaxOutBytesPerSec)), pgbAt(m.MaxOutTimestamp, clock))
		fmt.Println()
		fmt.Println("  " + pgbRateNote[0])
		fmt.Println("  " + pgbRateNote[1])

		fmt.Println(bold + "\nQUERY TIMING\n" + reset)
		fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Avg transaction time", micros(m.AvgXactMicros), microsI(m.MaxXactMicros), pgbAt(m.MaxXactTimestamp, clock))
		fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Avg query time", micros(m.AvgQueryMicros), microsI(m.MaxQueryMicros), pgbAt(m.MaxQueryTimestamp, clock))
		fmt.Println()
		fmt.Println("  " + pgbTimingNote[0])
		fmt.Println("  " + pgbTimingNote[1])

		fmt.Println(bold + "\nPOOL WAIT\n" + reset)
		if m.WaitIsQueueDepth() {
			fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Avg clients queued", queueDepth(m.AvgWaitMicros), queueDepth(float64(m.MaxWaitMicros)), pgbAt(m.MaxWaitTimestamp, clock))
			fmt.Println()
			printPgbNote(pgbQueueDepthNote(m.Version))
		} else {
			fmt.Printf("  %-25s : %s  (max snapshot avg %s%s)\n", "Avg pool wait time", micros(m.AvgWaitMicros), microsI(m.MaxWaitMicros), pgbAt(m.MaxWaitTimestamp, clock))
			fmt.Println()
			printPgbNote(pgbWaitNote)
		}
	}

	// Sessions. The heading is printed whether or not there is anything
	// under it: with log_disconnections = 0 the section used to vanish
	// from a page that went on reporting traffic and errors, leaving no
	// way to tell an idle pooler from a silenced one.
	if m.Sessions == 0 {
		fmt.Println(bold + "\nSESSIONS\n" + reset)
		fmt.Println("  (No data available)")
		fmt.Println()
		printPgbNote(pgbNoSessions.note(m.ClientLinesMissing()))
	}
	if m.Sessions > 0 {
		fmt.Println(bold + "\nSESSIONS\n" + reset)
		PrintHistogram(m.SessionDistribution, "Session time", "", 1, sessionBucketOrder)
		fmt.Printf("  %-25s : %d\n", "Completed sessions", m.Sessions)
		fmt.Printf("  %-25s : %s\n", "Average session time", formatSessionDuration(time.Duration(m.AvgSessionSeconds()*float64(time.Second))))
		if len(m.SessionReasons) > 0 {
			byClient := m.SessionsEndedByClient()
			fmt.Printf("  %-25s : %d\n", "Ended by the client", byClient)
			fmt.Printf("  %-25s : %d\n", "Ended by the pooler", m.Sessions-byClient)
		}
		printPgbCloseReasons(m.SessionReasons, m.Sessions)
		if len(m.SessionsPerDatabase) > 0 {
			printRankedCountTable("Sessions per database:", m.SessionsPerDatabase, m.Sessions, true)
		}
		if len(m.SessionsPerUser) > 0 {
			printRankedCountTable("Sessions per user:", m.SessionsPerUser, m.Sessions, true)
		}
		if len(m.SessionsPerHost) > 0 {
			printRankedCountTable("Sessions per host:", m.SessionsPerHost, m.Sessions, true)
		}
	}

	// Connections (client login attempts). Printed empty for the same
	// reason as Sessions above.
	if m.LoginAttempts == 0 {
		fmt.Println(bold + "\nCONNECTIONS\n" + reset)
		fmt.Println("  (No data available)")
		fmt.Println()
		printPgbNote(pgbNoConnections.note(m.ClientLinesMissing()))
	}
	if m.LoginAttempts > 0 {
		fmt.Println(bold + "\nCONNECTIONS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Login attempts", m.LoginAttempts)
		fmt.Printf("  %-25s : %d\n", "Server connections opened", m.ServerConnections)
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

	// Errors.
	if m.TotalErrors > 0 {
		fmt.Println(bold + "\nMOST FREQUENT ERRORS\n" + reset)
		printPgbErrors(m.Errors, m.TotalErrors, clock)
	}

	fmt.Println()
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
	fmt.Println("\n  Sessions per close reason:")
	for i, r := range rs {
		if i == pgbListMax {
			fmt.Printf("    \u2026 and %d more\n", len(rs)-i)
			break
		}
		avg := time.Duration(float64(r.TotalSeconds) / float64(r.Count) * float64(time.Second))
		fmt.Printf("    %-*s  %6d  %5.1f%%  avg %s\n", w, r.Reason, r.Count, float64(r.Count)/float64(total)*100, formatSessionDuration(avg))
	}
}

// printPgbErrors lists every distinct warning and error with when it was
// first and last logged, so a burst reads against the other timelines.
func printPgbErrors(es []analysis.PgBouncerError, total int, clock string) {
	w := 0
	for i, e := range es {
		if i < pgbListMax {
			w = max(w, len(e.Message))
		}
	}
	fmt.Println("\n  Errors:")
	for i, e := range es {
		if i == pgbListMax {
			fmt.Printf("    \u2026 and %d more\n", len(es)-i)
			break
		}
		when := e.First.Format(clock)
		if last := e.Last.Format(clock); last != when {
			when += " \u2192 " + last
		}
		fmt.Printf("    %-*s  %6d  %5.1f%%  %s\n", w, e.Message, e.Count, float64(e.Count)/float64(total)*100, when)
	}
}

// pgbTimeline adapts the pooler's events to the PostgreSQL timeline
// printer, so both read the same way.
func pgbTimeline(events []analysis.PgBouncerEvent) []analysis.ServerTimelineEvent {
	out := make([]analysis.ServerTimelineEvent, len(events))
	for i, e := range events {
		out[i] = analysis.ServerTimelineEvent{Timestamp: e.Time, Kind: e.Kind, Detail: e.Detail}
	}
	return out
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

// pgbRateNote states what the throughput figures are. pgBouncer does not
// log an instantaneous rate: every "stats:" line carries one average over
// the elapsed stats_period, so the largest value in the capture is the
// busiest snapshot average, and at the default 60 s period a real spike
// is flattened across the minute. Calling it a peak would promise a
// resolution the log never had.
var pgbRateNote = [2]string{
	"Rates are the pooler's own per-snapshot averages over stats_period;",
	"the max is the highest snapshot average, not the busiest instant.",
}

// pgbWaitNote draws the line the section split is there to draw. Queue
// wait is not query time: a slow query is a query problem, a long wait
// is a pool sizing problem, and the two are read against different
// settings (default_pool_size, reserve_pool_size, max_db_connections).
//
// The last two lines name what the figure cannot say. pgBouncer counts
// one wait per server assignment, and pool_mode decides how often that
// happens -- once per session, per transaction, or per statement -- so
// the same millisecond mean is a different statement under each mode.
// The setting appears in no log line: reports of the same traffic in
// session and in statement mode are identical but for the server
// connection count, so quellog cannot infer it and says so instead.
var pgbWaitNote = []string{
	"Time clients spent queued for a server connection, a pool sizing",
	"signal rather than a query one. Averaged as the timings above are.",
	"What it is averaged over depends on pool_mode (one wait per session,",
	"per transaction or per statement), which pgBouncer logs nowhere.",
}

// pgbQueueDepthNote replaces pgbWaitNote before pgBouncer 1.23, whose
// "wait" field adds up the time every client spent queued during each
// second: one second of it per second is one client queued on average.
func pgbQueueDepthNote(version string) []string {
	return []string{
		"PgBouncer " + version + " predates 1.23: its wait field is the time all",
		"clients spent queued per second, shown here as the number of clients",
		"queued on average. From 1.23 it is a wait time per server assignment.",
	}
}

// queueDepth renders a pre-1.23 wait, in microseconds per second, as the
// number of clients it amounts to.
func queueDepth(usPerSec float64) string {
	return fmt.Sprintf("%.1f clients", usPerSec/1_000_000)
}

// pgbNoStatsNote explains an empty telemetry block. It describes the
// cadence rather than blaming the configuration: a capture shorter than
// one stats_period legitimately holds no "stats:" line, and on a default
// 60 s period that is every capture of under a minute.
var pgbNoStatsNote = []string{
	`No "stats:" line in this window; request throughput, query timing and`,
	"pool wait all come from those lines. pgBouncer writes one per",
	"stats_period, 60 s by default, so a shorter capture carries none.",
	"Check stats_period in pgbouncer.ini; see docs/pgbouncer-setup.md.",
}

// pgbCoverageNote qualifies traffic figures whose snapshots account for
// less than half of the reported window. Without it a pooler sampled
// once at startup reports "0.0 queries/s" over a window holding
// hundreds of sessions, and states it as a measurement.
func pgbCoverageNote(m analysis.PgBouncerMetrics) []string {
	window := pgbDuration(m.MaxTimestamp.Sub(m.MinTimestamp))
	lead := fmt.Sprintf("A single stats snapshot for this %s window, of unknown extent:", window)
	if covered := m.StatsCoverage(); covered > 0 {
		lead = fmt.Sprintf("%d stats snapshots cover %s of this %s window:",
			m.StatsSampleCount, pgbDuration(covered), window)
	}
	return []string{
		lead,
		"pgBouncer logs nothing between two snapshots, so the figures below",
		"describe what was sampled, not the window.",
		"Check stats_period in pgbouncer.ini; see docs/pgbouncer-setup.md.",
	}
}

// pgbEmptySection holds what a client-side section says when it has
// nothing to show. Two wordings, because an empty Connections section
// has two readings and the report is useless if it cannot separate
// them: nobody connected, or the pooler was told not to log it.
type pgbEmptySection struct {
	// quiet is used when nothing in the window contradicts an idle
	// pooler. It states the absence and stops there -- accusing a
	// correctly configured pooler of losing lines it never had to write
	// would cost more trust than the warning buys.
	quiet []string
	// missing is used when the same window proves the pooler served
	// clients (see PgBouncerMetrics.ClientLinesMissing).
	missing []string
	// check names the pgbouncer.ini setting, in the form the PostgreSQL
	// sections use for their own "Check:" hints.
	check string
}

// note picks the wording and appends the setting to check.
func (s pgbEmptySection) note(clientLinesMissing bool) []string {
	lines := s.quiet
	if clientLinesMissing {
		lines = s.missing
	}
	return append(append(make([]string, 0, len(lines)+1), lines...), s.check)
}

var pgbNoConnections = pgbEmptySection{
	quiet: []string{"No client connection was logged in this window."},
	missing: []string{
		"Traffic over this window, but no client connection logged in it:",
		"the clients may predate the window, or the lines may be off.",
	},
	check: "Check log_connections = 1 in pgbouncer.ini; see docs/pgbouncer-setup.md.",
}

var pgbNoSessions = pgbEmptySection{
	quiet: []string{"No session close was logged in this window."},
	missing: []string{
		"Traffic over this window, but no session close logged in it: the",
		"sessions may still be open, or the lines may be off.",
	},
	check: "Check log_disconnections = 1 in pgbouncer.ini; see docs/pgbouncer-setup.md.",
}

// printPgbNote prints a note block at the two-space indentation the rest
// of the pooler report uses for its labels and notes.
func printPgbNote(lines []string) {
	for _, l := range lines {
		fmt.Println("  " + l)
	}
}

// pgbDuration renders a whole-second duration for the coverage note.
// Kept here (not in the Markdown file) so both renderers phrase the
// same window the same way.
func pgbDuration(d time.Duration) string {
	return d.Round(time.Second).String()
}

// pgbTimingNote states what the timing figures are, in the two formats
// that show them. pgBouncer only ever logs one average per snapshot, so
// the averages are weighted by each snapshot's operation count and skip
// the snapshots that timed none of that kind (which is why they can
// rest on fewer snapshots than the summary counts), and the column next
// to them is the highest snapshot average, never a slowest operation.
var pgbTimingNote = [2]string{
	"Averages skip the snapshots that timed none and weight the rest by",
	"operation count; the max is the highest snapshot average, not a peak.",
}

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
