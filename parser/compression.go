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

	return nil, nil, false
}

// detectCompressedParserWithError handles detection for compressed log files using the provided codec.
// Returns a LogParser and nil error on success, or nil parser and a typed error on failure.
func detectCompressedParserWithError(filename, baseName string, codec compressionCodec) (LogParser, error) {
	sample, err := readCompressedSample(filename, codec)
	if err != nil {
		slog.Error("failed to read compressed sample", "codec", codec.name, "file", filename, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrCompressionFailed, err)
	}

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
	switch parser.(type) {
	case *JsonParser:
		p := &JsonParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- []LogEntry) error {
			return p.parseReader(r, out)
		})
	case *CsvParser:
		p := &CsvParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- []LogEntry) error {
			return p.parseReader(r, out)
		})
	case *StderrParser:
		p := &StderrParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- []LogEntry) error {
			return p.parseReader(r, out)
		})
	default:
		slog.Error("unsupported parser type for compressed files", "codec", codec.name, "parser_type", fmt.Sprintf("%T", parser))
		return nil
	}
}

type compressedLogParser struct {
	parse func(io.Reader, chan<- []LogEntry) error
	codec compressionCodec
}

func newCompressedParser(codec compressionCodec, parse func(io.Reader, chan<- []LogEntry) error) LogParser {
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

	// Wrap the on-disk reader BEFORE the decompressor so progress
	// reflects compressed bytes consumed (= file size on disk = the
	// denominator the CLI bar shows). Wrapping the decompressed
	// stream would let the bar overshoot 100%.
	reader, err := c.codec.opener(WithProgress(file))
	if err != nil {
		return fmt.Errorf("failed to open %s reader for %s: %w", c.codec.name, filename, err)
	}
	defer reader.Close()

	return c.parse(reader, out)
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
