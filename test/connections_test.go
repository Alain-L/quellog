// test/connection_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestConnectionsJSONOutput(t *testing.T) {
	// Build the binary from source
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".." // Remonte au root du projet
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer os.Remove("../quellog_test") // Cleanup

	quellogBinary := "../quellog_test"

	// List of input test files in different formats
	inputs := []string{
		"testdata/test_connections.log",
		//"testdata/test_connection.csv",
		//"testdata/test_connection.json",
		//"testdata/test_connection.syslog",
	}

	var baseline interface{}

	for i, input := range inputs {
		// Run the CLI on each input with --json --connection
		cmd := exec.Command(quellogBinary, input, "--json", "--connections")
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

		if i == 0 {
			// first format becomes our baseline
			baseline = got
		} else {
			// compare to baseline
			if !reflect.DeepEqual(baseline, got) {
				// pretty-print both for debugging
				bs, _ := json.MarshalIndent(baseline, "", "  ")
				gs, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("JSON output for %s diverges from baseline (%s):\n--- baseline ---\n%s\n--- got ---\n%s",
					input, inputs[0], string(bs), string(gs))
			}
		}
	}
}
