// test/sql_normalization_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestSQLNormalizationEdgeCases tests that query normalization correctly handles:
// - Negative numbers (-100 → ?)
// - Decimal numbers (99.99 → ?)
// - Identifiers with numbers (table2, col_123, s5f, order_2023 → preserved)
func TestSQLNormalizationEdgeCases(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Run analysis on edge cases log
	cmd := exec.Command(quellogBinary, "testdata/sql_normalization_edge_cases.log", "--json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}

	// Verify SQL performance metrics exist
	sqlPerf, ok := result["sql_performance"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'sql_performance' section in output")
	}

	// Check total queries parsed
	totalQueries, ok := sqlPerf["total_queries_parsed"].(float64)
	if !ok {
		t.Fatalf("missing or invalid 'total_queries_parsed'")
	}
	if totalQueries != 8 {
		t.Errorf("expected 8 total queries, got %v", totalQueries)
	}

	// Check unique queries (normalization working correctly)
	// Expected: 4 unique (2 different SELECT, 1 INSERT, 1 UPDATE)
	uniqueQueries, ok := sqlPerf["total_unique_queries"].(float64)
	if !ok {
		t.Fatalf("missing or invalid 'total_unique_queries'")
	}
	if uniqueQueries != 4 {
		t.Errorf("expected 4 unique queries, got %v", uniqueQueries)
	}

	// Verify normalization is working by running text output and checking queries
	cmd2 := exec.Command(quellogBinary, "testdata/sql_normalization_edge_cases.log", "--sql-summary")
	var stdout2 bytes.Buffer
	cmd2.Stdout = &stdout2

	if err := cmd2.Run(); err != nil {
		t.Fatalf("failed to run text output: %v", err)
	}

	textOutput := stdout2.String()

	// Check that identifiers with numbers are preserved in normalized queries
	if !bytes.Contains(stdout2.Bytes(), []byte("table2")) {
		t.Error("expected 'table2' to be preserved in normalized queries")
	}
	if !bytes.Contains(stdout2.Bytes(), []byte("order_2023")) {
		t.Error("expected 'order_2023' to be preserved in normalized queries")
	}
	if !bytes.Contains(stdout2.Bytes(), []byte("col_123")) {
		t.Error("expected 'col_123' to be preserved in normalized queries")
	}
	if !bytes.Contains(stdout2.Bytes(), []byte("s5f")) {
		t.Error("expected 's5f' to be preserved in normalized queries")
	}

	// Check that numbers are normalized to ?
	if !bytes.Contains(stdout2.Bytes(), []byte("balance = ?")) {
		t.Error("expected negative numbers to be normalized: 'balance = ?'")
	}
	if !bytes.Contains(stdout2.Bytes(), []byte("values (?, ?)")) {
		t.Error("expected decimal numbers to be normalized: 'values (?, ?)'")
	}

	// Ensure literal numbers don't appear in normalized queries
	if bytes.Contains(stdout2.Bytes(), []byte("-100")) || bytes.Contains(stdout2.Bytes(), []byte("99.99")) {
		t.Errorf("literal numbers found in output, normalization may have failed:\n%s", textOutput)
	}
}
