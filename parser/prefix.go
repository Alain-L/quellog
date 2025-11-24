// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"strings"
	"unicode"
)

// TokenType represents whether a token is a "word" or "non-word"
type TokenType int

const (
	TokenWord    TokenType = iota // Alphanumeric content (potential field value)
	TokenNonWord                  // Separators, punctuation, whitespace
)

// TokenClass represents the semantic role of a token
type TokenClass int

const (
	TokenClassUnknown      TokenClass = iota
	TokenClassLabel                   // Fixed text (e.g., "USER", "DB", "app=")
	TokenClassValue                   // Variable data (timestamp, PID, username, etc.)
	TokenClassSeparator               // Delimiters (: [ ] @ - etc.)
	TokenClassTimestampYear           // Year (YYYY)
	TokenClassTimestampMonth          // Month (MM)
	TokenClassTimestampDay            // Day (DD)
	TokenClassTimestampHour           // Hour (HH)
	TokenClassTimestampMinute         // Minute (mm)
	TokenClassTimestampSecond         // Second (SS)
	TokenClassTimestampMillisecond    // Millisecond (sss)
	TokenClassPID                     // Process ID (4-6 digits)
	TokenClassSessionID               // Session ID (%c - hex string)
	TokenClassLogLineNumber           // Log line number (%l - small integer)
	TokenClassUser                    // Username
	TokenClassDatabase                // Database name
	TokenClassApplication             // Application name
	TokenClassHost                    // Hostname or IP address
)

// Token represents a segment of the prefix (word or non-word)
type Token struct {
	Type  TokenType
	Value string
	Class TokenClass // Semantic classification
}

// PrefixStructure represents the analyzed structure of a log_line_prefix
type PrefixStructure struct {
	Raw    string   // Original prefix string
	Tokens []Token  // Alternating word/non-word tokens
}

// severityMarkers are used to find where the prefix ends
var severityMarkers = []string{
	"LOG:", "ERROR:", "WARNING:", "FATAL:", "PANIC:",
	"INFO:", "DETAIL:", "HINT:", "STATEMENT:", "CONTEXT:",
}

// AnalyzePrefixes samples log lines and extracts the word/non-word structure
// It returns a structure representing the common pattern found in the prefixes
func AnalyzePrefixes(lines []string, sampleSize int) *PrefixStructure {
	if len(lines) == 0 {
		return nil
	}

	// Limit sample size
	if sampleSize <= 0 {
		sampleSize = 20
	}
	if len(lines) > sampleSize {
		lines = lines[:sampleSize]
	}

	// Extract prefixes (everything before severity marker)
	prefixes := make([]string, 0, len(lines))
	for _, line := range lines {
		if prefix := extractPrefix(line); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}

	if len(prefixes) == 0 {
		return nil
	}

	// Tokenize all prefixes
	allTokens := make([][]Token, 0, len(prefixes))
	for _, prefix := range prefixes {
		structure := analyzePrefix(prefix)
		if structure != nil {
			allTokens = append(allTokens, structure.Tokens)
		}
	}

	if len(allTokens) == 0 {
		return nil
	}

	// First, detect timestamp components (structural pattern)
	// This is done before variance-based classification to avoid marking
	// constant timestamps as labels
	for i := range allTokens {
		allTokens[i] = detectTimestamps(allTokens[i])
	}

	// Then detect PIDs (numeric patterns after timestamps)
	for i := range allTokens {
		allTokens[i] = detectPID(allTokens[i])
	}

	// Detect Session IDs (%c - long hex strings)
	for i := range allTokens {
		allTokens[i] = detectSessionID(allTokens[i])
	}

	// Detect Log Line Numbers (%l - small integers)
	for i := range allTokens {
		allTokens[i] = detectLogLineNumber(allTokens[i])
	}

	// Detect user/database/application by explicit labels (user=, db=, app=, etc.)
	for i := range allTokens {
		allTokens[i] = detectLabels(allTokens[i])
	}

	// Detect user@database positional pattern
	for i := range allTokens {
		allTokens[i] = detectPositional(allTokens[i])
	}

	// Detect IP addresses (host)
	for i := range allTokens {
		allTokens[i] = detectHost(allTokens[i])
	}

	// Then classify remaining tokens by comparing across all samples
	classifiedTokens := classifyTokens(allTokens)

	// Score remaining VALUE tokens as USER/DB/APP based on count and position
	classifiedTokens = scoreRemainingValues(classifiedTokens)

	return &PrefixStructure{
		Raw:    prefixes[0],
		Tokens: classifiedTokens,
	}
}

