// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// String builder pool for efficient string operations
// ============================================================================

// builderPool provides reusable strings.Builder instances to reduce allocations.
// This is especially important for high-throughput query normalization.
var builderPool = sync.Pool{
	New: func() interface{} {
		return new(strings.Builder)
	},
}

// ============================================================================
// Query type detection
// ============================================================================

// queryPrefix maps SQL keywords to short prefixes for query identification.
type queryPrefix struct {
	keyword string
	prefix  string
}

// queryPrefixes defines SQL command types and their corresponding ID prefixes.
// The order matters: more specific commands should come first.
// Query categories:
//   - DML: SELECT, INSERT, UPDATE, DELETE, MERGE
//   - COPY: COPY operations
//   - CTE: WITH queries (Common Table Expressions)
//   - DDL: CREATE, DROP, ALTER, TRUNCATE, COMMENT, SECURITY LABEL
//   - TCL: BEGIN, COMMIT, ROLLBACK, SAVEPOINT, START, END, RELEASE
//   - CURSOR: DECLARE, FETCH, CLOSE, MOVE
//   - Utility: EXPLAIN, ANALYZE, VACUUM, REINDEX, CLUSTER, LOCK, etc.
var queryPrefixes = [...]queryPrefix{
	// DML - Data Manipulation Language
	{"SELECT", "se-"},   // SELECT queries
	{"INSERT", "in-"},   // INSERT statements
	{"UPDATE", "up-"},   // UPDATE statements
	{"DELETE", "de-"},   // DELETE statements
	{"MERGE", "me-"},    // MERGE statements (PostgreSQL 15+)

	// COPY operations
	{"COPY", "co-"}, // COPY TO/FROM

	// CTE - Common Table Expressions (WITH queries)
	{"WITH", "wi-"}, // WITH ... SELECT/INSERT/UPDATE/DELETE

	// DDL - Data Definition Language
	{"CREATE", "cr-"},   // CREATE statements
	{"DROP", "dr-"},     // DROP statements
	{"ALTER", "al-"},    // ALTER statements
	{"TRUNCATE", "tr-"}, // TRUNCATE statements
	{"COMMENT", "cm-"},  // COMMENT ON statements
	{"REFRESH", "mv-"},  // REFRESH MATERIALIZED VIEW

	// TCL - Transaction Control Language
	{"BEGIN", "be-"},      // BEGIN transaction
	{"COMMIT", "ct-"},     // COMMIT transaction
	{"ROLLBACK", "rb-"},   // ROLLBACK transaction
	{"SAVEPOINT", "sv-"},  // SAVEPOINT
	{"RELEASE", "rl-"},    // RELEASE SAVEPOINT
	{"START", "st-"},      // START TRANSACTION
	{"END", "en-"},        // END (alias for COMMIT)
	{"ABORT", "ab-"},      // ABORT (alias for ROLLBACK)
	{"PREPARE", "pr-"},    // PREPARE TRANSACTION
	{"DEALLOCATE", "dl-"}, // DEALLOCATE prepared statement

	// CURSOR operations
	{"DECLARE", "dc-"}, // DECLARE cursor
	{"FETCH", "fe-"},   // FETCH from cursor
	{"CLOSE", "cl-"},   // CLOSE cursor
	{"MOVE", "mo-"},    // MOVE cursor

	// Utility commands
	{"EXPLAIN", "ex-"},  // EXPLAIN / EXPLAIN ANALYZE
	{"ANALYZE", "an-"},  // ANALYZE table
	{"VACUUM", "va-"},   // VACUUM
	{"REINDEX", "ri-"},  // REINDEX
	{"CLUSTER", "cu-"},  // CLUSTER
	{"LOCK", "lk-"},     // LOCK TABLE
	{"UNLISTEN", "ul-"}, // UNLISTEN (before LISTEN for prefix match)
	{"LISTEN", "li-"},   // LISTEN
	{"NOTIFY", "no-"},   // NOTIFY
	{"DISCARD", "di-"},  // DISCARD
	{"RESET", "re-"},    // RESET
	{"SET", "se-"},      // SET - shares prefix with SELECT intentionally
	{"SHOW", "sh-"},     // SHOW
	{"LOAD", "lo-"},     // LOAD
	{"CALL", "ca-"},     // CALL procedure
	{"DO", "do-"},       // DO anonymous block
	{"EXECUTE", "xe-"},  // EXECUTE prepared statement

	// Security
	{"GRANT", "gr-"},  // GRANT privileges
	{"REVOKE", "rv-"}, // REVOKE privileges
}

