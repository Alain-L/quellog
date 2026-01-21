// test/summary_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestSummaryJSONOutput verifies that the JSON output matches the golden file.
// Uses the comprehensive fixtures generated from a real PostgreSQL instance.
func TestSummaryJSONOutput(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Load the golden file (based on comprehensive/stderr.log)
	goldenFile := "testdata/golden.json"
	goldenJSON, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("Failed to read golden file: %v", err)
	}

	var baseline interface{}
	if err := json.Unmarshal(goldenJSON, &baseline); err != nil {
		t.Fatalf("Failed to unmarshal golden file: %v", err)
	}

	// Run quellog on stderr.log and compare to golden
	cmd := exec.Command(quellogBinary, "testdata/stderr.log", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run quellog: %v", err)
	}

	var got interface{}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}

	if !reflect.DeepEqual(baseline, got) {
		bs, _ := json.MarshalIndent(baseline, "", "  ")
		gs, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("JSON output diverges from golden file:\n--- golden ---\n%s\n--- got ---\n%s",
			string(bs), string(gs))
	}
}

// TestAllSectionsJSONOutput verifies that all section flags produce valid JSON output.
// Each section is tested individually with --json to ensure proper JSON structure.
func TestAllSectionsJSONOutput(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"
	testFile := "testdata/stderr.log"

	// All section flags with their expected top-level JSON keys
	sections := []struct {
		name         string
		flags        []string
		expectedKeys []string // Keys that MUST be present in JSON output
	}{
		{
			name:         "default",
			flags:        []string{"--json"},
			expectedKeys: []string{"summary", "checkpoints", "events", "connections", "clients", "maintenance", "locks", "temp_files", "sql_performance"},
		},
		{
			name:         "summary",
			flags:        []string{"--summary", "--json"},
			expectedKeys: []string{"summary"},
		},
		{
			name:         "checkpoints",
			flags:        []string{"--checkpoints", "--json"},
			expectedKeys: []string{"checkpoints"},
		},
		{
			name:         "events",
			flags:        []string{"--events", "--json"},
			expectedKeys: []string{"events"},
		},
		{
			name:         "connections",
			flags:        []string{"--connections", "--json"},
			expectedKeys: []string{"connections"},
		},
		{
			name:         "clients",
			flags:        []string{"--clients", "--json"},
			expectedKeys: []string{"clients"},
		},
		{
			name:         "maintenance",
			flags:        []string{"--maintenance", "--json"},
			expectedKeys: []string{"maintenance"},
		},
		{
			name:         "locks",
			flags:        []string{"--locks", "--json"},
			expectedKeys: []string{"locks"},
		},
		{
			name:         "tempfiles",
			flags:        []string{"--tempfiles", "--json"},
			expectedKeys: []string{"temp_files"},
		},
		{
			name:         "sql_summary",
			flags:        []string{"--sql-summary", "--json"},
			expectedKeys: []string{"sql_performance"},
		},
		{
			name:         "sql_performance",
			flags:        []string{"--sql-performance", "--json"},
			expectedKeys: []string{"total_queries_parsed", "total_unique_queries"},
		},
	}

	for _, section := range sections {
		t.Run(section.name, func(t *testing.T) {
			args := append([]string{testFile}, section.flags...)
			cmd := exec.Command(quellogBinary, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				t.Fatalf("failed to run quellog with %v: %v\nstderr: %s", section.flags, err, stderr.String())
			}

			// Verify output is valid JSON
			var result map[string]interface{}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON output for %s: %v\noutput: %s", section.name, err, stdout.String())
			}

			// Verify expected keys are present
			for _, key := range section.expectedKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("expected key %q not found in JSON output for %s\nkeys present: %v",
						key, section.name, getKeys(result))
				}
			}

			// Verify JSON is not empty (has some content)
			if len(result) == 0 {
				t.Errorf("JSON output for %s is empty", section.name)
			}
		})
	}
}

