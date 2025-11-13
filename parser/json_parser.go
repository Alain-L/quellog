// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// JsonParser parses PostgreSQL logs in JSON format.
// It supports multiple JSON log formats:
//   - Standard PostgreSQL jsonlog format with detailed fields
//   - Simple format: {"timestamp":"2025-01-01T12:00:00Z","message":"log text"}
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

	return p.parseReader(f, out)
}

// parseReader detects the JSON structure and dispatches to the appropriate parser.
func (p *JsonParser) parseReader(r io.Reader, out chan<- LogEntry) error {
	bufReader := bufio.NewReader(r)

	firstByte, err := peekFirstNonWhitespace(bufReader)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("failed to read JSON stream: %w", err)
	}

	switch firstByte {
	case '[':
		return p.parseJSONArray(bufReader, out)
	default:
		return p.parseJSONLines(bufReader, out)
	}
}

// parseJSONArray attempts to parse the file as a JSON array of log entries.
// Format: [{"timestamp":"...","message":"..."},...]
func (p *JsonParser) parseJSONArray(r io.Reader, out chan<- LogEntry) error {
	decoder := json.NewDecoder(r)

	tok, err := decoder.Token()
	if err != nil {
		return err
	}

	del, ok := tok.(json.Delim)
	if !ok || del != '[' {
		return fmt.Errorf("expected JSON array")
	}

	index := 0
	for decoder.More() {
		var obj map[string]interface{}
		if err := decoder.Decode(&obj); err != nil {
			return err
		}

		entry, err := extractLogEntry(obj)
		if err != nil {
			log.Printf("[WARN] Skipping malformed JSON entry at index %d: %v", index, err)
			continue
		}
		out <- entry
		index++
	}

	// Consume closing ']'
	if _, err := decoder.Token(); err != nil {
		return err
	}

	return nil
}

// parseJSONLines parses newline-delimited JSON (JSONL/NDJSON format).
// Format: {"timestamp":"...","message":"..."}\n{"timestamp":"...","message":"..."}\n
func (p *JsonParser) parseJSONLines(r io.Reader, out chan<- LogEntry) error {
	scanner := bufio.NewScanner(r)
	// Configure scanner with large buffer to handle long log lines
	buf := make([]byte, 4*1024*1024)
	scanner.Buffer(buf, 100*1024*1024)

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

// peekFirstNonWhitespace returns the first non-whitespace byte without consuming it.
func peekFirstNonWhitespace(reader *bufio.Reader) (byte, error) {
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if !isWhitespace(b) {
			if err := reader.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}

func isWhitespace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}

// extractLogEntry extracts timestamp and message from a JSON object.
// It supports multiple field name variations and formats commonly used
// in PostgreSQL JSON logs.
//
// PostgreSQL native JSON format includes fields like:
//   - timestamp, user, database, pid, remote_host, session_id
//   - error_severity, message, detail, hint, query, context
//   - application_name, backend_type
//
// Supported timestamp fields (in order of preference):
//   - "timestamp"
//   - "time"
//   - "ts"
//   - "@timestamp" (Elasticsearch/Logstash format)
//
// Message construction:
//   - Primary: "message" field
//   - Enriched with: "detail", "hint", "query", "context" if present
//   - Prefix with severity and context info if available
//
// Returns an error if required fields are missing or invalid.
func extractLogEntry(obj map[string]interface{}) (LogEntry, error) {
	// Extract timestamp
	timestamp, err := extractTimestamp(obj)
	if err != nil {
		return LogEntry{}, fmt.Errorf("timestamp extraction failed: %w", err)
	}

	// Extract and construct message
	message := constructMessage(obj)
	if message == "" {
		return LogEntry{}, fmt.Errorf("message extraction failed: no message content")
	}

	return LogEntry{
		Timestamp: timestamp,
		Message:   message,
	}, nil
}

// constructMessage builds a complete log message from PostgreSQL JSON log fields.
// It combines various fields to reconstruct a message similar to stderr format:
//   - Adds context prefix (user, database, application)
//   - Includes severity level
//   - Appends detail, hint, query, and context if present
//
// Special handling for Google Cloud SQL:
//   - If "textPayload" field exists, use it directly (already formatted PostgreSQL log)
func constructMessage(obj map[string]interface{}) string {
	// Check for Google Cloud SQL format first
	// Cloud SQL encapsulates PostgreSQL logs in "textPayload" field
	textPayload := getStringField(obj, "textPayload")
	if textPayload != "" {
		// textPayload contains pre-formatted PostgreSQL log like:
		// "[1234]: [1-1] db=production,user=webapp LOG: connection received..."
		return textPayload
	}

	// Standard PostgreSQL JSON format
	var parts []string

	// Extract context fields
	user := getStringField(obj, "user")
	database := getStringField(obj, "database")
	appName := getStringField(obj, "application_name")
	severity := getStringField(obj, "error_severity")
	pid := getStringField(obj, "pid")

	// Build context prefix: [pid]: user=X,db=Y,app=Z SEVERITY:
	var contextParts []string
	if pid != "" {
		contextParts = append(contextParts, fmt.Sprintf("[%s]:", pid))
	}
	if user != "" || database != "" || appName != "" {
		var userDbApp []string
		if user != "" {
			userDbApp = append(userDbApp, fmt.Sprintf("user=%s", user))
		}
		if database != "" {
			userDbApp = append(userDbApp, fmt.Sprintf("db=%s", database))
		}
		if appName != "" {
			userDbApp = append(userDbApp, fmt.Sprintf("app=%s", appName))
		}
		if len(userDbApp) > 0 {
			contextParts = append(contextParts, strings.Join(userDbApp, ","))
		}
	}

	if len(contextParts) > 0 {
		parts = append(parts, strings.Join(contextParts, " "))
	}

	// Add severity
	if severity != "" {
		parts = append(parts, severity+":")
	}

	// Main message
	message := getStringField(obj, "message")
	if message != "" {
		parts = append(parts, message)
	}

	// Additional detail fields
	detail := getStringField(obj, "detail")
	if detail != "" {
		parts = append(parts, "DETAIL: "+detail)
	}

	hint := getStringField(obj, "hint")
	if hint != "" {
		parts = append(parts, "HINT: "+hint)
	}

	query := getStringField(obj, "query")
	if query != "" {
		parts = append(parts, "STATEMENT: "+query)
	}

	context := getStringField(obj, "context")
	if context != "" {
		parts = append(parts, "CONTEXT: "+context)
	}

	return strings.Join(parts, " ")
}

// getStringField safely extracts a string field from a map, returning empty string if not found or wrong type.
func getStringField(obj map[string]interface{}, key string) string {
	if val, ok := obj[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
		// Handle numeric types (e.g., pid)
		return fmt.Sprintf("%v", val)
	}
	return ""
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
//   - PostgreSQL format (2025-01-01 12:00:00 or 2025-01-01 12:00:00.123)
//   - PostgreSQL with timezone (2025-01-01 12:00:00 CET, 2025-01-01 12:00:00.123 CET)
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
		// Try PostgreSQL format with milliseconds and timezone
		if t, err := time.Parse("2006-01-02 15:04:05.999 MST", v); err == nil {
			return t, nil
		}
		// Try PostgreSQL format with timezone
		if t, err := time.Parse("2006-01-02 15:04:05 MST", v); err == nil {
			return t, nil
		}
		// Try PostgreSQL format with milliseconds
		if t, err := time.Parse("2006-01-02 15:04:05.999", v); err == nil {
			return t, nil
		}
		// Try PostgreSQL format without timezone
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
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
