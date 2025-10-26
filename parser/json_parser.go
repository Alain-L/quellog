// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// JsonParser parses PostgreSQL logs in JSON format.
// It supports multiple JSON log formats:
//   - Standard format: {"timestamp":"2025-01-01T12:00:00Z","message":"log text"}
//   - Google Cloud SQL format with insertId and timestamp fields
//   - Nested structures with flexible field extraction
//
// The parser is lenient and attempts to extract timestamp and message fields
// even if the JSON structure doesn't exactly match LogEntry.
type JsonParser struct{}

// Parse reads a JSON format log file and streams parsed entries.
// The file should contain either:
//   - A JSON array of log objects
//   - Newline-delimited JSON objects (JSONL/NDJSON format)
//
// Each JSON object should have at minimum:
//   - A timestamp field (various formats supported)
//   - A message or text field
//
// The parser skips malformed JSON objects and logs warnings, but continues processing.
//
// IMPORTANT: This function does NOT close the output channel. The caller is responsible
// for channel lifecycle management (as per LogParser interface contract).
func (p *JsonParser) Parse(filename string, out chan<- LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer f.Close()

	// Try to parse as JSON array first, then fall back to JSONL
	if err := p.parseJSONArray(f, out); err == nil {
		return nil
	}

	// Reopen file for JSONL parsing (decoder consumed the file)
	f.Close()
	f, err = os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to reopen file %s: %w", filename, err)
	}
	defer f.Close()

	return p.parseJSONLines(f, out)
}

// parseJSONArray attempts to parse the file as a JSON array of log entries.
// Format: [{"timestamp":"...","message":"..."},...]
func (p *JsonParser) parseJSONArray(f *os.File, out chan<- LogEntry) error {
	var entries []map[string]interface{}
	decoder := json.NewDecoder(f)

	if err := decoder.Decode(&entries); err != nil {
		return err // Not a JSON array, will try JSONL
	}

	for i, obj := range entries {
		entry, err := extractLogEntry(obj)
		if err != nil {
			log.Printf("[WARN] Skipping malformed JSON entry at index %d: %v", i, err)
			continue
		}
		out <- entry
	}

	return nil
}

// parseJSONLines parses newline-delimited JSON (JSONL/NDJSON format).
// Format: {"timestamp":"...","message":"..."}\n{"timestamp":"...","message":"..."}\n
func (p *JsonParser) parseJSONLines(f *os.File, out chan<- LogEntry) error {
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			log.Printf("[WARN] Skipping malformed JSON at line %d: %v", lineNum, err)
			continue
		}

		entry, err := extractLogEntry(obj)
		if err != nil {
			log.Printf("[WARN] Skipping incomplete JSON entry at line %d: %v", lineNum, err)
			continue
		}

		out <- entry
	}

	return scanner.Err()
}

// extractLogEntry extracts timestamp and message from a JSON object.
// It supports multiple field name variations and formats commonly used
// in PostgreSQL JSON logs.
//
// Supported timestamp fields (in order of preference):
//   - "timestamp"
//   - "time"
//   - "ts"
//   - "@timestamp" (Elasticsearch/Logstash format)
//
// Supported message fields:
//   - "message"
//   - "msg"
//   - "text"
//   - "log"
//
// Returns an error if required fields are missing or invalid.
func extractLogEntry(obj map[string]interface{}) (LogEntry, error) {
	// Extract timestamp
	timestamp, err := extractTimestamp(obj)
	if err != nil {
		return LogEntry{}, fmt.Errorf("timestamp extraction failed: %w", err)
	}

	// Extract message
	message, err := extractMessage(obj)
	if err != nil {
		return LogEntry{}, fmt.Errorf("message extraction failed: %w", err)
	}

	return LogEntry{
		Timestamp: timestamp,
		Message:   message,
	}, nil
}

// extractTimestamp extracts and parses the timestamp from a JSON object.
// Supports multiple field names and time formats.
func extractTimestamp(obj map[string]interface{}) (time.Time, error) {
	// Try different field names
	timestampFields := []string{"timestamp", "time", "ts", "@timestamp"}

	for _, field := range timestampFields {
		if val, ok := obj[field]; ok && val != nil {
			return parseTimestampValue(val)
		}
	}

	return time.Time{}, fmt.Errorf("no timestamp field found")
}

// parseTimestampValue parses a timestamp value from various formats.
// Supports:
//   - RFC3339 strings (2025-01-01T12:00:00Z)
//   - Unix timestamps (seconds or milliseconds)
//   - PostgreSQL format (2025-01-01 12:00:00)
func parseTimestampValue(val interface{}) (time.Time, error) {
	switch v := val.(type) {
	case string:
		// Try RFC3339 format (ISO 8601)
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
		// Try RFC3339Nano
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, nil
		}
		// Try PostgreSQL format
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			return t, nil
		}
		// Try with timezone
		if t, err := time.Parse("2006-01-02 15:04:05 MST", v); err == nil {
			return t, nil
		}
		return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", v)

	case float64:
		// Unix timestamp (seconds or milliseconds)
		if v > 1e12 { // Likely milliseconds
			return time.Unix(0, int64(v)*int64(time.Millisecond)), nil
		}
		return time.Unix(int64(v), 0), nil

	case int64:
		// Unix timestamp
		return time.Unix(v, 0), nil

	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type: %T", val)
	}
}

// extractMessage extracts the message text from a JSON object.
// Tries multiple common field names.
func extractMessage(obj map[string]interface{}) (string, error) {
	// Try different field names
	messageFields := []string{"message", "msg", "text", "log"}

	for _, field := range messageFields {
		if val, ok := obj[field]; ok && val != nil {
			if msg, ok := val.(string); ok {
				return msg, nil
			}
		}
	}

	return "", fmt.Errorf("no message field found")
}
