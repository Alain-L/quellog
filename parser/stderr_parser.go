// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Buffer size constants for scanner
const (
	// scannerBuffer is the initial buffer size for reading log lines (4 MB)
	scannerBuffer = 4 * 1024 * 1024

	// scannerMaxBuffer is the maximum buffer size for very long log lines (100 MB)
	scannerMaxBuffer = 100 * 1024 * 1024

	// syslogTabMarker is the marker used in syslog format for tab characters
	syslogTabMarker = "#011"
)

// continuationPrefixes are the PostgreSQL secondary message types that follow
// a primary log entry (LOG, ERROR, etc.). These lines have their own timestamp
// and log_line_prefix in stderr format, but semantically belong to the previous
// entry and should not be counted as separate log entries.
//
// Reference: PostgreSQL documentation and src/backend/utils/error/elog.c
var continuationPrefixes = []string{
	"DETAIL:",
	"HINT:",
	"CONTEXT:",
	"STATEMENT:",
	"QUERY:",
	"LOCATION:",
}

// preCollectorMessages are emitted before the logging collector starts.
// In syslog mode, these appear but don't exist in stderr logs.
// We skip them to maintain parity between formats.
var preCollectorMessages = []string{
	"redirecting log output to logging collector process",
}

// isContinuationMessage checks if a log message is a continuation of a previous entry.
// These are secondary messages like DETAIL, HINT, STATEMENT, CONTEXT, QUERY that
// PostgreSQL emits with their own log_line_prefix but belong to the previous entry.
//
// The message may have prefixes like "[pid] SQLSTATE: db=X,user=Y DETAIL: ..."
// so we need to search for the continuation keywords, not just check the start.
//
// For syslog format with %l in log_line_prefix, we also detect [X-N] patterns
// where N>1 indicates a continuation line (e.g., [10-2] is line 2 of statement 10).
//
// Also skips pre-collector messages that only appear in syslog (not stderr).
func isContinuationMessage(message string) bool {
	if len(message) < 5 {
		return false
	}

	// Check for pre-collector messages (syslog-only, skip for parity)
	for _, skip := range preCollectorMessages {
		if strings.Contains(message, skip) {
			return true
		}
	}

	// Check for syslog line number pattern: [X-N] where N > 1
	// This handles multi-line SQL statements in syslog format where each line
	// gets its own syslog timestamp but belongs to the same PostgreSQL log entry.
	// Pattern examples: [10-2], [10-3], [1-2], [123-45]
	if isSyslogContinuationLine(message) {
		return true
	}

	// Check against known continuation prefixes anywhere in the message
	// We look for " DETAIL:" or "DETAIL:" at specific positions to avoid
	// false positives from messages that contain these words
	for _, prefix := range continuationPrefixes {
		// Check if it appears after typical PostgreSQL prefix patterns
		// Pattern: "... DETAIL: " or starts with "DETAIL:"
		idx := strings.Index(message, prefix)
		if idx == -1 {
			continue
		}

		// If at start, it's a continuation
		if idx == 0 {
			return true
		}

		// If preceded by space, it's likely the log level position
		// This handles: "[pid] SQLSTATE: db=X,user=Y DETAIL: ..."
		if message[idx-1] == ' ' {
			// Make sure it's not part of a larger word (check what comes before space)
			// Valid patterns: ", DETAIL:" or "app=X DETAIL:" or "] DETAIL:"
			return true
		}
	}
	return false
}

