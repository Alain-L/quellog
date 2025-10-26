// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"log"
	"time"
)

const (
	// DateTimeFormat is the expected format for --begin and --end flags.
	DateTimeFormat = "2006-01-02 15:04:05"
)

// parseDateTimes parses the begin and end datetime strings.
// Returns zero time.Time values if the strings are empty.
// Exits with fatal error if parsing fails.
func parseDateTimes(beginStr, endStr string) (time.Time, time.Time) {
	var begin, end time.Time

	if beginStr != "" {
		parsed, err := time.Parse(DateTimeFormat, beginStr)
		if err != nil {
			log.Fatalf("[ERROR] Invalid --begin datetime format. Expected: %s, Got: %s",
				DateTimeFormat, beginStr)
		}
		begin = parsed
	}

	if endStr != "" {
		parsed, err := time.Parse(DateTimeFormat, endStr)
		if err != nil {
			log.Fatalf("[ERROR] Invalid --end datetime format. Expected: %s, Got: %s",
				DateTimeFormat, endStr)
		}
		end = parsed
	}

	return begin, end
}

// parseWindow converts the window flag string to a time.Duration.
// Returns 0 if the string is empty.
// Exits with fatal error if parsing fails.
//
// Examples of valid duration strings:
//   - "30m" (30 minutes)
//   - "2h" (2 hours)
//   - "1h30m" (1 hour and 30 minutes)
func parseWindow(windowStr string) time.Duration {
	if windowStr == "" {
		return 0
	}

	duration, err := time.ParseDuration(windowStr)
	if err != nil {
		log.Fatalf("[ERROR] Invalid --window duration: %v", err)
	}

	return duration
}
