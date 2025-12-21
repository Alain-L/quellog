package analysis

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
)

// builderPool reuses strings.Builder instances to reduce allocations during normalization.
var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

var tempTableRegex = regexp.MustCompile(`pg_(temp|toast)(_\d+)+`)
var locationRegex = regexp.MustCompile(`(?s) at character \d+.*`)
var detailParamsRegex = regexp.MustCompile(`Key \([^)]+\)=\([^)]+\)`)

// ============================================================================ 
// SQL Pattern Extraction (Normalization)
// ============================================================================ 

// normalizeQuery parameterizes an SQL query by replacing literal values with '?'.
func normalizeQuery(query string) string {
	if len(query) == 0 {
		return ""
	}

	buf := builderPool.Get().(*strings.Builder)
	buf.Reset()
	buf.Grow(len(query) / 2)
	defer builderPool.Put(buf)

	lastWasSpace := false

	for i := 0; i < len(query); i++ {
		c := query[i]

		// Handle single-quoted strings (VALUES) - replace with ?
		if c == '\'' {
			buf.WriteByte('?')
			for i+1 < len(query) {
				i++
				if query[i] == '\'' {
					if i+1 < len(query) && query[i+1] == '\'' {
						i++
					} else {
						break
					}
				}
			}
			lastWasSpace = false
			continue
		}

		// Handle double-quoted identifiers - preserve but lowercase
		if c == '"' {
			buf.WriteByte('"')
			for i+1 < len(query) {
				i++
				c = query[i]
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
			for i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
				i++
			}
			continue
		}

		// Handle numbers
		if (c >= '0' && c <= '9') || (c == '-' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9') {
			// Check if isolated
			isPrevIdentifier := i > 0 && isIdentifierChar(query[i-1])
			if !isPrevIdentifier {
				buf.WriteByte('?')
				lastWasSpace = false
				if c == '-' {
					i++
				}
				for i+1 < len(query) && (isDigit(query[i+1]) || query[i+1] == '.') {
					i++
				}
				continue
			}
		}

		// Default: lowercase and copy
		if c >= 'A' && c <= 'Z' {
			buf.WriteByte(c + 32)
		} else {
			buf.WriteByte(c)
		}
		lastWasSpace = false
	}

	result := buf.String()
	// Mask temporary tables (e.g. pg_temp_123 -> pg_temp_?)
	// This helps grouping queries that use different temp tables but same structure.
	return tempTableRegex.ReplaceAllString(result, "pg_$1_?")
}

func isIdentifierChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// normalizeWhitespace collapses all whitespace into single spaces.
func normalizeWhitespace(s string) string {
	if len(s) == 0 {
		return ""
	}

	buf := builderPool.Get().(*strings.Builder)
	buf.Reset()
	buf.Grow(len(s))
	defer builderPool.Put(buf)

	lastWasSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' || c == ' ' {
			if !lastWasSpace {
				buf.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		buf.WriteByte(c)
		lastWasSpace = false
	}
	return strings.TrimSpace(buf.String())
}

// ============================================================================ 
// Hash & ID Generation
// ============================================================================ 

// GenerateQueryID creates a short, human-readable identifier for an SQL query.
func GenerateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	prefix := detectQueryPrefix(rawQuery)
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = hex.EncodeToString(hashBytes[:])
	shortHash := generateShortHash(hashBytes[:])
	id = prefix + shortHash
	return
}

func detectQueryPrefix(rawQuery string) string {
	query := skipLeadingComments(rawQuery)
	prefix := "xx-"
	for _, p := range queryPrefixes {
		if matchesKeyword(query, p.keyword) {
			prefix = p.prefix
			break
		}
	}
	return prefix
}

func skipLeadingComments(query string) string {
	i := 0
	n := len(query)
	for i < n {
		if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' || query[i] == '\r' {
			i++
			continue
		}
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
		if i+1 < n && query[i] == '-' && query[i+1] == '-' {
			i += 2
			for i < n && query[i] != '\n' {
				i++
			}
			if i < n {
				i++
			}
			continue
		}
		break
	}
	if i >= n {
		return ""
	}
	return query[i:]
}

