package output

import (
	"testing"

	"github.com/Alain-L/quellog/analysis"
)

// TestMaintenanceJSON_SkipsOnly pins the fix for a maintenance section that
// carries only skipped autovacuums/autoanalyzes and no completed vacuum — a
// default-config scenario (log_autovacuum_min_duration hides short vacuums).
// The JSON/YAML/HTML gate used to test only VacuumCount/AnalyzeCount, so the
// whole section vanished from the machine output while the text renderer (which
// already gated on the skip counts) still showed it. buildJSONData feeds JSON,
// YAML and the HTML payload, so this covers all three.
func TestMaintenanceJSON_SkipsOnly(t *testing.T) {
	m := analysis.AggregatedMetrics{
		Vacuum: analysis.VacuumMetrics{
			SkippedVacuumCount:   3,
			SkippedVacuumTables:  []analysis.VacuumSkip{{Table: "public.orders", Count: 3, Reason: "lock not available"}},
			SkippedAnalyzeCount:  1,
			SkippedAnalyzeTables: []analysis.VacuumSkip{{Table: "public.customers", Count: 1, Reason: "lock not available"}},
		},
	}

	data := buildJSONData(m, []string{"all"}, true)
	if mnt, ok := data["maintenance"]; !ok || mnt == nil {
		t.Fatal("maintenance section absent from JSON when only autovacuum/autoanalyze skips exist (no completed vacuum)")
	}
}
