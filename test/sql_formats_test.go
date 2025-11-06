// test/sql_formats_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestSQLFormatsEquivalence tests that different PostgreSQL logging formats
// (simple statements, prepared statements, extended protocol) produce the same
// SQL analysis results when the underlying queries are identical.
func TestSQLFormatsEquivalence(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test")

	quellogBinary := "../quellog_test"

	// Test files with identical queries logged in different formats
	inputs := []struct {
		name string
		file string
	}{
		{"simple statements", "testdata/sql_simple.log"},
		{"prepared statements", "testdata/sql_prepared.log"},
		{"extended protocol", "testdata/sql_extended.log"},
	}

	// Expected results (manually counted from test files):
	// - 10 total queries executed
	// - Total duration: 99.192 ms
	// - Queries: SELECT (5x), INSERT (3x), UPDATE (2x)
	//
	// Note: All 3 formats log the same underlying queries, just in different
	// PostgreSQL logging formats (simple statement, prepared statement, extended protocol)

	var results []map[string]interface{}

	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			cmd := exec.Command(quellogBinary, input.file, "--json")
			var stdout bytes.Buffer
			cmd.Stdout = &stdout

			if err := cmd.Run(); err != nil {
				t.Fatalf("failed to run %s: %v", input.file, err)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON from %s: %v\n%s", input.file, err, stdout.String())
			}

			// Verify SQL performance metrics exist
			sqlPerf, ok := result["sql_performance"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing or invalid 'sql_performance' section in output")
			}

			// Check total queries parsed
			totalQueries, ok := sqlPerf["total_queries_parsed"].(float64)
			if !ok {
				t.Fatalf("missing or invalid 'total_queries_parsed' in sql_performance section")
			}
			if totalQueries != 10 {
				t.Errorf("expected 10 total queries, got %v", totalQueries)
			}

			// Check total query duration
			totalDuration, ok := sqlPerf["total_query_duration"].(string)
			if !ok {
				t.Fatalf("missing or invalid 'total_query_duration' in sql_performance section")
			}
			// Expected: "99 ms" (rounded)
			if totalDuration != "99 ms" {
				t.Errorf("expected total duration '99 ms', got '%s'", totalDuration)
			}

		// Check unique queries (normalization test)
		uniqueQueries, ok := sqlPerf["total_unique_queries"].(float64)
		if !ok {
			t.Fatalf("missing or invalid 'total_unique_queries' in sql_performance section")
		}
		if uniqueQueries != 3 {
			t.Errorf("expected 3 unique queries (normalization working), got %v", uniqueQueries)
		}

			results = append(results, result)
		})
	}

	// Verify all 3 formats produce identical SQL metrics
	if len(results) == 3 {
		baseline := results[0]["sql_performance"].(map[string]interface{})
		for i := 1; i < 3; i++ {
			other := results[i]["sql_performance"].(map[string]interface{})

			// Compare key metrics
			if baseline["total_queries_parsed"] != other["total_queries_parsed"] {
				t.Errorf("total_queries_parsed mismatch between formats: %v vs %v",
					baseline["total_queries_parsed"], other["total_queries_parsed"])
			}
			if baseline["total_query_duration"] != other["total_query_duration"] {
				t.Errorf("total_query_duration mismatch between formats: %v vs %v",
					baseline["total_query_duration"], other["total_query_duration"])
			}
			if baseline["query_max_duration"] != other["query_max_duration"] {
				t.Errorf("query_max_duration mismatch between formats: %v vs %v",
					baseline["query_max_duration"], other["query_max_duration"])
			}
			if baseline["query_min_duration"] != other["query_min_duration"] {
				t.Errorf("query_min_duration mismatch between formats: %v vs %v",
					baseline["query_min_duration"], other["query_min_duration"])
			}
		}
	}
}
