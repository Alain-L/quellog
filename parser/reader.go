// Package parser provides reader-based parsing functions for WASM and other use cases.
package parser

import (
	"io"
	"strings"
)

// ParseFromReader parses log content from an io.Reader using the specified format.
// Supported formats: "csv", "json", "stderr"
// Returns error if format is unknown.
func ParseFromReader(r io.Reader, format string, out chan<- LogEntry) error {
	switch format {
	case "csv":
		p := &CsvParser{}
		return p.parseReader(r, out)
	case "json":
		p := &JsonParser{}
		return p.parseReader(r, out)
	case "stderr", "log":
		p := &StderrParser{}
		return p.parseReader(r, out)
	default:
		return ErrUnknownFormat
	}
}

// ParseFromString parses log content from a string using the specified format.
// This is a convenience wrapper around ParseFromReader.
func ParseFromString(content string, format string, out chan<- LogEntry) error {
	return ParseFromReader(strings.NewReader(content), format, out)
}

// DetectFormatFromContent detects the log format from content sample.
// Returns "csv", "json", "stderr", or empty string if unknown.
func DetectFormatFromContent(sample string) string {
	switch {
	case isCSVContent(sample):
		return "csv"
	case isJSONContent(sample):
		return "json"
	case isLogContent(sample):
		return "stderr"
	default:
		return ""
	}
}

// ParseFromReaderSync parses log content synchronously (no channels/goroutines).
// This is optimized for single-threaded environments like WASM.
// Returns the parsed entries directly instead of streaming via channels.
func ParseFromReaderSync(r io.Reader, format string) ([]LogEntry, error) {
	// Use a buffered channel and collect results
	// This avoids duplicating all parser logic while still being sync-friendly
	entryChan := make(chan LogEntry, 65536)

	var parseErr error
	go func() {
		parseErr = ParseFromReader(r, format, entryChan)
		close(entryChan)
	}()

	entries := make([]LogEntry, 0, 100000)
	for entry := range entryChan {
		entries = append(entries, entry)
	}

	return entries, parseErr
}

// ParseFromStringSync parses log content from a string synchronously.
// This is a convenience wrapper around ParseFromReaderSync.
func ParseFromStringSync(content string, format string) ([]LogEntry, error) {
	return ParseFromReaderSync(strings.NewReader(content), format)
}
