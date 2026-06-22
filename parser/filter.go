// Package parser provides log file parsing and filtering for PostgreSQL logs.
package parser

import (
	"context"
	"strings"
	"time"
)

// LogFilters defines criteria for filtering log entries.
// Filters are applied in the order: time range, database, user, application.
// An entry must match ALL specified filters to pass through.
//
// Zero values (empty slices, zero times) mean "no filtering for this criterion".
type LogFilters struct {
	BeginT      time.Time // entries at or after this time (zero = no lower bound)
	EndT        time.Time // entries at or before this time (zero = no upper bound)
	DbFilter    []string  // whitelist of database names (extracted from "db=<name>")
	UserFilter  []string  // whitelist of users (extracted from "user=<name>")
	ExcludeUser []string  // blacklist of users; takes precedence over UserFilter
	AppFilter   []string  // whitelist of application names (extracted from "app=<name>")
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
//
// IsEmpty returns true if no filters are configured.
func (f LogFilters) IsEmpty() bool {
	return f.BeginT.IsZero() && f.EndT.IsZero() &&
		len(f.DbFilter) == 0 && len(f.UserFilter) == 0 &&
		len(f.ExcludeUser) == 0 && len(f.AppFilter) == 0
}

// FilterStream forwards entries that pass the filters from in to out,
// closing out when in closes or when ctx is cancelled.
//
// On cancellation it stops forwarding immediately and drains any
// remaining entries from in (without re-forwarding them) so that
// upstream producers do not block on the channel send. This guarantees
// the upstream goroutine can exit cleanly even if the consumer aborted.
func FilterStream(ctx context.Context, in <-chan []LogEntry, out chan<- []LogEntry, filters LogFilters) {
	defer close(out)

	for {
		select {
		case <-ctx.Done():
			// Drain remaining entries to unblock upstream producers, then exit.
			for range in {
			}
			return
		case batch, ok := <-in:
			if !ok {
				return
			}
			// Filter in place to avoid allocating a new slice when nothing
			// is dropped. Most batches pass filters intact in the common case.
			kept := batch[:0]
			for _, e := range batch {
				if PassesFilters(e, filters) {
					kept = append(kept, e)
				}
			}
			if len(kept) == 0 {
				// Whole batch filtered out — return its backing storage to
				// the pool so the producers can reuse it.
				PutBatch(batch)
				continue
			}
			select {
			case out <- kept:
			case <-ctx.Done():
				for range in {
				}
				return
			}
		}
	}
}

// PassesFilters checks if a log entry matches all filter criteria.
// Returns true if the entry should be included in the output.
func PassesFilters(entry LogEntry, filters LogFilters) bool {
	// Time range filters. --begin/--end are wall-clock bounds: "09:00" means
	// the instant the log clock reads 09:00, independent of the log's or the
	// machine's timezone. BeginT/EndT are stored pre-projected onto that civil
	// timeline (see buildLogFilters), so project the entry the same way before
	// comparing. Skipped entirely when no time bound is set (the common path).
	if !filters.BeginT.IsZero() || !filters.EndT.IsZero() {
		wall := WallClock(entry.Timestamp)
		if !filters.BeginT.IsZero() && wall.Before(filters.BeginT) {
			return false
		}
		if !filters.EndT.IsZero() && wall.After(filters.EndT) {
			return false
		}
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

	return true
}

// extractValue extracts the value following a key in the format "key=value".
// The value is read until the next field separator. When fields are comma-separated
// (e.g., "user=X,db=Y,app=PostgreSQL JDBC Driver,client=Z"), the comma is used as
// the delimiter, allowing values to contain spaces.
//
// Examples:
//
//	"user=postgres,db=mydb" with key "user=" → "postgres"
//	"app=psql LOG: query" with key "app=" → "psql"
//	"app=PostgreSQL JDBC Driver,client=10.0.0.1" with key "app=" → "PostgreSQL JDBC Driver"
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

	// When fields are comma-separated (comma before key), skip space as separator
	commaSep := idx > 0 && line[idx-1] == ','
	endPos := len(rest)
	for _, sep := range []rune{' ', ',', '[', ']', '(', ')'} {
		if sep == ' ' && commaSep {
			continue
		}
		if pos := strings.IndexRune(rest, sep); pos != -1 && pos < endPos {
			endPos = pos
		}
	}
	if commaSep {
		// Last field before message ends at severity marker (" LOG:", " ERROR:", etc.)
		if pos := findSeverityMarker(rest[:endPos]); pos != -1 {
			endPos = pos
		}
	}

	// Extract and trim the value
	value := strings.TrimSpace(rest[:endPos])
	// Remove surrounding quotes if present (e.g., user="postgres" → postgres)
	value = strings.Trim(value, `"'`)
	return value
}

// findSeverityMarker returns the position of the first PostgreSQL severity
// marker (" LOG:", " ERROR:", etc.) in s, or -1 if not found.
func findSeverityMarker(s string) int {
	for _, sev := range []string{" LOG:", " ERROR:", " WARNING:", " FATAL:", " PANIC:"} {
		if pos := strings.Index(s, sev); pos != -1 {
			return pos
		}
	}
	return -1
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
