// Package parser provides log file parsing and format detection for PostgreSQL logs.
// It supports multiple formats: stderr/syslog, CSV, and JSON.
package parser

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
func ParseFile(filename string, out chan<- LogEntry) error {
	parser := detectParser(filename)
	if parser == nil {
		return fmt.Errorf("unknown log format for file: %s", filename)
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
func detectParser(filename string) LogParser {
	// Step 1: Validate file exists and is not empty
	fi, err := os.Stat(filename)
	if err != nil {
		log.Printf("[ERROR] Cannot stat file %s: %v", filename, err)
		return nil
	}
	if fi.Size() == 0 {
		log.Printf("[WARN] File %s is empty", filename)
		return nil
	}

	// Step 2: Open file and read sample
	f, err := os.Open(filename)
	if err != nil {
		log.Printf("[ERROR] Cannot open file %s: %v", filename, err)
		return nil
	}
	defer f.Close()

	sample, err := readFileSample(f)
	if err != nil {
		log.Printf("[ERROR] Failed to read sample from %s: %v", filename, err)
		return nil
	}

	// Step 3: Check for binary content
	if isBinaryContent(sample) {
		log.Printf("[ERROR] File %s appears to be binary. Binary formats are not supported.", filename)
		return nil
	}

	// Step 4: Try extension-based detection
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	parser := detectByExtension(filename, ext, sample)
	if parser != nil {
		return parser
	}

	// Step 5: Fall back to content-based detection
	return detectByContent(filename, sample)
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
func detectByExtension(filename, ext, sample string) LogParser {
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
func detectByContent(filename, sample string) LogParser {
	switch {
	case isJSONContent(sample):
		log.Printf("[INFO] Detected JSON format for %s (unknown extension)", filename)
		return &JsonParser{}

	case isCSVContent(sample):
		log.Printf("[INFO] Detected CSV format for %s (unknown extension)", filename)
		return &CsvParser{}

	case isLogContent(sample):
		log.Printf("[INFO] Detected stderr/syslog format for %s (unknown extension)", filename)
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

	// Show first 200 chars of sample
	preview := trimmed
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}

	// Must start with '{' or '['
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}

	// Try to unmarshal as a single JSON object/array first
	var js interface{}
	err := json.Unmarshal([]byte(trimmed), &js)

	// If it fails, try JSONL format (newline-delimited JSON)
	if err != nil {
		// Try to parse first line as JSON
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
	} else {
	}

	// Check for timestamp-related fields anywhere in the sample
	hasTimestamp := strings.Contains(trimmed, `"timestamp"`)
	hasTime := strings.Contains(trimmed, `"time"`)
	hasTs := strings.Contains(trimmed, `"ts"`)
	hasAtTimestamp := strings.Contains(trimmed, `"@timestamp"`)
	hasInsertId := strings.Contains(trimmed, `"insertId"`)

	result := hasTimestamp || hasTime || hasTs || hasAtTimestamp || hasInsertId

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
