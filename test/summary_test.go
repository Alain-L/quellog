// test/summary_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// TestSummaryReport verifies the JSON summary by comparing the parsed structures.
func TestSummaryReport(t *testing.T) {
	bin := "../bin/quellog"
	logFile := "testdata/test_summary.log"
	golden := "testdata/test_summary.golden.json"

	// Run the command under test
	cmd := exec.Command(bin, logFile, "--json", "--summary")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run command: %v", err)
	}
	gotBytes := out.Bytes()

	// If -update is specified, overwrite the golden file and exit
	if *update {
		if err := os.WriteFile(golden, gotBytes, 0644); err != nil {
			t.Fatalf("failed to update golden file: %v", err)
		}
		t.Logf("updated golden file: %s", golden)
		return
	}

	// Read the golden file
	wantBytes, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}

	// Unmarshal both into interface{} to compare their structure
	var gotJSON, wantJSON interface{}
	if err := json.Unmarshal(gotBytes, &gotJSON); err != nil {
		t.Fatalf("invalid JSON from command: %v\n%s", err, gotBytes)
	}
	if err := json.Unmarshal(wantBytes, &wantJSON); err != nil {
		t.Fatalf("invalid golden JSON: %v\n%s", err, wantBytes)
	}

	// Deep-compare the two JSON structures
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		// Re-marshal with indentation for a readable diff
		gotIndented, _ := json.MarshalIndent(gotJSON, "", "  ")
		wantIndented, _ := json.MarshalIndent(wantJSON, "", "  ")
		t.Errorf("JSON mismatch\n--- want ---\n%s\n--- got  ---\n%s\n", wantIndented, gotIndented)
	}
}
