// Package parser provides log file parsing and filtering for PostgreSQL logs.
package parser

import (
	"strings"
	"time"
)

// LogFilters defines criteria for filtering log entries.
// Filters are applied in the order: time range, database, user, application, grep patterns.
// An entry must match ALL specified filters to pass through.
//
// Zero values (empty slices, zero times) mean "no filtering for this criterion".
type LogFilters struct {
	// BeginT filters entries to only those at or after this time.
	// Zero value means no lower bound.
	BeginT time.Time

	// EndT filters entries to only those at or before this time.
	// Zero value means no upper bound.
	EndT time.Time

	// DbFilter is a whitelist of database names.
	// If non-empty, only entries matching one of these databases are included.
	// Database name is extracted from "db=<name>" in the message.
	DbFilter []string

	// UserFilter is a whitelist of database users.
	// If non-empty, only entries matching one of these users are included.
	// User name is extracted from "user=<name>" in the message.
	UserFilter []string

	// ExcludeUser is a blacklist of database users.
	// If non-empty, entries matching any of these users are excluded.
	// Takes precedence over UserFilter if a user appears in both.
	ExcludeUser []string

	// AppFilter is a whitelist of application names.
	// If non-empty, only entries matching one of these applications are included.
	// Application name is extracted from "app=<name>" in the message.
	AppFilter []string

	// GrepExpr is a list of patterns that must ALL be present in the message.
	// All patterns are treated as literal strings (not regex).
	// Empty slice means no grep filtering.
	GrepExpr []string
}

// FilterStream reads log entries from the input channel, applies filters,
// and sends matching entries to the output channel.
//
// The function:
//   - Closes the output channel when done (caller should NOT close it)
//   - Applies filters in order for optimal performance (time filters first)
//   - Continues processing even if individual entries fail extraction
//
// Filter order (for performance):
//  1. Time range (fastest, no string operations)
//  2. Database name
//  3. User name (including exclusions)
//  4. Application name
//  5. Grep patterns (slowest, requires multiple string searches)
func FilterStream(in <-chan LogEntry, out chan<- LogEntry, filters LogFilters) {
	defer close(out)

	for entry := range in {
		if !PassesFilters(entry, filters) {
			continue
		}
		out <- entry
	}
}

// PassesFilters checks if a log entry matches all filter criteria.
// Returns true if the entry should be included in the output.
func PassesFilters(entry LogEntry, filters LogFilters) bool {
	// Time range filters (fastest - direct time comparison, no allocations)
	if !filters.BeginT.IsZero() && entry.Timestamp.Before(filters.BeginT) {
		return false
	}

	if !filters.EndT.IsZero() && entry.Timestamp.After(filters.EndT) {
		return false
	}

	// Database filter
	if len(filters.DbFilter) > 0 {
		dbName := extractValue(entry.Message, "db=")
		if dbName == "" || !contains(filters.DbFilter, dbName) {
			return false
		}
	}

	// User exclusion filter (check before whitelist)
	if len(filters.ExcludeUser) > 0 {
		userName := extractValue(entry.Message, "user=")
		if contains(filters.ExcludeUser, userName) {
			return false
		}
	}

	// User whitelist filter
	if len(filters.UserFilter) > 0 {
		userName := extractValue(entry.Message, "user=")
		if userName == "" || !contains(filters.UserFilter, userName) {
			return false
		}
	}

	// Application filter
	if len(filters.AppFilter) > 0 {
		appName := extractValue(entry.Message, "app=")
		if appName == "" || !contains(filters.AppFilter, appName) {
			return false
		}
	}

	// Grep pattern filter (slowest - multiple string searches)
	if len(filters.GrepExpr) > 0 {
		if !containsAllPatterns(entry.Message, filters.GrepExpr) {
			return false
		}
	}

	return true
}

// extractValue extracts the value following a key in the format "key=value".
// The value is read until the first separator character (space, comma, bracket, paren).
//
// Examples:
//
//	"user=postgres,db=mydb" with key "user=" → "postgres"
//	"app=psql LOG: query" with key "app=" → "psql"
//	"db=test]" with key "db=" → "test"
//
// Returns empty string if the key is not found or the value is empty.
func extractValue(line, key string) string {
	idx := strings.Index(line, key)
	if idx == -1 {
		return ""
	}

	// Extract text after the key
	rest := line[idx+len(key):]

	// Find the first separator character
	separators := []rune{' ', ',', '[', ']', '(', ')'}
	endPos := len(rest) // Default to end of string

	for _, sep := range separators {
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < endPos {
			endPos = pos
		}
	}

	// Extract and trim the value
	value := strings.TrimSpace(rest[:endPos])
	// Remove surrounding quotes if present (e.g., user="postgres" → postgres)
	value = strings.Trim(value, `"'`)
	return value
}

// contains checks if a string slice contains a specific string.
// Returns false if the slice is empty or the string is not found.
func contains(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}

// containsAllPatterns checks if a string contains all specified patterns.
// All patterns are treated as literal strings (case-sensitive).
//
// Returns true if:
//   - patterns is empty (no filtering)
//   - all patterns are found in the string
//
// Returns false if any pattern is missing.
func containsAllPatterns(text string, patterns []string) bool {
	for _, pattern := range patterns {
		if !strings.Contains(text, pattern) {
			return false
		}
	}
	return true
}
