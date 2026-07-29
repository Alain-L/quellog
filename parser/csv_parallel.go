package parser

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// csvParallelMinSize is the file size below which the sequential CSV path is
// kept — same rationale as the stderr/JSON thresholds.
const csvParallelMinSize = 64 << 20 // 64 MB

// csvSegmentSize is the micro-segment length, same design as the other parallel
// paths: segments far smaller than fileSize/workers keep every worker busy
// regardless of where the ordered emission cursor sits.
const csvSegmentSize = 8 << 20 // 8 MB

// csvSegmentQueueDepth bounds one segment's output queue, in batches. An 8 MB
// segment of ~0.5-2 KB CSV records is ~16-64 batches; 128 keeps the no-park
// margin while bounding in-flight memory.
const csvSegmentQueueDepth = 64

// computeCSVBoundaries returns record-aligned segment offsets for a plain CSV
// file: boundaries[0]=0, boundaries[last]=size, and each intermediate is the
// first TRUE record boundary at or after k×csvSegmentSize. Unlike a local
// heuristic scan, it sweeps the whole file once while tracking the running
// double-quote parity, so a boundary is only ever placed on an offset that is
// outside any quoted field. A newline inside a quoted, multi-line message field
// — even one whose text begins with a valid PostgreSQL timestamp and a comma
// (a multi-line SQL statement, an echoed COPY/CSV payload, a multi-row VALUES) —
// is never mistaken for a record start.
//
// Why parity is authoritative: PostgreSQL always emits well-formed csvlog.
// Every quoted field is wrapped in a pair of double quotes and each internal
// quote is doubled ("") — quotes therefore only ever occur in pairs, so the
// number of double quotes seen from the start of the file is EVEN exactly when
// the cursor sits outside a quoted field. Field 0 (log_time) is never quoted,
// so a record always begins immediately after a newline seen at even parity.
// This is the same record-structure invariant the sequential scanner relies on,
// which is why the parallel parse it feeds reproduces the sequential parse
// byte-for-byte.
//
// Cost: this is a single byte-scanning pass (SIMD IndexByte/Count, no field
// parsing, no allocation). The expensive per-record work (field extraction,
// message building, timestamp parsing) stays fully parallel across segments;
// only the cheap boundary sweep is serial. This replaces the previous
// timestamp-shaped line heuristic (isCSVRecordStart), which placed boundaries
// mid-record whenever a quoted field contained an embedded timestamp line and
// silently corrupted the analysis on files ≥ csvParallelMinSize.
//
// Segments with no record boundary in their window (one record larger than a
// segment) collapse and are dropped, so every returned segment is non-empty.
func computeCSVBoundaries(f *os.File, size int64) ([]int64, error) {
	numSegs := int((size + csvSegmentSize - 1) / csvSegmentSize)
	boundaries := make([]int64, 0, numSegs+1)
	boundaries = append(boundaries, 0)

	const window = 256 << 10
	buf := make([]byte, window)
	var base int64   // absolute offset of buf[0]
	quoteParity := 0 // count of '"' seen so far, mod 2 (0 == outside a field)
	nextTarget := int64(csvSegmentSize)
	for base < size {
		n, err := f.ReadAt(buf, base)
		if n == 0 {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		chunk := buf[:n]
		i := 0
		for i < n {
			rel := bytes.IndexByte(chunk[i:n], '\n')
			if rel < 0 {
				// Line continues past this window: fold in its quotes and carry
				// the parity into the next read.
				quoteParity ^= bytes.Count(chunk[i:n], quoteSep) & 1
				break
			}
			nl := i + rel
			quoteParity ^= bytes.Count(chunk[i:nl], quoteSep) & 1 // quotes on the line, excl. '\n'
			if quoteParity == 0 {
				// Even parity at this newline: it terminates a record, so the
				// next byte starts a new one — a valid segment boundary.
				recordStart := base + int64(nl) + 1
				if recordStart >= nextTarget && recordStart < size {
					boundaries = append(boundaries, recordStart)
					// Advance past this start so a record larger than a segment
					// spans several targets instead of yielding empty segments.
					for nextTarget <= recordStart {
						nextTarget += csvSegmentSize
					}
				}
			}
			i = nl + 1
		}
		base += int64(n)
		if err == io.EOF {
			break
		}
	}

	boundaries = append(boundaries, size)
	return boundaries, nil
}

// quoteSep is the single-byte separator handed to bytes.Count/IndexByte in the
// boundary sweep; a package-level value avoids reallocating it per call.
var quoteSep = []byte{'"'}

// parseParallel parses a plain CSV file by record-aligned segments, re-emitting
// batches in file order. Same engine as the stderr/JSON paths: workers claim
// segment indices in order, a window semaphore caps how far they run ahead of
// the ordered fan-in, and each worker runs the UNCHANGED parseReader over an
// io.SectionReader (a fresh CsvParser per segment — cachedFormat/msgBuf are
// per-instance mutable state).
func (p *CsvParser) parseParallel(f *os.File, size int64, workers int, out chan<- []LogEntry) error {
	boundaries, err := computeCSVBoundaries(f, size)
	if err != nil {
		return err
	}
	numSegs := len(boundaries) - 1
	if numSegs < 2 {
		// Degenerate split (e.g. one giant record): sequential fallback.
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
		queues[i] = make(chan []LogEntry, csvSegmentQueueDepth)
	}
	errs := make([]error, numSegs)

	window := make(chan struct{}, workers+2)
	var cursor atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Per-worker reuse: the CsvParser (msgBuf, cached timestamp format),
			// the 1 MB bufio buffer and the scanner's 1 MB read buffer are
			// allocated once and reused across this worker's segments, not once
			// per segment — neutralizing the per-segment buffer churn.
			wp := &CsvParser{}
			sc := newCSVScanner(nil)
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
				sc.Reset(skipBOM(br))
				errs[i] = wp.parseScanner(sc, queues[i])
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
