package quellog_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Format-targeted regression tests for the syslog variants and the
// stdin path. The stderr/UTC and stderr/CET formats are exercised by
// every other fixture in the corpus.

// TestRegression_SyslogBSDFormat verifies the BSD syslog format
// (`Apr 20 08:00:00 dbhost01 postgres[30001]: ...`) parses correctly,
// timestamps are recognized and continuation lines fold.
func TestRegression_SyslogBSDFormat(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/parsers/syslog_bsd.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 7 {
		t.Errorf("summary.total_logs = %v, want 7 (8 raw lines, ERROR+STATEMENT folds into 1)", total)
	}
	events, _ := got["events"].([]any)
	counts := map[string]int{}
	for _, e := range events {
		m := e.(map[string]any)
		counts[m["type"].(string)] = int(m["count"].(float64))
	}
	if counts["LOG"] != 6 {
		t.Errorf("events.LOG.count = %d, want 6", counts["LOG"])
	}
	if counts["ERROR"] != 1 {
		t.Errorf("events.ERROR.count = %d, want 1", counts["ERROR"])
	}
	// Checkpoint metrics should be extracted from the BSD-formatted lines.
	if _, ok := got["checkpoints"]; !ok {
		t.Error("expected 'checkpoints' section, missing (BSD format checkpoint not parsed?)")
	}
}

// TestRegression_SyslogRFC5424Format verifies the RFC 5424 format
// (`<134>1 2026-04-20T09:00:00.100+00:00 host postgres 31001 - - ...`)
// parses correctly and that the priority field doesn't leak into the
// message text.
func TestRegression_SyslogRFC5424Format(t *testing.T) {
	got := runFixtureJSON(t, "testdata/regressions/parsers/syslog_rfc5424.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 5 {
		t.Errorf("summary.total_logs = %v, want 5 (6 lines, ERROR+STATEMENT folds)", total)
	}
	events, _ := got["events"].([]any)
	counts := map[string]int{}
	for _, e := range events {
		m := e.(map[string]any)
		counts[m["type"].(string)] = int(m["count"].(float64))
	}
	if counts["ERROR"] != 1 {
		t.Errorf("events.ERROR.count = %d, want 1", counts["ERROR"])
	}
	// The priority prefix '<134>1' must be stripped from the message —
	// it should not appear in any top_events signature.
	topEvents, _ := got["top_events"].([]any)
	for _, e := range topEvents {
		m := e.(map[string]any)
		if msg, _ := m["message"].(string); strings.Contains(msg, "<134>") {
			t.Errorf("priority prefix leaked into top_events.message: %q", msg)
		}
	}
}

// TestRegression_StdinInput verifies that piping a log through stdin
// works end to end: format autodetect, parsing, and a coherent JSON
// output.
func TestRegression_StdinInput(t *testing.T) {
	if harnessBinary == "" {
		t.Fatal("harness binary not built")
	}
	fixture, err := os.ReadFile("testdata/regressions/locks/deadlock_basic.log")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	cmd := exec.Command(harnessBinary, "-", "--json")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := io.Copy(stdin, bytes.NewReader(fixture)); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v\nstderr: %s", err, stderr.String())
	}

	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}

	// Sanity: same content as running on the file directly.
	summary := doc["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total < 2 {
		t.Errorf("stdin input produced %v logs, expected at least 2", total)
	}
	if locks, ok := doc["locks"].(map[string]any); ok {
		if dl, _ := locks["deadlock_events"].(float64); dl != 1 {
			t.Errorf("locks.deadlock_events = %v via stdin, want 1", dl)
		}
	} else {
		t.Error("missing 'locks' section in stdin output")
	}
}