// ============================================================================
// Query normalization
// ============================================================================

// normalizeQuery standardizes an SQL query for grouping and comparison.
// It performs the following transformations:
//   - Converts to lowercase
//   - Collapses multiple whitespaces/newlines to single space
//   - Replaces PostgreSQL parameters ($1, $2, etc.) with '?'
//
// This allows queries with different parameter values to be grouped together.
//
// Example:
//
//	Input:  "SELECT * FROM users WHERE id = $1"
//	Output: "select * from users where id = ?"
//
// Performance notes:
//   - Single-pass algorithm
//   - Uses pooled strings.Builder to reduce allocations
//   - Inline lowercase conversion (avoids strings.ToLower allocation)
func normalizeQuery(query string) string {
	if len(query) == 0 {
		return ""
	}

	buf := builderPool.Get().(*strings.Builder)
	buf.Reset()
	// Grow with conservative estimate: normalized queries are typically shorter
	// due to replacing values/numbers with '?'. Using len/2 reduces peak memory.
	buf.Grow(len(query) / 2)
	defer builderPool.Put(buf)

	lastWasSpace := false

	for i := 0; i < len(query); i++ {
		c := query[i]

		// Handle single-quoted strings (VALUES) - replace with ?
		if c == '\'' {
			buf.WriteByte('?')
			// Skip until closing quote
			for i+1 < len(query) {
				i++
				if query[i] == '\'' {
					// Check for escaped quote ('')
					if i+1 < len(query) && query[i+1] == '\'' {
						i++ // Skip escaped quote
					} else {
						break // End of string
					}
				}
			}
			lastWasSpace = false
			continue
		}

		// Handle double-quoted identifiers (tables/columns) - preserve as-is
		if c == '"' {
			buf.WriteByte('"')
			// Copy until closing quote
			for i+1 < len(query) {
				i++
				c = query[i]
				// Convert to lowercase inside quotes too
				if c >= 'A' && c <= 'Z' {
					buf.WriteByte(c + 32)
				} else {
					buf.WriteByte(c)
				}
				if c == '"' {
					break
				}
			}
			lastWasSpace = false
			continue
		}

		// Handle whitespace
		if c == '\n' || c == '\r' || c == '\t' || c == ' ' {
			if !lastWasSpace {
				buf.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}

		// Handle PostgreSQL parameters ($1, $2)
		if c == '$' {
			buf.WriteByte('?')
			lastWasSpace = false
			// Skip parameter number
			for i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
				i++
			}
			continue
		}

		// Handle negative numbers: -123 → ? (only if isolated)
		if c == '-' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
			// Check if previous character is part of identifier
			isPrevIdentifier := i > 0 && ((query[i-1] >= 'a' && query[i-1] <= 'z') ||
				(query[i-1] >= 'A' && query[i-1] <= 'Z') ||
				(query[i-1] >= '0' && query[i-1] <= '9') ||
				query[i-1] == '_')

			if !isPrevIdentifier {
				buf.WriteByte('?')
				lastWasSpace = false
				i++ // Skip the minus sign
				// Skip the number
				for i+1 < len(query) && (query[i+1] >= '0' && query[i+1] <= '9' || query[i+1] == '.') {
					i++
				}
				continue
			}
		}

		// Handle numbers - replace with ? ONLY if isolated (not part of an identifier)
		if c >= '0' && c <= '9' {
			// Check BOTH previous AND next character
			isPrevIdentifier := i > 0 && ((query[i-1] >= 'a' && query[i-1] <= 'z') ||
				(query[i-1] >= 'A' && query[i-1] <= 'Z') ||
				(query[i-1] >= '0' && query[i-1] <= '9') ||
				query[i-1] == '_')

			isNextIdentifier := i+1 < len(query) && ((query[i+1] >= 'a' && query[i+1] <= 'z') ||
				(query[i+1] >= 'A' && query[i+1] <= 'Z') ||
				query[i+1] == '_')

			// If part of identifier (sandwiched between identifier chars), keep it
			if isPrevIdentifier || isNextIdentifier {
				buf.WriteByte(c)
				lastWasSpace = false
				continue
			}

			// Isolated number - replace with ?
			buf.WriteByte('?')
			lastWasSpace = false
			// Skip entire number (including decimals)
			for i+1 < len(query) && (query[i+1] >= '0' && query[i+1] <= '9' || query[i+1] == '.') {
				i++
			}
			continue
		}

		// Convert uppercase to lowercase
		if c >= 'A' && c <= 'Z' {
			buf.WriteByte(c + 32)
		} else {
			buf.WriteByte(c)
		}
		lastWasSpace = false
	}

	return buf.String()
}

