// Package parser provides log file parsing and format detection for PostgreSQL logs.
package parser

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
)

// ParseStdin reads from standard input, detects the log format, and streams parsed entries.
// It reads a sample to detect the format, then creates a combined reader with the sample
// and remaining stdin data for streaming parsing.
func ParseStdin(out chan<- LogEntry) error {
	// Read a sample from stdin to detect format
	sample, err := readStdinSample(os.Stdin)
	if err != nil {
		log.Printf("[ERROR] Failed to read sample from stdin: %v", err)
		return fmt.Errorf("stdin: %w", ErrUnknownFormat)
	}

	// Check for binary content
	sampleStr := string(sample)
	if isBinaryContent(sampleStr) {
		log.Printf("[ERROR] stdin appears to contain binary data. Binary formats are not supported.")
		return fmt.Errorf("stdin: %w", ErrBinaryFile)
	}

	// Detect format from sample
	parser := detectFormatFromSample(sampleStr)
	if parser == nil {
		log.Printf("[ERROR] Unable to detect log format from stdin")
		return fmt.Errorf("stdin: %w", ErrUnknownFormat)
	}

	// Create a combined reader: sample + remaining stdin
	// This ensures we don't lose the sample data
	combined := io.MultiReader(bytes.NewReader(sample), os.Stdin)

	// Parse using the detected format
	return parseFromReader(parser, combined, out)
}

// readStdinSample reads a sample from stdin without consuming all of it.
// Returns up to sampleBufferSize bytes or until EOF.
func readStdinSample(r io.Reader) ([]byte, error) {
	buf := make([]byte, sampleBufferSize)
	n, err := io.ReadAtLeast(r, buf, 1)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

// detectFormatFromSample attempts to detect the log format from a sample.
// Returns the appropriate parser or nil if format cannot be detected.
func detectFormatFromSample(sample string) LogParser {
	// Try CSV detection first (most structured)
	if isCSVContent(sample) {
		log.Printf("[INFO] Detected CSV format from stdin")
		return &CsvParser{}
	}

	// Try JSON detection
	if isJSONContent(sample) {
		log.Printf("[INFO] Detected JSON format from stdin")
		return &JsonParser{}
	}

	// Try stderr/syslog detection
	if isLogContent(sample) {
		log.Printf("[INFO] Detected stderr/syslog format from stdin")
		return &StderrParser{}
	}

	return nil
}

// parseFromReader parses log entries from an io.Reader using the specified parser.
// This is a generic parsing function that works with any LogParser implementation.
func parseFromReader(parser LogParser, r io.Reader, out chan<- LogEntry) error {
	switch p := parser.(type) {
	case *CsvParser:
		return p.parseReader(r, out)
	case *JsonParser:
		return p.parseReader(r, out)
	case *StderrParser:
		return p.parseReader(r, out)
	default:
		return fmt.Errorf("unsupported parser type: %T", p)
	}
}
