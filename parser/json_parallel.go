package parser

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/tidwall/gjson"
)

// jsonParallelMinSize is the file size below which the sequential
// JSON-lines path is kept: worker setup, extra file descriptors and
// the ordered fan-in are not worth amortizing on small inputs.
const jsonParallelMinSize = 64 << 20 // 64 MB

// jsonSegmentSize is the micro-segment length. Segments must be MUCH
// smaller than fileSize/workers: the fan-in emits them in order, so a
// worker whose queue can hold its whole segment never parks on its
// own production and simply moves on to the next segment — keeping
// every worker busy regardless of where the ordered cursor is. (A
// first implementation used one big segment per worker with shallow
// queues; workers filled them and slept — 142% CPU on a 10-core box.)
const jsonSegmentSize = 8 << 20 // 8 MB

// jsonSegmentQueueDepth bounds one segment's output queue, in
// batches. Sized so a full segment fits: 8 MB of ~120-byte lines is
// ~70k entries = ~270 batches… but jsonlog lines run 0.5-2 KB in
// practice (~16-64 batches). 128 keeps the no-park guarantee with
// margin while bounding worst-case in-flight memory.
const jsonSegmentQueueDepth = 128

// parallelWorkers picks the worker count for parallel single-file
// parsing (JSON-lines and stderr segments). Capped at 8 like the
// multi-file worker pool.
func parallelWorkers() int {
	w := runtime.NumCPU() - 2
	if w > 8 {
		w = 8
	}
	return w
}

// parseJSONLinesParallel splits a plain JSON-lines file into ~8 MB
// segments parsed by a worker pool, then re-emits batches in file
// order.
//
// Boundary rule: a line belongs to the segment where its FIRST byte
// lies. Each worker skips the partial line at its start offset (it
// belongs to the previous segment) and finishes the line straddling
// its end offset. JSON-lines is strictly line-delimited — PostgreSQL
// jsonlog escapes newlines inside values — so byte chunking cannot
// split an entry.
//
// Ordering and memory: workers claim segment indices in order; a
// window semaphore (workers+2 slots, released by the fan-in after
// draining a segment) caps how far ahead of the ordered cursor they
// can run, bounding in-flight memory to ~window × segment size. The
// shared out channel stays open, per the LogParser contract.
func parseJSONLinesParallel(filename string, size int64, workers int, out chan<- []LogEntry) error {
	numSegs := int((size + jsonSegmentSize - 1) / jsonSegmentSize)
	if numSegs < 1 {
		numSegs = 1
	}
	if workers > numSegs {
		workers = numSegs
	}

	queues := make([]chan []LogEntry, numSegs)
	for i := range queues {
		queues[i] = make(chan []LogEntry, jsonSegmentQueueDepth)
	}
	errs := make([]error, numSegs)

	window := make(chan struct{}, workers+2)
	var cursor atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Per-worker reuse (mirrors the CSV path): the file handle, the
			// 1 MB bufio buffer and the JsonParser (cachedTSFmt, msgBuf) are
			// opened/allocated once and reused across this worker's
			// segments, not once per segment — neutralizing the per-segment
			// churn.
			var br *bufio.Reader
			p := &JsonParser{}
			f, openErr := os.Open(filename)
			if openErr == nil {
				defer f.Close()
			}
			for {
				i := int(cursor.Add(1)) - 1
				if i >= numSegs {
					return
				}
				window <- struct{}{} // released by the fan-in below
				if openErr != nil {
					errs[i] = openErr
					close(queues[i])
					continue
				}
				start := int64(i) * jsonSegmentSize
				end := start + jsonSegmentSize
				if end > size {
					end = size
				}
				errs[i] = parseJSONSegment(f, &br, p, start, end, queues[i])
				close(queues[i])
			}
		}()
	}

	// Ordered fan-in: forward each segment's batches in index order.
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

// parseJSONSegment parses the lines of one byte segment [start, end)
// of the worker's file handle into out. See parseJSONLinesParallel
// for the boundary rule. The caller owns f, *br and p and reuses them
// across its segments; *br is lazily allocated on the first segment.
func parseJSONSegment(f *os.File, br **bufio.Reader, p *JsonParser, start, end int64, out chan<- []LogEntry) error {
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}

	// Per-segment progress wrapper: the global counter sums the
	// segments to the file size, same denominator as sequential.
	if *br == nil {
		*br = bufio.NewReaderSize(WithProgress(f), 1<<20)
	} else {
		(*br).Reset(WithProgress(f))
	}
	rd := *br

	bs := NewBatchSender(out)
	defer bs.Flush()

	pos := start
	if start > 0 {
		// Skip the partial line: it belongs to the previous segment.
		skipped, err := discardLine(rd)
		pos += skipped
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}

	for pos < end {
		line, n, err := readLineBytes(rd)
		pos += n
		if len(line) > 0 {
			s := unsafeString(line)
			if !gjson.Valid(s) {
				slog.Warn("skipping malformed JSON", "offset", pos-n)
			} else if entry, eerr := p.extractFromResult(gjson.Parse(s)); eerr == nil {
				bs.Send(entry)
			} else if !errors.Is(eerr, errSkipEntry) {
				slog.Warn("skipping incomplete JSON entry", "offset", pos-n, "err", eerr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// discardLine consumes bytes up to and including the next '\n' and
// returns the byte count consumed. err is io.EOF when the reader ends
// before a newline.
func discardLine(rd *bufio.Reader) (int64, error) {
	var n int64
	for {
		chunk, err := rd.ReadSlice('\n')
		n += int64(len(chunk))
		if err == bufio.ErrBufferFull {
			continue
		}
		return n, err
	}
}

// readLineBytes returns the next line without its trailing \r?\n, the
// TOTAL bytes consumed (terminator included), and the reader error.
// The returned slice aliases the bufio buffer for short lines (valid
// until the next read — callers must finish with it first) and is an
// owned copy only when the line exceeded the buffer size.
func readLineBytes(rd *bufio.Reader) ([]byte, int64, error) {
	line, err := rd.ReadSlice('\n')
	n := int64(len(line))
	if err == bufio.ErrBufferFull {
		// Rare: line longer than the 1 MB buffer. Copy and keep
		// extending — allocation is acceptable on this path.
		owned := append([]byte(nil), line...)
		for err == bufio.ErrBufferFull {
			line, err = rd.ReadSlice('\n')
			n += int64(len(line))
			owned = append(owned, line...)
		}
		line = owned
	}
	return trimEOL(line), n, err
}

// trimEOL strips one trailing "\n" or "\r\n".
func trimEOL(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}
