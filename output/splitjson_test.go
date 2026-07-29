package output

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/klauspost/compress/zstd"
)

// TestSplitPeriodsJSON freezes the contract the JS period navigator depends on:
// SplitPeriodsJSON must return [{label, entries, errors, data}] per bucket, and
// each data blob must base64+zstd-decode to a report whose summary.total_logs
// equals that period's entry count and whose meta carries the source identity.
func TestSplitPeriodsJSON(t *testing.T) {
	start := time.Date(2026, 2, 4, 8, 0, 0, 0, time.UTC)
	buckets := []analysis.SplitBucket{
		{Label: "2026-02-04 08:00", Start: start, End: start.Add(time.Hour),
			Metrics: analysis.AggregatedMetrics{Global: analysis.GlobalMetrics{
				Count: 5, ErrorCount: 2, MinTimestamp: start, MaxTimestamp: start.Add(30 * time.Minute)}}},
		{Label: "2026-02-04 09:00", Start: start.Add(time.Hour), End: start.Add(2 * time.Hour),
			Metrics: analysis.AggregatedMetrics{Global: analysis.GlobalMetrics{
				Count: 3, FatalCount: 1, MinTimestamp: start.Add(time.Hour), MaxTimestamp: start.Add(90 * time.Minute)}}},
	}
	info := HTMLReportInfo{Filename: "test.log", FileSize: 1234, Format: "stderr"}

	out, err := SplitPeriodsJSON(buckets, info, []string{"all"})
	if err != nil {
		t.Fatalf("SplitPeriodsJSON: %v", err)
	}

	var periods []struct {
		Label   string `json:"label"`
		Entries int    `json:"entries"`
		Errors  int    `json:"errors"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &periods); err != nil {
		t.Fatalf("periods JSON invalid: %v\n%s", err, out)
	}

	want := []struct {
		label   string
		entries int
		errors  int
	}{
		{"2026-02-04 08:00", 5, 2}, // errors = ErrorCount
		{"2026-02-04 09:00", 3, 1}, // errors = FatalCount
	}
	if len(periods) != len(want) {
		t.Fatalf("got %d periods, want %d", len(periods), len(want))
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer dec.Close()

	for i, w := range want {
		p := periods[i]
		if p.Label != w.label || p.Entries != w.entries || p.Errors != w.errors {
			t.Errorf("period %d = {%q,%d,%d}, want {%q,%d,%d}",
				i, p.Label, p.Entries, p.Errors, w.label, w.entries, w.errors)
		}
		raw, err := base64.URLEncoding.DecodeString(p.Data)
		if err != nil {
			t.Fatalf("period %d data not base64: %v", i, err)
		}
		jsonBytes, err := dec.DecodeAll(raw, nil)
		if err != nil {
			t.Fatalf("period %d zstd decode: %v", i, err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &doc); err != nil {
			t.Fatalf("period %d blob not JSON: %v", i, err)
		}
		summary, _ := doc["summary"].(map[string]interface{})
		if summary == nil {
			t.Fatalf("period %d blob has no summary section", i)
		}
		if total, _ := summary["total_logs"].(float64); int(total) != w.entries {
			t.Errorf("period %d blob summary.total_logs = %v, want %d", i, total, w.entries)
		}
		meta, _ := doc["meta"].(map[string]interface{})
		if meta == nil || meta["filename"] != "test.log" {
			t.Errorf("period %d meta.filename = %v, want test.log", i, meta["filename"])
		}
	}
}
