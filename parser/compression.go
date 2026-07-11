//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
// This file contains compression handling code excluded from WASM builds.
package parser

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
)

// ErrCompressionFailed indicates a failure reading compressed content.
var ErrCompressionFailed = errors.New("failed to read compressed file")

// compressionCodec defines how to create a streaming reader for a compressed format.
type compressionCodec struct {
	name   string
	opener func(io.Reader) (io.ReadCloser, error)
}

var (
	gzipCodec = compressionCodec{
		name: "gzip",
		opener: func(r io.Reader) (io.ReadCloser, error) {
			return newParallelGzipReader(r)
		},
	}
	zstdCodec = compressionCodec{
		name: "zstd",
		opener: func(r io.Reader) (io.ReadCloser, error) {
			return newZstdDecoder(r)
		},
	}
)

// IsCompressed reports whether the filename has an extension that the
// pipeline treats as compressed/archived (gzip, zstd, zip, 7z, tar and
// the tar.{gz,zst,zstd} variants). Used by the cmd layer to decide
// whether multi-file parallelism is profitable: compressed parsers are
// CPU-bound on decompression, so spreading them over goroutines pays off
// even on small individual files. Plain log files don't share that
// property — see determineWorkerCount.
func IsCompressed(filename string) bool {
	lowerName := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lowerName, ".zip"),
		strings.HasSuffix(lowerName, ".7z"),
		strings.HasSuffix(lowerName, ".tar"),
		strings.HasSuffix(lowerName, ".tar.gz"),
		strings.HasSuffix(lowerName, ".tgz"),
		strings.HasSuffix(lowerName, ".tar.zst"),
		strings.HasSuffix(lowerName, ".tar.zstd"),
		strings.HasSuffix(lowerName, ".tzst"),
		strings.HasSuffix(lowerName, ".gz"),
		strings.HasSuffix(lowerName, ".zst"),
		strings.HasSuffix(lowerName, ".zstd"):
		return true
	}
	return false
}

// UsesStreamParallelStderr reports whether the input named filename, whose
// on-disk size is `size`, will be parsed by the entry-sharded parallel stderr
// engine (parseParallel for plain, parseStreamParallel for compressed) rather
// than the sequential single-goroutine reader. It is the single source of truth
// for two decisions that must agree, so they can never diverge:
//
//   - the compressed parse router (wrapCompressedParser), and
//   - the multi-file fan-in window (cmd.fanInWindow).
//
// A file self-parallelizes only when it is stream-parallel-eligible stderr
// (non-syslog stderr, per SupportsPIDSharding) AND large enough to clear the
// size gate (reachesStreamParallelSize). The fan-in window shrinks only for such
// files — they already saturate the CPU and fan out in-flight chunks — while a
// sub-gate stderr set (now parsed sequentially, one goroutine per file) keeps the
// wide window that keeps the worker pool busy at low RSS.
//
// Native-only (!js): it reaches into SupportsPIDSharding, which samples the
// gzip/zstd/tar codecs. The WASM build never multi-files and never calls this.
func UsesStreamParallelStderr(filename string, size int64) bool {
	if !SupportsPIDSharding(filename) {
		return false
	}
	// Tar members self-parallelize inside TarParser (routeStderrStream) with no
	// size gate, so a stderr-bearing archive always warrants the small window —
	// its members fan out regardless of the archive's on-disk size. Keep the
	// pre-existing behavior for tar and apply the size gate only to the direct
	// plain/compressed stderr paths, which are the ones that now fall back to
	// the sequential reader below the gate.
	if isTarArchiveName(strings.ToLower(filename)) {
		return true
	}
	return reachesStreamParallelSize(IsCompressed(filename), size)
}

