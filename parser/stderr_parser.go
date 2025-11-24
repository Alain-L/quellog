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
			// Try to parse as a new log entry - if it fails, it's a continuation
			timestamp, _ := parseStderrLine(line)
			if timestamp.IsZero() {
				// No valid timestamp = continuation line
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
				out <- LogEntry{Timestamp: timestamp, Message: message}
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
		out <- LogEntry{Timestamp: timestamp, Message: message}
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

	// Attempt 4: Parse syslog format (Mon DD HH:MM:SS)
	if timestamp, message, ok := parseSyslogFormat(line); ok {
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
	// "Jan _2 15:04:05" = 15 characters
	if n < 15 ||
		line[3] != ' ' || // Space after month abbreviation
		line[6] != ' ' || // Space after day
		line[9] != ':' || line[12] != ':' { // Time separators
		return time.Time{}, "", false
	}

	// Extract syslog timestamp (first 15 chars)
	syslogTimestamp := line[:15]

	// Add current year and parse
	currentYear := time.Now().Year()
	timestampStr := fmt.Sprintf("%04d %s", currentYear, syslogTimestamp)

	t, err := time.Parse("2006 Jan _2 15:04:05", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	// Skip whitespace before message
	i := 15
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
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
