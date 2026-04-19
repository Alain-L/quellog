// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"time"
)

const (
	// DateTimeFormat is the expected format for --begin and --end flags.
	DateTimeFormat = "2006-01-02 15:04:05"
)

// parseDateTimes parses the begin and end datetime strings.
// Returns zero time.Time values if the strings are empty.
// Returns an error if either string is non-empty and fails to parse.
func parseDateTimes(beginStr, endStr string) (time.Time, time.Time, error) {
	var begin, end time.Time

	if beginStr != "" {
		parsed, err := time.Parse(DateTimeFormat, beginStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf(
				"invalid --begin datetime format: expected %s, got %q",
				DateTimeFormat, beginStr)
		}
		begin = parsed
	}

	if endStr != "" {
		parsed, err := time.Parse(DateTimeFormat, endStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf(
				"invalid --end datetime format: expected %s, got %q",
				DateTimeFormat, endStr)
		}
		end = parsed
	}

	return begin, end, nil
}

// parseWindow converts the window flag string to a time.Duration.
// Returns 0 if the string is empty. Returns an error on parse failure.
//
// Examples of valid duration strings:
//   - "30m" (30 minutes)
//   - "2h" (2 hours)
//   - "1h30m" (1 hour and 30 minutes)
func parseWindow(windowStr string) (time.Duration, error) {
	if windowStr == "" {
		return 0, nil
	}

	duration, err := time.ParseDuration(windowStr)
	if err != nil {
		return 0, fmt.Errorf("invalid --window duration: %w", err)
	}

	return duration, nil
}

// parseLast converts the --last flag to begin/end timestamps.
// Returns (begin, end) where end = now and begin = now - duration.
// Returns zero time.Time values if the string is empty.
// Returns an error on parse failure or non-positive duration.
//
// Examples of valid duration strings:
//   - "1h" (last 1 hour)
//   - "30m" (last 30 minutes)
//   - "24h" (last 24 hours)
func parseLast(lastStr string) (time.Time, time.Time, error) {
	if lastStr == "" {
		return time.Time{}, time.Time{}, nil
	}

	duration, err := time.ParseDuration(lastStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --last duration: %w", err)
	}

	if duration <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("--last duration must be positive, got: %s", lastStr)
	}

	now := time.Now()
	begin := now.Add(-duration)
	return begin, now, nil
}
