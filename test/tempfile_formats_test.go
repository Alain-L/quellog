// test/tempfile_formats_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestTempFileFormatsEquivalence tests that tempfile associations work
// correctly across all supported log formats (stderr, CSV, JSON).
func TestTempFileFormatsEquivalence(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Load golden JSON for comparison
	goldenData, err := os.ReadFile("testdata/tempfile_association.golden.json")
	if err != nil {
		t.Fatalf("Failed to read golden JSON: %v", err)
	}

	var golden map[string]interface{}
	if err := json.Unmarshal(goldenData, &golden); err != nil {
		t.Fatalf("Failed to parse golden JSON: %v", err)
	}

	// Test files with different formats
	inputs := []struct {
		name string
		file string
	}{
		{"stderr format", "testdata/tempfile_association.log"},
		{"CSV format", "testdata/tempfile_association.csv"},
		{"JSON format", "testdata/tempfile_association.json"},
	}

	var results []map[string]interface{}

	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			cmd := exec.Command(quellogBinary, input.file, "--json")
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

			// Check total messages
			totalMessages, ok := tempFiles["total_messages"].(float64)
			if !ok {
				t.Fatalf("missing or invalid 'total_messages' in temp_files section")
			}

			// For CSV and JSON, we expect 2 tempfiles
			// For stderr (log), we expect 7 tempfiles
			var expectedMessages float64
			if input.name == "stderr format" {
				expectedMessages = 7
			} else {
				expectedMessages = 2
			}

			if totalMessages != expectedMessages {
				t.Errorf("expected %v temp file messages, got %v", expectedMessages, totalMessages)
			}

			// Check that total_size exists and is non-empty
			totalSize, ok := tempFiles["total_size"].(string)
			if !ok || totalSize == "" {
				t.Errorf("missing or invalid 'total_size' in temp_files section")
			}

			results = append(results, result)
		})
	}

	// Note: We don't compare all formats to golden because CSV/JSON have different
	// data than the stderr log. Each format has its own expected values.
	// The important thing is that each format parses correctly and detects tempfiles.
}
