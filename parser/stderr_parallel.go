package parser

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

// stderrParallelMinSize is the file size below which the sequential
// stderr path is kept — same rationale as the JSON threshold. This is
// the floor on the number of bytes to PARSE: for a plain file it is the
// file size, for a compressed file it is the estimated decompressed size
// (see reachesStreamParallelSize).
const stderrParallelMinSize = 64 << 20 // 64 MB

// streamExpansionFactor is a conservative floor for how much a compressed
// PostgreSQL log expands when decompressed (measured ratios run 14-23x;
// mirrors cmd.logExpansionFactor). Used to estimate the decompressed byte
// volume — what the parser actually processes — from the on-disk size, so
// the stream-parallel gate keys off parse work rather than disk size.
const streamExpansionFactor = 8

// streamParallelMinDecompressed is the estimated-decompressed-size floor
// above which COMPRESSED stderr uses the entry-sharded parallel engine
// (parseStreamParallel); below it the input is parsed by the sequential
// single-goroutine reader instead. It is deliberately higher than
// stderrParallelMinSize because a compressed stream's single-threaded
// decode — not the parse — paces the pipeline: on a 256 MB-decompressed
// single-frame .zst the parallel parse buys only a few percent of wall
// while its (workers+2) in-flight 4 MB chunks cost ~2.9x peak RSS
// (measured). The floor matches the analysis-sharding gate (256 MB), so a
// compressed input parses sequentially exactly when it is too small to
// shard and in parallel exactly when it shards.
const streamParallelMinDecompressed = 256 << 20

// streamWorkerScanBuf is the INITIAL bufio.Scanner buffer given to each
// stream-chunk parse worker (see parseStreamParallel). Smaller than the
// 4 MB default because a worker parses only a few-MB chunk and real log
// lines are a few KB; the scanner still grows to math.MaxInt32 on a longer
// line, so output is unchanged.
const streamWorkerScanBuf = 1 << 20

// reachesStreamParallelSize reports whether an input whose on-disk size is
// `size` has enough content to parse to justify the entry-sharded parallel
// stderr engine. It is pure (no I/O) so the routing decision is unit-testable
// with synthetic sizes, and it is the single size-gate both the parse router
// (wrapCompressedParser) and the multi-file fan-in window (UsesStreamParallelStderr)
// consult, so the two can never diverge. Plain inputs gate on their file size at
// stderrParallelMinSize (matching StderrParser.Parse's parseParallel routing);
// compressed inputs gate on their estimated decompressed size at the higher
// streamParallelMinDecompressed (see that constant for why).
func reachesStreamParallelSize(compressed bool, size int64) bool {
	if compressed {
		return size*streamExpansionFactor >= streamParallelMinDecompressed
	}
	return size >= stderrParallelMinSize
}

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
			wp := &StderrParser{prefixStructure: p.prefixStructure}
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

// streamChunkSize is the target in-memory chunk length for the stream
// (compressed-input) parallel path. Same order of magnitude as the
// seekable path's segments: big enough to amortize worker dispatch,
// small enough that (workers+2) in-flight chunks stay tens of MB.
const streamChunkSize = 4 << 20

// streamChunkQueueDepth bounds one chunk's output queue, in batches —
// same sizing rationale as stderrSegmentQueueDepth.
const streamChunkQueueDepth = 32

// streamChunkMaxSize caps the boundary-extension buffer for one stream
// chunk. The extension normally reaches the next entry-start a few lines
// past streamChunkSize; but a member with no column-0 entry-start line
// (JSON or foreign text mislabeled .log, a literal-prefixed log) would
// otherwise append line after line to EOF, buffering the whole
// decompressed member in one growing allocation before yielding a single
// zero-entry chunk. Capping the extension degrades such a member to
// bounded per-chunk memory; the remainder is picked up by the next
// bounded bulk read. 64 MB sits far above any real multi-line stderr
// entry, so well-formed logs never reach it. A var (not const) so tests
// can shrink it to exercise the cap with a small fixture.
var streamChunkMaxSize = 64 << 20

// streamChunkPoolMaxCap is the largest backing array returned to
// streamChunkPool. A chunk whose backing array grew past this — a giant
// single-line entry completed by ReadBytes, or a capped no-boundary run —
// is dropped rather than pooled, so one oversized array can't stay pinned
// across the whole in-flight window until GC. 2× the base capacity
// (streamChunkSize + 512 KB headroom) still recycles the common
// extend-into-headroom case.
const streamChunkPoolMaxCap = 2 * (streamChunkSize + (512 << 10))

// streamChunkPool recycles chunk buffers between the chunker and the
// workers: a chunk is dead as soon as its worker parsed it (parseReader
// copies every message out), so pooling caps the chunker's allocation
// churn at the in-flight window instead of the whole stream. Headroom
// past streamChunkSize absorbs the boundary extension without a
// realloc in the common case.
var streamChunkPool = sync.Pool{
	New: func() any { return make([]byte, 0, streamChunkSize+(512<<10)) },
}

