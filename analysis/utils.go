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
var queryPrefixes = [...]queryPrefix{
	{"SELECT", "se-"},   // SELECT queries
	{"INSERT", "in-"},   // INSERT statements
	{"UPDATE", "up-"},   // UPDATE statements
	{"DELETE", "de-"},   // DELETE statements
	{"COPY", "co-"},     // COPY operations
	{"REFRESH", "mv-"},  // REFRESH MATERIALIZED VIEW
	{"CREATE", "cr-"},   // CREATE statements
	{"DROP", "dr-"},     // DROP statements
	{"ALTER", "al-"},    // ALTER statements
	{"TRUNCATE", "tr-"}, // TRUNCATE statements
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
func detectQueryPrefix(rawQuery string) string {
	// Default prefix for unknown query types
	prefix := "xx-"

	// Try to match against known query prefixes
	for _, p := range queryPrefixes {
		if matchesKeyword(rawQuery, p.keyword) {
			prefix = p.prefix
			break
		}
	}

	return prefix
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
//	"se-A3bC9k" -> "select"
//	"in-XyZ123" -> "insert"
//	"xx-AbCdEf" -> "other"
func QueryTypeFromID(id string) string {
	if len(id) < 3 {
		return "other"
	}

	// Map prefix to query type
	switch id[:3] {
	case "se-":
		return "select"
	case "in-":
		return "insert"
	case "up-":
		return "update"
	case "de-":
		return "delete"
	case "co-":
		return "copy"
	case "mv-":
		return "refresh"
	case "cr-":
		return "create"
	case "dr-":
		return "drop"
	case "al-":
		return "alter"
	case "tr-":
		return "truncate"
	default:
		return "other"
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
