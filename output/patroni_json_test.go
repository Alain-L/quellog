package output

import (
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// The contract derives its counters from the changes: a retaken lock is
// not a leader change, and the leaderless total adds up every change.
func TestBuildPatroniJSONCounters(t *testing.T) {
	at := func(s string) time.Time { t, _ := time.Parse("15:04:05", s); return t }
	m := &analysis.PatroniMetrics{
		Start: at("10:00:00"), End: at("10:10:00"),
		Leadership: []analysis.PatroniTerm{
			{Member: "pg1", Start: at("10:00:00"), End: at("10:01:00")},
			{Member: "pg2", Start: at("10:01:30"), End: at("10:05:00")},
			{Member: "pg2", Start: at("10:06:00"), End: at("10:10:00")},
		},
		Changes: []analysis.PatroniChange{
			{Kind: "failover", From: "pg1", To: "pg2", Leaderless: 30 * time.Second},
			{Kind: "retaken", From: "pg2", To: "pg2", Leaderless: time.Minute},
		},
		LeaderlessAtEnd: true,
	}
	j := buildPatroniJSON(m)
	if j.LeaderChanges != 1 || j.Failovers != 1 || j.Switchovers != 0 || j.LockRetaken != 1 {
		t.Errorf("changes %d, failovers %d, switchovers %d, retaken %d; want 1, 1, 0, 1", j.LeaderChanges, j.Failovers, j.Switchovers, j.LockRetaken)
	}
	if j.LeaderlessTime != "1m30s" {
		t.Errorf("leaderless_time = %s, want 1m30s (sum of the changes)", j.LeaderlessTime)
	}
	if j.Leader != "" {
		t.Errorf("leader = %q, want none: the logs end with nobody holding the lock", j.Leader)
	}
}

// Patroni is gated like any section: out of a PostgreSQL report narrowed by
// section flags, in the full one, and the whole report without PostgreSQL.
func TestPatroniSectionGating(t *testing.T) {
	p := &analysis.PatroniMetrics{Entries: 1}
	var withPg analysis.AggregatedMetrics
	withPg.Global.Count, withPg.Patroni = 10, p
	if _, ok := buildJSONData(withPg, []string{"summary"}, false)["patroni"]; ok {
		t.Error("--summary report carries patroni")
	}
	if _, ok := buildJSONData(withPg, []string{"all"}, false)["patroni"]; !ok {
		t.Error("full report lacks patroni")
	}
	only := analysis.AggregatedMetrics{Patroni: p}
	if data := buildJSONData(only, []string{"summary", "patroni"}, false); len(data) != 1 || data["patroni"] == nil {
		t.Errorf("Patroni-only report keys = %v, want patroni alone", data)
	}
}

// The text and Markdown renderers gate Patroni through has() too.
func TestPatroniTextMarkdownGating(t *testing.T) {
	m := analysis.AggregatedMetrics{Patroni: &analysis.PatroniMetrics{Entries: 1}}
	m.Global.Count = 10
	for _, c := range []struct {
		sections []string
		want     bool
	}{{[]string{"summary"}, false}, {[]string{"all"}, true}} {
		text := captureStdout(t, func() { PrintMetrics(m, c.sections, false) })
		var md strings.Builder
		ExportMarkdown(&md, m, c.sections, false)
		if got := strings.Contains(text, "PATRONI"); got != c.want {
			t.Errorf("text %v: PATRONI shown = %v, want %v", c.sections, got, c.want)
		}
		if got := strings.Contains(md.String(), "## PATRONI"); got != c.want {
			t.Errorf("markdown %v: PATRONI shown = %v, want %v", c.sections, got, c.want)
		}
	}
}