// parseStreamParallel is the non-seekable sibling of parseParallel: it
// parses a decompressed stderr stream by cutting it into entry-aligned
// in-memory chunks parsed by a worker pool, then re-emits batches in
// chunk order. Chunk boundaries use the same isEntryStart predicate as
// the seekable segment path, so the emitted stream reproduces the
// sequential parse exactly. Syslog and prefixed streams fall back to
// the sequential reader (same routing as Parse).
func (p *StderrParser) parseStreamParallel(r io.Reader, workers int, out chan<- []LogEntry) error {
	// The stream can't seek: sample the head for format detection, then
	// replay the sample ahead of the remainder.
	sample := make([]byte, 64<<10)
	n, err := io.ReadFull(r, sample)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return err
	}
	sample = sample[:n]
	full := io.MultiReader(bytes.NewReader(sample), r)
	if f := detectSyslogFormat(sample); f != SyslogNone {
		return parseSyslogReader(full, f, out)
	}
	p.detectPrefixStructure(bytes.NewReader(sample))

	br := bufio.NewReaderSize(full, 1<<20)

	type job struct {
		data []byte
		q    chan []LogEntry
	}
	jobs := make(chan job, workers)
	// queueCh streams the per-chunk output queues to the ordered drain
	// below. Its capacity is the window: the chunker blocks creating
	// chunk k+workers+2 until the drain finished chunk k, bounding
	// in-flight memory to (workers+2) chunks.
	queueCh := make(chan chan []LogEntry, workers+2)

	var readErr error
	go func() {
		defer close(jobs)
		defer close(queueCh)
		capWarned := false // single chunker goroutine: a plain flag is race-free
		for {
			data := streamChunkPool.Get().([]byte)[:streamChunkSize]
			n, err := io.ReadFull(br, data)
			data = data[:n]
			if err == nil {
				// Stream continues: extend the chunk to the next entry
				// boundary. First complete the line the bulk read cut
				// mid-way, then append whole lines until one starts a
				// new entry — that line belongs to the next chunk and
				// stays buffered in br.
				rest, e := br.ReadBytes('\n')
				data = append(data, rest...)
				err = e
				for err == nil {
					// Cap the extension: without a boundary, a member that
					// never yields a column-0 entry-start would buffer to
					// EOF here. Stop at streamChunkMaxSize and dispatch what
					// we have; the remainder is read in the next bounded
					// chunk. A member that truly never yields an entry-start
					// then parses to zero entries either way, so cutting the
					// run mid-way changes nothing but the memory ceiling.
					if len(data) >= streamChunkMaxSize {
						if !capWarned {
							slog.Warn("stderr member has no entry boundary within the size cap; parsing in bounded chunks (mislabeled or foreign content?)",
								"cap_bytes", streamChunkMaxSize)
							capWarned = true
						}
						break
					}
					pk, e := br.Peek(64)
					if len(pk) == 0 {
						err = e
						break
					}
					if isEntryStart(pk) {
						break
					}
					line, e := br.ReadBytes('\n')
					data = append(data, line...)
					err = e
				}
			}
			if len(data) > 0 {
				q := make(chan []LogEntry, streamChunkQueueDepth)
				queueCh <- q
				jobs <- job{data: data, q: q}
			}
			if err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					readErr = err
				}
				return
			}
		}
	}()

	var wg sync.WaitGroup
	var parseErr error
	var errOnce sync.Once
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// parseReader (not parseFromBytes): it copies each entry's
			// message out of the chunk, so the 8 MB buffer is released as
			// soon as the chunk is parsed. The zero-copy variant would pin
			// one whole chunk per in-flight or retained message — measured
			// ~10× RSS on an 880 MB stream.
			var cr bytes.Reader
			wp := &StderrParser{prefixStructure: p.prefixStructure, scanBufInit: streamWorkerScanBuf}
			for j := range jobs {
				cr.Reset(j.data)
				if err := wp.parseReader(&cr, j.q); err != nil {
					errOnce.Do(func() { parseErr = err })
				}
				// Return the buffer to the pool unless append grew its
				// backing array far past the base size (a giant single-line
				// entry, or a capped no-boundary run). Recycling an oversized
				// array would pin tens of MB across the in-flight window;
				// dropping it lets New() mint a fresh base-sized buffer while
				// the common case still recycles.
				if cap(j.data) <= streamChunkPoolMaxCap {
					// The boxing allocation SA6002 warns about is one interface
					// header per 4 MB chunk — noise next to the buffer it recycles.
					//lint:ignore SA6002 slice-in-pool is intentional, see above
					streamChunkPool.Put(j.data[:0])
				}
				close(j.q)
			}
		}()
	}

	// Ordered fan-in: forward each chunk's batches in chunk order.
	for q := range queueCh {
		for batch := range q {
			out <- batch
		}
	}
	wg.Wait()

	if readErr != nil {
		return readErr
	}
	return parseErr
}
