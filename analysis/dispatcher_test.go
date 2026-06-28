package analysis

import (
	"context"
	"testing"

	"github.com/Alain-L/quellog/parser"
)

// feedEntries pushes entries through AggregateMetricsWithWorkers in batches,
// mimicking the parser's batch hand-off.
func feedEntries(entries []parser.LogEntry, workers int) AggregatedMetrics {
	in := make(chan []parser.LogEntry, 8)
	go func() {
		const bs = 256
		for start := 0; start < len(entries); start += bs {
			end := start + bs
			if end > len(entries) {
				end = len(entries)
			}
			batch := make([]parser.LogEntry, end-start)
			copy(batch, entries[start:end])
			in <- batch
		}
		close(in)
	}()
	return AggregateMetricsWithWorkers(context.Background(), in, workers)
}

// TestDispatcherShardingParity drives the full dispatcher at workers=1 and
// workers∈{2,4,8} over a real fixture and asserts the integer/count fields
// match exactly. This is the integration check on top of the per-analyzer
// Merge parity tests: it catches routing, refcount and merge-wiring bugs
// (a misrouted or dropped entry shows up as a count mismatch). Floats
// (duration sums) are covered by the per-analyzer tolerance tests and are
// not re-checked here. Connections is hybrid (never sharded) so its fields
// are identical across worker counts by construction.
func TestDispatcherShardingParity(t *testing.T) {
	entries := parseFixtureEntries(t, "../test/testdata/stderr.log")
	if len(entries) == 0 {
		t.Skip("stderr.log produced no entries")
	}

	base := feedEntries(entries, 1)
	// Sanity: the fixture must actually spread across shards, else vacuous.
	used := map[int]bool{}
	for i := range entries {
		used[shardForPID(entries[i].PID, 4)] = true
	}
	if len(used) < 2 {
		t.Fatal("fixture did not spread across shards; parity check vacuous")
	}

	check := func(name string, a, b int) {
		t.Helper()
		if a != b {
			t.Errorf("%s: workers=1 gave %d, sharded gave %d", name, a, b)
		}
	}

	for _, w := range []int{2, 4, 8} {
		g := feedEntries(entries, w)
		check("Global.Count", base.Global.Count, g.Global.Count)
		check("ErrorCount", base.Global.ErrorCount, g.Global.ErrorCount)
		check("FatalCount", base.Global.FatalCount, g.Global.FatalCount)
		check("WarningCount", base.Global.WarningCount, g.Global.WarningCount)
		check("LogCount", base.Global.LogCount, g.Global.LogCount)
		check("SQL.TotalQueries", base.SQL.TotalQueries, g.SQL.TotalQueries)
		check("SQL.UniqueQueries", base.SQL.UniqueQueries, g.SQL.UniqueQueries)
		check("len(SQL.QueryStats)", len(base.SQL.QueryStats), len(g.SQL.QueryStats))
		check("TempFiles.Count", base.TempFiles.Count, g.TempFiles.Count)
		check("Vacuum.VacuumCount", base.Vacuum.VacuumCount, g.Vacuum.VacuumCount)
		check("Vacuum.AnalyzeCount", base.Vacuum.AnalyzeCount, g.Vacuum.AnalyzeCount)
		check("Checkpoints.CompleteCount", base.Checkpoints.CompleteCount, g.Checkpoints.CompleteCount)
		check("Checkpoints.WarningCount", base.Checkpoints.WarningCount, g.Checkpoints.WarningCount)
		check("Connections.ConnectionReceivedCount", base.Connections.ConnectionReceivedCount, g.Connections.ConnectionReceivedCount)
		check("len(EventSummaries)", len(base.EventSummaries), len(g.EventSummaries))
		check("len(TopEvents)", len(base.TopEvents), len(g.TopEvents))
		check("UniqueEntities.UniqueDbs", base.UniqueEntities.UniqueDbs, g.UniqueEntities.UniqueDbs)
		check("UniqueEntities.UniqueUsers", base.UniqueEntities.UniqueUsers, g.UniqueEntities.UniqueUsers)
		check("Server.StartCount", base.Server.StartCount, g.Server.StartCount)
		check("Replication.HasAny", b2i(base.Replication.HasAny), b2i(g.Replication.HasAny))
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
