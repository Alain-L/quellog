// Package output provides formatting and display functions for analysis results.
package output

import (
	"regexp"
	"strings"
)

// formatSQL formats a SQL query for better readability with basic indentation.
// This is a simple formatter that handles common SQL keywords and basic structure.
func formatSQL(query string) string {
	if query == "" {
		return query
	}

	// Normalize whitespace
	query = strings.TrimSpace(query)
	query = regexp.MustCompile(`\s+`).ReplaceAllString(query, " ")

	// Keywords that should start a new line (case insensitive)
	majorKeywords := []string{
		"SELECT", "FROM", "WHERE", "JOIN", "INNER JOIN", "LEFT JOIN", "RIGHT JOIN",
		"FULL JOIN", "CROSS JOIN", "ON", "GROUP BY", "HAVING", "ORDER BY",
		"LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT",
	}

	// Keywords that should be indented
	indentedKeywords := []string{
		"AND", "OR",
	}

	result := query

	// Replace major keywords with newline + keyword
	for _, keyword := range majorKeywords {
		// Use word boundaries to avoid matching partial words
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			return "\n" + strings.ToUpper(match)
		})
	}

	// Replace indented keywords (AND, OR) with newline + indent + keyword
	for _, keyword := range indentedKeywords {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			return "\n  " + strings.ToUpper(match)
		})
	}

	// Split into lines and process
	lines := strings.Split(result, "\n")
	formatted := make([]string, 0, len(lines))
	indentLevel := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Count opening and closing parentheses to adjust indent
		openCount := strings.Count(line, "(")
		closeCount := strings.Count(line, ")")

		// Decrease indent before line if it starts with closing paren
		if strings.HasPrefix(line, ")") && indentLevel > 0 {
			indentLevel--
		}

		// Apply current indentation
		indent := strings.Repeat("  ", indentLevel)
		formatted = append(formatted, indent+line)

		// Increase indent after line based on unmatched opening parens
		indentLevel += openCount - closeCount

		// Prevent negative indentation
		if indentLevel < 0 {
			indentLevel = 0
		}
	}

	return strings.Join(formatted, "\n")
}
