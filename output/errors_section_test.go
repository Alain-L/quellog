package output

import (
	"encoding/json"
	"testing"

	"github.com/Alain-L/quellog/analysis"
)

// TestErrorsSectionDropsNonErrorSeverities locks the monkey-test finding: the
// --errors section must restrict the events list to the error classes in JSON
// (and YAML, which derives from it), just like text/markdown — not silently
// emit the full --events list including LOG/INFO/DEBUG/NOTICE.
func TestErrorsSectionDropsNonErrorSeverities(t *testing.T) {
	m := analysis.AggregatedMetrics{
		EventSummaries: []analysis.EventSummary{
			{Type: "LOG", Count: 3709, Percentage: 97.0},
			{Type: "FATAL", Count: 82, Percentage: 2.1},
			{Type: "ERROR", Count: 19, Percentage: 0.5},
			{Type: "WARNING", Count: 12, Percentage: 0.3},
			{Type: "NOTICE", Count: 5, Percentage: 0.1},
		},
		TopEvents: []analysis.EventStat{
			{ID: "lo-0001", Severity: "LOG", Count: 100, Message: "log line"},
			{ID: "fa-0001", Severity: "FATAL", Count: 80, Message: "boom"},
		},
	}

	types := func(sections []string) []string {
		s, err := ExportJSONString(m, sections)
		if err != nil {
			t.Fatalf("ExportJSONString(%v): %v", sections, err)
		}
		var doc struct {
			Events []struct {
				Type string `json:"type"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(s), &doc); err != nil {
			t.Fatalf("invalid JSON for %v: %v", sections, err)
		}
		out := make([]string, len(doc.Events))
		for i, e := range doc.Events {
			out[i] = e.Type
		}
		return out
	}

	contains := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}

	events := types([]string{"events"})
	if !contains(events, "LOG") || !contains(events, "NOTICE") {
		t.Errorf("--events should keep every severity, got %v", events)
	}

	errs := types([]string{"errors"})
	for _, sev := range []string{"LOG", "INFO", "DEBUG", "NOTICE"} {
		if contains(errs, sev) {
			t.Errorf("--errors must drop %s, got %v", sev, errs)
		}
	}
	for _, sev := range []string{"FATAL", "ERROR", "WARNING"} {
		if !contains(errs, sev) {
			t.Errorf("--errors must keep %s, got %v", sev, errs)
		}
	}
}

func TestNonErrorSeverity(t *testing.T) {
	for _, s := range []string{"LOG", "INFO", "DEBUG", "NOTICE"} {
		if !nonErrorSeverity(s) {
			t.Errorf("nonErrorSeverity(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"ERROR", "FATAL", "PANIC", "WARNING"} {
		if nonErrorSeverity(s) {
			t.Errorf("nonErrorSeverity(%q) = true, want false", s)
		}
	}
}
