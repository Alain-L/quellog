// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"io"
	"sync/atomic"
)

// Live progress counters consumed by the CLI bar. Package-level so
// the cmd layer doesn't have to know which concrete parser autodetect
// picked. Safe because quellog runs a single parse at a time per
// process. Both counters are reset between parse cycles by the cmd
// layer via the Reset* helpers.
var (
	currentFileBytes atomic.Int64 // bytes consumed in the current file
	parsedEntries    atomic.Int64 // total LogEntry produced so far
)

func ResetCurrentFileProgress() { currentFileBytes.Store(0) }
func CurrentFileProgress() int64 { return currentFileBytes.Load() }
func ResetParsedEntries()        { parsedEntries.Store(0) }
func ParsedEntries() int64       { return parsedEntries.Load() }

// reportFileProgress is called from the mmap parser hot loop to
// publish its byte cursor (Store, not Add — it's an absolute offset).
func reportFileProgress(bytes int64) { currentFileBytes.Store(bytes) }

// progressReader wraps an io.Reader so every Read accumulates bytes
// into currentFileBytes. Used by the CSV, JSON, bufio-stderr, gzip
// and zstd paths via WithProgress. Compressed inputs are wrapped
// BEFORE the decompressor so the count tracks on-disk bytes (= the
// CLI bar's denominator).
type progressReader struct{ r io.Reader }

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		currentFileBytes.Add(int64(n))
	}
	return n, err
}

// WithProgress wraps r so subsequent Reads publish progress.
func WithProgress(r io.Reader) io.Reader {
	if _, ok := r.(*progressReader); ok {
		return r
	}
	return &progressReader{r: r}
}