// detectCompressedFile checks if the file is compressed or a tar archive and returns the appropriate parser.
// Returns (parser, error, handled). If handled is false, the caller should continue with normal detection.
func detectCompressedFile(filename string) (LogParser, error, bool) {
	lowerName := strings.ToLower(filename)

	// Check for zip archives
	if strings.HasSuffix(lowerName, ".zip") {
		return &ZipParser{}, nil, true
	}

	// Check for 7z archives
	if strings.HasSuffix(lowerName, ".7z") {
		return &SevenZipParser{}, nil, true
	}

	// Check for tar archives
	if strings.HasSuffix(lowerName, ".tar.gz") ||
		strings.HasSuffix(lowerName, ".tgz") ||
		strings.HasSuffix(lowerName, ".tar.zst") ||
		strings.HasSuffix(lowerName, ".tar.zstd") ||
		strings.HasSuffix(lowerName, ".tzst") ||
		strings.HasSuffix(lowerName, ".tar") {
		return &TarParser{}, nil, true
	}

	if strings.HasSuffix(lowerName, ".gz") {
		baseName := filename[:len(filename)-len(".gz")]
		parser, err := detectCompressedParserWithError(filename, baseName, gzipCodec)
		return parser, err, true
	}

	if strings.HasSuffix(lowerName, ".zstd") {
		baseName := filename[:len(filename)-len(".zstd")]
		parser, err := detectCompressedParserWithError(filename, baseName, zstdCodec)
		return parser, err, true
	}

	if strings.HasSuffix(lowerName, ".zst") {
		baseName := filename[:len(filename)-len(".zst")]
		parser, err := detectCompressedParserWithError(filename, baseName, zstdCodec)
		return parser, err, true
	}

	// No known compression extension — sniff the leading magic bytes so a
	// gzip/zstd stream with a plain or wrong extension (e.g. a renamed .log)
	// is still decompressed instead of being rejected as binary. The base
	// name keeps the full filename so inner-format detection falls back to
	// the decompressed content.
	if codec, ok := sniffCompressionMagic(filename); ok {
		parser, err := detectCompressedParserWithError(filename, filename, codec)
		return parser, err, true
	}

	return nil, nil, false
}

// sniffCompressionMagic returns the codec matching the file's leading magic
// bytes (gzip: 1F 8B, zstd: 28 B5 2F FD), or ok=false if neither matches.
// PostgreSQL text logs never start with these bytes, so the check is safe.
func sniffCompressionMagic(filename string) (compressionCodec, bool) {
	f, err := os.Open(filename)
	if err != nil {
		return compressionCodec{}, false
	}
	defer f.Close()

	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	switch {
	case n >= 2 && magic[0] == 0x1F && magic[1] == 0x8B:
		return gzipCodec, true
	case n >= 4 && magic[0] == 0x28 && magic[1] == 0xB5 && magic[2] == 0x2F && magic[3] == 0xFD:
		return zstdCodec, true
	}
	return compressionCodec{}, false
}

// detectCompressedParserWithError handles detection for compressed log files using the provided codec.
// Returns a LogParser and nil error on success, or nil parser and a typed error on failure.
func detectCompressedParserWithError(filename, baseName string, codec compressionCodec) (LogParser, error) {
	sample, err := readCompressedSample(filename, codec)
	if err != nil {
		slog.Error("failed to read compressed sample", "codec", codec.name, "file", filename, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrCompressionFailed, err)
	}
	// A BOM inside the compressed payload would derail detection the same way
	// it does for plain files; strip it (the parsers strip it again at parse).
	sample = strings.TrimPrefix(sample, "\ufeff")

	if isBinaryContent(sample) {
		slog.Error("file appears to be binary after decompression", "file", filename, "codec", codec.name)
		return nil, ErrBinaryFile
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))

	parser := detectByExtension(baseName, ext, sample)
	if parser == nil {
		// Only try content detection if extension was unknown
		// If extension was known but content didn't match, error already logged
		if ext != "csv" && ext != "json" && ext != "log" {
			parser = detectByContent(baseName, sample)
		} else {
			return nil, ErrInvalidFormat
		}
	}

	if parser == nil {
		return nil, ErrUnknownFormat
	}

	return wrapCompressedParser(parser, codec), nil
}

// compressedSampleShardable reports whether the decompressed content of a
// single-file gzip/zstd input is non-syslog stderr — i.e. whether the analysis
// of this input benefits from PID-sharding (see sampleShardable). baseName is
// filename with the compression suffix stripped, so the inner extension drives
// format detection.
func compressedSampleShardable(filename string, codec compressionCodec, baseName string) bool {
	sample, err := readCompressedSample(filename, codec)
	if err != nil {
		return false
	}
	sample = strings.TrimPrefix(sample, string(utf8BOM))
	return sampleShardable(filepath.Base(baseName), sample)
}

// readCompressedSample streams the first portion of the compressed file and returns it as text.
func readCompressedSample(filename string, codec compressionCodec) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	cr, err := codec.opener(file)
	if err != nil {
		return "", err
	}
	defer cr.Close()

	buf := make([]byte, sampleBufferSize)
	n, err := cr.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}

	rawSample := string(buf[:n])
	lastNewline := strings.LastIndex(rawSample, "\n")

	if lastNewline == -1 {
		extended, err := readUntilNLinesCompressed(cr, extendedSampleLines)
		if err != nil {
			return "", err
		}
		return extended, nil
	}

	return rawSample[:lastNewline], nil
}