// isSyslogContinuationLine detects syslog entries with [X-N] where N > 1
// that contain SQL continuations (not PostgreSQL log continuations like HINT).
//
// PostgreSQL's %l in log_line_prefix produces [statement_id-line_number].
// Line 1 is the primary entry; lines 2+ are continuations.
//
// However, there are two types of continuations:
// 1. SQL continuations: "[10-2] \t id SERIAL..." - should be concatenated
// 2. Log continuations: "[1-2] 2025-11-30... HINT:..." - separate log entry
//
// This function returns true ONLY for SQL continuations (type 1).
func isSyslogContinuationLine(message string) bool {
	// Find first '[' - it may be preceded by syslog prefix or PG timestamp
	start := strings.IndexByte(message, '[')
	if start == -1 || start+4 >= len(message) {
		return false
	}

	// Look for pattern [digits-digits] within reasonable range
	for i := start; i < len(message) && i < start+200; i++ {
		if message[i] != '[' {
			continue
		}

		// Parse [X-Y] pattern
		j := i + 1
		// Skip first number (statement id)
		for j < len(message) && message[j] >= '0' && message[j] <= '9' {
			j++
		}
		if j == i+1 || j >= len(message) || message[j] != '-' {
			continue
		}

		// Parse line number after '-'
		k := j + 1
		lineNum := 0
		for k < len(message) && message[k] >= '0' && message[k] <= '9' {
			lineNum = lineNum*10 + int(message[k]-'0')
			k++
		}

		// Must end with ']' and line number > 1 means continuation
		if k < len(message) && message[k] == ']' && lineNum > 1 {
			// Check content after ']' - if it starts with a PostgreSQL timestamp,
			// this is a log continuation (HINT/DETAIL), not SQL continuation
			content := message[k+1:]
			// Skip leading whitespace
			for len(content) > 0 && (content[0] == ' ' || content[0] == '\t') {
				content = content[1:]
			}
			// Check for PostgreSQL timestamp pattern (YYYY-MM-DD)
			if len(content) >= 10 &&
				content[0] >= '1' && content[0] <= '2' && // Year starts with 1 or 2
				content[4] == '-' && content[7] == '-' {
				// This is a PostgreSQL log continuation with full metadata
				// NOT a SQL continuation - treat as new entry
				return false
			}
			return true
		}
	}
	return false
}

// extractSyslogPID extracts the PostgreSQL backend PID from a syslog message.
// Supports multiple formats:
//   - BSD syslog: "172.20.0.2 postgres[160]: [10-1] ..."
//   - ISO/RFC5424 extracted: "[10-1] 2025-11-30 21:10:20.100 UTC [55] 00000: ..."
//   - RFC5424 header: "<134>1 timestamp host postgres 55 - - [1-1] ..."
//
// Returns the PID as a string, or empty string if not found.
func extractSyslogPID(message string) string {
	// Format 1: Find "postgres[" pattern (BSD syslog and ISO)
	idx := strings.Index(message, "postgres[")
	if idx != -1 {
		// Extract PID from postgres[PID]
		start := idx + 9 // len("postgres[")
		end := start
		for end < len(message) && message[end] >= '0' && message[end] <= '9' {
			end++
		}
		if end > start && end < len(message) && message[end] == ']' {
			return message[start:end]
		}
	}

	// Format 2: RFC5424 header format "postgres PID -" (PID as separate field)
	// Example: "<134>1 2025-11-30T21:10:20+00:00 host postgres 55 - - [1-1] ..."
	pgIdx := strings.Index(message, " postgres ")
	if pgIdx != -1 {
		start := pgIdx + 10 // len(" postgres ")
		end := start
		for end < len(message) && message[end] >= '0' && message[end] <= '9' {
			end++
		}
		if end > start && end < len(message) && message[end] == ' ' {
			return message[start:end]
		}
	}

	// Format 3: Find "UTC [PID]" or "timezone [PID]" pattern (ISO/RFC5424 extracted message)
	// Pattern: "[X-Y] timestamp UTC [PID] SQLSTATE: ..."
	utcIdx := strings.Index(message, " UTC [")
	if utcIdx != -1 {
		start := utcIdx + 6 // len(" UTC [")
		end := start
		for end < len(message) && message[end] >= '0' && message[end] <= '9' {
			end++
		}
		if end > start && end < len(message) && message[end] == ']' {
			return message[start:end]
		}
	}

	// Format 4: Try pattern "[digits]:" anywhere
	for i := 0; i < len(message)-3; i++ {
		if message[i] == '[' && message[i+1] >= '0' && message[i+1] <= '9' {
			// Found potential PID bracket
			j := i + 1
			for j < len(message) && message[j] >= '0' && message[j] <= '9' {
				j++
			}
			if j < len(message) && message[j] == ']' && j+1 < len(message) && message[j+1] == ':' {
				return message[i+1 : j]
			}
		}
	}

	return ""
}

