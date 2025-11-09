// test/tempfile_association_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestTempFileQueryAssociation tests that temporary files are correctly associated
// with the SQL queries that generated them across all supported patterns.
//
// Patterns tested:
// - Pattern 1: tempfile → STATEMENT (temp file first, then STATEMENT line)
// - Pattern 2: duration/statement → tempfile (query cached by PID, then temp file)
//
// KNOWN LIMITATION: This test currently fails because the first query with
// "duration: statement:" appears BEFORE the first tempfile in the test file.
// For performance reasons (saves ~6s on 11GB files), queries are not cached
// until after the first tempfile is seen. This affects <0.01% of real-world
// tempfile associations. See analysis/temp_files.go Process() doc for details.
//
// TODO: Either accept this limitation and remove/adjust this test, or adjust
// the test file to have the first tempfile appear before the first query.
func TestTempFileQueryAssociation(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Run analysis on tempfile association log
	cmd := exec.Command(quellogBinary, "testdata/tempfile_association.log", "--json")
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

	// Check total temp file messages
	totalMessages, ok := tempFiles["total_messages"].(float64)
	if !ok {
		t.Fatalf("missing or invalid 'total_messages' in temp_files section")
	}
	if totalMessages != 7 {
		t.Errorf("expected 7 temp file messages, got %v", totalMessages)
	}

	// Check total size
	totalSize, ok := tempFiles["total_size"].(string)
	if !ok {
		t.Fatalf("missing or invalid 'total_size' in temp_files section")
	}
	if totalSize != "337.50 MB" {
		t.Errorf("expected total size '337.50 MB', got '%s'", totalSize)
	}

	// Note: Tempfile associations are currently only exported in text output,
	// not in JSON. The JSON only contains basic tempfile metrics.

	// Run text output to verify associations are displayed
	cmd2 := exec.Command(quellogBinary, "testdata/tempfile_association.log")
	var stdout2 bytes.Buffer
	cmd2.Stdout = &stdout2

	if err := cmd2.Run(); err != nil {
		t.Fatalf("failed to run text output: %v", err)
	}

	// Check that the "Queries generating temp files:" section exists
	if !bytes.Contains(stdout2.Bytes(), []byte("Queries generating temp files:")) {
		t.Error("expected 'Queries generating temp files:' section in text output")
	}

	// Check for specific query associations
	expectedAssociations := []string{
		"select * from large_table order by id",
		"select * from medium_table join large_table using (id)",
		"insert into archive select * from active where date < now()",
		"delete from temp_data where processed = true",
		"create index idx_huge on huge_table(col1, col2)",
		"update stats set count = count + ?",
		"select id from orphan_tempfile",
	}

	for _, query := range expectedAssociations {
		if !bytes.Contains(stdout2.Bytes(), []byte(query)) {
			t.Errorf("expected query '%s' in temp file associations, but not found", query)
		}
	}

	// Verify sizes are shown
	expectedSizes := []string{
		"100.00 MB",
		"50.00 MB",
		"20.00 MB",
		"10.00 MB",
		"150.00 MB",
		"5.00 MB",
		"2.50 MB",
	}

	for _, size := range expectedSizes {
		if !bytes.Contains(stdout2.Bytes(), []byte(size)) {
			t.Errorf("expected size '%s' in temp file associations output", size)
		}
	}
}
