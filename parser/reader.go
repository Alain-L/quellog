// Package parser provides reader-based parsing functions for WASM and other use cases.
package parser

import (
	"bytes"
	"io"
)

// ParseFromReader parses log content from an io.Reader using the specified format.
// Supported formats: "csv", "json", "stderr"
// Returns error if format is unknown.
func ParseFromReader(r io.Reader, format string, out chan<- []LogEntry) error {
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

// ParseFromBytesStream parses log content from a byte slice and streams
// batches through out. Caller is responsible for consuming and recycling
// each batch (call PutBatch when done with it). Same shape as the CLI's
// ParseFile pipeline so a single consumer pattern works for both paths.
//
// For stderr format, uses the optimized direct byte parser
// (StderrParser.parseFromBytes) which avoids scanner.Text() allocations.
// For CSV and JSON, wrap the byte slice in a bytes.Reader so we don't
// pay a string(data) copy upfront — that copy was 200-350 MB of wasm
// linear memory leaked under tinygo gc=leaking on big JSON/CSV files.
func ParseFromBytesStream(data []byte, format string, out chan<- []LogEntry) error {
	if format == "stderr" || format == "log" {
		p := &StderrParser{}
		return p.parseFromBytes(data, out)
	}
	return ParseFromReader(bytes.NewReader(data), format, out)
}
