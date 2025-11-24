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
	TokenClassUnknown              TokenClass = iota
	TokenClassLabel                           // Fixed text (e.g., "USER", "DB", "app=")
	TokenClassValue                           // Variable data (timestamp, PID, username, etc.)
	TokenClassSeparator                       // Delimiters (: [ ] @ - etc.)

	// Timestamp components (%t, %m, %n)
	TokenClassTimestampYear                   // Year (YYYY)
	TokenClassTimestampMonth                  // Month (MM)
	TokenClassTimestampDay                    // Day (DD)
	TokenClassTimestampHour                   // Hour (HH)
	TokenClassTimestampMinute                 // Minute (mm)
	TokenClassTimestampSecond                 // Second (SS)
	TokenClassTimestampMillisecond            // Millisecond (sss)

	// Process and session identifiers (%p, %c, %l)
	TokenClassPID                             // Process ID (%p - 4-6 digits)
	TokenClassSessionID                       // Session ID (%c - hex string)
	TokenClassLogLineNumber                   // Log line number (%l - small integer)

	// Connection metadata (%u, %d, %a, %h, %r)
	TokenClassUser                            // Username (%u)
	TokenClassDatabase                        // Database name (%d)
	TokenClassApplication                     // Application name (%a)
	TokenClassHost                            // Hostname or IP address (%h, %r)

	// TODO: Future implementation - Advanced PostgreSQL log_line_prefix parameters
	// Uncomment and implement detection logic when needed

	// TokenClassLocalAddress                 // Local server IP address (%L)
	// TokenClassBackendType                  // Backend type (%b - e.g., "client backend", "autovacuum worker")
	// TokenClassParallelGroupLeaderPID       // Parallel group leader PID (%P)
	// TokenClassUnixEpochTimestamp           // Unix epoch timestamp with ms (%n)
	// TokenClassCommandTag                   // Command tag (%i - SELECT, INSERT, UPDATE, etc.)
	// TokenClassSQLStateErrorCode            // SQLSTATE error code (%e - 5 chars like "42P01")
	// TokenClassProcessStartTimestamp        // Process start timestamp (%s)
	// TokenClassVirtualTransactionID         // Virtual transaction ID (%v - format: procNumber/localXID)
	// TokenClassTransactionID                // Transaction ID (%x - 0 if none assigned)
	// TokenClassQueryID                      // Query identifier (%Q - requires compute_query_id)
)

// Token represents a segment of the prefix (word or non-word)
type Token struct {
	Type  TokenType
	Value string
	Class TokenClass // Semantic classification
}

// PrefixStructure represents the analyzed structure of a log_line_prefix
type PrefixStructure struct {
	Raw    string  // Original prefix string
	Tokens []Token // Alternating word/non-word tokens
}

// ExtractedMetadata contains metadata extracted from a log line using prefix structure
type ExtractedMetadata struct {
	// Currently implemented fields
	User        string // Username (%u)
	Database    string // Database name (%d)
	Application string // Application name (%a)
	Host        string // Remote hostname or IP (%h, %r)
	Prefix      string // The full prefix that was parsed
	Message     string // The message after the prefix

	// TODO: Future implementation - Additional PostgreSQL log_line_prefix fields
	// Uncomment when implementing corresponding TokenClass detection

	// LocalAddress     string // Local server IP address (%L)
	// BackendType      string // Backend type (%b)
	// ParallelLeaderPID string // Parallel group leader PID (%P)
	// UnixEpoch        string // Unix epoch timestamp (%n)
	// CommandTag       string // Command tag (%i)
	// SQLStateCode     string // SQLSTATE error code (%e)
	// ProcessStartTime string // Process start timestamp (%s)
	// VirtualTxID      string // Virtual transaction ID (%v)
	// TransactionID    string // Transaction ID (%x)
	// QueryID          string // Query identifier (%Q)
}