// extractPrefix removes the severity marker and message, returning just the prefix
func extractPrefix(line string) string {
	for _, marker := range severityMarkers {
		if idx := strings.Index(line, marker); idx > 0 {
			return strings.TrimSpace(line[:idx])
		}
	}
	return ""
}

// analyzePrefix tokenizes a prefix into alternating word/non-word segments
func analyzePrefix(prefix string) *PrefixStructure {
	var tokens []Token

	i := 0
	for i < len(prefix) {
		// Check if current position is a word character
		r := rune(prefix[i])
		if isWordChar(r) {
			// Extract word
			start := i
			for i < len(prefix) && isWordChar(rune(prefix[i])) {
				i++
			}
			tokens = append(tokens, Token{
				Type:  TokenWord,
				Value: prefix[start:i],
			})
		} else {
			// Extract non-word
			start := i
			for i < len(prefix) && !isWordChar(rune(prefix[i])) {
				i++
			}
			tokens = append(tokens, Token{
				Type:  TokenNonWord,
				Value: prefix[start:i],
			})
		}
	}

	return &PrefixStructure{
		Raw:    prefix,
		Tokens: tokens,
	}
}

// isWordChar returns true if the rune is alphanumeric or underscore
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// detectTimestamps identifies timestamp components in the token sequence
// It looks for patterns like YYYY-MM-DD HH:mm:SS(.sss) by examining word sequences
func detectTimestamps(tokens []Token) []Token {
	// Extract word-only sequence (positions and values)
	type wordPos struct {
		index int
		value string
	}
	var words []wordPos
	for i, token := range tokens {
		if token.Type == TokenWord {
			words = append(words, wordPos{index: i, value: token.Value})
		}
	}

	// Look for timestamp patterns in the word sequence
	// Pattern: YYYY MM DD HH mm SS [sss] (ignoring separators)
	for i := 0; i < len(words); i++ {
		// Check if this could be the start of a timestamp (4-digit year)
		if len(words[i].value) != 4 || !isAllDigits(words[i].value) {
			continue
		}

		// Try to match timestamp pattern starting here
		pattern := []struct {
			length int
			class  TokenClass
		}{
			{4, TokenClassTimestampYear},        // YYYY
			{2, TokenClassTimestampMonth},       // MM
			{2, TokenClassTimestampDay},         // DD
			{2, TokenClassTimestampHour},        // HH
			{2, TokenClassTimestampMinute},      // mm
			{2, TokenClassTimestampSecond},      // SS
			{3, TokenClassTimestampMillisecond}, // sss (optional)
		}

		matched := 0
		for j := 0; j < len(pattern) && (i+j) < len(words); j++ {
			w := words[i+j]
			expectedLen := pattern[j].length

			// Check if word matches expected length and is all digits
			if len(w.value) == expectedLen && isAllDigits(w.value) {
				tokens[w.index].Class = pattern[j].class
				matched++
			} else {
				break
			}
		}

		// We need at least YYYY-MM-DD HH:mm:SS (6 components) for a valid timestamp
		if matched >= 6 {
			// Successfully detected timestamp, skip ahead
			i += matched - 1
		}
	}

	return tokens
}

