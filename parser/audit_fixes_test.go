package parser

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// chunkReader serves predefined byte chunks, one Read per remaining chunk
// (or a partial when the caller's buffer is smaller). It lets a test place a
// buffer-refill boundary at an exact offset — here, mid-garbage after a
// closing quote — which iotest.OneByteReader can't target precisely.
type chunkReader struct{ chunks [][]byte }

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if len(c.chunks[0]) == 0 {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

// TestStreamParallel_GiantMultilineEntryNotCapped is the R-4 regression: a
// legitimate multi-line stderr entry (a header followed by a long run of
// continuation lines — a giant statement/CONTEXT) begins at a valid
// entry-start, so the boundary-extension cap must NOT fire and the entry
// must be kept whole, exactly as the sequential reader does. The size cap
// only guards content that never begins at an entry-start (see
// TestStreamParallel_ForeignContentCapped).
//
// The observable is the emitted entry's message length: the header absorbs
// the continuation lines, so a correctly-uncapped run yields a message
// spanning the whole run.
func TestStreamParallel_GiantMultilineEntryNotCapped(t *testing.T) {
	// Shrink the cap so a small (few-MB) fixture exercises it. The bulk
	// read is always streamChunkSize (4 MB), so the cap only bites in the
	// extension when it sits at or below the running chunk length; a value
	// just above streamChunkSize keeps chunk 1 ~= the cap.
	orig := streamChunkMaxSize
	streamChunkMaxSize = streamChunkSize + (512 << 10) // ~4.5 MB
	defer func() { streamChunkMaxSize = orig }()

	// One valid entry-start, then ~10 MB of no-timestamp lines (no leading
	// space, no timestamp -> continuations of the header). > streamChunkSize
	// so the bulk read fills and the extension loop actually runs.
	var b strings.Builder
	b.WriteString("2026-04-19 08:00:00.000 CET [1]: LOG:  start\n")
	line := strings.Repeat("x", 127) + "\n" // 128 bytes, no timestamp
	const contBytes = 10 << 20
	for n := 0; n < contBytes; n += len(line) {
		b.WriteString(line)
	}
	stream := b.String()

	entries := collectEntries(t, func(out chan<- []LogEntry) error {
		p := &StderrParser{}
		return p.parseStreamParallel(readOnly{strings.NewReader(stream)}, 4, out)
	})

	// R-4: the run begins at a valid entry-start (the header), so it is one
	// legitimate multi-line entry whose continuations must be kept WHOLE, as
	// the sequential reader does. The cap must NOT fire on a run that began at
	// an entry-start — capping it would silently truncate the entry.
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (one giant multi-line entry)", len(entries))
	}
	msgLen := len(entries[0].Message)
	if msgLen < contBytes {
		t.Fatalf("message length %d < %d — the giant multi-line entry was truncated (cap fired on a run that began at an entry-start)",
			msgLen, contBytes)
	}
}

// TestStreamParallel_ForeignContentCapped covers the actual R2 OOM scenario:
// a member whose content NEVER begins at a valid entry-start (JSON/foreign
// text mislabeled .log) must not buffer to EOF in one allocation. No chunk
// begins at an entry-start, so the size cap fires and the run is processed in
// bounded chunks; with no entry-start anywhere it parses to zero entries.
func TestStreamParallel_ForeignContentCapped(t *testing.T) {
	orig := streamChunkMaxSize
	streamChunkMaxSize = streamChunkSize + (512 << 10) // ~4.5 MB
	defer func() { streamChunkMaxSize = orig }()

	// ~10 MB of JSON-shaped lines: none begins with a column-0 PostgreSQL
	// timestamp, so isEntryStart is false across the whole stream.
	var b strings.Builder
	line := `{"level":"info","msg":"` + strings.Repeat("x", 100) + `"}` + "\n"
	const total = 10 << 20
	for b.Len() < total {
		b.WriteString(line)
	}
	stream := b.String()

	entries := collectEntries(t, func(out chan<- []LogEntry) error {
		p := &StderrParser{}
		return p.parseStreamParallel(readOnly{strings.NewReader(stream)}, 4, out)
	})
	// No entry-start anywhere -> zero entries, and the run terminated (the cap
	// kept the chunker from buffering the whole 10 MB into one allocation).
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 (foreign content has no entry-start)", len(entries))
	}
}

// TestStreamParallel_GiantSingleEntryPreserved is the positive counterpart:
// a real stderr entry whose single line exceeds streamChunkSize must still
// parse to exactly the sequential entries. ReadBytes completes the giant
// line before the extension cap is consulted, so the cap never truncates a
// legitimate single-line entry, and Bug 3's oversized-buffer drop must not
// corrupt it.
func TestStreamParallel_GiantSingleEntryPreserved(t *testing.T) {
	var b strings.Builder
	b.WriteString("2026-04-19 08:00:00.000 CET [1]: LOG:  duration: 1.0 ms  statement: SELECT 1\n")
	// One statement line ~5 MB, larger than streamChunkSize (4 MB).
	b.WriteString("2026-04-19 08:00:01.000 CET [2]: LOG:  duration: 2.0 ms  statement: SELECT '" +
		strings.Repeat("z", 5<<20) + "'\n")
	b.WriteString("2026-04-19 08:00:02.000 CET [3]: LOG:  duration: 3.0 ms  statement: SELECT 3\n")
	stream := b.String()

	seq := collectEntries(t, func(out chan<- []LogEntry) error {
		return (&StderrParser{}).parseReader(strings.NewReader(stream), out)
	})
	if len(seq) != 3 {
		t.Fatalf("sequential produced %d entries, want 3", len(seq))
	}

	for _, workers := range []int{2, 4} {
		par := collectEntries(t, func(out chan<- []LogEntry) error {
			return (&StderrParser{}).parseStreamParallel(readOnly{strings.NewReader(stream)}, workers, out)
		})
		if len(par) != len(seq) {
			t.Fatalf("workers=%d: %d entries, sequential had %d", workers, len(par), len(seq))
		}
		for i := range seq {
			if !seq[i].Timestamp.Equal(par[i].Timestamp) ||
				seq[i].Message != par[i].Message ||
				seq[i].PID != par[i].PID {
				t.Fatalf("workers=%d: entry %d differs (len seq=%d par=%d)",
					workers, i, len(seq[i].Message), len(par[i].Message))
			}
		}
	}
}

