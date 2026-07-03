package parser

import (
	"bufio"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// stderrParallelMinSize is the file size below which the sequential
// stderr path is kept — same rationale as the JSON threshold.
const stderrParallelMinSize = 64 << 20 // 64 MB

// stderrSegmentSize is the micro-segment length. Same design as the
// JSON path: segments far smaller than fileSize/workers keep every
// worker busy regardless of where the ordered emission cursor is.
const stderrSegmentSize = 8 << 20 // 8 MB

// stderrSegmentQueueDepth bounds one segment's output queue, in
// batches. Deep queues let a worker finish its segment without
// parking even when the fan-in cursor is behind, at the cost of
// pinning more message strings in flight. Measured 64 vs 192 on a
// 1 GB and an 11 GB log: identical wall and RSS (peak memory there
// is GC pacing under the higher allocation rate, not queue depth),
// so the smaller theoretical ceiling wins.
const stderrSegmentQueueDepth = 64

// isEntryStart reports whether a line begins a new stderr entry,
// using the exact predicate parseReader applies (continuation = line
// starting with space/tab, or no timestamp). Segment boundaries
// computed with the same predicate make parallel parsing equivalent
// to sequential by construction: parser state never has to cross a
// boundary.
//
// This is deliberately prefix-unaware (it never strips a StderrParser
// literal prefix). Files needing a prefix offset never reach this path:
// Parse skips the parallel segment engine when prefixLen > 0 and uses
// the prefix-aware sequential parseReader instead. Keeping the boundary
// predicate prefix-free avoids threading prefixLen through the segment
// scan for a rare non-standard format.
func isEntryStart(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	if line[0] == ' ' || line[0] == '\t' {
		return false
	}
	return hasTimestampBytes(line)
}

// computeEntryBoundaries returns entry-aligned segment offsets for
// the file: boundaries[0] = 0, boundaries[len-1] = size, and every
// intermediate value is the offset of the first entry-start line at
// or after k×stderrSegmentSize. Segments whose window contains no
// entry start (one entry larger than a segment) collapse and are
// dropped, so every returned segment is non-empty.
func computeEntryBoundaries(f *os.File, size int64) ([]int64, error) {
	numSegs := int((size + stderrSegmentSize - 1) / stderrSegmentSize)
	boundaries := make([]int64, 0, numSegs+1)
	boundaries = append(boundaries, 0)
	for k := 1; k < numSegs; k++ {
		b, err := findEntryBoundary(f, int64(k)*stderrSegmentSize, size)
		if err != nil {
			return nil, err
		}
		if b >= size {
			break
		}
		if b > boundaries[len(boundaries)-1] {
			boundaries = append(boundaries, b)
		}
	}
	boundaries = append(boundaries, size)
	return boundaries, nil
}

// findEntryBoundary scans forward from offset and returns the
// absolute offset of the first line start that satisfies
// isEntryStart. The line at `offset` itself is skipped even when
// offset already sits at a line start: a boundary may only move
// forward, and starting at the next '\n' keeps the scan O(window)
// without needing to know whether offset is mid-line.
func findEntryBoundary(f *os.File, offset, size int64) (int64, error) {
	const window = 256 << 10
	buf := make([]byte, window)
	pos := offset
	lineStart := int64(-1) // unknown until the first '\n'

	for pos < size {
		n, err := f.ReadAt(buf, pos)
		if n == 0 {
			if err == io.EOF {
				return size, nil
			}
			return 0, err
		}
		chunk := buf[:n]
		i := 0
		for {
			if lineStart >= 0 {
				// We sit at a line start inside (or beyond) the chunk.
				rel := int(lineStart - pos)
				if rel >= n {
					break // line start beyond this chunk: refill from it
				}
				line := chunk[rel:]
				if len(line) >= 32 || pos+int64(n) >= size {
					if isEntryStart(line) {
						return lineStart, nil
					}
				} else {
					break // too short to decide: refill from lineStart
				}
				i = rel
			}
			// Advance to the next '\n' from i.
			nl := indexByteFrom(chunk, i, '\n')
			if nl == -1 {
				lineStart = -1
				break // no newline in chunk: read next window
			}
			lineStart = pos + int64(nl) + 1
			i = nl + 1
		}
		if lineStart >= 0 && lineStart > pos {
			pos = lineStart
		} else {
			pos += int64(n)
		}
		if err == io.EOF && pos >= size {
			return size, nil
		}
	}
	return size, nil
}

// indexByteFrom is bytes.IndexByte over b[from:], returning an index
// relative to b (or -1).
func indexByteFrom(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// parseParallel parses a plain stderr file by entry-aligned segments,
// re-emitting batches in file order. Same engine as the JSON path:
// workers claim segment indices in order, a window semaphore caps how
// far they run ahead of the ordered fan-in, and each worker runs the
// UNCHANGED parseReader over an io.SectionReader — all the stderr
// subtleties (continuations, syslog tab markers, cloud prefixes) stay
// in one implementation.
//
// The receiver's prefixStructure must already be detected; worker
// instances share it read-only.
func (p *StderrParser) parseParallel(f *os.File, size int64, workers int, out chan<- []LogEntry) error {
	boundaries, err := computeEntryBoundaries(f, size)
	if err != nil {
		return err
	}
	numSegs := len(boundaries) - 1
	if numSegs < 2 {
		// Degenerate split (e.g. one giant entry): sequential fallback.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return p.parseReader(WithProgress(f), out)
	}
	if workers > numSegs {
		workers = numSegs
	}

	queues := make([]chan []LogEntry, numSegs)
	for i := range queues {
		queues[i] = make(chan []LogEntry, stderrSegmentQueueDepth)
	}
	errs := make([]error, numSegs)

	window := make(chan struct{}, workers+2)
	var cursor atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Per-worker reuse (mirrors the CSV path): the 1 MB bufio buffer
			// is allocated once and Reset per segment, not once per segment —
			// neutralizing the per-segment buffer churn.
			var br *bufio.Reader
			for {
				i := int(cursor.Add(1)) - 1
				if i >= numSegs {
					return
				}
				window <- struct{}{} // released by the fan-in below
				// ReadAt on *os.File is concurrency-safe, so all the
				// SectionReaders share one descriptor.
				sec := io.NewSectionReader(f, boundaries[i], boundaries[i+1]-boundaries[i])
				rd := WithProgress(sec)
				if br == nil {
					br = bufio.NewReaderSize(rd, 1<<20)
				} else {
					br.Reset(rd)
				}
				wp := &StderrParser{prefixStructure: p.prefixStructure}
				errs[i] = wp.parseReader(br, queues[i])
				close(queues[i])
			}
		}()
	}

	for i := 0; i < numSegs; i++ {
		for batch := range queues[i] {
			out <- batch
		}
		<-window
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