// TestSectionJSONStructure verifies that specific sections have proper structure
func TestSectionJSONStructure(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"
	testFile := "testdata/stderr.log"

	t.Run("summary_structure", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--summary", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		summary, ok := result["summary"].(map[string]interface{})
		if !ok {
			t.Fatal("summary section missing or wrong type")
		}

		// Check required fields in summary
		requiredFields := []string{"total_logs", "start_date", "end_date", "duration"}
		for _, field := range requiredFields {
			if _, ok := summary[field]; !ok {
				t.Errorf("summary missing required field: %s", field)
			}
		}
	})

	t.Run("checkpoints_structure", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--checkpoints", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		checkpoints, ok := result["checkpoints"].(map[string]interface{})
		if !ok {
			t.Fatal("checkpoints section missing or wrong type")
		}

		// Check required fields
		requiredFields := []string{"total_checkpoints", "avg_checkpoint_time"}
		for _, field := range requiredFields {
			if _, ok := checkpoints[field]; !ok {
				t.Errorf("checkpoints missing required field: %s", field)
			}
		}
	})

	t.Run("connections_structure", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--connections", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		connections, ok := result["connections"].(map[string]interface{})
		if !ok {
			t.Fatal("connections section missing or wrong type")
		}

		// Check required fields
		requiredFields := []string{"connection_count", "disconnection_count"}
		for _, field := range requiredFields {
			if _, ok := connections[field]; !ok {
				t.Errorf("connections missing required field: %s", field)
			}
		}
	})

	t.Run("sql_performance_structure", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--sql-performance", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		// --sql-performance exports a flat structure (dedicated export)
		// Check required fields at root level
		requiredFields := []string{"total_queries_parsed", "total_unique_queries", "total_query_duration"}
		for _, field := range requiredFields {
			if _, ok := result[field]; !ok {
				t.Errorf("sql_performance missing required field: %s", field)
			}
		}
	})

	t.Run("temp_files_structure", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--tempfiles", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		tempFiles, ok := result["temp_files"].(map[string]interface{})
		if !ok {
			t.Fatal("temp_files section missing or wrong type")
		}

		// Check required fields
		requiredFields := []string{"total_messages", "total_size"}
		for _, field := range requiredFields {
			if _, ok := tempFiles[field]; !ok {
				t.Errorf("temp_files missing required field: %s", field)
			}
		}
	})
}

// TestMultipleSectionsJSON verifies combining multiple sections works correctly
func TestMultipleSectionsJSON(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"
	testFile := "testdata/stderr.log"

	t.Run("summary_and_checkpoints", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile, "--summary", "--checkpoints", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		if _, ok := result["summary"]; !ok {
			t.Error("missing summary section")
		}
		if _, ok := result["checkpoints"]; !ok {
			t.Error("missing checkpoints section")
		}
	})

	t.Run("all_main_sections", func(t *testing.T) {
		cmd := exec.Command(quellogBinary, testFile,
			"--summary", "--checkpoints", "--events", "--connections", "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed: %v", err)
		}

		var result map[string]interface{}
		json.Unmarshal(stdout.Bytes(), &result)

		expectedKeys := []string{"summary", "checkpoints", "events", "connections"}
		for _, key := range expectedKeys {
			if _, ok := result[key]; !ok {
				t.Errorf("missing section: %s", key)
			}
		}
	})
}

// getKeys returns the keys of a map for error messages
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestJSONOutputValidity ensures JSON output is always syntactically valid
func TestJSONOutputValidity(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"
	testFile := "testdata/stderr.log"

	// Test that JSON output starts with { and ends with }
	cmd := exec.Command(quellogBinary, testFile, "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(output, "{") {
		t.Errorf("JSON output should start with '{', got: %.50s...", output)
	}
	if !strings.HasSuffix(output, "}") {
		t.Errorf("JSON output should end with '}'")
	}

	// Verify it's valid JSON
	var result interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Errorf("JSON output is not valid: %v", err)
	}
}
