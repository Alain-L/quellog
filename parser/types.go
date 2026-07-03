// Package parser provides types and interfaces for PostgreSQL log parsing.
package parser

import (
	"strings"
	"sync"
	"time"
)

// LogEntry is a single parsed PostgreSQL log entry: timestamp + the full
// message (with any DETAIL/HINT/STATEMENT continuation lines
// concatenated). Examples for the three input formats:
//
//	stderr: 2025-01-01 12:00:00 CET LOG: database system is ready to accept connections
//	CSV:    2025-01-01 12:00:00.123 CET,,,12345,,LOG,00000,"database system is ready",,0
//	JSON:   {"timestamp":"2025-01-01T12:00:00Z","message":"database system is ready"}
type LogEntry struct {
	Timestamp time.Time // for syslog (no year), the current year is assumed
	Message   string    // full message including the severity prefix (LOG:/ERROR:/...)
	// IsContinuation is true for DETAIL/HINT/STATEMENT/CONTEXT/QUERY
	// lines: in stderr format PostgreSQL emits them as separate lines
	// with their own timestamp + prefix but they belong to the previous
	// LOG/ERROR. They are not counted in total_logs but are still passed
	// to the analyzers.
	IsContinuation bool
	// PID is the backend PID extracted from the prefix ("[12345]"),
	// populated once at construction so the eight analyzers don't each
	// re-parse the prefix. Empty if not extractable.
	PID string
	// Seq is a monotonic stream position assigned by the analysis
	// dispatcher before PID-sharding. Analyzers that build ordered
	// per-occurrence event slices stamp it onto each event so the
	// sharded merge can reproduce the exact single-pass (stream) order
	// when timestamps tie. Zero outside the dispatcher (e.g. unit tests
	// constructing entries directly), which is harmless.
	Seq int64
	// BodyOffset is the byte offset of the message body — the text right
	// after the severity marker (" LOG: ", " ERROR: ", …) and an optional
	// verbose SQLSTATE token ("00000: "). Stamped once by the analysis
	// dispatcher, like PID, so analyzers can anchor their pattern gates in
	// O(1) instead of scanning the whole message. Zero when no severity
	// marker was found (continuation lines, unit-test entries): analyzers
	// must then fall back to their unanchored slow path.
	BodyOffset int32
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

// LogParser is implemented by every format-specific parser
// (StderrParser, CsvParser, JsonParser). The parser reads the file
// and streams batches of LogEntry through `out`. The caller owns the
// channel lifecycle (parsers must NOT close it). Individual malformed
// lines are logged as warnings; only critical I/O or format errors are
// returned.
type LogParser interface {
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
