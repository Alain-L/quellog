// parser/stderr_parser.go
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// StderrParser parses PostgreSQL logs in stderr format.
type StderrParser struct{}

// Parse reads a file and streams LogEntry objects through the provided channel.
func (p *StderrParser) Parse(filename string, out chan<- LogEntry) error {
	// Open the file.
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Optionally increase the buffer size if needed.
	buf := make([]byte, 1024*1024)    // 1 MB
	scanner.Buffer(buf, 10*1024*1024) // up to 10 MB

	var currentEntry string

	// Process the file line by line, handling multi-line log entries.
	for scanner.Scan() {
		line := scanner.Text()

		// If the line starts with a space or a tab, it's a continuation of the previous entry.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// Concatenate with a space for proper separation.
			currentEntry += " " + strings.TrimSpace(line)
		} else {
			// If there is an accumulated entry, parse and send it to the output channel.
			if currentEntry != "" {
				timestamp, message := parseStderrLine(currentEntry)
				out <- LogEntry{Timestamp: timestamp, Message: message}
			}
			// Start a new entry with the current line.
			currentEntry = line
		}
	}
	// Process the last accumulated entry, if any.
	if currentEntry != "" {
		timestamp, message := parseStderrLine(currentEntry)
		out <- LogEntry{Timestamp: timestamp, Message: message}
	}

	return scanner.Err()
}

// parseStderrLine extracts the timestamp and message from a stderr log line.
// It assumes the line starts with a timestamp, e.g. "2024-06-05 00:00:01 CET ..."
func parseStderrLine(line string) (time.Time, string) {
	// Split the line by whitespace.
	parts := strings.Fields(line)
	if len(parts) < 4 {
		// Not enough fields to parse the timestamp, return an empty time and the original line.
		return time.Time{}, line
	}
	// Combine the first three fields to form the complete timestamp string.
	// Example: "2024-06-05", "00:00:01", "CET"
	timestampStr := fmt.Sprintf("%s %s %s", parts[0], parts[1], parts[2])
	// Parse the timestamp with timezone.
	timestamp, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		// On error, return an empty timestamp and the original line.
		return time.Time{}, line
	}
	// The message is the rest of the line (starting from the 4th field).
	message := strings.Join(parts[3:], " ")
	return timestamp, message
}

// ParseSessionTime converts a string in the format "HH:MM:SS.mmm" into a time.Duration.
func ParseSessionTime(s string) (time.Duration, error) {
	// Example: "0:00:00.004" → hours, minutes, seconds, milliseconds.
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid format for session time: %s", s)
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("failed to parse hours: %w", err)
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse minutes: %w", err)
	}
	// The seconds part can include milliseconds, e.g. "00.004"
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse seconds: %w", err)
	}
	totalSeconds := float64(hours*3600+minutes*60) + seconds
	return time.Duration(totalSeconds * float64(time.Second)), nil
}

// ExtractKeyValue extracts a value associated with a key from a log line.
// It returns the value and a boolean indicating whether the key was found.
func ExtractKeyValue(line, key string) (string, bool) {
	idx := strings.Index(line, key)
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len(key):]

	// Define a set of separators based on the log format.
	separators := []rune{' ', ',', '[', ')'}
	minIndex := len(rest)
	for _, sep := range separators {
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < minIndex {
			minIndex = pos
		}
	}
	value := strings.TrimSpace(rest[:minIndex])
	if value == "" || strings.EqualFold(value, "unknown") || strings.EqualFold(value, "[unknown]") {
		value = "UNKNOWN"
	}
	return value, true
}