// normalizeWhitespace collapses all whitespace (spaces, tabs, newlines) into single spaces.
// This ensures consistent raw_query formatting across log formats (stderr uses spaces,
// CSV/JSON preserve newlines from the original query).
//
// Example:
//
//	Input:  "BEGIN;\n            UPDATE users\n            SET name = 'foo';"
//	Output: "BEGIN; UPDATE users SET name = 'foo';"
func normalizeWhitespace(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}

	// Fast path: check if normalization is needed at all
	// This avoids allocation for strings that are already normalized
	needsNormalization := false
	start := 0
	end := n

	// Find first non-whitespace (for leading trim check)
	for start < n {
		c := s[start]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		needsNormalization = true
		start++
	}

	// Find last non-whitespace (for trailing trim check)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		needsNormalization = true
		end--
	}

	// Check for multiple consecutive whitespace or non-space whitespace
	if !needsNormalization {
		lastWasSpace := false
		for i := start; i < end; i++ {
			c := s[i]
			isWhitespace := c == ' ' || c == '\n' || c == '\r' || c == '\t'
			if isWhitespace {
				if lastWasSpace || c != ' ' {
					needsNormalization = true
					break
				}
				lastWasSpace = true
			} else {
				lastWasSpace = false
			}
		}
	}

	// Fast path: no normalization needed, return original (or trimmed slice)
	if !needsNormalization {
		if start == 0 && end == n {
			return s // Completely unchanged
		}
		return s[start:end] // Just trimmed, no internal changes
	}

	// Slow path: allocate and normalize
	buf := make([]byte, 0, end-start)
	lastWasSpace := true // Start true to skip leading spaces

	for i := start; i < end; i++ {
		c := s[i]

		// Collapse whitespace (space, newline, tab, carriage return)
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			if !lastWasSpace {
				buf = append(buf, ' ')
				lastWasSpace = true
			}
			continue
		}

		buf = append(buf, c)
		lastWasSpace = false
	}

	// Trim trailing space (shouldn't happen due to end calculation, but safe)
	if len(buf) > 0 && buf[len(buf)-1] == ' ' {
		buf = buf[:len(buf)-1]
	}

	return string(buf)
}

// ============================================================================
// Query ID generation
// ============================================================================

// GenerateQueryID creates a short, human-readable identifier for an SQL query.
// It combines a type prefix (e.g., "se-" for SELECT) with a hash-based suffix.
//
// Returns:
//   - id: Short identifier like "se-A3bC9k" (prefix + 6 chars)
//   - fullHash: Complete MD5 hash in hexadecimal (32 chars)
//
// The short ID is designed to be:
//   - Human-readable (starts with meaningful prefix)
//   - Collision-resistant (6 alphanumeric chars = 36 bits of entropy)
//   - URL-safe (no special characters)
//
// Example:
//
//	Input:  "SELECT * FROM users WHERE id = $1"
//	Output: id="se-A3bC9k", fullHash="5d41402abc4b2a76b9719d911017c592"
func GenerateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	// Detect query type from first keyword
	prefix := detectQueryPrefix(rawQuery)

	// Compute MD5 hash of normalized query
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = hex.EncodeToString(hashBytes[:])

	// Generate short hash suffix (6 alphanumeric characters)
	shortHash := generateShortHash(hashBytes[:])

	id = prefix + shortHash
	return
}

// detectQueryPrefix identifies the SQL command type and returns its prefix.
// Uses case-insensitive matching on the first word of the query.
// Handles queries that start with comments (/* ... */ or -- ...).
func detectQueryPrefix(rawQuery string) string {
	// Skip leading whitespace and comments to find actual SQL keyword
	query := skipLeadingComments(rawQuery)

	// Default prefix for unknown query types
	prefix := "xx-"

	// Try to match against known query prefixes
	for _, p := range queryPrefixes {
		if matchesKeyword(query, p.keyword) {
			prefix = p.prefix
			break
		}
	}

	return prefix
}