// isAllDigits returns true if the string contains only digits
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// isHexString returns true if the string contains only hexadecimal characters (0-9, a-f, A-F)
func isHexString(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

// isTimestampClass returns true if the class is a timestamp component
func isTimestampClass(class TokenClass) bool {
	return class == TokenClassTimestampYear ||
		class == TokenClassTimestampMonth ||
		class == TokenClassTimestampDay ||
		class == TokenClassTimestampHour ||
		class == TokenClassTimestampMinute ||
		class == TokenClassTimestampSecond ||
		class == TokenClassTimestampMillisecond
}

// detectPID identifies process IDs in the token sequence
// PIDs are typically 4-6 digit numbers that aren't timestamp components
func detectPID(tokens []Token) []Token {
	// First pass: look for pure numeric tokens of 4-6 digits
	foundPID := false
	for i, token := range tokens {
		// Skip if not a word or already classified
		if token.Type != TokenWord || token.Class != TokenClassUnknown {
			continue
		}

		// Check if it's a numeric value with typical PID length (4-6 digits)
		if len(token.Value) >= 4 && len(token.Value) <= 6 && isAllDigits(token.Value) {
			tokens[i].Class = TokenClassPID
			foundPID = true
		}
	}

	// Fallback: if no PID found, look for digit sequences within tokens
	if !foundPID {
		for i, token := range tokens {
			// Skip if not a word or already classified as timestamp
			if token.Type != TokenWord || isTimestampClass(token.Class) {
				continue
			}

			// Look for a digit sequence of 4-6 digits within the token
			if digitSeq := extractDigitSequence(token.Value, 4, 6); digitSeq != "" {
				// Only classify if not already classified
				if tokens[i].Class == TokenClassUnknown {
					tokens[i].Class = TokenClassPID
				}
				foundPID = true
				break // Only take the first PID found
			}
		}
	}

	return tokens
}

// extractDigitSequence finds the first sequence of consecutive digits
// with length between minLen and maxLen in the string
func extractDigitSequence(s string, minLen, maxLen int) string {
	start := -1
	for i, r := range s {
		if r >= '0' && r <= '9' {
			if start == -1 {
				start = i
			}
			length := i - start + 1
			if length >= minLen && length <= maxLen {
				// Check if next char is also a digit (sequence continues)
				if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
					continue
				}
				// Found a valid sequence
				return s[start : i+1]
			}
		} else {
			// Reset if we find a non-digit
			if start != -1 {
				length := i - start
				if length >= minLen && length <= maxLen {
					return s[start:i]
				}
			}
			start = -1
		}
	}
	// Check final sequence
	if start != -1 {
		length := len(s) - start
		if length >= minLen && length <= maxLen {
			return s[start:]
		}
	}
	return ""
}

// detectSessionID identifies PostgreSQL session IDs (%c)
// Session IDs are long hexadecimal strings (typically 24 characters)
func detectSessionID(tokens []Token) []Token {
	for i, token := range tokens {
		// Skip if not a word or already classified
		if token.Type != TokenWord || token.Class != TokenClassUnknown {
			continue
		}

		// Session ID: long hex string (at least 16 chars, only 0-9a-f)
		if len(token.Value) >= 16 && isHexString(token.Value) {
			tokens[i].Class = TokenClassSessionID
			break // Only one session ID per prefix
		}
	}
	return tokens
}

// detectLogLineNumber identifies log line numbers (%l)
// Log line numbers are small integers (typically 1-4 digits)
func detectLogLineNumber(tokens []Token) []Token {
	for i, token := range tokens {
		// Skip if not a word or already classified
		if token.Type != TokenWord || token.Class != TokenClassUnknown {
			continue
		}

		// Skip if it's already detected as timestamp or PID
		if isTimestampClass(token.Class) || token.Class == TokenClassPID {
			continue
		}

		// Log line number: 1-4 digit number
		if len(token.Value) >= 1 && len(token.Value) <= 4 && isAllDigits(token.Value) {
			tokens[i].Class = TokenClassLogLineNumber
			// Don't break - there could be multiple in theory, but first one is most likely
		}
	}
	return tokens
}

