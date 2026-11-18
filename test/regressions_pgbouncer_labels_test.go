package quellog_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// writeMultiTenantProbe builds a pooler capture with 150 distinct
// databases, users and hosts, weighted so the ten heaviest together
// carry 12.8% of the traffic. It is the multi-tenant shape the report
// has never been run against: every breakdown is cut at ten rows, and
// the percentages of what is shown sum to almost nothing.
func writeMultiTenantProbe(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	ts := time.Date(2026, 9, 9, 21, 0, 0, 0, time.UTC)
	stamp := func() string { return ts.Format("2006-01-02 15:04:05.000") + " UTC" }
	b.WriteString(stamp() + " [1] LOG stats: 10 xacts/s, 50 queries/s, 0 client parses/s," +
		" 0 server parses/s, 0 binds/s, in 100 B/s, out 200 B/s," +
		" xact 1000 us, query 500 us, wait 20 us\n")
	for i := 0; i < 150; i++ {
		who := fmt.Sprintf("tenant%03d/user%03d@10.0.0.%d:35088", i, i, i%256)
		for k := 0; k < 150-i; k++ {
			ts = ts.Add(7 * time.Millisecond)
			b.WriteString(fmt.Sprintf("%s [1] LOG C-0x7f3b3ad2f3c0: %s login attempt:"+
				" db=tenant%03d user=user%03d tls=no replication=no\n", stamp(), who, i, i))
			ts = ts.Add(3 * time.Millisecond)
			b.WriteString(fmt.Sprintf("%s [1] LOG C-0x7f3b3ad2f3c0: %s"+
				" closing because: client close request (age=1s)\n", stamp(), who))
		}
	}
	path := filepath.Join(t.TempDir(), "multitenant.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	return path
}

const labelFixture = "testdata/regressions/pgbouncer/pgbouncer_saturation.log"

func pgbText(t *testing.T, args ...string) string {
	t.Helper()
	return ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, args...)), "")
}

// TestPgBouncerNamesServerConnectionsAsOpens freezes COR-59. The counter
// adds one per "new connection to server" line, so it is the number of
// server connections opened over the window, not the number held. A
// pool capped at five can legitimately open ten, and a reader comparing
// the report with SHOW POOLS must not be told those are the same thing.
func TestPgBouncerNamesServerConnectionsAsOpens(t *testing.T) {
	text := pgbText(t, labelFixture)
	found := false
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "Server connections") {
			found = true
			if !strings.Contains(trimmed, " opened") {
				t.Errorf("text report does not name the counter as an open count: %q", trimmed)
			}
		}
	}
	if !found {
		t.Errorf("text report has no server connections row:\n%s", text)
	}

	md := mdUnpadded(runHarness(t, false, labelFixture, "--md"))
	if !regexp.MustCompile(`\| Server connections \| \d+ opened`).MatchString(md) {
		t.Errorf("Markdown report does not name the counter as an open count:\n%s", md)
	}
}

// TestPgBouncerSeparatesPoolWaitFromQueryTiming freezes COR-51. Query
// time is a query problem; time queued for a server connection is a pool
// sizing problem. Filing the second under query timing erases exactly
// the distinction the reader opened the report to make.
func TestPgBouncerSeparatesPoolWaitFromQueryTiming(t *testing.T) {
	text := pgbText(t, labelFixture)
	if !strings.Contains(text, "Avg pool wait time") {
		t.Errorf("text report does not name the wait as a pool wait:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Avg query time") && strings.Contains(line, "wait") {
			t.Errorf("the query time row carries the queue wait: %q", line)
		}
	}
}

// TestPgBouncerThroughputPeaksAreSnapshotAverages freezes COR-80. The
// pooler logs one averaged rate per stats_period, so the largest value
// seen is the busiest snapshot average, never the busiest instant: the
// detailed report states the maxima under that name, and calls none of
// them a peak.
func TestPgBouncerThroughputPeaksAreSnapshotAverages(t *testing.T) {
	for _, args := range [][]string{{"--pgbouncer-detail"}, {"--pgbouncer-detail", "--md"}} {
		out := pgbText(t, append([]string{labelFixture}, args...)...)
		if !strings.Contains(out, "Max snapshot averages") {
			t.Errorf("args=%v: the maxima are not qualified as snapshot averages:\n%s", args, out)
		}
		if strings.Contains(strings.ToLower(out), "peak") {
			t.Errorf("args=%v: a snapshot maximum is called a peak", args)
		}
	}
}

