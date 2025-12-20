//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
// This file contains compression handling code excluded from WASM builds.
package parser

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
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

// detectCompressedFile checks if the file is compressed or a tar archive and returns the appropriate parser.
// Returns (parser, error, handled). If handled is false, the caller should continue with normal detection.
func detectCompressedFile(filename string) (LogParser, error, bool) {
	lowerName := strings.ToLower(filename)

	// Check for tar archives first
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

// detectCompressedParser handles detection for compressed log files using the provided codec.
func detectCompressedParser(filename, baseName string, codec compressionCodec) LogParser {
	parser, _ := detectCompressedParserWithError(filename, baseName, codec)
	return parser
}

// detectCompressedParserWithError handles detection for compressed log files using the provided codec.
// Returns a LogParser and nil error on success, or nil parser and a typed error on failure.
func detectCompressedParserWithError(filename, baseName string, codec compressionCodec) (LogParser, error) {
	sample, err := readCompressedSample(filename, codec)
	if err != nil {
		log.Printf("[ERROR] Failed to read %s sample from %s: %v", codec.name, filename, err)
		return nil, fmt.Errorf("%w: %v", ErrCompressionFailed, err)
	}

	if isBinaryContent(sample) {
		log.Printf("[ERROR] File %s appears to be binary after %s decompression. Binary formats are not supported.", filename, codec.name)
		return nil, ErrBinaryFile
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))

	parser := detectByExtension(baseName, ext, sample, false)
	if parser == nil {
		// Only try content detection if extension was unknown
		// If extension was known but content didn't match, error already logged
		if ext != "csv" && ext != "json" && ext != "log" {
			parser = detectByContent(baseName, sample, false)
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
		return newCompressedParser(codec, func(r io.Reader, out chan<- LogEntry) error {
			return p.parseReader(r, out)
		})
	case *CsvParser:
		p := &CsvParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- LogEntry) error {
			return p.parseReader(r, out)
		})
	case *StderrParser:
		p := &StderrParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- LogEntry) error {
			return p.parseReader(r, out)
		})
	case *MmapStderrParser:
		// mmap is not supported with compressed streams; fall back to standard stderr parser
		p := &StderrParser{}
		return newCompressedParser(codec, func(r io.Reader, out chan<- LogEntry) error {
			return p.parseReader(r, out)
		})
	default:
		log.Printf("[ERROR] Unsupported parser type for %s compressed files: %T", codec.name, parser)
		return nil
	}
}

type compressedLogParser struct {
	parse func(io.Reader, chan<- LogEntry) error
	codec compressionCodec
}

func newCompressedParser(codec compressionCodec, parse func(io.Reader, chan<- LogEntry) error) LogParser {
	return &compressedLogParser{
		parse: parse,
		codec: codec,
	}
}

func (c *compressedLogParser) Parse(filename string, out chan<- LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	reader, err := c.codec.opener(file)
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