// detectLabels identifies user/database/application fields by explicit labels
// Looks for patterns like "user=", "db=", "app=", "USER[", "DB[", etc.
func detectLabels(tokens []Token) []Token {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]

		// Skip if already classified
		if token.Class != TokenClassUnknown {
			continue
		}

		// Check if this is a label word
		if token.Type == TokenWord {
			lowerValue := strings.ToLower(token.Value)

			// User labels (user=, usr=, u=)
			if lowerValue == "user" || lowerValue == "usr" || lowerValue == "u" {
				// Mark as label
				tokens[i].Class = TokenClassLabel
				// Check if next token is the user value
				if i+2 < len(tokens) && tokens[i+1].Type == TokenNonWord {
					sep := tokens[i+1].Value
					if sep == "=" || sep == ":" || sep == "[" {
						// Mark the value token
						if tokens[i+2].Type == TokenWord && tokens[i+2].Class == TokenClassUnknown {
							tokens[i+2].Class = TokenClassUser
						}
					}
				}
			}

			// Database labels (db=, database=, d=)
			if lowerValue == "db" || lowerValue == "database" || lowerValue == "d" {
				tokens[i].Class = TokenClassLabel
				if i+2 < len(tokens) && tokens[i+1].Type == TokenNonWord {
					sep := tokens[i+1].Value
					if sep == "=" || sep == ":" || sep == "[" {
						if tokens[i+2].Type == TokenWord && tokens[i+2].Class == TokenClassUnknown {
							tokens[i+2].Class = TokenClassDatabase
						}
					}
				}
			}

			// Application labels (app=, application=, a=)
			if lowerValue == "app" || lowerValue == "application" || lowerValue == "a" {
				tokens[i].Class = TokenClassLabel
				if i+2 < len(tokens) && tokens[i+1].Type == TokenNonWord {
					sep := tokens[i+1].Value
					if sep == "=" || sep == ":" || sep == "[" {
						if tokens[i+2].Type == TokenWord && tokens[i+2].Class == TokenClassUnknown {
							tokens[i+2].Class = TokenClassApplication
						}
					}
				}
			}

			// Process/PID labels (proc=, process=, pid=, p=)
			// Note: "proc" and "p" refer to process ID, not application name
			if lowerValue == "proc" || lowerValue == "process" || lowerValue == "pid" || lowerValue == "p" {
				tokens[i].Class = TokenClassLabel
				if i+2 < len(tokens) && tokens[i+1].Type == TokenNonWord {
					sep := tokens[i+1].Value
					if sep == "=" || sep == ":" || sep == "[" {
						if tokens[i+2].Type == TokenWord && tokens[i+2].Class == TokenClassUnknown {
							tokens[i+2].Class = TokenClassPID
						}
					}
				}
			}
		}
	}

	return tokens
}

// detectPositional identifies user/database by positional patterns
// Looks for patterns like "user@database" where @ indicates the relationship
func detectPositional(tokens []Token) []Token {
	for i := 0; i < len(tokens)-2; i++ {
		// Look for pattern: WORD @ WORD
		if tokens[i].Type == TokenWord &&
			tokens[i+1].Type == TokenNonWord &&
			tokens[i+2].Type == TokenWord {

			// Check if separator is @
			if tokens[i+1].Value == "@" {
				// Token before @ is likely user, after @ is likely database
				if tokens[i].Class == TokenClassUnknown {
					tokens[i].Class = TokenClassUser
				}
				if tokens[i+2].Class == TokenClassUnknown {
					tokens[i+2].Class = TokenClassDatabase
				}
			}
		}
	}

	return tokens
}

