// test/client_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

func TestClientsJSONOutput(t *testing.T) {
	// Path to your built CLI binary
	quellogBinary := "../bin/quellog_test"

	// List of input test files in different formats
	inputs := []string{
		"testdata/test_clients.log",
		//"testdata/test_client.csv",
		//"testdata/test_client.json",
		//"testdata/test_client.syslog",
	}

	var baseline interface{}

	for i, input := range inputs {
		// Run the CLI on each input with --json --client
		cmd := exec.Command(quellogBinary, input, "--json", "--clients")
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
