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
	if !strings.Contains(text, "Server connections opened") {
		t.Errorf("text report does not name the counter as an open count:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Server connections ") && !strings.HasPrefix(trimmed, "Server connections opened") {
			t.Errorf("text report still labels the counter %q", trimmed)
		}
	}

	md := string(runHarness(t, false, labelFixture, "--md"))
	if !strings.Contains(md, "| Server connections opened |") {
		t.Errorf("Markdown report does not name the counter as an open count:\n%s", md)
	}
}

// TestPgBouncerSeparatesPoolWaitFromQueryTiming freezes COR-51. Query
// time is a query problem; time queued for a server connection is a pool
// sizing problem. Filing the second under "QUERY TIMING" erases exactly
// the distinction the reader opened the section to make.
func TestPgBouncerSeparatesPoolWaitFromQueryTiming(t *testing.T) {
	text := pgbText(t, labelFixture)
	if !strings.Contains(text, "POOL WAIT") {
		t.Errorf("text report has no pool-wait section of its own:\n%s", text)
	}
	if !strings.Contains(text, "Avg pool wait time") {
		t.Errorf("text report does not name the wait as a pool wait:\n%s", text)
	}
	qt := section(text, "QUERY TIMING")
	if strings.Contains(qt, "wait") {
		t.Errorf("QUERY TIMING still carries the queue wait:\n%s", qt)
	}

	md := string(runHarness(t, false, labelFixture, "--md"))
	if !strings.Contains(md, "## Pool Wait") {
		t.Errorf("Markdown report has no Pool Wait section:\n%s", md)
	}
}

// sectionHeaderRe matches a pooler report section header: a line that is
// nothing but capitals and spaces, which is how PrintPgBouncer titles
// every block.
var sectionHeaderRe = regexp.MustCompile(`(?m)^[A-Z][A-Z ]+$`)

// section returns the body between the given header and the next one, so
// a single section can be asserted on in isolation.
func section(text, header string) string {
	i := strings.Index(text, header)
	if i < 0 {
		return ""
	}
	rest := text[i+len(header):]
	if loc := sectionHeaderRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return rest
}

// TestPgBouncerThroughputPeaksAreSnapshotAverages freezes COR-80. The
// pooler logs one averaged rate per stats_period, so the largest value
// seen is the busiest snapshot average, never the busiest instant.
// Calling it a peak invents a precision the log does not carry.
func TestPgBouncerThroughputPeaksAreSnapshotAverages(t *testing.T) {
	text := pgbText(t, labelFixture)
	tp := section(text, "REQUEST THROUGHPUT")
	if strings.Count(tp, "max snapshot avg") != 4 {
		t.Errorf("throughput rows do not all qualify their max as a snapshot average:\n%s", tp)
	}

	md := string(runHarness(t, false, labelFixture, "--md"))
	if strings.Contains(md, "| Metric | Average | Peak |") {
		t.Errorf("Markdown throughput table still heads its max column \"Peak\":\n%s", md)
	}
	if !strings.Contains(md, "| Metric | Average | Max snapshot average |") {
		t.Errorf("Markdown throughput table does not name the snapshot average:\n%s", md)
	}
}

// TestPgBouncerDeclaresTruncatedBreakdowns freezes COR-58. On a 150-tenant
// capture the breakdowns show ten rows summing to 12.8% and say nothing
// about the 140 they dropped. The PostgreSQL report already has a form
// for this ("… and N more"); the pooler report must use it.
func TestPgBouncerDeclaresTruncatedBreakdowns(t *testing.T) {
	probe := writeMultiTenantProbe(t)

	text := pgbText(t, probe)
	if n := strings.Count(text, "… and 140 more"); n != 6 {
		t.Errorf("text report declares %d truncated breakdowns, want 6:\n%s", n, text)
	}

	md := string(runHarness(t, false, probe, "--md"))
	if n := strings.Count(md, "_… and 140 more_"); n != 6 {
		t.Errorf("Markdown report declares %d truncated breakdowns, want 6:\n%s", n, md)
	}
}
