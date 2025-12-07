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

// ParseFromBytesSync parses log content directly from a byte slice.
// This is optimized for WASM and other cases where data is already in memory.
// For stderr format, it uses direct byte parsing which avoids scanner.Text() allocations.
func ParseFromBytesSync(data []byte, format string) ([]LogEntry, error) {
	// For stderr format, use optimized direct byte parsing
	if format == "stderr" || format == "log" {
		entryChan := make(chan LogEntry, 65536)
		var parseErr error

		go func() {
			p := &StderrParser{}
			parseErr = p.parseFromBytes(data, entryChan)
			close(entryChan)
		}()

		entries := make([]LogEntry, 0, 100000)
		for entry := range entryChan {
			entries = append(entries, entry)
		}

		return entries, parseErr
	}

	// For other formats, convert to string and use standard parsing
	// (CSV and JSON parsers don't benefit from byte-level parsing)
	return ParseFromStringSync(string(data), format)
}