// extractSyslogContinuationContent extracts the SQL content from a syslog continuation line.
// It returns the content after the [X-N] pattern, which is the actual SQL fragment.
//
// Example input message: "000 172.20.0.2 postgres[108]: [10-2]     CREATE TABLE users ("
// Returns: "CREATE TABLE users ("
func extractSyslogContinuationContent(message string) string {
	// Find the [X-N] pattern and return content after it
	start := strings.IndexByte(message, '[')
	if start == -1 || start+4 >= len(message) {
		return strings.TrimSpace(message)
	}

	// Look for pattern [digits-digits] within reasonable range
	for i := start; i < len(message) && i < start+200; i++ {
		if message[i] != '[' {
			continue
		}

		// Parse [X-Y] pattern
		j := i + 1
		// Skip first number (statement id)
		for j < len(message) && message[j] >= '0' && message[j] <= '9' {
			j++
		}
		if j == i+1 || j >= len(message) || message[j] != '-' {
			continue
		}

		// Parse line number after '-'
		k := j + 1
		lineNum := 0
		for k < len(message) && message[k] >= '0' && message[k] <= '9' {
			lineNum = lineNum*10 + int(message[k]-'0')
			k++
		}

		// Found [X-N] pattern - return content after ']'
		if k < len(message) && message[k] == ']' && lineNum > 1 {
			content := strings.TrimSpace(message[k+1:])
			// Check if content starts with a PostgreSQL timestamp (YYYY-MM-DD)
			// If so, this is a full log continuation (HINT/DETAIL/CONTEXT), not SQL
			if len(content) >= 10 &&
				content[0] >= '1' && content[0] <= '2' && // Year starts with 1 or 2
				content[4] == '-' && content[7] == '-' {
				// This is a PostgreSQL log continuation with full metadata
				// Return empty to skip appending (it will be handled as a new entry)
				return ""
			}
			return content
		}
	}

	return strings.TrimSpace(message)
}

// StderrParser parses PostgreSQL logs in stderr/syslog format.
// It handles multi-line log entries and supports both standard stderr format
// (YYYY-MM-DD HH:MM:SS TZ) and syslog format (Mon DD HH:MM:SS).
//
// The parser can automatically detect log_line_prefix structure and normalize
// messages to extract metadata (user, database, application) into a standard format.
type StderrParser struct {
	prefixStructure *PrefixStructure // Detected prefix structure (nil if not detected or disabled)
}

// Parse reads a PostgreSQL stderr/syslog format log file and streams parsed entries.
// Multi-line entries (continuation lines starting with whitespace) are automatically
// assembled into single LogEntry records.
//
// The parser handles:
//   - Standard stderr format: "2006-01-02 15:04:05 MST message..."
//   - Syslog format: "Jan _2 15:04:05 message..."
//   - Multi-line log entries (DETAIL, HINT, STATEMENT, etc.)
//   - Syslog tab markers (#011)
//   - Automatic prefix structure detection and message normalization
func (p *StderrParser) Parse(filename string, out chan<- LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	// Try to detect prefix structure from a sample of lines
	p.detectPrefixStructure(file)

	// Reset file to beginning for actual parsing
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek to start: %w", err)
	}

	return p.parseReader(file, out)
}

// parseReader runs the stderr parsing logic against any io.Reader.
func (p *StderrParser) parseReader(r io.Reader, out chan<- LogEntry) error {
	scanner := bufio.NewScanner(r)
	// Configure scanner with large buffer to handle long log lines
	// (e.g., STATEMENT lines with large queries)
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	// Accumulate multi-line entries
	var currentEntry string

	for scanner.Scan() {
		line := scanner.Text()

		// Handle syslog tab markers: "#011" represents a tab character
		// Replace it with a space for consistency
		if idx := strings.Index(line, syslogTabMarker); idx != -1 {
			line = " " + line[idx+len(syslogTabMarker):]
		}

		// Check if this is a continuation line
		// Fast path: starts with whitespace (most continuation lines)
		isContinuation := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")

		// Fallback: if not indented AND we have a current entry, check for timestamp
		// This handles cases like GCP where SQL continuation lines are not indented
		if !isContinuation && currentEntry != "" {
			// Fast path: quick timestamp format check before expensive parsing
			// Most logs start with YYYY-MM-DD (stderr) or Mon DD (syslog) or <pri> (RFC5424)
			n := len(line)
			hasTimestamp := false
			if n >= 19 {
				// Check stderr format: YYYY-MM-DD (line[4]=='-' && line[7]=='-')
				if line[4] == '-' && line[7] == '-' && line[10] == ' ' {
					hasTimestamp = true
				} else if n >= 15 && line[3] == ' ' && line[6] == ' ' && line[9] == ':' {
					// Check syslog BSD format: "Mon DD HH:MM:SS"
					hasTimestamp = true
				} else if line[0] == '<' {
					// Check RFC5424 format: "<pri>..."
					hasTimestamp = true
				}
			}
			if !hasTimestamp {
				// No recognizable timestamp pattern = continuation line
				isContinuation = true
			}
		}

		if isContinuation {
			// Append to current entry
			currentEntry += " " + strings.TrimSpace(line)
		} else {
			// This is a new entry, so process the previous one
			if currentEntry != "" {
				// Try to normalize with detected structure (if not RDS/Azure format)
				normalizedEntry := p.normalizeEntryBeforeParsing(currentEntry)
				timestamp, message := parseStderrLine(normalizedEntry)
				out <- LogEntry{
					Timestamp:      timestamp,
					Message:        message,
					IsContinuation: isContinuationMessage(message),
				}
			}
			// Start accumulating new entry
			currentEntry = line
		}
	}

	// Process the last accumulated entry
	if currentEntry != "" {
		// Try to normalize with detected structure (if not RDS/Azure format)
		normalizedEntry := p.normalizeEntryBeforeParsing(currentEntry)
		timestamp, message := parseStderrLine(normalizedEntry)
		out <- LogEntry{
			Timestamp:      timestamp,
			Message:        message,
			IsContinuation: isContinuationMessage(message),
		}
	}

	return scanner.Err()
}

