//go:build !js

package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

func samplePgBouncerMetrics() analysis.PgBouncerMetrics {
	base := time.Date(2026, 9, 9, 21, 0, 0, 0, time.UTC)
	return analysis.PgBouncerMetrics{
		MinTimestamp:           base,
		MaxTimestamp:           base.Add(2 * time.Minute),
		Lines:                  200,
		StatsSampleCount:       2,
		AvgQueriesPerSec:       2.5,
		MaxQueriesPerSec:       48,
		AvgWaitMicros:          471210,
		MaxWaitMicros:          11091050,
		LoginAttempts:          10,
		ServerConnections:      3,
		Sessions:               8,
		TotalSessionSeconds:    16,
		SessionDistribution:    map[string]int{"< 1s": 5, "1s - 1min": 3},
		ConnectionsPerDatabase: map[string]int{"app": 7, "billing": 3},
		SessionsPerUser:        map[string]int{"app_user": 5, "batch": 3},
		ReservePoolActivations: 1,
		Errors:                 map[string]int{"pooler error: password authentication failed": 2, "<script>evil</script>": 1},
		TotalErrors:            3,
		StatsSeries: []analysis.PgBouncerStatsPoint{
			{Time: base, Queries: 1, XactMicros: 100},
			{Time: base.Add(time.Second), Queries: 48, XactMicros: 2000},
		},
	}
}

// TestPgBouncerHTMLSmoke checks the HTML report renders, embeds the chart
// containers and telemetry, and HTML-escapes hostile map keys.
func TestPgBouncerHTMLSmoke(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportPgBouncerHTML(&buf, samplePgBouncerMetrics(), HTMLReportInfo{Version: "test"}); err != nil {
		t.Fatalf("ExportPgBouncerHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`id="chart-throughput"`,
		`id="chart-io"`,
		`id="chart-timing"`,
		`id="pgb-chart-data"`,
		"Most Frequent Errors",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML report missing %q", want)
		}
	}
	// The hostile key must be escaped, not injected as a live tag.
	if strings.Contains(out, "<script>evil</script>") {
		t.Error("error key was not HTML-escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;evil&lt;/script&gt;") {
		t.Error("expected escaped error key in output")
	}
}

// TestPgBouncerJSONRoundTrip checks the JSON export is valid and preserves
// the headline counters.
func TestPgBouncerJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	ExportPgBouncerJSON(&buf, samplePgBouncerMetrics(), true)

	var got map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["login_attempts"].(float64) != 10 {
		t.Errorf("login_attempts = %v, want 10", got["login_attempts"])
	}
	if got["total_errors"].(float64) != 3 {
		t.Errorf("total_errors = %v, want 3", got["total_errors"])
	}
}

// TestExportJSONCombined checks the pgBouncer sub-object is added to the
// PostgreSQL payload only when pgBouncer metrics are present.
func TestExportJSONCombined(t *testing.T) {
	m := analysis.AggregatedMetrics{}
	m.Global.Count = 1

	// With pgb: a top-level "pgbouncer" key carries the pooler metrics.
	var withPgb bytes.Buffer
	pgb := samplePgBouncerMetrics()
	ExportJSONCombined(&withPgb, m, &pgb, []string{"all"}, true, true)
	var got map[string]interface{}
	if err := json.Unmarshal(withPgb.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	pb, ok := got["pgbouncer"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected pgbouncer object, got %T", got["pgbouncer"])
	}
	if pb["login_attempts"].(float64) != 10 {
		t.Errorf("pgbouncer.login_attempts = %v, want 10", pb["login_attempts"])
	}

	// Without pgb (pure PostgreSQL run): no pgbouncer key.
	var noPgb bytes.Buffer
	ExportJSONCombined(&noPgb, m, nil, []string{"all"}, true, true)
	var got2 map[string]interface{}
	if err := json.Unmarshal(noPgb.Bytes(), &got2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, present := got2["pgbouncer"]; present {
		t.Error("pgbouncer key present in a pure PostgreSQL payload")
	}
}