// readUntilNLinesCompressed reads additional lines from the decompressed stream when the initial buffer had no newline.
func readUntilNLinesCompressed(r io.Reader, n int) (string, error) {
	var sample strings.Builder
	var lineCount int

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		sample.WriteString(scanner.Text())
		sample.WriteString("\n")
		lineCount++
		if lineCount >= n {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return sample.String(), nil
}

// wrapCompressedParser converts an existing parser into a codec-aware parser.
func wrapCompressedParser(parser LogParser, codec compressionCodec) LogParser {
	switch src := parser.(type) {
	case *JsonParser:
		p := &JsonParser{}
		return newCompressedParser(codec, func(r io.Reader, size int64, out chan<- []LogEntry) error {
			return p.parseReader(r, out)
		})
	case *CsvParser:
		p := &CsvParser{}
		return newCompressedParser(codec, func(r io.Reader, size int64, out chan<- []LogEntry) error {
			return p.parseReader(r, out)
		})
	case *StderrParser:
		// Carry the detected leading-prefix offset into the decompression
		// wrapper; otherwise a prefixed log would detect the prefix on its
		// sample and then silently parse zero entries from the stream.
		p := &StderrParser{prefixLen: src.prefixLen}
		return newCompressedParser(codec, func(r io.Reader, size int64, out chan<- []LogEntry) error {
			// Compressed streams can't seek, so the segment engine doesn't
			// apply — but the stream-chunked sibling parallelizes the parse
			// the same way. Prefixed streams keep the prefix-aware sequential
			// reader (same routing as Parse). Small streams also stay
			// sequential: the parallel path's (workers+2) in-flight 4 MB
			// chunks cost peak RSS for a negligible wall win when the
			// single-threaded decode paces the pipeline, so gate it on the
			// estimated decompressed size exactly as the plain seekable path
			// gates parseParallel on file size (reachesStreamParallelSize).
			// Below the gate we fall back to the pre-parallel single-goroutine
			// reader — the behavior small compressed sets had before the
			// stream engine existed. The fan-in window keys off the same gate
			// (UsesStreamParallelStderr), so a sequential file gets the wide
			// window that keeps the worker pool busy.
			// Half the segment-path worker count: the stream chunker (decode +
			// boundary scan, single goroutine) paces the pipeline well below
			// what 4 parse workers absorb — 8 only added allocation-rate GC
			// headroom (measured: same wall, −18% RSS on a single .zst).
			if workers := parallelWorkers() / 2; workers >= 2 && p.prefixLen == 0 &&
				reachesStreamParallelSize(true, size) {
				return p.parseStreamParallel(r, workers, out)
			}
			return p.parseReader(r, out)
		})
	default:
		slog.Error("unsupported parser type for compressed files", "codec", codec.name, "parser_type", fmt.Sprintf("%T", parser))
		return nil
	}
}

type compressedLogParser struct {
	// parse runs a format parser over the decompressed stream. size is the
	// on-disk (compressed) byte size, threaded through so the stderr router
	// can gate the parallel engine on the estimated decompressed size.
	parse func(r io.Reader, size int64, out chan<- []LogEntry) error
	codec compressionCodec
}

func newCompressedParser(codec compressionCodec, parse func(r io.Reader, size int64, out chan<- []LogEntry) error) LogParser {
	return &compressedLogParser{
		parse: parse,
		codec: codec,
	}
}

func (c *compressedLogParser) Parse(filename string, out chan<- []LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	// On-disk (compressed) size feeds the stderr router's decompressed-size
	// gate. A stat failure leaves size 0, which reads as "below the gate" —
	// the safe, sequential-parse default.
	var size int64
	if st, serr := file.Stat(); serr == nil {
		size = st.Size()
	}

	// Wrap the on-disk reader BEFORE the decompressor so progress
	// reflects compressed bytes consumed (= file size on disk = the
	// denominator the CLI bar shows). Wrapping the decompressed
	// stream would let the bar overshoot 100%.
	reader, err := c.codec.opener(WithProgress(file))
	if err != nil {
		return fmt.Errorf("failed to open %s reader for %s: %w", c.codec.name, filename, err)
	}
	defer reader.Close()

	return c.parse(reader, size, out)
}

// newParallelGzipReader returns a pgzip reader configured for parallel decompression.
func newParallelGzipReader(r io.Reader) (*pgzip.Reader, error) {
	threads := runtime.GOMAXPROCS(0)
	if threads < 1 {
		threads = 1
	}
	if threads > 8 {
		threads = 8 // cap to avoid excessive goroutine churn on large hosts
	}

	const blockSize = 1 << 20 // 1 MiB blocks balance throughput and memory usage
	return pgzip.NewReaderN(r, blockSize, threads)
}

type zstdReadCloser struct {
	*zstd.Decoder
}

func (z *zstdReadCloser) Close() error {
	z.Decoder.Close()
	return nil
}

// newZstdDecoder returns a zstd decoder configured for streaming decompression.
func newZstdDecoder(r io.Reader) (io.ReadCloser, error) {
	dec, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	return &zstdReadCloser{Decoder: dec}, nil
}