// detectHost identifies IP addresses in the token sequence
// Looks for patterns like: digit.digit.digit.digit (IPv4)
func detectHost(tokens []Token) []Token {
	for i := 0; i < len(tokens)-6; i++ {
		// Pattern: WORD . WORD . WORD . WORD
		// Where each WORD is 1-3 digits
		if i+6 >= len(tokens) {
			break
		}

		// Check if we have the IPv4 pattern
		if tokens[i].Type == TokenWord &&
			tokens[i+1].Type == TokenNonWord && tokens[i+1].Value == "." &&
			tokens[i+2].Type == TokenWord &&
			tokens[i+3].Type == TokenNonWord && tokens[i+3].Value == "." &&
			tokens[i+4].Type == TokenWord &&
			tokens[i+5].Type == TokenNonWord && tokens[i+5].Value == "." &&
			tokens[i+6].Type == TokenWord {

			// Verify all parts are 1-3 digits
			if isAllDigits(tokens[i].Value) && len(tokens[i].Value) <= 3 &&
				isAllDigits(tokens[i+2].Value) && len(tokens[i+2].Value) <= 3 &&
				isAllDigits(tokens[i+4].Value) && len(tokens[i+4].Value) <= 3 &&
				isAllDigits(tokens[i+6].Value) && len(tokens[i+6].Value) <= 3 {

				// Skip if any part is already classified as timestamp
				if isTimestampClass(tokens[i].Class) ||
					isTimestampClass(tokens[i+2].Class) ||
					isTimestampClass(tokens[i+4].Class) ||
					isTimestampClass(tokens[i+6].Class) {
					continue
				}

				// Mark all 4 parts as host
				if tokens[i].Class == TokenClassUnknown || tokens[i].Class == TokenClassLabel || tokens[i].Class == TokenClassValue {
					tokens[i].Class = TokenClassHost
				}
				if tokens[i+2].Class == TokenClassUnknown || tokens[i+2].Class == TokenClassLabel || tokens[i+2].Class == TokenClassValue {
					tokens[i+2].Class = TokenClassHost
				}
				if tokens[i+4].Class == TokenClassUnknown || tokens[i+4].Class == TokenClassLabel || tokens[i+4].Class == TokenClassValue {
					tokens[i+4].Class = TokenClassHost
				}
				if tokens[i+6].Class == TokenClassUnknown || tokens[i+6].Class == TokenClassLabel || tokens[i+6].Class == TokenClassValue {
					tokens[i+6].Class = TokenClassHost
				}

				// Skip past this IP
				i += 6
			}
		}
	}

	return tokens
}

// scoreRemainingValues classifies unidentified VALUE tokens as USER/DB/APP
// based on their count and position in the prefix
func scoreRemainingValues(tokens []Token) []Token {
	// Common PostgreSQL application names
	knownApps := map[string]bool{
		"psql":         true,
		"pgbench":      true,
		"pgadmin":      true,
		"pgadmin4":     true,
		"pg_dump":      true,
		"pg_restore":   true,
		"pg_basebackup": true,
		"psycopg2":     true,
		"jdbc":         true,
		"odbc":         true,
		"rails":        true,
		"django":       true,
		"spring":       true,
		"node":         true,
	}

	// Collect VALUE tokens that are still unclassified (not USER/DB/APP/HOST/PID)
	var valueIndices []int
	for i, token := range tokens {
		if token.Type == TokenWord && token.Class == TokenClassValue {
			valueIndices = append(valueIndices, i)
		}
	}

	if len(valueIndices) == 0 {
		return tokens
	}

	// Score based on count and position
	switch len(valueIndices) {
	case 1:
		// Single VALUE → check if it's a known app first, otherwise USER
		val := strings.ToLower(tokens[valueIndices[0]].Value)
		if knownApps[val] || strings.Contains(val, "psql") || strings.Contains(val, "pg") {
			tokens[valueIndices[0]].Class = TokenClassApplication
		} else {
			tokens[valueIndices[0]].Class = TokenClassUser
		}

	case 2:
		// Two VALUES → likely USER + DB (in that order)
		tokens[valueIndices[0]].Class = TokenClassUser
		tokens[valueIndices[1]].Class = TokenClassDatabase

	case 3:
		// Three VALUES → likely USER + DB + APP (in that order)
		tokens[valueIndices[0]].Class = TokenClassUser
		tokens[valueIndices[1]].Class = TokenClassDatabase
		tokens[valueIndices[2]].Class = TokenClassApplication

	default:
		// 4+ VALUES → USER + DB + APP + others remain VALUE
		tokens[valueIndices[0]].Class = TokenClassUser
		tokens[valueIndices[1]].Class = TokenClassDatabase

		// Check if any looks like a known application
		foundApp := false
		for i := 2; i < len(valueIndices); i++ {
			val := strings.ToLower(tokens[valueIndices[i]].Value)
			if knownApps[val] || strings.Contains(val, "psql") || strings.Contains(val, "pg") {
				tokens[valueIndices[i]].Class = TokenClassApplication
				foundApp = true
				break
			}
		}

		// If no known app found, assume third VALUE is app
		if !foundApp && len(valueIndices) >= 3 {
			tokens[valueIndices[2]].Class = TokenClassApplication
		}
	}

	return tokens
}