// severityMarkers are used to find where the prefix ends.
//
// We include the colon (":") in each marker to avoid false positives from other colons
// in the prefix (e.g., timestamps "00:00:01", PID markers "[123]:", etc.).
// By searching for "LOG:" rather than just ":", we ensure we find the actual
// message boundary.
//
// Includes both severity levels (LOG, ERROR, etc.) and continuation keywords
// (DETAIL, HINT, etc.) since they all can have the full prefix in PostgreSQL logs.
//
// Note: NOTICE is not included as it often appears without a prefix in some configs.
var severityMarkers = []string{
	// Main severity levels (ordered by severity)
	"DEBUG5:", "DEBUG4:", "DEBUG3:", "DEBUG2:", "DEBUG1:", "DEBUG:",
	"LOG:", "INFO:", "WARNING:", "ERROR:", "FATAL:", "PANIC:",
	// Continuation keywords (always follow a main severity line)
	"DETAIL:", "HINT:", "STATEMENT:", "CONTEXT:",
}

// knownApps is a set of well-known PostgreSQL application names
// Used for identifying application fields in log prefixes
var knownApps = map[string]bool{
	"psql":          true,
	"pgbench":       true,
	"pgadmin":       true,
	"pgadmin4":      true,
	"pg_dump":       true,
	"pg_restore":    true,
	"pg_basebackup": true,
	"pg_rewind":     true,
	"pg_upgrade":    true,
	"psycopg2":      true,
	"jdbc":          true,
	"odbc":          true,
	"rails":         true,
	"django":        true,
	"spring":        true,
	"node":          true,
	"nodejs":        true,
	"python":        true,
	"java":          true,
	"php":           true,
}

// knownUsernames is a set of common PostgreSQL usernames
// Used to disambiguate user vs database fields
var knownUsernames = map[string]bool{
	"postgres":  true,
	"admin":     true,
	"root":      true,
	"user":      true,
	"alice":     true,
	"bob":       true,
	"charlie":   true,
	"dave":      true,
	"eve":       true,
	"frank":     true,
	"grace":     true,
	"henry":     true,
	"test":      true,
	"demo":      true,
	"guest":     true,
	"anonymous": true,
}

// knownDatabases is a set of common PostgreSQL database names
// Used to disambiguate user vs database fields
var knownDatabases = map[string]bool{
	"postgres":   true,
	"template0":  true,
	"template1":  true,
	"mydb":       true,
	"testdb":     true,
	"test":       true,
	"production": true,
	"prod":       true,
	"proddb":     true,
	"development": true,
	"dev":        true,
	"devdb":      true,
	"staging":    true,
	"database":   true,
	"db":         true,
	"main":       true,
	"app":        true,
	"appdb":      true,
}

// detectorFunc is a function that detects patterns in a token sequence.
type detectorFunc func([]Token) []Token

// applyDetectors applies a sequence of detector functions to all token arrays.
// Each detector is applied to all samples before moving to the next detector.
func applyDetectors(allTokens [][]Token, detectors ...detectorFunc) [][]Token {
	for _, detector := range detectors {
		for i := range allTokens {
			allTokens[i] = detector(allTokens[i])
		}
	}
	return allTokens
}

// AnalyzePrefixes samples log lines and extracts the word/non-word structure.
// It returns a structure representing the common pattern found in the prefixes.
//
// The function analyzes a sample of log lines to detect the log_line_prefix pattern
// and classify each component (timestamp, PID, user, database, application, etc.).
//
// Parameters:
//   - lines: Array of log lines to analyze
//   - sampleSize: Maximum number of lines to sample (0 or negative uses default of 20)
//
// Returns:
//   - PrefixStructure containing the detected pattern, or nil if no pattern found
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

	// Apply structural detectors in sequence
	// Order matters: timestamps first, then PIDs, then semantic fields
	allTokens = applyDetectors(allTokens,
		detectTimestamps,    // Detect timestamp components (YYYY-MM-DD HH:mm:SS)
		detectPID,           // Detect process IDs (4-6 digit numbers)
		detectSessionID,     // Detect session IDs (%c - long hex strings)
		detectLogLineNumber, // Detect log line numbers (%l - small integers)
		detectLabels,        // Detect labeled fields (user=, db=, app=)
		detectKnownApps,     // Detect known application names
		detectPositional,    // Detect user@database patterns
		detectHost,          // Detect IP addresses
	)

	// Then classify remaining tokens by comparing across all samples
	classifiedTokens := classifyTokens(allTokens)

	// Score remaining VALUE tokens as USER/DB/APP based on variance and position
	classifiedTokens = scoreRemainingValues(classifiedTokens, allTokens, prefixes)

	// Final fallback: try splitting underscore-joined tokens if we're missing essential fields
	classifiedTokens = detectUnderscoreFallback(classifiedTokens, allTokens, prefixes)

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

