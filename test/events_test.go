// test/events_test.go
package quellog_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

func TestEventsJSONOutput(t *testing.T) {
	// Path to your built CLI binary
	quellogBinary := "../bin/quellog_test"

	// List of input test files in different formats
	inputs := []string{
		"testdata/test_events.log",
		//"testdata/test_summary.csv",
		//"testdata/test_summary.json",
		//"testdata/test_summary.syslog",
	}

	var baseline interface{}

	for i, input := range inputs {
		// Run the CLI on each input with --json --summary
		cmd := exec.Command(quellogBinary, input, "--json", "--summary")
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
