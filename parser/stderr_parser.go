// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
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

	return p.parseReader(file, out)
}

// parseReader runs the stderr parsing logic against any io.Reader.
func (p *StderrParser) parseReader(r io.Reader, out chan<- LogEntry) error {
	scanner := bufio.NewScanner(r)
	// Configure scanner with large buffer to handle long log lines
	// (e.g., STATEMENT lines with large queries)
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	// Accumulate multi-line entries
	var currentEntry bytes.Buffer // Changed to bytes.Buffer

	for scanner.Scan() {
		lineBytes := scanner.Bytes() // Changed to scanner.Bytes()

		// Handle syslog tab markers: "#011" represents a tab character
		// Replace it with a space for consistency
		if idx := bytes.Index(lineBytes, []byte(syslogTabMarker)); idx != -1 { // Changed to bytes.Index
			// Create a new byte slice for the modified line
			modifiedLineBytes := make([]byte, 0, len(lineBytes))
			modifiedLineBytes = append(modifiedLineBytes, ' ')
			modifiedLineBytes = append(modifiedLineBytes, lineBytes[idx+len(syslogTabMarker):]...)
			lineBytes = modifiedLineBytes // Assign the modified slice back
		}

		// Check if this is a continuation line (starts with whitespace)
		if bytes.HasPrefix(lineBytes, []byte(" ")) || bytes.HasPrefix(lineBytes, []byte("\t")) { // Changed to bytes.HasPrefix
			// Append to current entry
			if currentEntry.Len() > 0 {
				currentEntry.WriteByte(' ') // Changed to WriteByte
				currentEntry.Write(bytes.TrimSpace(lineBytes)) // Changed to Write and bytes.TrimSpace
			}
		} else {
			// This is a new entry, so process the previous one
			if currentEntry.Len() > 0 {
				timestamp, message, pid := parseStderrLine(currentEntry.Bytes())
				out <- LogEntry{Timestamp: timestamp, Message: message, Pid: pid}
			}
			// Start accumulating new entry
			currentEntry.Reset()
			currentEntry.Write(lineBytes) // Changed to Write
		}
	}

	// Process the last accumulated entry
	if currentEntry.Len() > 0 {
		timestamp, message, pid := parseStderrLine(currentEntry.Bytes())
		out <- LogEntry{Timestamp: timestamp, Message: message, Pid: pid}
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
func parseStderrLine(lineBytes []byte) (time.Time, string, int) {
	n := len(lineBytes)

	// Need at least 20 characters for a valid timestamp
	if n < 20 {
		return time.Time{}, string(lineBytes), 0
	}

	// Attempt 1: Parse stderr format (YYYY-MM-DD HH:MM:SS TZ)
	if timestamp, message, pid, ok := parseStderrFormat(lineBytes); ok {
		return timestamp, message, pid
	}

	// Attempt 2: Parse syslog format (Mon DD HH:MM:SS)
	if timestamp, message, pid, ok := parseSyslogFormat(lineBytes); ok {
		return timestamp, message, pid
	}

	// Unable to parse timestamp, return line as-is
	return time.Time{}, string(lineBytes), 0
}

// parseStderrFormat attempts to parse the standard stderr format:
// "YYYY-MM-DD HH:MM:SS TZ message..."
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after timestamp
//   - ok: true if parsing succeeded
func parseStderrFormat(lineBytes []byte) (time.Time, string, int, bool) {
	n := len(lineBytes)

	// Quick positional validation: check for date/time separators
	if n < 20 ||
		lineBytes[4] != '-' || lineBytes[7] != '-' || // Date separators
		lineBytes[10] != ' ' || // Space between date and time
		        lineBytes[13] != ':' || lineBytes[16] != ':' { // Time separators
				return time.Time{}, "", 0, false
			}
		
			// Find the timezone field
			// Format: "YYYY-MM-DD HH:MM:SS TZ"
			//          0123456789012345678901...
			// Expected space after seconds (HH:MM:SS) at position 19
		
			spaceAfterTime := 19
			if lineBytes[spaceAfterTime] != ' ' {
				// Handle cases with no space or multiple spaces
				// Scan forward to find next space
				i := 19
				for i < n && lineBytes[i] != ' ' && lineBytes[i] != '\t' {
					i++
				}
				if i >= n {
					return time.Time{}, "", 0, false
				}
				spaceAfterTime = i
			}
		
			// Skip whitespace to find timezone token
			i := spaceAfterTime + 1
			for i < n && (lineBytes[i] == ' ' || lineBytes[i] == '\t') {
				i++
			}
			tzStart := i
		
			// Find end of timezone token
			for i < n && lineBytes[i] != ' ' && lineBytes[i] != '\t' {
				i++
			}
			tzEnd := i
		
			if tzEnd <= tzStart {
				return time.Time{}, "", 0, false
			}
		
			// Parse timestamp: "YYYY-MM-DD HH:MM:SS TZ"
			timestampBytes := lineBytes[:tzEnd]
			t, err := time.Parse("2006-01-02 15:04:05 MST", string(timestampBytes))
			if err != nil {
				return time.Time{}, "", 0, false
			}
	// Skip whitespace before message
	for i < n && (lineBytes[i] == ' ' || lineBytes[i] == '\t') {
		i++
	}

	// Extract PID if present (e.g., [12345])
	pid := 0
	pidStart := bytes.IndexByte(lineBytes[i:], '[')
	if pidStart != -1 {
		pidEnd := bytes.IndexByte(lineBytes[i+pidStart:], ']')
		if pidEnd != -1 {
			pidStr := lineBytes[i+pidStart+1 : i+pidStart+pidEnd]
			p, err := strconv.Atoi(string(pidStr))
			if err == nil {
				pid = p
				i += pidStart + pidEnd + 1 // Move past PID section
				// Skip any additional whitespace after PID
				for i < n && (lineBytes[i] == ' ' || lineBytes[i] == '\t') {
					i++
				}
			}
		}
	}

	messageBytes := []byte("")
	if i < n {
		messageBytes = lineBytes[i:]
	}

	return t, string(messageBytes), pid, true
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
func parseSyslogFormat(lineBytes []byte) (time.Time, string, int, bool) {
	n := len(lineBytes)

	// Quick positional validation for syslog format
	// "Jan _2 15:04:05" = 15 characters
	if n < 15 ||
		lineBytes[3] != ' ' || // Space after month abbreviation
		lineBytes[6] != ' ' || // Space after day
		lineBytes[9] != ':' || lineBytes[12] != ':' { // Time separators
		return time.Time{}, "", 0, false
	}

	// Extract syslog timestamp (first 15 chars)
	syslogTimestampBytes := lineBytes[:15]

	// Add current year and parse
	currentYear := time.Now().Year()
	timestampStr := fmt.Sprintf("%04d %s", currentYear, string(syslogTimestampBytes))

	t, err := time.Parse("2006 Jan _2 15:04:05", timestampStr)
	if err != nil {
		return time.Time{}, "", 0, false
	}

	// Skip whitespace before message
	i := 15
	for i < n && (lineBytes[i] == ' ' || lineBytes[i] == '\t') {
		i++
	}

	// Extract PID if present (e.g., [12345])
	pid := 0
	pidStart := bytes.IndexByte(lineBytes[i:], '[')
	if pidStart != -1 {
		pidEnd := bytes.IndexByte(lineBytes[i+pidStart:], ']')
		if pidEnd != -1 {
			pidStr := lineBytes[i+pidStart+1 : i+pidStart+pidEnd]
			p, err := strconv.Atoi(string(pidStr))
			if err == nil {
				pid = p
				i += pidStart + pidEnd + 1 // Move past PID section
				// Skip any additional whitespace after PID
				for i < n && (lineBytes[i] == ' ' || lineBytes[i] == '\t') {
					i++
				}
			}
		}
	}

	messageBytes := []byte("")
	if i < n {
		messageBytes = lineBytes[i:]
	}

	return t, string(messageBytes), pid, true
}