func matchesKeyword(query, keyword string) bool {
	if len(query) < len(keyword) {
		return false
	}
	for i := 0; i < len(keyword); i++ {
		c := query[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		if c != keyword[i] {
			return false
		}
	}
	return true
}

func generateShortHash(hashBytes []byte) string {
	b64 := base64.StdEncoding.EncodeToString(hashBytes)
	var shortHash [6]byte
	j := 0
	for i := 0; i < len(b64) && j < 6; i++ {
		c := b64[i]
		if c != '+' && c != '/' && c != '=' {
			shortHash[j] = c
			j++
		}
	}
	return string(shortHash[:])
}

// ============================================================================

// Event Pattern Extraction (Normalization)

// ============================================================================



// NormalizeEvent transforms a raw log message into a generic fingerprint.

// It removes severity prefixes, masks quoted identifiers/values, and numbers.

//

// Example:

//   Input:  "ERROR: relation \"users\" does not exist at character 15"

//   Output: "relation ? does not exist at character ?"

func NormalizeEvent(msg string) string {

	if len(msg) == 0 {

		return ""

	}



	// 1. Strip everything before known severity markers to focus on the core message.

	// PostgreSQL stderr/syslog messages often include "[pid] sqlstate: db=... user=... "

	// before the severity level.

	severities := []string{"PANIC:", "FATAL:", "ERROR:", "WARNING:", "NOTICE:", "LOG:", "INFO:", "DEBUG:", "DETAIL:", "HINT:", "CONTEXT:", "STATEMENT:"}

	start := 0

	for _, sev := range severities {

		if idx := strings.Index(msg, sev); idx != -1 {

			start = idx + len(sev)

			break

		}

	}



	// If no severity marker found, check for a simple colon at the beginning (legacy/simple formats)

	if start == 0 {

		if idx := strings.IndexByte(msg, ':'); idx != -1 && idx < 15 {

			start = idx + 1

		}

	}



		msg = msg[start:]



		// Skip extra spaces after the marker



		for len(msg) > 0 && (msg[0] == ' ' || msg[0] == '\t') {



			msg = msg[1:]



		}



	



		if len(msg) == 0 {



			return ""



		}



	



		// 2. Strip technical suffixes often appended by CSV/JSON parsers.



		// We want the clean message signature, not the variable context.



		// Look for standard PostgreSQL metadata keywords that might appear after the main message.



		suffixes := []string{



			" DETAIL:",



			" HINT:",



			" QUERY:",



			" STATEMENT:",



			" CONTEXT:",



			" SQLSTATE =",



			" LOCATION:",



		}



	



		shortestIdx := -1



		for _, suffix := range suffixes {



			if idx := strings.Index(msg, suffix); idx != -1 {



				if shortestIdx == -1 || idx < shortestIdx {



					shortestIdx = idx



				}



			}



		}



		if shortestIdx != -1 {



			msg = msg[:shortestIdx]



		}



	



		// 3. Strip location info which prevents grouping



		// e.g. "syntax error at character 14" vs "syntax error at character 25"



		msg = locationRegex.ReplaceAllString(msg, "")



	



		



	



			// Mask variable details in unique constraint violations



	



			// e.g. "Key (email)=(foo@bar.com) already exists." -> "Key (?)=(?) already exists."



	



			msg = detailParamsRegex.ReplaceAllString(msg, "Key (?)=(?)")



	



		



	



			buf := builderPool.Get().(*strings.Builder)

	buf.Reset()

	buf.Grow(len(msg))

	defer builderPool.Put(buf)



	lastWasSpace := false



	for i := 0; i < len(msg); i++ {

		c := msg[i]



		// Handle double-quoted identifiers ("users")

		if c == '"' {

			buf.WriteByte('?')

			for i+1 < len(msg) {

				i++

				if msg[i] == '"' {

					break

				}

			}

			lastWasSpace = false

			continue

		}



		// Handle single-quoted values ('2025-01-01')

		if c == '\'' {

			buf.WriteByte('?')

			for i+1 < len(msg) {

				i++

				if msg[i] == '\'' {

					if i+1 < len(msg) && msg[i+1] == '\'' {

						i++

					} else {

						break

					}

				}

			}

			lastWasSpace = false

			continue

		}



		// Handle numbers (isolated integers)

		if c >= '0' && c <= '9' {

			isPrevIdentifier := i > 0 && isIdentifierChar(msg[i-1])

			if !isPrevIdentifier {

				buf.WriteByte('?')

				for i+1 < len(msg) && isDigit(msg[i+1]) {

					i++

				}

				lastWasSpace = false

				continue

			}

		}



		// Handle whitespace

		if c == '\n' || c == '\r' || c == '\t' || c == ' ' {

			if !lastWasSpace {

				buf.WriteByte(' ')

				lastWasSpace = true

			}

			continue

		}



		buf.WriteByte(c)

		lastWasSpace = false

	}



	return strings.TrimSpace(buf.String())

}