// TestStreamChunkPool_DropThreshold pins Bug 3's decision: a base-sized
// buffer (what the pool's New mints) stays poolable, while a buffer grown
// far past it — a giant single-line entry or a capped no-boundary run — is
// over the threshold and is dropped rather than pinned across the in-flight
// window. This mirrors the `cap(j.data) <= streamChunkPoolMaxCap` guard.
func TestStreamChunkPool_DropThreshold(t *testing.T) {
	base := make([]byte, 0, streamChunkSize+(512<<10)) // pool's New sizing
	if cap(base) > streamChunkPoolMaxCap {
		t.Fatalf("base buffer cap %d exceeds pool max %d — common case would never recycle",
			cap(base), streamChunkPoolMaxCap)
	}
	// A chunk grown to hold, say, a capped 64 MB run.
	grown := make([]byte, 0, 8*(streamChunkSize+(512<<10)))
	if cap(grown) <= streamChunkPoolMaxCap {
		t.Fatalf("oversized buffer cap %d within pool max %d — would be pinned in the pool",
			cap(grown), streamChunkPoolMaxCap)
	}
}

// TestCSVScanner_PostQuoteGarbageStraddle is the regression for R4-9: the
// lenient skip between a closing quote and the delimiter must refill at the
// buffer boundary like its sibling scan loops, or garbage straddling a read
// boundary prematurely terminates the record. Feeding the input one byte at
// a time forces every scan step to hit the boundary, exercising the refill
// (and compaction) branch for the post-quote skip.
func TestCSVScanner_PostQuoteGarbageStraddle(t *testing.T) {
	// Two records; each first field is quoted then followed by lenient
	// garbage before the comma. Record 2 starts at a non-zero offset so a
	// real compaction (with sp.start/sp.end fix-up) is exercised too.
	const content = `"a"xx,b` + "\n" + `"cc"yy,dd` + "\n"
	s := newCSVScanner(iotest.OneByteReader(strings.NewReader(content)))

	want := [][2]string{{"a", "b"}, {"cc", "dd"}}
	for i, w := range want {
		ok, err := s.Next()
		if err != nil {
			t.Fatalf("record %d: Next error: %v", i, err)
		}
		if !ok {
			t.Fatalf("record %d: unexpected EOF", i)
		}
		rec := s.record()
		if rec.len() != 2 {
			t.Fatalf("record %d: %d fields, want 2 (post-quote garbage terminated the record early?)", i, rec.len())
		}
		if got0 := rec.field(0).raw; got0 != w[0] {
			t.Fatalf("record %d field0 = %q, want %q", i, got0, w[0])
		}
		if got1 := rec.field(1).raw; got1 != w[1] {
			t.Fatalf("record %d field1 = %q, want %q", i, got1, w[1])
		}
	}
	if ok, err := s.Next(); ok || err != nil {
		t.Fatalf("expected clean EOF after 2 records, got ok=%v err=%v", ok, err)
	}
}

// TestCSVScanner_PostQuoteGarbageCompaction targets the compaction accounting
// of the same fix: the refill boundary is placed inside the post-quote
// garbage of a record that starts at a non-zero buffer offset, so the skip
// loop's compaction runs with a non-zero delta and must shift the already
// frozen sp.start AND sp.end. A missing `sp.end -= d` would corrupt field 0.
func TestCSVScanner_PostQuoteGarbageCompaction(t *testing.T) {
	// Record 1 fits entirely in the first chunk (no compaction), so record 2
	// begins at offset 6. Record 2's quoted "c" and the byte after its close
	// are buffered (no compaction during the quote scan); the buffer is only
	// exhausted mid-garbage, forcing the first — non-zero-delta — compaction
	// in the post-quote skip.
	r := &chunkReader{chunks: [][]byte{
		[]byte(`"a",b` + "\n" + `"c"GG`),
		[]byte(`GGG,d` + "\n"),
	}}
	s := newCSVScanner(r)

	want := [][2]string{{"a", "b"}, {"c", "d"}}
	for i, w := range want {
		ok, err := s.Next()
		if err != nil {
			t.Fatalf("record %d: Next error: %v", i, err)
		}
		if !ok {
			t.Fatalf("record %d: unexpected EOF", i)
		}
		rec := s.record()
		if rec.len() != 2 {
			t.Fatalf("record %d: %d fields, want 2", i, rec.len())
		}
		if got0 := rec.field(0).raw; got0 != w[0] {
			t.Fatalf("record %d field0 = %q, want %q (compaction shifted sp.end wrong?)", i, got0, w[0])
		}
		if got1 := rec.field(1).raw; got1 != w[1] {
			t.Fatalf("record %d field1 = %q, want %q", i, got1, w[1])
		}
	}
}
