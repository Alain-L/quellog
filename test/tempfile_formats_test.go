// test/tempfile_formats_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

// TestTempFileFormatsEquivalence tests that tempfile analysis produces
// identical results across all supported log formats (stderr, CSV, JSON, syslog).
//
// Uses the comprehensive fixtures generated from a real PostgreSQL instance
// where all formats logged the same events simultaneously.
func TestTempFileFormatsEquivalence(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// All comprehensive fixtures should have the same tempfile data
	formats := []struct {
		name string
		file string
	}{
		{"stderr", "testdata/stderr.log"},
		{"csv", "testdata/csvlog.csv"},
		{"json", "testdata/jsonlog.json"},
		{"syslog_bsd", "testdata/syslog_bsd.log"},
	}

	// Collect results from each format
	results := make(map[string]tempFileMetrics)

	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			cmd := exec.Command(quellogBinary, format.file, "--tempfiles", "--json")
			var stdout bytes.Buffer
			cmd.Stdout = &stdout

			if err := cmd.Run(); err != nil {
				t.Fatalf("failed to run: %v", err)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
			}

			// Verify temp_files section exists
			tempFiles, ok := result["temp_files"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing or invalid 'temp_files' section in output")
			}

			// Extract metrics
			metrics := tempFileMetrics{
				TotalMessages: int(getFloatValue(tempFiles, "total_messages")),
				TotalSize:     getStringValue(tempFiles, "total_size"),
				AvgSize:       getStringValue(tempFiles, "avg_size"),
			}

			// Count events
			if events, ok := tempFiles["events"].([]interface{}); ok {
				metrics.EventCount = len(events)
			}

			// Count queries
			if queries, ok := tempFiles["queries"].([]interface{}); ok {
				metrics.QueryCount = len(queries)
			}

			results[format.name] = metrics

			// All formats should have 7 tempfile messages
			if metrics.TotalMessages != 7 {
				t.Errorf("expected 7 temp file messages, got %d", metrics.TotalMessages)
			}
		})
	}

	// Compare all formats to stderr (reference)
	if len(results) < 2 {
		t.Skip("Not enough formats to compare")
	}

	reference := results["stderr"]

	t.Run("format_parity", func(t *testing.T) {
		for name, metrics := range results {
			if name == "stderr" {
				continue
			}

			if metrics.TotalMessages != reference.TotalMessages {
				t.Errorf("%s: TotalMessages %d != stderr %d", name, metrics.TotalMessages, reference.TotalMessages)
			}
			if metrics.TotalSize != reference.TotalSize {
				t.Errorf("%s: TotalSize %s != stderr %s", name, metrics.TotalSize, reference.TotalSize)
			}
			if metrics.EventCount != reference.EventCount {
				t.Errorf("%s: EventCount %d != stderr %d", name, metrics.EventCount, reference.EventCount)
			}
		}
	})
}

// tempFileMetrics holds metrics for comparison
type tempFileMetrics struct {
	TotalMessages int
	TotalSize     string
	AvgSize       string
	EventCount    int
	QueryCount    int
}

// TestTempFileSyslogFormats tests that all syslog variants produce identical results
func TestTempFileSyslogFormats(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// All syslog formats
	formats := []struct {
		name string
		file string
	}{
		{"bsd", "testdata/syslog_bsd.log"},
		{"iso", "testdata/syslog.log"},
		{"rfc5424", "testdata/syslog_rfc5424.log"},
	}

	results := make(map[string]tempFileMetrics)

	for _, format := range formats {
		cmd := exec.Command(quellogBinary, format.file, "--tempfiles", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run on %s: %v", format.name, err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON from %s: %v", format.name, err)
		}

		tempFiles, _ := result["temp_files"].(map[string]interface{})
		metrics := tempFileMetrics{
			TotalMessages: int(getFloatValue(tempFiles, "total_messages")),
			TotalSize:     getStringValue(tempFiles, "total_size"),
		}
		results[format.name] = metrics
	}

	// All syslog formats should be identical
	t.Run("syslog_parity", func(t *testing.T) {
		if !reflect.DeepEqual(results["bsd"], results["iso"]) {
			t.Errorf("BSD and ISO differ:\n  BSD: %+v\n  ISO: %+v", results["bsd"], results["iso"])
		}
		if !reflect.DeepEqual(results["iso"], results["rfc5424"]) {
			t.Errorf("ISO and RFC5424 differ:\n  ISO: %+v\n  RFC5424: %+v", results["iso"], results["rfc5424"])
		}
	})
}

// getFloatValue safely extracts a float64 from a map
func getFloatValue(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// getStringValue safely extracts a string from a map
func getStringValue(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
