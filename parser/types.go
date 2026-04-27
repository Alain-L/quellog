// Package parser provides types and interfaces for PostgreSQL log parsing.
package parser

import (
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single parsed PostgreSQL log entry.
// Each entry consists of a timestamp and the complete log message,
// including any continuation lines (DETAIL, HINT, STATEMENT, etc.).
//
// Example stderr format entry:
//
//	2025-01-01 12:00:00 CET LOG: database system is ready to accept connections
//
// Example CSV format entry:
//
//	2025-01-01 12:00:00.123 CET,,,12345,,LOG,00000,"database system is ready",,0
//
// Example JSON format entry:
//
//	{"timestamp":"2025-01-01T12:00:00Z","message":"database system is ready"}
type LogEntry struct {
	// Timestamp is the time when the log entry was created.
	// For formats without a year (e.g., syslog), the current year is assumed.
	Timestamp time.Time

	// Message is the complete log message text, including severity level.
	// Multi-line entries (continuation lines) are concatenated with spaces.
	//
	// Examples:
	//   "LOG: database system is ready"
	//   "ERROR: relation \"users\" does not exist HINT: Did you mean \"user\"?"
	Message string

	// IsContinuation indicates this entry is a continuation of a previous entry.
	// In stderr format, PostgreSQL emits DETAIL/HINT/STATEMENT/CONTEXT/QUERY as
	// separate lines with their own timestamp and log_line_prefix, but they belong
	// to the previous LOG/ERROR/etc entry. These should not be counted as separate
	// log entries for total_logs, but may still be processed by analyzers.
	IsContinuation bool

	// PID is the PostgreSQL backend process id extracted from the message
	// (the "[12345]" that follows log_line_prefix). Populated once by the
	// parser so downstream analyzers don't have to re-parse the message.
	// Empty string when no PID can be extracted.
	PID string
}

// NewLogEntry builds a LogEntry with its PID field pre-populated from
// the message. Parsers should use this helper at every construction
// site so downstream analyzers can read entry.PID directly instead of
// calling ExtractPID themselves on every entry. On a real log (J.log:
// 23M entries, 8 analyzer call sites) this avoids ~184M redundant
// parses of the same prefix.
func NewLogEntry(timestamp time.Time, message string, isContinuation bool) LogEntry {
	return LogEntry{
		Timestamp:      timestamp,
		Message:        message,
		IsContinuation: isContinuation,
		PID:            ExtractPID(message),
	}
}

// LogParser defines the interface that all format-specific parsers must implement.
// Parsers read a log file and stream parsed LogEntry records through a channel,
// enabling efficient processing of large log files without loading everything into memory.
//
// Implementations:
//   - StderrParser: Parses stderr/syslog format logs
//   - CsvParser: Parses PostgreSQL CSV format logs
//   - JsonParser: Parses JSON format logs
//
// Usage example:
//
//	entries := make(chan LogEntry, 1000)
//	parser := &StderrParser{}
//	go func() {
//	    if err := parser.Parse("postgresql.log", entries); err != nil {
//	        log.Fatal(err)
//	    }
//	    close(entries)
//	}()
//	for entry := range entries {
//	    // Process entry
//	}
type LogParser interface {
	// Parse reads a PostgreSQL log file and sends parsed entries to the output channel.
	// The parser is responsible for:
	//   - Opening and reading the file
	//   - Detecting and handling multi-line entries
	//   - Parsing timestamps and messages
	//   - Handling format-specific quirks
	//
	// The output channel should NOT be closed by the parser; the caller is responsible
	// for channel lifecycle management.
	//
	// Returns an error if:
	//   - The file cannot be opened or read
	//   - A critical parsing error occurs
	//
	// Note: Individual malformed lines may be logged as warnings but should not
	// cause the entire parsing operation to fail.
	Parse(filename string, out chan<- []LogEntry) error
}

// batchSize is the target batch size for parsers emitting through
// chan<- []LogEntry. Tuned via sweep on J.log/I.log: variation between
// 32 and 2048 is within ~3% of wall (noise), so any value past ~32
// captures the gain — 256 picked as a balanced default that doesn't
// hold large transient slices in flight.
const batchSize = 256

// batchPool recycles []LogEntry buffers across the pipeline to avoid
// re-allocating ~batchSize × N entries per parse. Producers (parsers
// via BatchSender) acquire via getBatch, consumers (analyzers,
// FilterStream-on-drop) return via PutBatch.
var batchPool = sync.Pool{
	New: func() interface{} {
		s := make([]LogEntry, 0, batchSize)
		return &s
	},
}

// getBatch returns a fresh empty batch with cap == batchSize, reusing
// pool memory when available.
func getBatch() []LogEntry {
	p := batchPool.Get().(*[]LogEntry)
	return (*p)[:0]
}

// PutBatch returns a batch to the pool for reuse. Safe to call with
// any slice — undersized batches are discarded to keep pool quality.
//
// Each LogEntry is zeroed before pooling so its Message string (and
// any future pointer-typed field) is no longer reachable from the
// pooled slice's backing array. Without this, the GC could not free
// the messages in flight: b[:0] reduces the length but the cap-sized
// underlying array still references every Message string of the
// previous batch. On C.csv this kept ~190 MB of strings live across
// the pipeline. Zeroing here costs ~batchSize×sizeof(LogEntry) bytes
// of writes per batch — negligible compared to per-entry parse cost.
func PutBatch(b []LogEntry) {
	if cap(b) < batchSize {
		return
	}
	full := b[:cap(b)]
	for i := range full {
		full[i] = LogEntry{}
	}
	s := b[:0]
	batchPool.Put(&s)
}

// BatchSender accumulates LogEntry into a slice and flushes it to a
// chan<- []LogEntry when the batch reaches batchSize. Reduces channel
// signaling overhead (and consumer wake-ups) by giving the consumer
// enough work per wake to stay busy. Backed by batchPool so flushed
// slices are recycled rather than freshly allocated.
type BatchSender struct {
	out chan<- []LogEntry
	buf []LogEntry
}

// NewBatchSender wraps a chan<- []LogEntry with a batching buffer.
func NewBatchSender(out chan<- []LogEntry) *BatchSender {
	return &BatchSender{out: out, buf: getBatch()}
}

// Send appends an entry; flushes automatically when buffer is full.
// On flush we publish the batch size to the parsedEntries counter so
// the CLI live header can report a running entry count without a
// per-Send atomic in the hot path (one Add per ~batchSize entries).
func (b *BatchSender) Send(e LogEntry) {
	b.buf = append(b.buf, e)
	if len(b.buf) >= batchSize {
		parsedEntries.Add(int64(len(b.buf)))
		b.out <- b.buf
		b.buf = getBatch()
	}
}

// Flush sends any remaining buffered entries. Must be called at the end
// of parsing to avoid losing the trailing partial batch.
func (b *BatchSender) Flush() {
	if len(b.buf) > 0 {
		parsedEntries.Add(int64(len(b.buf)))
		b.out <- b.buf
		b.buf = nil
	}
}

// ExtractPID extracts the PostgreSQL process ID from a log message.
// Looks for the first occurrence of "[digits]" pattern.
//
// Examples:
//   - "[12345]: LOG: ..." → "12345"
//   - "postgre[12345]: LOG: ..." → "12345"
//   - "[12345-1]: LOG: ..." → "12345"
//
// Returns empty string if no PID found.
func ExtractPID(message string) string {
	// Find '[' followed by digits and ']'
	start := strings.IndexByte(message, '[')
	if start == -1 {
		return ""
	}

	// Look for the closing ']' and extract digits between
	end := strings.IndexByte(message[start:], ']')
	if end == -1 {
		return ""
	}

	pidCandidate := message[start+1 : start+end]

	// Verify it's all digits (or digits followed by -)
	if len(pidCandidate) == 0 {
		return ""
	}

	// Extract only the numeric part (before any dash)
	dashIdx := strings.IndexByte(pidCandidate, '-')
	if dashIdx != -1 {
		pidCandidate = pidCandidate[:dashIdx]
	}

	// Verify all digits
	for i := 0; i < len(pidCandidate); i++ {
		if pidCandidate[i] < '0' || pidCandidate[i] > '9' {
			return ""
		}
	}

	return pidCandidate
}