// parseStderrLine extracts the timestamp and message from a log line.
// It attempts to parse four formats:
//  1. Stderr format: "2006-01-02 15:04:05 TZ message..."
//  2. RDS format: "2006-01-02 15:04:05 TZ:host(port):user@db:[pid]:severity: message..."
//  3. Azure format: "2006-01-02 15:04:05 TZ-session_id-severity: message..."
//  4. Syslog format: "Jan _2 15:04:05 message..." (current year is assumed)
//
// If parsing fails, returns zero time and the original line as message.
//
// This function uses positional checks for performance, avoiding regex
// and string splitting when possible.
func parseStderrLine(line string) (time.Time, string) {
	n := len(line)

	// Need at least 20 characters for a valid timestamp
	if n < 20 {
		return time.Time{}, line
	}

	// Attempt 1: Parse stderr format (YYYY-MM-DD HH:MM:SS TZ)
	if timestamp, message, ok := parseStderrFormat(line); ok {
		return timestamp, message
	}

	// Attempt 2: Parse RDS format (YYYY-MM-DD HH:MM:SS TZ:...)
	if timestamp, message, ok := parseRDSFormat(line); ok {
		return timestamp, message
	}

	// Attempt 3: Parse Azure format (YYYY-MM-DD HH:MM:SS TZ-...)
	if timestamp, message, ok := parseAzureFormat(line); ok {
		return timestamp, message
	}

	// Attempt 4: Parse syslog BSD format (Mon DD HH:MM:SS)
	if timestamp, message, ok := parseSyslogFormat(line); ok {
		return timestamp, message
	}

	// Attempt 5: Parse syslog ISO format (YYYY-MM-DDTHH:MM:SS+00:00 host ...)
	if timestamp, message, ok := parseSyslogFormatISO(line); ok {
		return timestamp, message
	}

	// Attempt 6: Parse syslog RFC5424 format (<pri>ver timestamp host ...)
	if timestamp, message, ok := parseSyslogFormatRFC5424(line); ok {
		return timestamp, message
	}

	// Unable to parse timestamp, return line as-is
	return time.Time{}, line
}

