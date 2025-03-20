// analysis/utils.go
package analysis

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// normalizeQuery standardizes the SQL query by replacing dynamic values.
// It converts newlines to spaces, converts to lower case, and replaces PostgreSQL variable symbols.
func normalizeQuery(query string) string {
	query = strings.ReplaceAll(query, "\n", " ") // Convert newlines to a space.
	query = strings.ToLower(query)               // Convert to lower case.
	query = strings.ReplaceAll(query, "$", "?")  // Replace PostgreSQL variable symbols.
	return query
}

// GenerateQueryID generates a short identifier from the raw and normalized query.
// It determines a prefix based on the query type and computes an MD5 hash from the normalized query.
func GenerateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	// Préfixes SQL typiques
	queryPrefixes := map[string]string{
		"SELECT":  "se-",
		"INSERT":  "in-",
		"UPDATE":  "up-",
		"DELETE":  "de-",
		"COPY":    "co-",
		"REFRESH": "mv-",
	}

	// Trim et détecte le préfixe sans ToLower.
	rawQuery = strings.TrimSpace(rawQuery)
	prefix := "xx-" // Default
	for keyword, short := range queryPrefixes {
		if strings.HasPrefix(rawQuery, keyword) || strings.HasPrefix(rawQuery, strings.ToLower(keyword)) {
			prefix = short
			break
		}
	}

	// Compute the MD5 hash of the normalized query.
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = hex.EncodeToString(hashBytes[:]) // 32 hex characters.

	// Convert to base64 and sanitize output.
	b64 := base64.StdEncoding.EncodeToString(hashBytes[:])
	b64 = strings.NewReplacer("+", "", "/", "", "=", "").Replace(b64)

	// Truncate for shorter hash ID.
	shortHash := b64
	if len(b64) > 6 {
		shortHash = b64[:6]
	}

	id = prefix + shortHash
	return
}

// QueryTypeFromID returns the query type based on the identifier prefix.
func QueryTypeFromID(id string) string {
	switch {
	case strings.HasPrefix(id, "se-"):
		return "select"
	case strings.HasPrefix(id, "in-"):
		return "insert"
	case strings.HasPrefix(id, "up-"):
		return "update"
	case strings.HasPrefix(id, "de-"):
		return "delete"
	case strings.HasPrefix(id, "co-"):
		return "copy"
	case strings.HasPrefix(id, "mv-"):
		return "refresh"
	default:
		return "other"
	}
}