// TestPgBouncerDeclaresTruncatedBreakdowns freezes COR-58. On a 150-tenant
// capture the breakdowns show ten rows summing to 12.8% and say nothing
// about the 140 they dropped. The PostgreSQL report already has a form
// for this ("… and N more"); the pooler report must use it.
func TestPgBouncerDeclaresTruncatedBreakdowns(t *testing.T) {
	probe := writeMultiTenantProbe(t)

	text := pgbText(t, probe, "--pgbouncer-detail")
	if n := strings.Count(text, "… and 140 more"); n != 3 {
		t.Errorf("text report declares %d truncated breakdowns, want 3:\n%s", n, text)
	}

	md := string(runHarness(t, false, probe, "--pgbouncer-detail", "--md"))
	if n := strings.Count(md, "_… and 140 more_"); n != 3 {
		t.Errorf("Markdown report declares %d truncated breakdowns, want 3:\n%s", n, md)
	}
}

// TestPgBouncerMarkdownCellsKeepTheirColumns is a refutation test. A
// database name is chosen by a client that has not authenticated yet --
// pgBouncer logs "login attempt: db=… user=…" before checking anything
// -- so a name holding a pipe lands in a Markdown cell and silently
// shifts every column after it. The PostgreSQL tables already escape
// the pipe; the pooler tables did not.
//
// Counting pipes is what separates the two rules: every row of a table
// has as many as its header whatever the cell holds, and an unescaped
// name adds its own.
func TestPgBouncerMarkdownCellsKeepTheirColumns(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "pipes.log")
	lines := "2026-09-20 10:00:00.000 CEST [1] LOG stats: 3 xacts/s, 21 queries/s, 0 client parses/s, " +
		"0 server parses/s, 0 binds/s, in 1291 B/s, out 565 B/s, xact 1990 us, query 261 us, wait 331 us\n" +
		"2026-09-20 10:00:01.000 CEST [1] LOG C-0x1: a|b|c/u@h:1 login attempt: db=a|b|c user=u\n" +
		"2026-09-20 10:00:02.000 CEST [1] ERROR pooler error: no such database: x|y\n"
	if err := os.WriteFile(probe, []byte(lines), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	header := 0 // separators of the current table's header, 0 outside a table
	for _, line := range strings.Split(string(runHarness(t, false, probe, "--md")), "\n") {
		if !strings.HasPrefix(line, "|") {
			header = 0
			continue
		}
		// Count the pipes that actually delimit columns: an escaped one
		// is literal text, not a separator.
		got := strings.Count(strings.ReplaceAll(line, `\|`, ""), "|")
		if header == 0 {
			header = got
		} else if got != header {
			t.Errorf("row has %d column separators, its header %d: %q", got, header, line)
		}
	}
}

// mdUnpadded collapses the padding mdTable aligns its columns with, so a
// test can name a header or a row by its cells.
func mdUnpadded(md []byte) string {
	return regexp.MustCompile(` {2,}`).ReplaceAllString(string(md), " ")
}

// TestPgBouncerTextSpansRunForward: in the text report a queueing stretch
// reads from its start to its end, and an error from its first occurrence
// to its last. The values alone are right either way round, so only the
// order separates the rule from its reverse.
func TestPgBouncerTextSpansRunForward(t *testing.T) {
	text := pgbText(t, labelFixture, "--pgbouncer-detail")
	for _, want := range []string{
		"from 13:57:57",       // the stretch, from the start of its first period
		"13:58:05 → 13:58:13", // query_wait_timeout, first to last
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text report missing %q:\n%s", want, text)
		}
	}
}

// TestPgBouncerTextListCapAndDates: the text error list stops at twenty
// distinct errors and states the rest, and a pooler whose window crosses
// midnight dates its times rather than printing a bare clock.
func TestPgBouncerTextListCapAndDates(t *testing.T) {
	var log strings.Builder
	log.WriteString("2026-09-25 23:59:30.000 UTC [7] LOG process up: PgBouncer 1.25.2, libevent 2.1.13-stable\n")
	for i := range 21 {
		fmt.Fprintf(&log, "2026-09-25 23:59:%02d.000 UTC [7] WARNING C-0x1: app/u@10.0.0.1:1 pooler error: failure %02d\n", 31+i, i)
	}
	log.WriteString("2026-09-26 00:00:10.000 UTC [7] LOG SHUTDOWN command issued\n")
	probe := filepath.Join(t.TempDir(), "midnight.log")
	if err := os.WriteFile(probe, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	text := pgbText(t, probe, "--pgbouncer-detail")
	for _, want := range []string{"… and 1 more", "2026-09-25 23:59:31"} {
		if !strings.Contains(text, want) {
			t.Errorf("text report missing %q:\n%s", want, text)
		}
	}
}