// parseStderrFormat attempts to parse the standard stderr format:
// "YYYY-MM-DD HH:MM:SS TZ message..."
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after timestamp
//   - ok: true if parsing succeeded
func parseStderrFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation: check for date/time separators
	if n < 20 ||
		line[4] != '-' || line[7] != '-' || // Date separators
		line[10] != ' ' || // Space between date and time
		line[13] != ':' || line[16] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Find the timezone field
	// Format: "YYYY-MM-DD HH:MM:SS TZ"
	//          0123456789012345678901...
	// Expected space after seconds (HH:MM:SS) at position 19

	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		// Handle cases with no space or multiple spaces
		// Scan forward to find next space
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, "", false
		}
		spaceAfterTime = i
	}

	// Skip whitespace to find timezone token
	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	tzStart := i

	// Find end of timezone token
	for i < n && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	if tzEnd <= tzStart {
		return time.Time{}, "", false
	}

	// Parse timestamp: "YYYY-MM-DD HH:MM:SS TZ" or "YYYY-MM-DD HH:MM:SS.mmm TZ"
	timestampStr := line[:tzEnd]

	// Try with milliseconds first (e.g., GCP Cloud SQL format)
	t, err := time.Parse("2006-01-02 15:04:05.999 MST", timestampStr)
	if err != nil {
		// Try without milliseconds
		t, err = time.Parse("2006-01-02 15:04:05 MST", timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	// Skip whitespace before message
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}

// parseStderrFormatFromBytes is the zero-copy version of parseStderrFormat.
// Returns the message offset instead of copying the message string.
func parseStderrFormatFromBytes(line []byte) (time.Time, int, bool) {
	n := len(line)

	// Quick positional validation: check for date/time separators
	if n < 20 ||
		line[4] != '-' || line[7] != '-' || // Date separators
		line[10] != ' ' || // Space between date and time
		line[13] != ':' || line[16] != ':' { // Time separators
		return time.Time{}, 0, false
	}

	// Find the timezone field
	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		// Scan forward to find next space
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, 0, false
		}
		spaceAfterTime = i
	}

	// Skip whitespace to find timezone token
	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	tzStart := i

	// Find end of timezone token
	for i < n && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	if tzEnd <= tzStart {
		return time.Time{}, 0, false
	}

	// Parse timestamp - must convert to string for time.Parse
	timestampStr := string(line[:tzEnd])

	// Try with milliseconds first
	t, err := time.Parse("2006-01-02 15:04:05.999 MST", timestampStr)
	if err != nil {
		// Try without milliseconds
		t, err = time.Parse("2006-01-02 15:04:05 MST", timestampStr)
		if err != nil {
			return time.Time{}, 0, false
		}
	}

	// Skip whitespace before message
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	// Return offset instead of copying message
	return t, i, true
}

// parseRDSFormat attempts to parse the AWS RDS PostgreSQL log format:
// "YYYY-MM-DD HH:MM:SS TZ:host(port):user@db:[pid]:severity: message..."
//
// RDS uses a fixed log_line_prefix format: %t:%r:%u@%d:[%p]:
// Where:
//   - %t = timestamp with timezone
//   - %r = remote host and port
//   - %u = user name
//   - %d = database name
//   - %p = process ID
//
// The function extracts metadata (host, user, database) and injects them into
// the message in a format compatible with our entity tracking (host=, user=, db=).
//
// Returns:
//   - timestamp: parsed time
//   - message: enriched message with metadata
//   - ok: true if parsing succeeded
func parseRDSFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation: check for date/time separators
	if n < 40 || // Need more characters for RDS format
		line[4] != '-' || line[7] != '-' || // Date separators
		line[10] != ' ' || // Space between date and time
		line[13] != ':' || line[16] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Find the timezone field and check for RDS marker (colon after timezone)
	// Format: "YYYY-MM-DD HH:MM:SS TZ:..."
	//          0123456789012345678901...
	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		// Scan forward to find space after seconds
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, "", false
		}
		spaceAfterTime = i
	}

	// Skip whitespace to find timezone token
	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	// Find end of timezone token (should be followed by colon for RDS format)
	for i < n && line[i] != ':' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	// Check for RDS marker: colon after timezone
	if i >= n || line[i] != ':' {
		return time.Time{}, "", false // Not RDS format
	}

	// Parse timestamp
	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	// Now parse RDS-specific fields: :host(port):user@db:[pid]:message
	i++ // Skip the colon after timezone

	// Extract host(port)
	hostStart := i
	for i < n && line[i] != ':' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	hostAndPort := line[hostStart:i]
	i++ // Skip colon

	// Extract user@database
	userDbStart := i
	for i < n && line[i] != ':' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	userDb := line[userDbStart:i]
	i++ // Skip colon

	// Extract [pid] - skip it, we don't use it
	if i < n && line[i] == '[' {
		for i < n && line[i] != ']' {
			i++
		}
		if i < n {
			i++ // Skip closing bracket
		}
		if i < n && line[i] == ':' {
			i++ // Skip colon after pid
		}
	}

	// Rest is the message
	message := ""
	if i < n {
		message = line[i:]
	}

	// Extract host (without port)
	host := hostAndPort
	if idx := strings.Index(hostAndPort, "("); idx != -1 {
		host = hostAndPort[:idx]
	}

	// Extract user and database
	user := ""
	database := ""
	if idx := strings.Index(userDb, "@"); idx != -1 {
		user = userDb[:idx]
		database = userDb[idx+1:]
	}

	// Enrich message with metadata in format compatible with entity tracking
	// Insert at the beginning so they're always captured
	enrichedMessage := message
	if host != "" && host != "[unknown]" {
		enrichedMessage = "host=" + host + " " + enrichedMessage
	}
	if user != "" && user != "[unknown]" {
		enrichedMessage = "user=" + user + " " + enrichedMessage
	}
	if database != "" && database != "[unknown]" {
		enrichedMessage = "db=" + database + " " + enrichedMessage
	}

	return t, enrichedMessage, true
}

