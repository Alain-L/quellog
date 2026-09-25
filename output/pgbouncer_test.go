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
		Errors: []analysis.PgBouncerError{
			{Message: "pooler error: password authentication failed", Count: 2, First: base, Last: base.Add(time.Minute)},
			{Message: "<script>evil</script>", Count: 1, First: base, Last: base},
		},
		TotalErrors: 3,
		StatsSeries: []analysis.PgBouncerStatsPoint{
			{Time: base, Queries: 1, XactMicros: 100},
			{Time: base.Add(time.Second), Queries: 48, XactMicros: 2000},
		},
	}
}

// TestPgBouncerHTMLSmoke checks the pgBouncer-only HTML report renders
// through the shared web app: it carries the pgbouncer payload and the
// pgbouncer_only flag so the app suppresses the empty PostgreSQL sections.
func TestPgBouncerHTMLSmoke(t *testing.T) {
	var buf bytes.Buffer
	m := analysis.AggregatedMetrics{}
	pgb := samplePgBouncerMetrics()
	if err := ExportHTMLCombined(&buf, m, []analysis.PgBouncerMetrics{pgb}, HTMLReportInfo{Version: "test"}, []string{"all"}, true); err != nil {
		t.Fatalf("ExportHTMLCombined: %v", err)
	}
	out := buf.String()
	// The payload is zstd+base64-compressed in the page; the section is
	// rendered client-side from it. Assert the report scaffold is present
	// and the page is a non-trivial standalone document.
	if !strings.Contains(out, "COMPRESSED_DATA") {
		t.Error("HTML report missing embedded COMPRESSED_DATA payload")
	}
	if len(out) < 10000 {
		t.Errorf("HTML report suspiciously small (%d bytes)", len(out))
	}
}

// TestExportJSONCombined checks the "pgbouncer" key: one entry per pooler,
// the same shape in a mixed and in a pure pgBouncer run, absent from a pure
// PostgreSQL run.
func TestExportJSONCombined(t *testing.T) {
	m := analysis.AggregatedMetrics{}
	m.Global.Count = 1
	pgb := []analysis.PgBouncerMetrics{samplePgBouncerMetrics()}

	decode := func(pgb []analysis.PgBouncerMetrics, pgbOnly bool) map[string]interface{} {
		t.Helper()
		var buf bytes.Buffer
		ExportJSONCombined(&buf, m, pgb, []string{"all"}, true, true, pgbOnly)
		var got map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return got
	}
	for _, pgbOnly := range []bool{false, true} {
		got := decode(pgb, pgbOnly)
		list, ok := got["pgbouncer"].([]interface{})
		if !ok || len(list) != 1 {
			t.Fatalf("pgbOnly=%v: expected a one-pooler pgbouncer list, got %T", pgbOnly, got["pgbouncer"])
		}
		if n := list[0].(map[string]interface{})["login_attempts"]; n != 10.0 {
			t.Errorf("pgbOnly=%v: pgbouncer[0].login_attempts = %v, want 10", pgbOnly, n)
		}
		if _, present := got["summary"]; present == pgbOnly {
			t.Errorf("pgbOnly=%v: summary present = %v", pgbOnly, present)
		}
	}
	if _, present := decode(nil, false)["pgbouncer"]; present {
		t.Error("pgbouncer key present in a pure PostgreSQL payload")
	}
}
