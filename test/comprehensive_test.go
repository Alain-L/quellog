// test/comprehensive_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

// TestComprehensiveFormatParity verifies that all PostgreSQL log formats
// produce identical analysis results when processing the same database events.
//
// The test uses fixtures generated from a real PostgreSQL instance where all
// log formats were enabled simultaneously. This ensures the same events are
// captured in each format.
//
// Formats tested:
//   - stderr.log: PostgreSQL text format with custom log_line_prefix
//   - csvlog.csv: PostgreSQL CSV format
//   - jsonlog.json: PostgreSQL JSON format (PG15+)
//   - syslog_bsd.log: BSD syslog format via syslog-ng
//
// All metrics must be identical across formats. Any divergence indicates
// a parsing or analysis bug.
func TestComprehensiveFormatParity(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Test files - all generated from the same PostgreSQL instance
	formats := []struct {
		name string
		file string
	}{
		{"stderr", "testdata/stderr.log"},
		{"csv", "testdata/csvlog.csv"},
		{"json", "testdata/jsonlog.json"},
		{"syslog_bsd", "testdata/syslog_bsd.log"},
	}

	// Parse all formats and collect results
	results := make(map[string]map[string]interface{})

	for _, format := range formats {
		cmd := exec.Command(quellogBinary, format.file, "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run quellog on %s: %v", format.file, err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON from %s: %v\n%s", format.file, err, stdout.String())
		}

		results[format.name] = result
	}

	// Use stderr as reference (it's the most common format)
	reference := results["stderr"]
	referenceMetrics := extractKeyMetrics(t, reference)

	// Compare all other formats to reference
	for _, format := range formats[1:] {
		t.Run(format.name+"_vs_stderr", func(t *testing.T) {
			metrics := extractKeyMetrics(t, results[format.name])

			if !reflect.DeepEqual(referenceMetrics, metrics) {
				t.Errorf("Format %s diverges from stderr:\n  stderr: %+v\n  %s: %+v",
					format.name, referenceMetrics, format.name, metrics)
			}
		})
	}
}

// keyMetrics holds the essential metrics that must be identical across formats
type keyMetrics struct {
	TotalLogs        int
	Checkpoints      int
	Connections      int
	Disconnections   int
	SQLQueries       int
	SQLUniqueQueries int
	UniqueUsers      int
	UniqueApps       int
	UniqueHosts      int
	EventLOG         int
	EventERROR       int
	EventFATAL       int
}

// extractKeyMetrics extracts comparable metrics from a quellog JSON result
func extractKeyMetrics(t *testing.T, result map[string]interface{}) keyMetrics {
	t.Helper()

	metrics := keyMetrics{}

	// Summary
	if summary, ok := result["summary"].(map[string]interface{}); ok {
		metrics.TotalLogs = int(getFloat(summary, "total_logs"))
	}

	// Checkpoints
	if checkpoints, ok := result["checkpoints"].(map[string]interface{}); ok {
		metrics.Checkpoints = int(getFloat(checkpoints, "total_checkpoints"))
	}

	// Connections
	if connections, ok := result["connections"].(map[string]interface{}); ok {
		metrics.Connections = int(getFloat(connections, "connection_count"))
		metrics.Disconnections = int(getFloat(connections, "disconnection_count"))
	}

	// SQL Performance
	if sql, ok := result["sql_performance"].(map[string]interface{}); ok {
		metrics.SQLQueries = int(getFloat(sql, "total_queries_parsed"))
		metrics.SQLUniqueQueries = int(getFloat(sql, "total_unique_queries"))
	}

	// Clients
	if clients, ok := result["clients"].(map[string]interface{}); ok {
		metrics.UniqueUsers = int(getFloat(clients, "unique_users"))
		metrics.UniqueApps = int(getFloat(clients, "unique_apps"))
		metrics.UniqueHosts = int(getFloat(clients, "unique_hosts"))
	}

	// Events
	if events, ok := result["events"].([]interface{}); ok {
		for _, e := range events {
			if event, ok := e.(map[string]interface{}); ok {
				eventType, _ := event["type"].(string)
				count := int(getFloat(event, "count"))
				switch eventType {
				case "LOG":
					metrics.EventLOG = count
				case "ERROR":
					metrics.EventERROR = count
				case "FATAL":
					metrics.EventFATAL = count
				}
			}
		}
	}

	return metrics
}