// parseAzureFormat attempts to parse the Azure Database for PostgreSQL log format:
// "YYYY-MM-DD HH:MM:SS TZ-session_id-severity: message..."
//
// Azure uses the default log_line_prefix format: %t-%c-
// Where:
//   - %t = timestamp with timezone
//   - %c = session ID (hexadecimal)
//
// The session ID is skipped as it's not useful for analysis, but we still
// need to parse past it to get to the actual log message.
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after timestamp and session ID
//   - ok: true if parsing succeeded
func parseAzureFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation: check for date/time separators
	if n < 30 || // Need more characters for Azure format
		line[4] != '-' || line[7] != '-' || // Date separators
		line[10] != ' ' || // Space between date and time
		line[13] != ':' || line[16] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Find the timezone field and check for Azure marker (dash after timezone)
	// Format: "YYYY-MM-DD HH:MM:SS TZ-..."
	//          0123456789012345678901...
	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		// Scan forward to find space after seconds
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, "", false
		}
		spaceAfterTime = i
	}

	// Skip whitespace to find timezone token
	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	// Find end of timezone token (should be followed by dash for Azure format)
	for i < n && line[i] != '-' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	// Check for Azure marker: dash after timezone
	if i >= n || line[i] != '-' {
		return time.Time{}, "", false // Not Azure format
	}

	// Parse timestamp
	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	// Now skip Azure-specific fields: -session_id-
	i++ // Skip the dash after timezone

	// Skip session ID (until next dash)
	for i < n && line[i] != '-' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	i++ // Skip the dash after session ID

	// Rest is the message
	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}

// parseSyslogFormat attempts to parse the syslog format:
// "Mon DD HH:MM:SS message..."
//
// Since syslog format doesn't include year, the current year is assumed.
//
// Returns:
//   - timestamp: parsed time with current year
//   - message: remaining text after timestamp
//   - ok: true if parsing succeeded
func parseSyslogFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation for syslog format
	// "Jan _2 15:04:05" = 15 characters (minimum)
	// With milliseconds: "Jan _2 15:04:05.999" = 19 characters
	if n < 15 ||
		line[3] != ' ' || // Space after month abbreviation
		line[6] != ' ' || // Space after day
		line[9] != ':' || line[12] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Check for fractional seconds (position 15 should be '.' if present)
	// Supports variable precision: .XXX (milliseconds) to .XXXXXXXXX (nanoseconds)
	hasFrac := n > 15 && line[15] == '.'
	timestampLen := 15
	if hasFrac {
		// Scan forward to find the end of fractional digits
		timestampLen = 16 // Start after '.'
		for timestampLen < n && line[timestampLen] >= '0' && line[timestampLen] <= '9' {
			timestampLen++
		}
		// Minimum: at least one digit after the dot
		if timestampLen <= 16 {
			return time.Time{}, "", false
		}
	}

	// Extract syslog timestamp
	syslogTimestamp := line[:timestampLen]

	// Add current year and parse
	currentYear := time.Now().Year()
	timestampStr := fmt.Sprintf("%04d %s", currentYear, syslogTimestamp)

	var t time.Time
	var err error
	if hasFrac {
		// .999999999 handles any precision from 1-9 digits
		t, err = time.Parse("2006 Jan _2 15:04:05.999999999", timestampStr)
	} else {
		t, err = time.Parse("2006 Jan _2 15:04:05", timestampStr)
	}
	if err != nil {
		return time.Time{}, "", false
	}

	// Skip whitespace before message
	i := timestampLen
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}

