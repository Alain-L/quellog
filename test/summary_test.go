// test/summary_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestSummaryJSONOutput(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".." // Remonte au root du projet
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test") // Cleanup

	quellogBinary := "../quellog_test"

	// Load the golden file
	goldenFile := "testdata/test_summary.golden.json"
	goldenJSON, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("Failed to read golden file: %v", err)
	}

	var baseline interface{}
	if err := json.Unmarshal(goldenJSON, &baseline); err != nil {
		t.Fatalf("Failed to unmarshal golden file: %v", err)
	}

	// List of input test files in different formats
	inputs := []string{
		"testdata/test_summary.log",
		"testdata/test_summary.csv",
		"testdata/test_summary.json",
		"testdata/test_summary_sys.log",
		"testdata/test_summary.log.gz",
		"testdata/test_summary.csv.gz",
		"testdata/test_summary.json.gz",
		"testdata/test_summary_sys.log.gz",
		"testdata/test_summary.tar",
		"testdata/test_summary.tar.gz",
		"testdata/test_summary.log.zst",
		"testdata/test_summary.csv.zst",
		"testdata/test_summary.json.zst",
		"testdata/test_summary_sys.log.zst",
		"testdata/test_summary.tar.zst",
		"testdata/test_summary.log.zstd",
		"testdata/test_summary.csv.zstd",
		"testdata/test_summary.json.zstd",
		"testdata/test_summary_sys.log.zstd",
		"testdata/test_summary.tar.zstd",
		"testdata/test_summary.tzst",
	}

	for _, input := range inputs {
		// Run the CLI on each input with --json
		cmd := exec.Command(quellogBinary, input, "--json")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stdout // include stderr in case of format detection messages

		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s: %v", input, err)
		}

		// Unmarshal the output JSON
		var got interface{}
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("invalid JSON from %s: %v\n%s", input, err, stdout.String())
		}

		// compare to baseline
		if !reflect.DeepEqual(baseline, got) {
			// pretty-print both for debugging
			bs, _ := json.MarshalIndent(baseline, "", "  ")
			gs, _ := json.MarshalIndent(got, "", "  ")
			t.Errorf("JSON output for %s diverges from golden file:\n--- golden ---\n%s\n--- got ---\n%s",
				input, string(bs), string(gs))
		}
	}
}
