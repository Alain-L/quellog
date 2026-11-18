package parser

import (
	"testing"
	"unsafe"
)

// TestLogEntrySize pins LogEntry's footprint, multiplied by every line of
// every run: pgBackRest-only fields go behind the PgBackRest pointer.
func TestLogEntrySize(t *testing.T) {
	const want = 80
	if got := unsafe.Sizeof(LogEntry{}); got != want {
		t.Errorf("unsafe.Sizeof(LogEntry{}) = %d, want %d", got, want)
	}
}
