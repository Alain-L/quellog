// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// Buffer size constants for scanner
const (
	// scannerBuffer is the initial buffer size for reading log lines (4 MB)
	scannerBuffer = 4 * 1024 * 1024

	// scannerMaxBuffer is the maximum buffer size for very long log lines (100 MB)
	scannerMaxBuffer = 100 * 1024 * 1024

	// syslogTabMarker is the marker used in syslog format for tab characters
	syslogTabMarker = "#011"
)

// StderrParser parses PostgreSQL logs in stderr/syslog format.
// It handles multi-line log entries and supports both standard stderr format
// (YYYY-MM-DD HH:MM:SS TZ) and syslog format (Mon DD HH:MM:SS).
type StderrParser struct{}

// Parse reads a PostgreSQL stderr/syslog format log file and streams parsed entries.
// Multi-line entries (continuation lines starting with whitespace) are automatically
// assembled into single LogEntry records.
//
// The parser handles:
//   - Standard stderr format: "2006-01-02 15:04:05 MST message..."
//   - Syslog format: "Jan _2 15:04:05 message..."
//   - Multi-line log entries (DETAIL, HINT, STATEMENT, etc.)
//   - Syslog tab markers (#011)
func (p *StderrParser) Parse(filename string, out chan<- LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Configure scanner with large buffer to handle long log lines
	// (e.g., STATEMENT lines with large queries)
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	// Accumulate multi-line entries
	var currentEntry string

	for scanner.Scan() {
		line := scanner.Text()

		// Handle syslog tab markers: "#011" represents a tab character
		// Replace it with a space for consistency
		if idx := strings.Index(line, syslogTabMarker); idx != -1 {
			line = " " + line[idx+len(syslogTabMarker):]
		}

		// Check if this is a continuation line (starts with whitespace)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// Append to current entry
			currentEntry += " " + strings.TrimSpace(line)
		} else {
			// This is a new entry, so process the previous one
			if currentEntry != "" {
				timestamp, message := parseStderrLine(currentEntry)
				out <- LogEntry{Timestamp: timestamp, Message: message}
			}
			// Start accumulating new entry
			currentEntry = line
		}
	}

	// Process the last accumulated entry
	if currentEntry != "" {
		timestamp, message := parseStderrLine(currentEntry)
		out <- LogEntry{Timestamp: timestamp, Message: message}
	}

	return scanner.Err()
}

// parseStderrLine extracts the timestamp and message from a log line.
// It attempts to parse two formats:
//  1. Stderr format: "2006-01-02 15:04:05 TZ message..."
//  2. Syslog format: "Jan _2 15:04:05 message..." (current year is assumed)
//
// If parsing fails, returns zero time and the original line as message.
//
// This function uses positional checks for performance, avoiding regex
// and string splitting when possible.
func parseStderrLine(line string) (time.Time, string) {
	n := len(line)

	// Need at least 20 characters for a valid timestamp
	if n < 20 {
		return time.Time{}, line
	}

	// Attempt 1: Parse stderr format (YYYY-MM-DD HH:MM:SS TZ)
	if timestamp, message, ok := parseStderrFormat(line); ok {
		return timestamp, message
	}

	// Attempt 2: Parse syslog format (Mon DD HH:MM:SS)
	if timestamp, message, ok := parseSyslogFormat(line); ok {
		return timestamp, message
	}

	// Unable to parse timestamp, return line as-is
	return time.Time{}, line
}

// parseStderrFormat attempts to parse the standard stderr format:
// "YYYY-MM-DD HH:MM:SS TZ message..."
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after timestamp
//   - ok: true if parsing succeeded
func parseStderrFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation: check for date/time separators
	if n < 20 ||
		line[4] != '-' || line[7] != '-' || // Date separators
		line[10] != ' ' || // Space between date and time
		line[13] != ':' || line[16] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Find the timezone field
	// Format: "YYYY-MM-DD HH:MM:SS TZ"
	//          0123456789012345678901...
	// Expected space after seconds (HH:MM:SS) at position 19

	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		// Handle cases with no space or multiple spaces
		// Scan forward to find next space
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, "", false
		}
		spaceAfterTime = i
	}

	// Skip whitespace to find timezone token
	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	tzStart := i

	// Find end of timezone token
	for i < n && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	if tzEnd <= tzStart {
		return time.Time{}, "", false
	}

	// Parse timestamp: "YYYY-MM-DD HH:MM:SS TZ"
	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	// Skip whitespace before message
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}

// parseSyslogFormat attempts to parse the syslog format:
// "Mon DD HH:MM:SS message..."
//
// Since syslog format doesn't include year, the current year is assumed.
//
// Returns:
//   - timestamp: parsed time with current year
//   - message: remaining text after timestamp
//   - ok: true if parsing succeeded
func parseSyslogFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation for syslog format
	// "Jan _2 15:04:05" = 15 characters
	if n < 15 ||
		line[3] != ' ' || // Space after month abbreviation
		line[6] != ' ' || // Space after day
		line[9] != ':' || line[12] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Extract syslog timestamp (first 15 chars)
	syslogTimestamp := line[:15]

	// Add current year and parse
	currentYear := time.Now().Year()
	timestampStr := fmt.Sprintf("%04d %s", currentYear, syslogTimestamp)

	t, err := time.Parse("2006 Jan _2 15:04:05", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	// Skip whitespace before message
	i := 15
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}