// getFloat safely extracts a float64 from a map, returning 0 if not found
func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// TestSyslogAllFormats verifies that all syslog formats are parseable and produce
// consistent results. ISO and RFC5424 formats have pre-collector messages that
// don't appear in stderr, so they are compared among themselves.
func TestSyslogAllFormats(t *testing.T) {
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

	results := make(map[string]keyMetrics)

	for _, format := range formats {
		cmd := exec.Command(quellogBinary, format.file, "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run quellog on %s: %v", format.file, err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON from %s: %v\n%s", format.file, err, stdout.String())
		}

		results[format.name] = extractKeyMetrics(t, result)
	}

	// ISO and RFC5424 should be identical (same pre-collector messages)
	t.Run("iso_vs_rfc5424", func(t *testing.T) {
		if !reflect.DeepEqual(results["iso"], results["rfc5424"]) {
			t.Errorf("ISO and RFC5424 diverge:\n  iso: %+v\n  rfc5424: %+v",
				results["iso"], results["rfc5424"])
		}
	})

	// All formats should have the same total_logs and structural metrics
	t.Run("structural_parity", func(t *testing.T) {
		bsd := results["bsd"]
		iso := results["iso"]

		// These should be identical regardless of pre-collector messages
		if bsd.TotalLogs != iso.TotalLogs {
			t.Errorf("TotalLogs differ: bsd=%d, iso=%d", bsd.TotalLogs, iso.TotalLogs)
		}
		if bsd.Checkpoints != iso.Checkpoints {
			t.Errorf("Checkpoints differ: bsd=%d, iso=%d", bsd.Checkpoints, iso.Checkpoints)
		}
		if bsd.Connections != iso.Connections {
			t.Errorf("Connections differ: bsd=%d, iso=%d", bsd.Connections, iso.Connections)
		}
		if bsd.SQLQueries != iso.SQLQueries {
			t.Errorf("SQLQueries differ: bsd=%d, iso=%d", bsd.SQLQueries, iso.SQLQueries)
		}
	})
}

// TestComprehensiveCompression verifies that compressed files produce
// identical results to uncompressed files.
func TestComprehensiveCompression(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Reference file
	baseFile := "testdata/stderr.log"

	// Create compressed versions in temp directory
	tempDir := t.TempDir()

	// Get reference result
	cmd := exec.Command(quellogBinary, baseFile, "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run quellog on %s: %v", baseFile, err)
	}

	var reference map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &reference); err != nil {
		t.Fatalf("invalid JSON from %s: %v", baseFile, err)
	}
	referenceMetrics := extractKeyMetrics(t, reference)

	// Test gzip compression
	t.Run("gzip", func(t *testing.T) {
		gzFile := tempDir + "/stderr.log.gz"
		gzipCmd := exec.Command("gzip", "-c", baseFile)
		gzOut, err := os.Create(gzFile)
		if err != nil {
			t.Fatalf("failed to create gzip file: %v", err)
		}
		gzipCmd.Stdout = gzOut
		if err := gzipCmd.Run(); err != nil {
			gzOut.Close()
			t.Fatalf("failed to gzip: %v", err)
		}
		gzOut.Close()

		cmd := exec.Command(quellogBinary, gzFile, "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run quellog on gzip file: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON from gzip: %v", err)
		}

		metrics := extractKeyMetrics(t, result)
		if !reflect.DeepEqual(referenceMetrics, metrics) {
			t.Errorf("gzip result differs from uncompressed:\n  uncompressed: %+v\n  gzip: %+v",
				referenceMetrics, metrics)
		}
	})

	// Test zstd compression
	t.Run("zstd", func(t *testing.T) {
		// Check if zstd is available
		if _, err := exec.LookPath("zstd"); err != nil {
			t.Skip("zstd not available")
		}

		zstFile := tempDir + "/stderr.log.zst"
		zstdCmd := exec.Command("zstd", "-q", "-o", zstFile, baseFile)
		if err := zstdCmd.Run(); err != nil {
			t.Fatalf("failed to zstd: %v", err)
		}

		cmd := exec.Command(quellogBinary, zstFile, "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run quellog on zstd file: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON from zstd: %v", err)
		}

		metrics := extractKeyMetrics(t, result)
		if !reflect.DeepEqual(referenceMetrics, metrics) {
			t.Errorf("zstd result differs from uncompressed:\n  uncompressed: %+v\n  zstd: %+v",
				referenceMetrics, metrics)
		}
	})
}
