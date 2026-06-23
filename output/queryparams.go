package output

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// slowestRunDisplayCap caps the substituted slowest-run query at display
// time. Pathological logs can inject multi-MB array literals via
// "DETAIL: parameters:"; the JSON output keeps the full text, but the
// terminal and markdown renderings truncate so the screen stays usable.
const slowestRunDisplayCap = 4000

// truncateForDisplay cuts s at max bytes when longer, leaving the
// stylistic framing to the caller. Returns the (possibly truncated)
// text, whether truncation occurred, and the original length so the
// caller can render a hint in the format it controls (ANSI italic
// for text, markdown emphasis for `.md`, etc.). Operates on byte
// length to avoid scanning multi-MB strings rune by rune.
func truncateForDisplay(s string, max int) (text string, truncated bool, fullLen int) {
	if len(s) <= max {
		return s, false, len(s)
	}
	return s[:max], true, len(s)
}

// truncationHint builds the " truncated, X of Y chars shown" suffix the
// renderers append right after the "[…]" marker. The marker itself stays
// in the surrounding text style; only this hint gets styled (italic grey
// in the terminal, plain text inside markdown code blocks).
func truncationHint(shownLen, fullLen int) string {
	return fmt.Sprintf(" truncated, %d of %d chars shown", shownLen, fullLen)
}

// paramRef matches "$N" placeholders. We rely on the regex word-boundary
// semantics indirectly: ReplaceAllStringFunc receives the full match so
// $10 is replaced as a whole, never split into $1 + literal "0".
var paramRef = regexp.MustCompile(`\$(\d+)`)

// SubstituteParameters returns query with every $N placeholder replaced
// by the corresponding value parsed from a PostgreSQL "DETAIL: parameters:"
// payload. Unmapped placeholders are left untouched. Returns query as-is
// when params is empty.
//
// Example:
//
//	q := "SELECT * FROM t WHERE id = $1 AND name = $2"
//	p := "$1 = '42', $2 = 'foo'"
//	SubstituteParameters(q, p) // "SELECT * FROM t WHERE id = '42' AND name = 'foo'"
func SubstituteParameters(query, params string) string {
	if params == "" || !strings.ContainsRune(query, '$') {
		return query
	}
	values := parseParameterValues(params)
	if len(values) == 0 {
		return query
	}
	return paramRef.ReplaceAllStringFunc(query, func(match string) string {
		n, err := strconv.Atoi(match[1:])
		if err != nil {
			return match
		}
		if v, ok := values[n]; ok {
			return v
		}
		return match
	})
}

// parseParameterValues turns "$1 = '393', $2 = NULL, $3 = '54'" into a
// map[int]string preserving the quoted form ("'393'") for string values
// and the bareword form for NULL. Single quotes inside string values are
// doubled per PostgreSQL convention.
func parseParameterValues(params string) map[int]string {
	out := make(map[int]string)
	i := 0
	for i < len(params) {
		// Find next '$'
		if params[i] != '$' {
			i++
			continue
		}
		i++ // skip '$'
		nStart := i
		for i < len(params) && params[i] >= '0' && params[i] <= '9' {
			i++
		}
		if i == nStart {
			continue
		}
		n, err := strconv.Atoi(params[nStart:i])
		if err != nil {
			continue
		}
		// Skip " = "
		for i < len(params) && (params[i] == ' ' || params[i] == '=') {
			i++
		}
		if i >= len(params) {
			break
		}

		// NULL literal
		if params[i] == 'N' && strings.HasPrefix(params[i:], "NULL") {
			out[n] = "NULL"
			i += 4
			continue
		}

		// Quoted string: '...', with '' as the doubled-quote escape.
		if params[i] == '\'' {
			start := i
			i++
			for i < len(params) {
				if params[i] != '\'' {
					i++
					continue
				}
				if i+1 < len(params) && params[i+1] == '\'' {
					i += 2 // escaped quote
					continue
				}
				break // closing quote
			}
			if i < len(params) {
				i++ // include closing '
			}
			out[n] = params[start:i]
			continue
		}

		// Unknown value form (bare numeric, bool, etc.); read until comma.
		start := i
		for i < len(params) && params[i] != ',' {
			i++
		}
		out[n] = strings.TrimSpace(params[start:i])
	}
	return out
}
