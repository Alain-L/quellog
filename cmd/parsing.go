// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"strconv"
	"strings"
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
// Accepts the units recognised by parseDuration (see parseDuration doc).
func parseWindow(windowStr string) (time.Duration, error) {
	if windowStr == "" {
		return 0, nil
	}

	duration, err := parseDuration(windowStr)
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
// Accepts the units recognised by parseDuration (see parseDuration doc).
func parseLast(lastStr string) (time.Time, time.Time, error) {
	if lastStr == "" {
		return time.Time{}, time.Time{}, nil
	}

	duration, err := parseDuration(lastStr)
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

// parseDuration extends time.ParseDuration with the day ("d") and year
// ("y") suffixes that DBAs naturally type. PostgreSQL log analysis is
// usually done over windows like "1d" (last 24h) or "5y" (audit trail),
// not "24h" or "43800h" — and the stdlib only accepts ns/us/ms/s/m/h.
//
// Supported single-unit suffixes:
//   - "Nd" -> N * 24h          (1d, 7d, 30d, ...)
//   - "Nw" -> N * 7 * 24h      (1w, 2w, ...)
//   - "Ny" -> N * 365 * 24h    (1y, 5y, ...) - 365-day approximation
//
// Composite forms ("1d12h", "2w3d") are NOT supported here; they fall
// through to time.ParseDuration which rejects them. Use the underlying
// stdlib units for those (e.g. "36h", "17d" -> "408h").
//
// All other inputs are passed through to time.ParseDuration unchanged,
// so existing usage ("1h", "30m", "1h30m") keeps working.
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	last := s[len(s)-1]
	multiplier := time.Duration(0)
	switch last {
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	case 'y':
		multiplier = 365 * 24 * time.Hour
	default:
		return time.ParseDuration(s)
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(s, string(last)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("time: invalid number before %q in duration %q", last, s)
	}
	d := time.Duration(n) * multiplier
	// Guard against int64-nanosecond overflow: time.Duration tops out near
	// 292 years, so a large N with d/w/y wraps around. Without this check
	// "9999y" silently becomes ~55y (positive wrap) and absurd values can
	// flip negative — both yield a wrong window instead of a clean error.
	if n != 0 && d/multiplier != time.Duration(n) {
		return 0, fmt.Errorf("duration %q is too large", s)
	}
	return d, nil
}
