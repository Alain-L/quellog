// test/tempfile_association_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestTempFileAnalysis tests that temporary files are correctly detected and analyzed
// using the comprehensive fixtures generated from a real PostgreSQL instance.
//
// The comprehensive fixtures contain 7 temporary file events from parallel query execution.
func TestTempFileAnalysis(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Run analysis on comprehensive stderr fixture
	cmd := exec.Command(quellogBinary, "testdata/stderr.log", "--tempfiles", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}

	// Verify temp files section exists
	tempFiles, ok := result["temp_files"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'temp_files' section in output")
	}

	// Check total temp file messages (comprehensive fixtures have 7)
	totalMessages, ok := tempFiles["total_messages"].(float64)
	if !ok {
		t.Fatalf("missing or invalid 'total_messages' in temp_files section")
	}
	if totalMessages != 7 {
		t.Errorf("expected 7 temp file messages, got %v", totalMessages)
	}

	// Check total size exists and is reasonable
	totalSize, ok := tempFiles["total_size"].(string)
	if !ok || totalSize == "" {
		t.Fatalf("missing or invalid 'total_size' in temp_files section")
	}

	// Check that events array exists
	events, ok := tempFiles["events"].([]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'events' in temp_files section")
	}
	if len(events) != 7 {
		t.Errorf("expected 7 events, got %d", len(events))
	}

	// Verify each event has required fields
	for i, e := range events {
		event, ok := e.(map[string]interface{})
		if !ok {
			t.Errorf("event %d is not a map", i)
			continue
		}

		if _, ok := event["timestamp"]; !ok {
			t.Errorf("event %d missing timestamp", i)
		}
		if _, ok := event["size"]; !ok {
			t.Errorf("event %d missing size", i)
		}
	}

	// Check that queries array exists (query association)
	queries, ok := tempFiles["queries"].([]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'queries' in temp_files section")
	}
	if len(queries) == 0 {
		t.Error("expected at least one query association")
	}

	// Verify queries have required fields
	for i, q := range queries {
		query, ok := q.(map[string]interface{})
		if !ok {
			t.Errorf("query %d is not a map", i)
			continue
		}

		requiredFields := []string{"id", "normalized_query", "count", "total_size"}
		for _, field := range requiredFields {
			if _, ok := query[field]; !ok {
				t.Errorf("query %d missing field: %s", i, field)
			}
		}
	}
}

// TestTempFileTextOutput verifies that text output includes tempfile information
func TestTempFileTextOutput(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Run text output
	cmd := exec.Command(quellogBinary, "testdata/stderr.log", "--tempfiles")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run: %v", err)
	}

	output := stdout.String()

	// Check for tempfiles section header
	if !bytes.Contains(stdout.Bytes(), []byte("TEMP FILES")) {
		t.Error("expected 'TEMP FILES' section in text output")
	}

	// Check for total count
	if !bytes.Contains(stdout.Bytes(), []byte("7")) {
		t.Error("expected tempfile count '7' in text output")
	}

	// Check for size information
	if !bytes.Contains(stdout.Bytes(), []byte("MB")) {
		t.Errorf("expected size in MB in text output, got: %s", output)
	}
}