// skipLeadingComments removes leading whitespace and SQL comments from a query.
// Handles:
//   - Block comments: /* ... */
//   - Line comments: -- ... (until newline)
//   - Nested block comments (PostgreSQL supports them)
func skipLeadingComments(query string) string {
	i := 0
	n := len(query)

	for i < n {
		// Skip whitespace
		if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' || query[i] == '\r' {
			i++
			continue
		}

		// Skip block comments /* ... */ (may be nested in PostgreSQL)
		if i+1 < n && query[i] == '/' && query[i+1] == '*' {
			i += 2
			depth := 1
			for i+1 < n && depth > 0 {
				if query[i] == '/' && query[i+1] == '*' {
					depth++
					i += 2
				} else if query[i] == '*' && query[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}

		// Skip line comments -- ...
		if i+1 < n && query[i] == '-' && query[i+1] == '-' {
			i += 2
			for i < n && query[i] != '\n' {
				i++
			}
			if i < n {
				i++ // Skip the newline
			}
			continue
		}

		// Found non-comment, non-whitespace character
		break
	}

	if i >= n {
		return ""
	}
	return query[i:]
}

// matchesKeyword checks if a query starts with a specific SQL keyword (case-insensitive).
func matchesKeyword(query, keyword string) bool {
	if len(query) < len(keyword) {
		return false
	}

	// Case-insensitive comparison of first word
	for i := 0; i < len(keyword); i++ {
		c := query[i]
		// Convert to uppercase for comparison
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		if c != keyword[i] {
			return false
		}
	}

	return true
}

// generateShortHash extracts 6 alphanumeric characters from a hash.
// It encodes the hash in base64 and filters out non-alphanumeric characters.
//
// This provides a good balance between:
//   - Brevity (6 characters)
//   - Collision resistance (36 bits of entropy)
//   - Readability (no special characters)
func generateShortHash(hashBytes []byte) string {
	// Encode hash to base64
	b64 := base64.StdEncoding.EncodeToString(hashBytes)

	// Extract 6 alphanumeric characters (skip +, /, =)
	var shortHash [6]byte
	j := 0

	for i := 0; i < len(b64) && j < 6; i++ {
		c := b64[i]
		// Keep only alphanumeric: 0-9, A-Z, a-z
		// Skip: + (43), / (47), = (61)
		if c != '+' && c != '/' && c != '=' {
			shortHash[j] = c
			j++
		}
	}

	return string(shortHash[:])
}

// ============================================================================
// Query type extraction
// ============================================================================

// QueryTypeFromID extracts the query type from a generated query ID.
// This is useful for filtering or grouping queries by type.
//
// Example:
//
//	"se-A3bC9k" -> "SELECT"
//	"in-XyZ123" -> "INSERT"
//	"xx-AbCdEf" -> "OTHER"
func QueryTypeFromID(id string) string {
	if len(id) < 3 {
		return "OTHER"
	}

	// Map prefix to query type
	switch id[:3] {
	// DML
	case "se-":
		return "SELECT"
	case "in-":
		return "INSERT"
	case "up-":
		return "UPDATE"
	case "de-":
		return "DELETE"
	case "me-":
		return "MERGE"
	// COPY
	case "co-":
		return "COPY"
	// CTE
	case "wi-":
		return "WITH"
	// DDL
	case "cr-":
		return "CREATE"
	case "dr-":
		return "DROP"
	case "al-":
		return "ALTER"
	case "tr-":
		return "TRUNCATE"
	case "cm-":
		return "COMMENT"
	case "mv-":
		return "REFRESH"
	// TCL
	case "be-":
		return "BEGIN"
	case "ct-":
		return "COMMIT"
	case "rb-":
		return "ROLLBACK"
	case "sv-":
		return "SAVEPOINT"
	case "rl-":
		return "RELEASE"
	case "st-":
		return "START"
	case "en-":
		return "END"
	case "ab-":
		return "ABORT"
	case "pr-":
		return "PREPARE"
	case "dl-":
		return "DEALLOCATE"
	// CURSOR
	case "dc-":
		return "DECLARE"
	case "fe-":
		return "FETCH"
	case "cl-":
		return "CLOSE"
	case "mo-":
		return "MOVE"
	// Utility
	case "ex-":
		return "EXPLAIN"
	case "an-":
		return "ANALYZE"
	case "va-":
		return "VACUUM"
	case "ri-":
		return "REINDEX"
	case "cu-":
		return "CLUSTER"
	case "lk-":
		return "LOCK"
	case "ul-":
		return "UNLISTEN"
	case "li-":
		return "LISTEN"
	case "no-":
		return "NOTIFY"
	case "di-":
		return "DISCARD"
	case "re-":
		return "RESET"
	case "sh-":
		return "SHOW"
	case "lo-":
		return "LOAD"
	case "ca-":
		return "CALL"
	case "do-":
		return "DO"
	case "xe-":
		return "EXECUTE"
	// Security
	case "gr-":
		return "GRANT"
	case "rv-":
		return "REVOKE"
	default:
		return "OTHER"
	}
}

// QueryCategory returns the high-level category for a query type.
// Categories: DML, COPY, CTE, DDL, TCL, CURSOR, UTILITY, Security, OTHER
func QueryCategory(queryType string) string {
	switch queryType {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE":
		return "DML"
	case "COPY":
		return "COPY"
	case "WITH":
		return "CTE"
	case "CREATE", "DROP", "ALTER", "TRUNCATE", "COMMENT", "REFRESH":
		return "DDL"
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "START", "END", "ABORT", "PREPARE", "DEALLOCATE":
		return "TCL"
	case "DECLARE", "FETCH", "CLOSE", "MOVE":
		return "CURSOR"
	case "EXPLAIN", "ANALYZE", "VACUUM", "REINDEX", "CLUSTER", "LOCK", "UNLISTEN", "LISTEN", "NOTIFY", "DISCARD", "RESET", "SHOW", "LOAD", "CALL", "DO", "EXECUTE", "GRANT", "REVOKE", "SET":
		return "UTILITY"
	default:
		return "OTHER"
	}
}

// ============================================================================
// Entity count sorting
// ============================================================================

// EntityCount represents an entity with its occurrence count.
type EntityCount struct {
	Name  string
	Count int
}

// SortByCount converts a count map to a sorted slice of EntityCount.
// Entities are sorted by count (descending), with alphabetical ordering as tiebreaker.
// Returns all entities (no limit).
func SortByCount(counts map[string]int) []EntityCount {
	items := make([]EntityCount, 0, len(counts))
	for name, count := range counts {
		items = append(items, EntityCount{Name: name, Count: count})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count // Descending by count
		}
		return items[i].Name < items[j].Name // Alphabetical tiebreaker
	})

	return items
}