// parseSyslogFormatISO parses a syslog line with ISO 8601 timestamp format.
// Format: "2025-11-30T21:10:20+00:00 host process[pid]: message"
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after syslog header (host, process info)
//   - ok: true if parsing succeeded
func parseSyslogFormatISO(line string) (time.Time, string, bool) {
	n := len(line)

	// Minimum: "2025-11-30T21:10:20+00:00 h p[1]: m" = ~35 chars
	if n < 25 {
		return time.Time{}, "", false
	}

	// Validate ISO timestamp structure: YYYY-MM-DDTHH:MM:SS
	if line[4] != '-' || line[7] != '-' || line[10] != 'T' || line[13] != ':' || line[16] != ':' {
		return time.Time{}, "", false
	}

	// Find end of timestamp (after timezone offset or Z)
	// Formats: +00:00, -05:00, Z, or with fractional seconds .123456+00:00
	timestampEnd := 19 // After "YYYY-MM-DDTHH:MM:SS"

	// Check for fractional seconds
	if timestampEnd < n && line[timestampEnd] == '.' {
		timestampEnd++
		for timestampEnd < n && line[timestampEnd] >= '0' && line[timestampEnd] <= '9' {
			timestampEnd++
		}
	}

	// Check for timezone: Z or +/-HH:MM
	if timestampEnd < n {
		if line[timestampEnd] == 'Z' {
			timestampEnd++
		} else if line[timestampEnd] == '+' || line[timestampEnd] == '-' {
			// +00:00 or -05:00
			if timestampEnd+6 <= n {
				timestampEnd += 6
			}
		}
	}

	// Parse timestamp
	timestampStr := line[:timestampEnd]
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		// Try without fractional seconds
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	// Skip to message: find "]: " or "]: [" pattern after hostname and process
	// Format: "2025-11-30T21:10:20+00:00 172.20.0.2 postgres[55]: [1-1] message"
	rest := line[timestampEnd:]
	colonBracket := strings.Index(rest, "]: ")
	if colonBracket == -1 {
		return time.Time{}, "", false
	}

	message := rest[colonBracket+3:]
	return t, message, true
}

// parseSyslogFormatRFC5424 parses a syslog line with RFC 5424 format.
// Format: "<priority>version timestamp host app procid msgid structured-data msg"
// Example: "<134>1 2025-11-30T21:10:20+00:00 172.20.0.2 postgres 55 - - [1-1] message"
//
// Returns:
//   - timestamp: parsed time
//   - message: remaining text after RFC5424 header
//   - ok: true if parsing succeeded
func parseSyslogFormatRFC5424(line string) (time.Time, string, bool) {
	n := len(line)

	// Minimum length check
	if n < 30 || line[0] != '<' {
		return time.Time{}, "", false
	}

	// Find end of priority: "<NNN>"
	priorityEnd := strings.IndexByte(line, '>')
	if priorityEnd == -1 || priorityEnd > 5 {
		return time.Time{}, "", false
	}

	// Skip version number and space: "1 "
	pos := priorityEnd + 1
	if pos >= n || line[pos] < '0' || line[pos] > '9' {
		return time.Time{}, "", false
	}
	pos++ // Skip version
	if pos >= n || line[pos] != ' ' {
		return time.Time{}, "", false
	}
	pos++ // Skip space

	// Parse ISO timestamp starting at pos
	if pos+19 > n {
		return time.Time{}, "", false
	}

	// Find end of timestamp
	timestampStart := pos
	timestampEnd := pos + 19 // After "YYYY-MM-DDTHH:MM:SS"

	// Check for fractional seconds
	if timestampEnd < n && line[timestampEnd] == '.' {
		timestampEnd++
		for timestampEnd < n && line[timestampEnd] >= '0' && line[timestampEnd] <= '9' {
			timestampEnd++
		}
	}

	// Check for timezone: Z or +/-HH:MM
	if timestampEnd < n {
		if line[timestampEnd] == 'Z' {
			timestampEnd++
		} else if line[timestampEnd] == '+' || line[timestampEnd] == '-' {
			if timestampEnd+6 <= n {
				timestampEnd += 6
			}
		}
	}

	// Parse timestamp
	timestampStr := line[timestampStart:timestampEnd]
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	// RFC5424 format after timestamp: "host app procid msgid structured-data msg"
	// We need to find the PostgreSQL message which starts with "[X-Y]"
	// Skip: hostname, app, procid, msgid, structured-data (all space-separated, "-" for nil)
	rest := line[timestampEnd:]

	// Find the PostgreSQL log pattern: " [digit" which starts the [X-Y] pattern
	bracketIdx := strings.Index(rest, " [")
	if bracketIdx == -1 {
		return time.Time{}, "", false
	}

	// Verify it's the PostgreSQL pattern [X-Y] not structured-data
	checkPos := bracketIdx + 2
	if checkPos < len(rest) && rest[checkPos] >= '0' && rest[checkPos] <= '9' {
		message := rest[bracketIdx+1:]
		return t, message, true
	}

	return time.Time{}, "", false
}