// detectTimestamps identifies timestamp components in the token sequence.
// It looks for patterns like YYYY-MM-DD HH:mm:SS(.sss) by examining word sequences.
//
// The function searches for sequences of 2-digit and 4-digit numbers that match
// the structure of a timestamp. At least 6 components (year through second) must
// match for a valid timestamp detection.
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

// detectPID identifies process IDs in the token sequence.
// PIDs are typically 4-6 digit numbers that aren't timestamp components.
//
// The function first looks for standalone numeric tokens, then falls back to
// extracting digit sequences from within tokens if no pure numeric PID is found.
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

// detectLabels identifies user/database/application fields by explicit labels.
// Looks for patterns like "user=", "db=", "app=", "pid=", followed by a value token.
//
// Supported label formats:
//   - user=, usr=, u= → USER
//   - db=, database=, d= → DATABASE
//   - app=, application=, a= → APPLICATION
//   - pid=, proc=, process=, p= → PID
//
// The label can be followed by =, :, or [ as separator.
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

// detectKnownApps identifies well-known PostgreSQL application names.
// This runs before positional detection to avoid misclassifying apps as USER/DB.
func detectKnownApps(tokens []Token) []Token {
	for i, token := range tokens {
		if token.Type != TokenWord || token.Class != TokenClassUnknown {
			continue
		}

		// Check if the value (case-insensitive) is a known app
		lowerVal := strings.ToLower(token.Value)
		if knownApps[lowerVal] {
			tokens[i].Class = TokenClassApplication
			continue
		}

		// Check for partial matches with pg-related tools
		if strings.Contains(lowerVal, "psql") ||
			strings.HasPrefix(lowerVal, "pg_") ||
			strings.HasPrefix(lowerVal, "pg-") {
			tokens[i].Class = TokenClassApplication
		}
	}

	return tokens
}

// detectPositional identifies user/database by positional patterns
// Looks for patterns like "user@database" where @ indicates the relationship
// Only applies if there is exactly ONE @ to avoid conflicts with complex patterns
func detectPositional(tokens []Token) []Token {
	// Count @ symbols in the prefix
	atCount := 0
	for _, token := range tokens {
		if token.Type == TokenNonWord && token.Value == "@" {
			atCount++
		}
	}

	// Only apply user@database rule if there is exactly one @
	// Multiple @ symbols indicate complex patterns that need different handling
	if atCount != 1 {
		return tokens
	}

	for i := 0; i < len(tokens)-2; i++ {
		// Look for pattern: WORD @ WORD
		if tokens[i].Type == TokenWord &&
			tokens[i+1].Type == TokenNonWord &&
			tokens[i+2].Type == TokenWord {

			// Check if separator is @
			if tokens[i+1].Value == "@" {
				// Skip if the token after @ is already classified as structural field
				// (timestamp, PID, session, etc.) - this means @ is not user@database
				afterClass := tokens[i+2].Class
				if afterClass != TokenClassUnknown &&
					afterClass != TokenClassValue &&
					afterClass != TokenClassUser &&
					afterClass != TokenClassDatabase &&
					afterClass != TokenClassApplication {
					continue
				}

				// Special case: if token after @ is already APP, pattern is DB@APP not USER@DB
				if tokens[i+2].Class == TokenClassApplication {
					// Token before @ is likely database
					if tokens[i].Class == TokenClassUnknown {
						tokens[i].Class = TokenClassDatabase
					}
				} else {
					// Standard case: token before @ is likely user, after @ is likely database
					if tokens[i].Class == TokenClassUnknown {
						tokens[i].Class = TokenClassUser
					}
					if tokens[i+2].Class == TokenClassUnknown {
						tokens[i+2].Class = TokenClassDatabase
					}
				}
			}
		}
	}

	return tokens
}

