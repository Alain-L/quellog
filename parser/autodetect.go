// Package parser provides log file parsing and format detection for PostgreSQL logs.
// It supports multiple formats: stderr/syslog, CSV, and JSON.
package parser

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
)

// Detection errors - used to distinguish between different failure causes
var (
	ErrFileEmpty          = errors.New("file is empty")
	ErrBinaryFile         = errors.New("file appears to be binary")
	ErrInvalidFormat      = errors.New("file content doesn't match expected format for extension")
	ErrUnknownFormat      = errors.New("unable to detect log format")
	ErrCompressionFailed  = errors.New("failed to read compressed file")
)

// Constants for format detection
const (
	// sampleBufferSize is the initial buffer size for reading file samples
	sampleBufferSize = 32 * 1024 // 32 KB

	// extendedSampleLines is the number of lines to read when initial sample has no newlines
	extendedSampleLines = 5

	// minCSVFields is the minimum number of fields expected in a PostgreSQL CSV log
	minCSVFields = 12

	// minCSVCommas is the minimum number of commas expected in a CSV log line
	minCSVCommas = 12

	// binaryThreshold is the maximum ratio of non-printable characters before considering a file binary
	binaryThreshold = 0.3
)

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

// Regex patterns for format detection (compiled once at init time)
var (
	// csvTimestampRegex matches PostgreSQL CSV log timestamp format
	csvTimestampRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?(?: [A-Z]{2,5})?$`)

	// jsonFieldRegex checks for required fields in JSON logs
	jsonFieldRegex = regexp.MustCompile(`^\s*\{\s*"(timestamp|insertId)"\s*:`)

	// logPatterns define various PostgreSQL log format patterns
	logPatterns = []*regexp.Regexp{
		// Pattern 1: ISO-style timestamp (2025-01-01 12:00:00 or 2025-01-01T12:00:00)
		// with optional fractional seconds, timezone, and log level
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?: [A-Z]{2,5})?.*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 2: Syslog format (Mon  3 12:34:56)
		// with host, process info, and log level
		regexp.MustCompile(`^[A-Z][a-z]{2}\s+\d+\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:\s+[A-Z]{2,5})?\s+\S+\s+\S+\[\d+\]:(?:\s+\[[^\]]+\])?\s+\[\d+(?:-\d+)?\].*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 3: Epoch timestamp or simple date-time
		regexp.MustCompile(`^(?:\d{10}\.\d{3}|\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}).*?\b(?:LOG|WARNING|ERROR|FATAL|PANIC|DETAIL|STATEMENT|HINT|CONTEXT):\s+`),

		// Pattern 4: Minimal ISO format with basic log levels
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}.*?\b(?:LOG|ERROR|WARNING|FATAL|PANIC):\s+`),
	}
)

// ParseFile detects the log format and parses the file in streaming mode.
// It automatically detects whether the file is in stderr/syslog, CSV, or JSON format.
// Returns an error if the format is unknown or parsing fails.
//
// For stderr/syslog format, uses memory-mapped I/O by default with automatic
// fallback to buffered I/O if mmap fails (network filesystems, pipes, etc.).
func ParseFile(filename string, out chan<- LogEntry) error {
	parser, err := detectParser(filename)
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	return parser.Parse(filename, out)
}

// detectParser reads a sample from the file to identify its format.
// It tries to detect the format based on file extension first, then falls back
// to content-based detection.
//
// Detection order:
//  1. Check file existence and size
//  2. Read a sample (32KB or until 5 newlines)
//  3. Check for binary content
//  4. Try extension-based detection (.log, .csv, .json)
//  5. Fall back to content-based detection
//
// Returns a LogParser and nil error on success, or nil parser and a typed error on failure.
func detectParser(filename string) (LogParser, error) {
	lowerName := strings.ToLower(filename)
	if strings.HasSuffix(lowerName, ".tar.gz") ||
		strings.HasSuffix(lowerName, ".tgz") ||
		strings.HasSuffix(lowerName, ".tar.zst") ||
		strings.HasSuffix(lowerName, ".tar.zstd") ||
		strings.HasSuffix(lowerName, ".tzst") ||
		strings.HasSuffix(lowerName, ".tar") {
		return &TarParser{}, nil
	}

	if strings.HasSuffix(lowerName, ".gz") {
		baseName := filename[:len(filename)-len(".gz")]
		return detectCompressedParserWithError(filename, baseName, gzipCodec)
	}

	if strings.HasSuffix(lowerName, ".zstd") {
		baseName := filename[:len(filename)-len(".zstd")]
		return detectCompressedParserWithError(filename, baseName, zstdCodec)
	}

	if strings.HasSuffix(lowerName, ".zst") {
		baseName := filename[:len(filename)-len(".zst")]
		return detectCompressedParserWithError(filename, baseName, zstdCodec)
	}

	// Step 1: Validate file exists and is not empty
	fi, err := os.Stat(filename)
	if err != nil {
		log.Printf("[ERROR] Cannot stat file %s: %v", filename, err)
		return nil, fmt.Errorf("%w: %v", ErrUnknownFormat, err)
	}
	if fi.Size() == 0 {
		log.Printf("[WARN] File %s is empty", filename)
		return nil, ErrFileEmpty
	}

	// Step 2: Open file and read sample
	f, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] Cannot open file %s: %v", filename, err)
		return nil, fmt.Errorf("%w: %v", ErrUnknownFormat, err)
	}
	defer f.Close()

	sample, err := readFileSample(f)
	if err != nil {
		log.Printf("[ERROR] Failed to read sample from %s: %v", filename, err)
		return nil, fmt.Errorf("%w: %v", ErrUnknownFormat, err)
	}

	// Step 3: Check for binary content
	if isBinaryContent(sample) {
		log.Printf("[ERROR] File %s appears to be binary. Binary formats are not supported.", filename)
		return nil, ErrBinaryFile
	}

	// Step 4: Try extension-based detection
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	parser := detectByExtension(filename, ext, sample, true)
	if parser != nil {
		return parser, nil
	}

	// Step 5: Fall back to content-based detection
	// Only try content detection if extension was unknown (not csv/json/log)
	// If extension was known but content didn't match, we already logged a specific error
	if ext == "csv" || ext == "json" || ext == "log" {
		// Extension was known but content validation failed - error already logged
		return nil, ErrInvalidFormat
	}

	parser = detectByContent(filename, sample, true)
	if parser != nil {
		return parser, nil
	}

	return nil, ErrUnknownFormat
}

// readFileSample reads a representative sample from the file.
// It reads up to sampleBufferSize bytes, ensuring we don't cut a line in the middle.
// If no newlines are found in the initial buffer, it extends the read.
func readFileSample(f *os.File) (string, error) {
	buf := make([]byte, sampleBufferSize)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}

	rawSample := string(buf[:n])
	lastNewline := strings.LastIndex(rawSample, "\n")

	// If no newlines found, try to read more lines
	if lastNewline == -1 {
		extendedSample, err := readUntilNLines(f, extendedSampleLines)
		if err != nil {
			return "", fmt.Errorf("extending read: %w", err)
		}
		if extendedSample == "" {
			return "", fmt.Errorf("no newlines found in file")
		}
		return extendedSample, nil
	}

	// Return sample without the last incomplete line
	return rawSample[:lastNewline], nil
}

// readUntilNLines reads from the file until n complete lines are found.
// Returns the accumulated text or an error.
func readUntilNLines(f *os.File, n int) (string, error) {
	var sample strings.Builder
	var lineCount int

	scanner := bufio.NewScanner(f)
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

// detectByExtension attempts to detect the parser based on file extension.
// Returns nil if the extension doesn't match or content validation fails.
func detectByExtension(filename, ext, sample string, allowMmap bool) LogParser {
	switch ext {
	case "json":
		if isJSONContent(sample) {
			return &JsonParser{}
		}
		log.Printf("[ERROR] File %s has .json extension but content is not valid JSON", filename)
		return nil

	case "csv":
		if isCSVContent(sample) {
			return &CsvParser{}
		}
		log.Printf("[ERROR] File %s has .csv extension but content is not valid CSV", filename)
		return nil

	case "log":
		if isLogContent(sample) {
			if allowMmap {
				return &MmapStderrParser{}
			}
			return &StderrParser{}
		}
		log.Printf("[ERROR] File %s has .log extension but content is not valid log format", filename)
		return nil

	default:
		return nil
	}
}

// detectByContent attempts to detect the parser based on file content.
// This is used when the file extension doesn't provide enough information.
func detectByContent(filename, sample string, allowMmap bool) LogParser {
	switch {
	case isJSONContent(sample):
		log.Printf("[INFO] Detected JSON format for %s (unknown extension)", filename)
		return &JsonParser{}

	case isCSVContent(sample):
		log.Printf("[INFO] Detected CSV format for %s (unknown extension)", filename)
		return &CsvParser{}

	case isLogContent(sample):
		log.Printf("[INFO] Detected stderr/syslog format for %s (unknown extension)", filename)
		if allowMmap {
			return &MmapStderrParser{}
		}
		return &StderrParser{}

	default:
		log.Printf("[ERROR] Unknown log format for file: %s", filename)
		return nil
	}
}

// ============================================================================
// Format validation functions
// ============================================================================

// isJSONContent checks if the sample appears to be valid JSON log content.
// It verifies:
//  1. Sample is not empty
//  2. Starts with '{' or '['
//  3. Can be unmarshaled as JSON (either single object/array or JSONL)
//  4. Contains timestamp-related fields
func isJSONContent(sample string) bool {
	trimmed := strings.TrimSpace(sample)

	if trimmed == "" {
		return false
	}

	// Must start with '{' or '['
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}

	// Try to unmarshal as a single JSON object/array first
	// If the sample is truncated (common with large JSON arrays), validation may fail
	// but we can still detect it as JSON by checking the structure
	var js interface{}
	err := json.Unmarshal([]byte(trimmed), &js)

	// If unmarshaling the full sample fails, try JSONL format (newline-delimited JSON)
	// or check if it's a truncated JSON array with valid structure
	if err != nil {
		// For JSON arrays, the sample might be truncated mid-entry
		// Check if it starts like a JSON array with objects
		if strings.HasPrefix(trimmed, "[") {
			// Look for opening of first object
			firstObjStart := strings.Index(trimmed, "{")
			if firstObjStart == -1 {
				return false
			}
			// Try to extract and validate the first complete object
			objDepth := 0
			inString := false
			escape := false
			for i := firstObjStart; i < len(trimmed); i++ {
				c := trimmed[i]

				if escape {
					escape = false
					continue
				}

				if c == '\\' {
					escape = true
					continue
				}

				if c == '"' && !escape {
					inString = !inString
					continue
				}

				if inString {
					continue
				}

				if c == '{' {
					objDepth++
				} else if c == '}' {
					objDepth--
					if objDepth == 0 {
						// Found a complete object
						firstObj := trimmed[firstObjStart : i+1]
						if err := json.Unmarshal([]byte(firstObj), &js); err == nil {
							// Valid JSON object in array
							goto checkFields
						}
						return false
					}
				}
			}
			// Couldn't find a complete object, but structure looks like JSON array
			goto checkFields
		}

		// Try JSONL format (newline-delimited JSON)
		lines := strings.Split(trimmed, "\n")
		if len(lines) == 0 {
			return false
		}

		firstLine := strings.TrimSpace(lines[0])
		if firstLine == "" {
			return false
		}

		if err := json.Unmarshal([]byte(firstLine), &js); err != nil {
			return false
		}
	}

checkFields:
	// Check for timestamp-related fields anywhere in the sample
	hasTimestamp := strings.Contains(trimmed, `"timestamp"`)
	hasTime := strings.Contains(trimmed, `"time"`)
	hasTs := strings.Contains(trimmed, `"ts"`)
	hasAtTimestamp := strings.Contains(trimmed, `"@timestamp"`)
	hasInsertId := strings.Contains(trimmed, `"insertId"`)
	hasTextPayload := strings.Contains(trimmed, `"textPayload"`)

	result := hasTimestamp || hasTime || hasTs || hasAtTimestamp || hasInsertId || hasTextPayload

	return result
}

// isCSVContent checks if the sample appears to be a valid PostgreSQL CSV log.
// It verifies:
//  1. Minimum number of commas present
//  2. Can be parsed as CSV
//  3. Has minimum required fields
//  4. First field is a valid timestamp
func isCSVContent(sample string) bool {
	// Quick check: must have enough commas
	if strings.Count(sample, ",") < minCSVCommas {
		return false
	}

	// Try parsing as CSV
	r := csv.NewReader(strings.NewReader(sample))
	record, err := r.Read()
	if err != nil {
		return false
	}

	// Check field count
	if len(record) < minCSVFields {
		return false
	}

	// Validate timestamp format in first field
	firstField := strings.TrimSpace(record[0])
	return csvTimestampRegex.MatchString(firstField)
}

// isLogContent checks if the sample appears to be a PostgreSQL stderr/syslog format log.
// It tests the sample against multiple regex patterns that match various PostgreSQL
// log format variations (ISO timestamps, syslog format, epoch timestamps, etc.).
func isLogContent(sample string) bool {
	lines := strings.Split(sample, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check if line matches any of the known log patterns
		for _, pattern := range logPatterns {
			if pattern.MatchString(trimmed) {
				return true
			}
		}
	}

	return false
}

// isBinaryContent checks if the sample contains non-printable characters
// that would indicate a binary (non-text) file.
//
// A file is considered binary if:
//  1. It contains null bytes (\x00)
//  2. More than 30% of characters are non-printable control characters
func isBinaryContent(sample string) bool {
	if len(sample) == 0 {
		return false
	}

	// Immediate rejection for null bytes
	if strings.Contains(sample, "\x00") {
		return true
	}

	// Count non-printable characters (excluding common whitespace)
	nonPrintable := 0
	for _, r := range sample {
		// ASCII control characters (< 32) except newline, carriage return, and tab
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			nonPrintable++
		}
	}

	// Check if ratio exceeds threshold
	ratio := float64(nonPrintable) / float64(len(sample))
	return ratio > binaryThreshold
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