// detectPrefixStructure reads a sample of lines from the file and attempts
// to detect the log_line_prefix structure using AnalyzePrefixes.
//
// This is called once at the start of parsing to enable automatic metadata
// extraction and message normalization.
func (p *StderrParser) detectPrefixStructure(f *os.File) {
	const sampleSize = 50 // Read 50 lines for structure detection

	scanner := bufio.NewScanner(f)
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	var lines []string
	for scanner.Scan() && len(lines) < sampleSize {
		line := scanner.Text()

		// Handle syslog tab markers
		if idx := strings.Index(line, syslogTabMarker); idx != -1 {
			line = " " + line[idx+len(syslogTabMarker):]
		}

		// Skip empty lines and continuation lines
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}

		lines = append(lines, line)
	}

	// Try to detect prefix structure
	if len(lines) >= 10 { // Need at least 10 lines for reliable detection
		p.prefixStructure = AnalyzePrefixes(lines, sampleSize)
	}
}

// normalizeEntryBeforeParsing attempts to normalize a log entry using the detected
// prefix structure. It only applies normalization for standard stderr format;
// RDS and Azure formats are skipped since they have their own normalization logic.
//
// The function extracts metadata (user, database, application, host) from the prefix
// and reconstructs the line with normalized metadata in the format:
// "TIMESTAMP user=X db=Y app=Z SEVERITY: message"
func (p *StderrParser) normalizeEntryBeforeParsing(line string) string {
	// Skip normalization if no structure detected
	if p.prefixStructure == nil {
		return line
	}

	// Skip normalization for RDS format (already normalized by parseRDSFormat)
	// RDS format pattern: "TIMESTAMP TZ:host:user@db:[pid]:"
	if strings.Contains(line, ":") && len(line) > 30 {
		// Quick check: if we see ":host(...):user@db:[" pattern, it's likely RDS
		// Let parseRDSFormat handle it
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 {
			// Check if format matches "...TZ:host:user@db"
			if strings.Contains(parts[2], "@") {
				return line
			}
		}
	}

	// Skip normalization for Azure format (has its own handling)
	// Azure format pattern: "TIMESTAMP TZ-session-"
	if len(line) > 25 && line[19] == ' ' {
		// Check position after potential timezone
		for i := 20; i < len(line) && i < 30; i++ {
			if line[i] == '-' {
				// Look for hex session ID pattern after dash
				if i+10 < len(line) {
					sessionPart := line[i+1 : i+10]
					// Simple heuristic: if we see hex characters after dash, likely Azure
					if strings.ContainsAny(sessionPart, "0123456789abcdefABCDEF") {
						return line
					}
				}
			}
		}
	}

	// Try to extract and normalize metadata
	metadata := ExtractMetadataFromLine(line, p.prefixStructure)
	if metadata == nil {
		return line
	}

	// If no metadata was extracted, return original line
	if metadata.User == "" && metadata.Database == "" && metadata.Application == "" && metadata.Host == "" {
		return line
	}

	// Extract timestamp part (everything before the metadata/prefix)
	// We need to preserve the timestamp when reconstructing the line
	prefixStart := strings.Index(line, metadata.Prefix)
	if prefixStart == -1 {
		return line
	}

	timestampPart := line[:prefixStart]

	// Build normalized line: "TIMESTAMP user=X db=Y app=Z SEVERITY: message"
	var normalized strings.Builder
	normalized.WriteString(timestampPart)

	// Add normalized metadata
	if metadata.User != "" && metadata.User != "[unknown]" {
		normalized.WriteString("user=")
		normalized.WriteString(metadata.User)
		normalized.WriteString(" ")
	}
	if metadata.Database != "" && metadata.Database != "[unknown]" {
		normalized.WriteString("db=")
		normalized.WriteString(metadata.Database)
		normalized.WriteString(" ")
	}
	if metadata.Application != "" && metadata.Application != "[unknown]" {
		normalized.WriteString("app=")
		normalized.WriteString(metadata.Application)
		normalized.WriteString(" ")
	}
	if metadata.Host != "" && metadata.Host != "[unknown]" {
		normalized.WriteString("host=")
		normalized.WriteString(metadata.Host)
		normalized.WriteString(" ")
	}

	// Add the message (which includes SEVERITY: ...)
	normalized.WriteString(metadata.Message)

	return normalized.String()
}