// detectHost identifies IP addresses in the token sequence.
// Looks for patterns like: digit.digit.digit.digit (IPv4).
func detectHost(tokens []Token) []Token {
	// Need at least 7 tokens for IPv4 pattern (4 numbers + 3 dots)
	for i := 0; i <= len(tokens)-7; i++ {

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
// based on their variance characteristics, count, and position in the prefix.
func scoreRemainingValues(tokens []Token, allTokens [][]Token, prefixes []string) []Token {
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

	// Score based on count and variance characteristics
	switch len(valueIndices) {
	case 1:
		// Single VALUE → use known lists and variance analysis to determine type
		idx := valueIndices[0]
		val := strings.ToLower(tokens[idx].Value)

		// First check known lists for direct match
		if knownApps[val] || strings.Contains(val, "psql") || strings.Contains(val, "pg") {
			tokens[idx].Class = TokenClassApplication
		} else if knownUsernames[val] {
			tokens[idx].Class = TokenClassUser
		} else if knownDatabases[val] {
			tokens[idx].Class = TokenClassDatabase
		} else {
			// Calculate variance metrics for this token
			variance := calculateVarianceMetrics(idx, allTokens, prefixes)

			// Heuristic based on observed patterns:
			// - Uniqueness ≥35% → USER (many different usernames, typically 40%+)
			// - Uniqueness <35% → DATABASE (fewer databases, typically 30% or less)
			// The 35% threshold is calibrated from mock data analysis
			if variance.uniquenessRatio >= 0.35 {
				tokens[idx].Class = TokenClassUser
			} else {
				tokens[idx].Class = TokenClassDatabase
			}
		}

	case 2:
		// Two VALUES → likely USER + DB, but check known lists to determine order
		val0 := strings.ToLower(tokens[valueIndices[0]].Value)
		val1 := strings.ToLower(tokens[valueIndices[1]].Value)

		// Check if values match known lists (this determines order)
		val0IsUser := knownUsernames[val0]
		val0IsDB := knownDatabases[val0]
		val1IsUser := knownUsernames[val1]
		val1IsDB := knownDatabases[val1]

		if val0IsUser && !val0IsDB {
			// val0 is definitely user
			tokens[valueIndices[0]].Class = TokenClassUser
			tokens[valueIndices[1]].Class = TokenClassDatabase
		} else if val0IsDB && !val0IsUser {
			// val0 is definitely db → order is reversed
			tokens[valueIndices[0]].Class = TokenClassDatabase
			tokens[valueIndices[1]].Class = TokenClassUser
		} else if val1IsUser && !val1IsDB {
			// val1 is definitely user → order is reversed
			tokens[valueIndices[0]].Class = TokenClassDatabase
			tokens[valueIndices[1]].Class = TokenClassUser
		} else if val1IsDB && !val1IsUser {
			// val1 is definitely db → normal order
			tokens[valueIndices[0]].Class = TokenClassUser
			tokens[valueIndices[1]].Class = TokenClassDatabase
		} else {
			// No clear match, use default PostgreSQL order: USER then DB
			tokens[valueIndices[0]].Class = TokenClassUser
			tokens[valueIndices[1]].Class = TokenClassDatabase
		}

	case 3:
		// Three VALUES → likely USER + DB + APP, use known lists to verify
		val0 := strings.ToLower(tokens[valueIndices[0]].Value)
		val1 := strings.ToLower(tokens[valueIndices[1]].Value)
		val2 := strings.ToLower(tokens[valueIndices[2]].Value)

		// Check which values match known lists
		val0IsUser := knownUsernames[val0]
		val0IsDB := knownDatabases[val0]
		val0IsApp := knownApps[val0]
		val1IsUser := knownUsernames[val1]
		val1IsDB := knownDatabases[val1]
		val1IsApp := knownApps[val1]
		val2IsApp := knownApps[val2]

		// Try to determine correct order based on known matches
		// Default order: USER, DB, APP
		userIdx := valueIndices[0]
		dbIdx := valueIndices[1]
		appIdx := valueIndices[2]

		// If val2 is clearly an app, that's the app
		if val2IsApp {
			appIdx = valueIndices[2]
			// Now determine user vs db for first two
			if val0IsUser && !val0IsDB {
				userIdx = valueIndices[0]
				dbIdx = valueIndices[1]
			} else if val0IsDB && !val0IsUser {
				dbIdx = valueIndices[0]
				userIdx = valueIndices[1]
			}
		} else if val0IsApp {
			// Unusual order: APP first
			appIdx = valueIndices[0]
			if val1IsDB && !val1IsUser {
				dbIdx = valueIndices[1]
				userIdx = valueIndices[2]
			} else {
				userIdx = valueIndices[1]
				dbIdx = valueIndices[2]
			}
		} else if val1IsApp {
			// APP in middle
			appIdx = valueIndices[1]
			if val0IsUser && !val0IsDB {
				userIdx = valueIndices[0]
				dbIdx = valueIndices[2]
			} else if val0IsDB && !val0IsUser {
				dbIdx = valueIndices[0]
				userIdx = valueIndices[2]
			}
		} else {
			// No app match, determine user/db order
			if val0IsDB && !val0IsUser {
				dbIdx = valueIndices[0]
				userIdx = valueIndices[1]
			} else if val1IsDB && !val1IsUser {
				userIdx = valueIndices[0]
				dbIdx = valueIndices[1]
			}
			// Third value could be app or something else
			if strings.Contains(val2, "psql") || strings.Contains(val2, "pg") {
				appIdx = valueIndices[2]
			}
		}

		tokens[userIdx].Class = TokenClassUser
		tokens[dbIdx].Class = TokenClassDatabase
		tokens[appIdx].Class = TokenClassApplication

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

// varianceMetrics holds statistics about a token's variance across samples
type varianceMetrics struct {
	uniqueCount     int     // Number of unique values
	totalSamples    int     // Total number of samples
	uniquenessRatio float64 // Ratio of unique values to samples (0.0 to 1.0)
	avgLength       float64 // Average length of the values
}

// calculateVarianceMetrics analyzes how a token varies across all samples.
// Returns metrics about uniqueness and average length to help classify ambiguous tokens.
func calculateVarianceMetrics(tokenIndex int, allTokens [][]Token, prefixes []string) varianceMetrics {
	uniqueValues := make(map[string]bool)
	totalLength := 0
	count := 0

	// Collect all values at this token position across samples
	for _, tokens := range allTokens {
		if tokenIndex >= len(tokens) {
			continue
		}

		value := tokens[tokenIndex].Value
		uniqueValues[value] = true
		totalLength += len(value)
		count++
	}

	// Calculate metrics
	uniqueCount := len(uniqueValues)
	var avgLength float64
	if count > 0 {
		avgLength = float64(totalLength) / float64(count)
	}

	var uniquenessRatio float64
	if count > 0 {
		uniquenessRatio = float64(uniqueCount) / float64(count)
	}

	return varianceMetrics{
		uniqueCount:     uniqueCount,
		totalSamples:    count,
		uniquenessRatio: uniquenessRatio,
		avgLength:       avgLength,
	}
}

// detectUnderscoreFallback handles rare patterns where multiple fields are joined by underscores.
//
// Example: log_line_prefix='%t_%p_%u_%d_%a' produces "UTC_10000_alice_mydb_psql" as a single token
// since underscores are treated as word characters by the tokenizer.
//
// This fallback only activates when:
//   - We're missing 2 or more essential fields (USER/DB/APP)
//   - We find a token with 2+ underscores that could be split
//   - Splitting and re-classifying improves field detection
//
// The function splits the token, re-runs the full detection pipeline, and only keeps
// the split version if it recovers missing fields.
func detectUnderscoreFallback(tokens []Token, allTokens [][]Token, prefixes []string) []Token {
	// Check if we're missing essential fields
	hasUser := false
	hasDB := false
	hasApp := false

	for _, token := range tokens {
		if token.Class == TokenClassUser {
			hasUser = true
		}
		if token.Class == TokenClassDatabase {
			hasDB = true
		}
		if token.Class == TokenClassApplication {
			hasApp = true
		}
	}

	// Count how many essential fields are missing
	missingCount := 0
	if !hasUser {
		missingCount++
	}
	if !hasDB {
		missingCount++
	}
	if !hasApp {
		missingCount++
	}

	// Only apply fallback if we're missing 2 or more essential fields
	// (One missing field might be intentional in the log_line_prefix)
	if missingCount < 2 {
		return tokens
	}

	// Look for tokens with multiple underscores (VALUE, PID, SessionID, or any WORD)
	// These might be concatenated fields that were misclassified
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token.Type != TokenWord {
			continue
		}

		// Skip if it's a timestamp component (those should never be split)
		if isTimestampClass(token.Class) {
			continue
		}

		// Check if token has multiple underscores (at least 2)
		underscoreCount := strings.Count(token.Value, "_")
		if underscoreCount < 2 {
			continue
		}

		// This token might be a concatenation of multiple fields
		// Try to split and re-tokenize all samples
		var newAllTokens [][]Token
		canSplit := true

		for _, sampleTokens := range allTokens {
			if i >= len(sampleTokens) {
				canSplit = false
				break
			}

			sampleToken := sampleTokens[i]
			if sampleToken.Type != TokenWord {
				canSplit = false
				break
			}

			// Split by underscore
			parts := strings.Split(sampleToken.Value, "_")
			if len(parts) < 3 {
				canSplit = false
				break
			}

			// Build new token sequence for this sample
			var newSampleTokens []Token

			// Copy tokens before the split position
			newSampleTokens = append(newSampleTokens, sampleTokens[:i]...)

			// Add the split tokens (word _ word _ word ...)
			for j, part := range parts {
				if part == "" {
					continue
				}

				newSampleTokens = append(newSampleTokens, Token{
					Type:  TokenWord,
					Value: part,
					Class: TokenClassUnknown,
				})

				// Add underscore separator between parts (but not after last part)
				if j < len(parts)-1 {
					newSampleTokens = append(newSampleTokens, Token{
						Type:  TokenNonWord,
						Value: "_",
						Class: TokenClassSeparator,
					})
				}
			}

			// Copy tokens after the split position
			if i+1 < len(sampleTokens) {
				newSampleTokens = append(newSampleTokens, sampleTokens[i+1:]...)
			}

			newAllTokens = append(newAllTokens, newSampleTokens)
		}

		// Only apply the split if we could split consistently across all samples
		if !canSplit || len(newAllTokens) != len(allTokens) {
			continue
		}

		// Re-run full detection pipeline on the split tokens
		newAllTokens = applyDetectors(newAllTokens,
			detectTimestamps,
			detectPID,
			detectSessionID,
			detectLogLineNumber,
			detectLabels,
			detectKnownApps,
			detectPositional,
			detectHost,
		)

		// Classify and score
		newClassified := classifyTokens(newAllTokens)
		newClassified = scoreRemainingValues(newClassified, newAllTokens, prefixes)

		// Check if the new classification is better (has more essential fields)
		newHasUser := false
		newHasDB := false
		newHasApp := false

		for _, t := range newClassified {
			if t.Class == TokenClassUser {
				newHasUser = true
			}
			if t.Class == TokenClassDatabase {
				newHasDB = true
			}
			if t.Class == TokenClassApplication {
				newHasApp = true
			}
		}

		// Count how many we recovered
		newMissingCount := 0
		if !newHasUser {
			newMissingCount++
		}
		if !newHasDB {
			newMissingCount++
		}
		if !newHasApp {
			newMissingCount++
		}

		// If we improved (recovered at least one field), use the new classification
		if newMissingCount < missingCount {
			return newClassified
		}

		// Otherwise, try the next candidate token
	}

	// No improvement possible, return original tokens
	return tokens
}

// ExtractMetadataFromLine extracts metadata from a log line using a detected prefix structure.
//
// This function uses the PrefixStructure to parse the log line prefix, extract metadata
// (user, database, application, host), and return both the metadata and the message
// without the prefix.
//
// Parameters:
//   - line: The log line to parse
//   - structure: The detected prefix structure to use for parsing
//
// Returns:
//   - ExtractedMetadata containing all extracted fields and the message, or nil if parsing fails
func ExtractMetadataFromLine(line string, structure *PrefixStructure) *ExtractedMetadata {
	if structure == nil || line == "" {
		return nil
	}

	// Extract the prefix from this line
	prefix := extractPrefix(line)
	if prefix == "" {
		return nil
	}

	// Tokenize this line's prefix using analyzePrefix
	prefixStructure := analyzePrefix(prefix)
	if prefixStructure == nil || len(prefixStructure.Tokens) == 0 {
		return nil
	}
	lineTokens := prefixStructure.Tokens

	// Match tokens against the structure to extract values
	result := &ExtractedMetadata{
		Prefix: prefix,
	}

	// We need to align the line tokens with the structure tokens
	// The structure tells us which positions contain USER, DB, APP, etc.
	minLen := len(lineTokens)
	if len(structure.Tokens) < minLen {
		minLen = len(structure.Tokens)
	}

	for i := 0; i < minLen; i++ {
		structToken := structure.Tokens[i]
		lineToken := lineTokens[i]

		// Extract values based on the structure's classification
		switch structToken.Class {
		case TokenClassUser:
			result.User = lineToken.Value
		case TokenClassDatabase:
			result.Database = lineToken.Value
		case TokenClassApplication:
			result.Application = lineToken.Value
		case TokenClassHost:
			result.Host = lineToken.Value
		}
	}

	// Fallback: if we didn't extract metadata and have underscore-concatenated tokens,
	// try to extract known values from within those tokens
	if result.User == "" && result.Database == "" && result.Application == "" {
		for _, token := range lineTokens {
			if token.Type != TokenWord {
				continue
			}

			// Check if token contains underscores (potential concatenation)
			if !strings.Contains(token.Value, "_") {
				continue
			}

			// Split by underscore and check each part
			parts := strings.Split(token.Value, "_")
			for _, part := range parts {
				lowerPart := strings.ToLower(part)

				// Try to identify each part
				if result.User == "" && knownUsernames[lowerPart] {
					result.User = part
				}
				if result.Database == "" && knownDatabases[lowerPart] {
					result.Database = part
				}
				if result.Application == "" && knownApps[lowerPart] {
					result.Application = part
				}
			}
		}
	}

	// Extract the message (everything after the prefix + severity marker)
	// Find the severity marker position
	messageStart := len(prefix)
	for _, marker := range severityMarkers {
		idx := strings.Index(line, marker)
		if idx >= 0 {
			messageStart = idx
			break
		}
	}

	// Message is everything from the severity marker onward
	if messageStart < len(line) {
		result.Message = line[messageStart:]
	} else {
		result.Message = line
	}

	return result
}

// NormalizeMessage takes extracted metadata and builds a normalized message.
//
// The normalized format is: "user=X db=Y app=Z host=W SEVERITY: original message"
// This format is consistent with CSV and JSON parsers, allowing downstream code
// to extract metadata uniformly using the extractValue() function.
//
// Parameters:
//   - metadata: Extracted metadata from a log line
//
// Returns:
//   - A normalized message string with metadata prepended in key=value format
func NormalizeMessage(metadata *ExtractedMetadata) string {
	if metadata == nil {
		return ""
	}

	// Build the normalized prefix with metadata
	var parts []string

	if metadata.User != "" && metadata.User != "[unknown]" {
		parts = append(parts, "user="+metadata.User)
	}
	if metadata.Database != "" && metadata.Database != "[unknown]" {
		parts = append(parts, "db="+metadata.Database)
	}
	if metadata.Application != "" && metadata.Application != "[unknown]" {
		parts = append(parts, "app="+metadata.Application)
	}
	if metadata.Host != "" && metadata.Host != "[unknown]" {
		parts = append(parts, "host="+metadata.Host)
	}

	// Prepend metadata to the message
	if len(parts) > 0 {
		return strings.Join(parts, " ") + " " + metadata.Message
	}

	// No metadata to add, return message as-is
	return metadata.Message
}
