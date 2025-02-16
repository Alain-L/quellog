// analysis/temp_files.go
package analysis

import (
	"regexp"
	"strconv"
	"strings"

	"dalibo/quellog/parser"
)

// CalculateTotalTemporaryFileSize scans the provided log entries, extracts the file size
// from lines indicating a temporary file, and returns the total size (in bytes).
//
// It expects log lines to contain a segment like: "temporary file: ... size <number>"
// and uses a regular expression to extract the size.
func CalculateTotalTemporaryFileSize(entries []parser.LogEntry) int64 {
	var totalSize int64 = 0
	// Define a regex to capture the size number after "size"
	// This regex looks for the word "size" followed by one or more spaces and then a number.
	re := regexp.MustCompile(`size\s+(\d+)`)

	for _, entry := range entries {
		// Check if the log line contains the indicator for a temporary file.
		if strings.Contains(entry.Message, "temporary file:") {
			// Try to find the size value using the regex.
			matches := re.FindStringSubmatch(entry.Message)
			if len(matches) == 2 {
				// Convert the extracted size string to an integer (64-bit).
				size, err := strconv.ParseInt(matches[1], 10, 64)
				if err == nil {
					totalSize += size
				}
			}
		}
	}

	return totalSize
}

// CalculateTotalTemporaryFileCount scans through the provided log entries and returns
// the total count of entries that indicate a temporary file.
// It does so by checking if the log message contains the substring "temporary file:".
func CalculateTotalTemporaryFileCount(entries []parser.LogEntry) int {
	var count int = 0

	// Iterate over each log entry.
	for _, entry := range entries {
		// Check if the log message indicates a temporary file.
		if strings.Contains(entry.Message, "temporary file:") {
			// Increment the count if the substring is found.
			count++
		}
	}

	// Return the total count of temporary file entries.
	return count
}