// classifyTokens compares tokens across multiple samples to classify them
// as labels (constant words), values (variable words), or separators (constant non-words)
func classifyTokens(allTokens [][]Token) []Token {
	if len(allTokens) == 0 {
		return nil
	}

	// Use the first sample as reference
	reference := allTokens[0]
	result := make([]Token, len(reference))

	// For each token position
	for i := 0; i < len(reference); i++ {
		refToken := reference[i]
		result[i] = refToken

		// Skip if already classified by structural detection (timestamp, PID, etc.)
		if refToken.Class != TokenClassUnknown {
			continue
		}

		// Count unique values at this position across all samples
		uniqueValues := make(map[string]bool)
		allSameType := true

		for _, tokens := range allTokens {
			// Skip if this sample doesn't have enough tokens
			if i >= len(tokens) {
				continue
			}

			token := tokens[i]
			uniqueValues[token.Value] = true

			// Check if all samples have the same token type at this position
			if token.Type != refToken.Type {
				allSameType = false
			}
		}

		// Skip classification if token types don't match across samples
		if !allSameType {
			result[i].Class = TokenClassUnknown
			continue
		}

		// Classify based on type and variance
		numUnique := len(uniqueValues)
		totalSamples := len(allTokens)

		if refToken.Type == TokenNonWord {
			// Non-words are separators (should be constant)
			if numUnique == 1 {
				result[i].Class = TokenClassSeparator
			} else {
				// Variable non-word is unusual, mark as unknown
				result[i].Class = TokenClassUnknown
			}
		} else {
			// Words can be labels or values
			if numUnique == 1 {
				// Constant word - likely a label
				// Additional heuristic: check if it looks like a label
				if looksLikeLabel(refToken.Value) {
					result[i].Class = TokenClassLabel
				} else {
					// Could be a value that happens to be constant in our sample
					result[i].Class = TokenClassLabel // Default to label for constant words
				}
			} else if float64(numUnique)/float64(totalSamples) > 0.5 {
				// High variance - likely a value
				result[i].Class = TokenClassValue
			} else {
				// Medium variance - could be either, default to value
				result[i].Class = TokenClassValue
			}
		}
	}

	return result
}

// looksLikeLabel returns true if a word looks like a label (uppercase, common keywords)
func looksLikeLabel(word string) bool {
	// Common label keywords
	labelKeywords := map[string]bool{
		"USER": true, "DB": true, "PID": true, "APP": true,
		"TIME": true, "SESSION": true, "HOST": true,
		"user": true, "db": true, "pid": true, "app": true,
		"time": true, "session": true, "host": true,
	}

	if labelKeywords[word] {
		return true
	}

	// Check if word ends with common label suffixes
	if strings.HasSuffix(word, "=") || strings.HasSuffix(word, ":") {
		return true
	}

	// Check if word is all uppercase (likely a label)
	if len(word) > 0 && word == strings.ToUpper(word) && unicode.IsLetter(rune(word[0])) {
		return true
	}

	return false
}