// ============================================================================
// Duration statistics
// ============================================================================

// DurationStats contains statistical information about a set of durations.
type DurationStats struct {
	Count  int
	Min    time.Duration
	Max    time.Duration
	Avg    time.Duration
	Median time.Duration
}

// CalculateDurationStats computes min, max, avg, and median for a set of durations.
// Returns zero values if the input slice is empty.
func CalculateDurationStats(durations []time.Duration) DurationStats {
	if len(durations) == 0 {
		return DurationStats{}
	}

	// Calculate min, max, and sum
	min := durations[0]
	max := durations[0]
	var sum time.Duration

	for _, d := range durations {
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
		sum += d
	}

	avg := sum / time.Duration(len(durations))

	// Calculate median (requires sorting)
	median := CalculateMedian(durations)

	return DurationStats{
		Count:  len(durations),
		Min:    min,
		Max:    max,
		Avg:    avg,
		Median: median,
	}
}

// CalculateMedian calculates the median duration from a slice of durations.
// Note: This function sorts a copy of the input slice.
func CalculateMedian(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	// Make a copy to avoid modifying the original slice
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)

	// Sort the copy
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	// Calculate median
	n := len(sorted)
	if n%2 == 1 {
		// Odd number of elements: return middle element
		return sorted[n/2]
	}
	// Even number of elements: return average of two middle elements
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// CalculateDurationDistribution groups durations into buckets and returns counts per bucket.
// Buckets: < 1s, 1s - 1min, 1min - 30min, 30min - 2h, 2h - 5h, > 5h
func CalculateDurationDistribution(durations []time.Duration) map[string]int {
	dist := map[string]int{
		"< 1s":         0,
		"1s - 1min":    0,
		"1min - 30min": 0,
		"30min - 2h":   0,
		"2h - 5h":      0,
		"> 5h":         0,
	}

	for _, d := range durations {
		switch {
		case d < time.Second:
			dist["< 1s"]++
		case d < time.Minute:
			dist["1s - 1min"]++
		case d < 30*time.Minute:
			dist["1min - 30min"]++
		case d < 2*time.Hour:
			dist["30min - 2h"]++
		case d < 5*time.Hour:
			dist["2h - 5h"]++
		default:
			dist["> 5h"]++
		}
	}

	return dist
}
