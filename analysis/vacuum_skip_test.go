package analysis

import (
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

func TestParseSkippedTarget(t *testing.T) {
	cases := []struct {
		in         string
		wantTable  string
		wantReason string
		wantOK     bool
	}{
		{`"t1" --- lock not available`, "t1", "lock not available", true},
		{`"my_table" --- relation no longer exists`, "my_table", "relation no longer exists", true},
		{`"weird name" --- lock not available`, "weird name", "lock not available", true},
		{`"t2"`, "", "", false},          // no reason separator -> not a real skip msg
		{`"" --- x`, "", "", false},      // empty relation name
		{`nothing' here`, "", "", false}, // SQL text, not quoted -> rejected
	}
	for _, c := range cases {
		gotTable, gotReason, gotOK := parseSkippedTarget(c.in)
		if gotTable != c.wantTable || gotReason != c.wantReason || gotOK != c.wantOK {
			t.Errorf("parseSkippedTarget(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, gotTable, gotReason, gotOK, c.wantTable, c.wantReason, c.wantOK)
		}
	}
}

// skipEntries builds the synthetic mix used by the detection + parity tests:
// vacuum and analyze skips on a few relations, interleaved with a normal
// autovacuum line and a non-maintenance line that must not be miscounted.
func skipEntries() []parser.LogEntry {
	base := time.Date(2025, 11, 30, 21, 10, 0, 0, time.UTC)
	lines := []struct{ pid, msg string }{
		{"100", `LOG:  skipping vacuum of "t_orders" --- lock not available`},
		{"200", `LOG:  skipping vacuum of "t_orders" --- lock not available`},
		{"300", `LOG:  skipping vacuum of "t_events" --- lock not available`},
		// "uto" in the prefix (user=auto) must NOT suppress skip detection.
		{"500", `db=app,user=auto LOG:  skipping vacuum of "t_uto" --- lock not available`},
		{"100", `LOG:  skipping analyze of "t_orders" --- lock not available`},
		{"200", `LOG:  skipping analyze of "t_stats" --- lock not available`},
		{"400", `LOG:  automatic vacuum of table "app.public.users": index scans: 1, pages: 10 removed, 90 remain`},
		{"100", `LOG:  duration: 1.250 ms  statement: SELECT 'skipping vacuum of nothing'`},
	}
	entries := make([]parser.LogEntry, 0, len(lines))
	for i, l := range lines {
		ts := base.Add(time.Duration(i) * time.Second)
		entries = append(entries, parser.NewLogEntry(ts, "["+l.pid+"] "+l.msg, false))
	}
	return entries
}

func TestVacuumSkipDetection(t *testing.T) {
	m := vacuumRunSingle(skipEntries())

	if m.SkippedVacuumCount != 4 {
		t.Errorf("SkippedVacuumCount = %d, want 4", m.SkippedVacuumCount)
	}
	if m.SkippedAnalyzeCount != 2 {
		t.Errorf("SkippedAnalyzeCount = %d, want 2", m.SkippedAnalyzeCount)
	}

	// Vacuum skips: t_orders (2) ranks before t_events (1) and t_uto (1).
	if len(m.SkippedVacuumTables) != 3 {
		t.Fatalf("SkippedVacuumTables = %+v, want 3 rows", m.SkippedVacuumTables)
	}
	if got := m.SkippedVacuumTables[0]; got.Table != "t_orders" || got.Count != 2 || got.Reason != "lock not available" {
		t.Errorf("top skipped vacuum = %+v, want t_orders/2/lock not available", got)
	}
	if got := m.SkippedVacuumTables[1]; got.Table != "t_events" || got.Count != 1 {
		t.Errorf("second skipped vacuum = %+v, want t_events/1", got)
	}

	// Analyze skips: t_orders and t_stats, one each.
	if len(m.SkippedAnalyzeTables) != 2 {
		t.Fatalf("SkippedAnalyzeTables = %+v, want 2 rows", m.SkippedAnalyzeTables)
	}

	// The SELECT mentioning "skipping vacuum of" in its text must not count,
	// and the real autovacuum line must not leak into the skip totals.
	if m.VacuumCount != 1 {
		t.Errorf("VacuumCount = %d, want 1 (the genuine autovacuum line)", m.VacuumCount)
	}
}

func TestVacuumSkipShardMergeParity(t *testing.T) {
	entries := skipEntries()
	for _, n := range []int{2, 3, 4} {
		single := vacuumRunSingle(entries)
		sharded, spread := vacuumRunSharded(entries, n)
		if spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", n, spread)
		}
		vacuumAssertMetricsEqual(t, "skip-parity n="+itoa(n), single, sharded)
	}
}
