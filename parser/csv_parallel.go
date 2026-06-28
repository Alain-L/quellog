package parser

import (
	"bufio"
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

// isCSVRecordStart reports whether line begins a new PostgreSQL csvlog record.
// Field 0 (log_time) is an UNQUOTED ISO timestamp immediately followed by the
// field separator: "YYYY-MM-DD HH:MM:SS[.ffffff][ TZ],". This is the segment
// equivalent of stderr's isEntryStart. A regex-free, fixed-position + range
// check keeps the boundary scan O(window) while being strong enough that a
// newline inside a quoted field (the only thing that could split a record
// mid-way) practically never matches: it would need the quoted text to contain
// "\n<a calendar-valid ISO timestamp>,". The residual (a genuinely valid
// timestamp embedded in a quoted message) is the same rare, accepted class of
// false positive as stderr's isEntryStart.
func isCSVRecordStart(line []byte) bool {
	if len(line) < 20 || line[0] == '"' {
		return false
	}
	// Fixed-position date-time skeleton: YYYY-MM-DD HH:MM:SS
	if line[4] != '-' || line[7] != '-' || line[10] != ' ' ||
		line[13] != ':' || line[16] != ':' {
		return false
	}
	for _, i := range [...]int{0, 1, 2, 3} { // year digits, any value
		if line[i] < '0' || line[i] > '9' {
			return false
		}
	}
	// Calendar ranges reject coincidental digit runs inside quoted text.
	mo, day := twoDigit(line, 5), twoDigit(line, 8)
	hh, mi, ss := twoDigit(line, 11), twoDigit(line, 14), twoDigit(line, 17)
	if mo < 1 || mo > 12 || day < 1 || day > 31 ||
		hh < 0 || hh > 23 || mi < 0 || mi > 59 || ss < 0 || ss > 59 {
		return false
	}
	// Optional ".fraction", optional " TZ", then the field-0 comma.
	j := 19
	if j < len(line) && line[j] == '.' {
		j++
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
	}
	if j < len(line) && line[j] == ' ' {
		j++
		for j < len(line) && line[j] >= 'A' && line[j] <= 'Z' {
			j++
		}
	}
	return j < len(line) && line[j] == ','
}

// twoDigit reads the two ASCII digits at line[i:i+2] as an int, or -1 if either
// byte is not a digit. Callers have already bounds-checked len(line).
func twoDigit(line []byte, i int) int {
	a, b := line[i], line[i+1]
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return -1
	}
	return int(a-'0')*10 + int(b-'0')
}

// computeCSVBoundaries returns record-aligned segment offsets, exactly like
// stderr's computeEntryBoundaries but with the CSV record-start predicate:
// boundaries[0]=0, boundaries[last]=size, intermediates at the first record
// start at or after k×csvSegmentSize. Segments with no record start in their
// window (one record larger than a segment) collapse and are dropped.
func computeCSVBoundaries(f *os.File, size int64) ([]int64, error) {
	numSegs := int((size + csvSegmentSize - 1) / csvSegmentSize)
	boundaries := make([]int64, 0, numSegs+1)
	boundaries = append(boundaries, 0)
	for k := 1; k < numSegs; k++ {
		b, err := findCSVBoundary(f, int64(k)*csvSegmentSize, size)
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

// findCSVBoundary scans forward from offset and returns the absolute offset of
// the first line start satisfying isCSVRecordStart. Same scan as stderr's
// findEntryBoundary (the line at offset is skipped; boundaries only move
// forward), with the CSV predicate.
func findCSVBoundary(f *os.File, offset, size int64) (int64, error) {
	const window = 256 << 10
	buf := make([]byte, window)
	pos := offset
	lineStart := int64(-1)

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
				rel := int(lineStart - pos)
				if rel >= n {
					break // line start beyond this chunk: refill from it
				}
				line := chunk[rel:]
				if len(line) >= 32 || pos+int64(n) >= size {
					if isCSVRecordStart(line) {
						return lineStart, nil
					}
				} else {
					break // too short to decide: refill from lineStart
				}
				i = rel
			}
			nl := indexByteFrom(chunk, i, '\n')
			if nl == -1 {
				lineStart = -1
				break
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
