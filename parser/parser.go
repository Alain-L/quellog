// parser/parser.go
package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

const timeLayout = "2006-01-02 15:04:05" // Adjust the layout if needed

// ParseLine attempts to extract the timestamp and message from a log line.
func ParseLine(line string) (LogEntry, error) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return LogEntry{}, fmt.Errorf("line too short: %s", line)
	}

	// Combine the first two fields to form the timestamp string.
	timestampStr := parts[0] + " " + parts[1]
	timestamp, err := time.Parse(timeLayout, timestampStr)
	if err != nil {
		return LogEntry{}, fmt.Errorf("failed to parse timestamp '%s': %w", timestampStr, err)
	}

	// The rest of the line is considered the message.
	message := ""
	if len(parts) == 3 {
		message = parts[2]
	}
	return LogEntry{Timestamp: timestamp, Message: message}, nil
}

// // ParseLogFile reads a file and returns a slice of valid log entries.
func ParseLogFile(filename string) ([]LogEntry, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		entry, err := ParseLine(line)
		if err != nil {
			// Optionally log the error; for now, we skip malformed lines.
			continue
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", filename, err)
	}
	return entries, nil
}
