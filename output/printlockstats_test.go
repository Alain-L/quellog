package output

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	f()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// TestPrintLockStatsDeterministic guards the tie-break: printLockStats sorts
// map entries by count, and without a secondary key the equal-count entries
// (very common in the "Relations" list, where many tables appear once) came
// out in Go's randomized map-iteration order — different on every run.
func TestPrintLockStatsDeterministic(t *testing.T) {
	// Many count=1 ties to make randomized order overwhelmingly likely on the
	// unfixed code; one higher count to check primary ordering still holds.
	stats := map[string]int{
		"alpha": 1, "bravo": 1, "charlie": 1, "delta": 1, "echo": 1,
		"foxtrot": 1, "golf": 1, "hotel": 1, "india": 1, "juliet": 1,
		"kilo": 3,
	}
	first := captureStdout(t, func() { printLockStats(stats, 13) })
	for i := 0; i < 50; i++ {
		if got := captureStdout(t, func() { printLockStats(stats, 13) }); got != first {
			t.Fatalf("printLockStats non-deterministic across runs:\n--- run 0 ---\n%s\n--- run %d ---\n%s", first, i+1, got)
		}
	}

	// Highest count first, then alphabetical on ties.
	lines := strings.Split(strings.TrimRight(first, "\n"), "\n")
	if len(lines) != 11 {
		t.Fatalf("expected 11 lines, got %d:\n%s", len(lines), first)
	}
	if !strings.Contains(lines[0], "kilo") {
		t.Errorf("highest count should be first, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[2], "bravo") {
		t.Errorf("ties should be alphabetical, got %q then %q", lines[1], lines[2])
	}
}
