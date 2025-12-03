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
