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
)

// continuationPrefixes are the PostgreSQL secondary message types that follow
// a primary log entry (LOG, ERROR, etc.). These lines have their own timestamp
// and log_line_prefix in stderr format, but semantically belong to the previous
// entry and should not be counted as separate log entries.
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
	if isSyslogContinuationLine(message) {
		return true
	}

	// Check against known continuation prefixes anywhere in the message
	for _, prefix := range continuationPrefixes {
		idx := strings.Index(message, prefix)
		if idx == -1 {
			continue
		}

		if idx == 0 {
			return true
		}

		if message[idx-1] == ' ' {
			return true
		}
	}
	return false
}

// StderrParser parses PostgreSQL logs in stderr/syslog format.
type StderrParser struct {
	prefixStructure *PrefixStructure // Detected prefix structure (nil if not detected or disabled)
}

// Parse reads a PostgreSQL stderr/syslog format log file and streams parsed entries.
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
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	var entryBuilder strings.Builder
	entryBuilder.Grow(512)

	for scanner.Scan() {
		line := scanner.Text()

		if idx := strings.Index(line, syslogTabMarker); idx != -1 {
			line = " " + line[idx+len(syslogTabMarker):]
		}

		isContinuation := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")

		if !isContinuation && entryBuilder.Len() > 0 {
			if !hasTimestampString(line) {
				isContinuation = true
			}
		}

		if isContinuation {
			entryBuilder.WriteByte(' ')
			entryBuilder.WriteString(strings.TrimSpace(line))
		} else {
			if entryBuilder.Len() > 0 {
				currentEntry := entryBuilder.String()
				normalizedEntry := p.normalizeEntryBeforeParsing(currentEntry)
				timestamp, message := parseStderrLine(normalizedEntry)
				out <- LogEntry{
					Timestamp:      timestamp,
					Message:        message,
					IsContinuation: isContinuationMessage(message),
				}
				entryBuilder.Reset()
			}
			entryBuilder.WriteString(line)
		}
	}

	if entryBuilder.Len() > 0 {
		currentEntry := entryBuilder.String()
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

// hasTimestampString checks if a line starts with a recognizable timestamp pattern.
func hasTimestampString(line string) bool {
	n := len(line)
	if n < 15 {
		return false
	}
	// Check stderr format: YYYY-MM-DD
	if n >= 19 && line[4] == '-' && line[7] == '-' && line[10] == ' ' {
		return true
	}
	// Check syslog BSD format: "Mon DD HH:MM:SS"
	if line[3] == ' ' && line[6] == ' ' && line[9] == ':' {
		return true
	}
	// Check RFC5424 format: "<pri>..."
	if line[0] == '<' {
		return true
	}
	return false
}

// parseFromBytes parses stderr log data directly from a byte slice.
func (p *StderrParser) parseFromBytes(data []byte, out chan<- LogEntry) error {
	var currentEntry []byte
	currentEntry = make([]byte, 0, 8192)

	dataLen := len(data)
	lineStart := 0

	for i := 0; i <= dataLen; i++ {
		if i < dataLen && data[i] != '\n' {
			continue
		}

		lineEnd := i
		if lineEnd > lineStart && data[lineEnd-1] == '\r' {
			lineEnd--
		}

		lineBytes := data[lineStart:lineEnd]
		lineStart = i + 1

		if len(lineBytes) == 0 {
			continue
		}

		if idx := bytesIndex(lineBytes, []byte(syslogTabMarker)); idx != -1 {
			newLine := make([]byte, 1+len(lineBytes)-idx-len(syslogTabMarker))
			newLine[0] = ' '
			copy(newLine[1:], lineBytes[idx+len(syslogTabMarker):])
			lineBytes = newLine
		}

		isContinuation := len(lineBytes) > 0 && (lineBytes[0] == ' ' || lineBytes[0] == '\t')

		if !isContinuation && len(currentEntry) > 0 {
			if !hasTimestampBytes(lineBytes) {
				isContinuation = true
			}
		}

		if isContinuation {
			if len(currentEntry) > 0 {
				currentEntry = append(currentEntry, ' ')
			}
			currentEntry = append(currentEntry, trimSpaceBytes(lineBytes)...)
		} else {
			if len(currentEntry) > 0 {
				timestamp, message := p.parseEntryFromBytes(currentEntry)
				out <- LogEntry{
					Timestamp:      timestamp,
					Message:        message,
					IsContinuation: isContinuationMessage(message),
				}
				currentEntry = currentEntry[:0]
			}
			currentEntry = append(currentEntry[:0], lineBytes...)
		}
	}

	if len(currentEntry) > 0 {
		timestamp, message := p.parseEntryFromBytes(currentEntry)
		out <- LogEntry{
			Timestamp:      timestamp,
			Message:        message,
			IsContinuation: isContinuationMessage(message),
		}
	}

	return nil
}

func (p *StderrParser) parseEntryFromBytes(entry []byte) (time.Time, string) {
	n := len(entry)
	if n >= 20 && entry[4] == '-' && entry[7] == '-' && entry[10] == ' ' && entry[13] == ':' && entry[16] == ':' {
		if timestamp, msgOffset, ok := parseStderrFormatFromBytes(entry); ok {
			if msgOffset >= n {
				return timestamp, ""
			}
			return timestamp, string(entry[msgOffset:])
		}
	}

	entryStr := string(entry)
	normalizedEntry := p.normalizeEntryBeforeParsing(entryStr)
	return parseStderrLine(normalizedEntry)
}

func hasTimestampBytes(line []byte) bool {
	n := len(line)
	if n < 15 {
		return false
	}
	if n >= 19 && line[4] == '-' && line[7] == '-' && line[10] == ' ' {
		return true
	}
	if line[3] == ' ' && line[6] == ' ' && line[9] == ':' {
		return true
	}
	if line[0] == '<' {
		return true
	}
	return false
}

func trimSpaceBytes(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

func bytesIndex(s, sep []byte) int {
	n := len(sep)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-n; i++ {
		if s[i] == sep[0] {
			match := true
			for j := 1; j < n; j++ {
				if s[i+j] != sep[j] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
	}
	return -1
}

func parseStderrLine(line string) (time.Time, string) {
	if timestamp, message, ok := parseStderrFormat(line); ok {
		return timestamp, message
	}
	if timestamp, message, ok := parseRDSFormat(line); ok {
		return timestamp, message
	}
	if timestamp, message, ok := parseAzureFormat(line); ok {
		return timestamp, message
	}
	if timestamp, message, ok := parseSyslogFormat(line); ok {
		return timestamp, message
	}
	if timestamp, message, ok := parseSyslogFormatISO(line); ok {
		return timestamp, message
	}
	if timestamp, message, ok := parseSyslogFormatRFC5424(line); ok {
		return timestamp, message
	}
	return time.Time{}, line
}

func parseStderrFormat(line string) (time.Time, string, bool) {
	n := len(line)
	if n < 20 || line[4] != '-' || line[7] != '-' || line[10] != ' ' || line[13] != ':' || line[16] != ':' {
		return time.Time{}, "", false
	}

	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, "", false
		}
		spaceAfterTime = i
	}

	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	tzStart := i
	for i < n && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i
	if tzEnd <= tzStart {
		return time.Time{}, "", false
	}

	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05.999 MST", timestampStr)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05 MST", timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	message := ""
	if i < n {
		message = line[i:]
	}
	return t, message, true
}

func parseStderrFormatFromBytes(line []byte) (time.Time, int, bool) {
	n := len(line)
	if n < 20 || line[4] != '-' || line[7] != '-' || line[10] != ' ' || line[13] != ':' || line[16] != ':' {
		return time.Time{}, 0, false
	}

	spaceAfterTime := 19
	if line[spaceAfterTime] != ' ' {
		i := 19
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if i >= n {
			return time.Time{}, 0, false
		}
		spaceAfterTime = i
	}

	i := spaceAfterTime + 1
	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	tzStart := i
	for i < n && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i
	if tzEnd <= tzStart {
		return time.Time{}, 0, false
	}

	timestampStr := string(line[:tzEnd])
	t, err := time.Parse("2006-01-02 15:04:05.999 MST", timestampStr)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05 MST", timestampStr)
		if err != nil {
			return time.Time{}, 0, false
		}
	}

	for i < n && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return t, i, true
}

func (p *StderrParser) detectPrefixStructure(f *os.File) {
	const sampleSize = 50
	scanner := bufio.NewScanner(f)
	buf := make([]byte, scannerBuffer)
	scanner.Buffer(buf, scannerMaxBuffer)

	var lines []string
	for scanner.Scan() && len(lines) < sampleSize {
		line := scanner.Text()
		if idx := strings.Index(line, syslogTabMarker); idx != -1 {
			line = " " + line[idx+len(syslogTabMarker):]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) >= 10 {
		p.prefixStructure = AnalyzePrefixes(lines, sampleSize)
	}
}

func (p *StderrParser) normalizeEntryBeforeParsing(line string) string {
	if p.prefixStructure == nil {
		return line
	}
	if strings.Contains(line, ":") && len(line) > 30 {
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 && strings.Contains(parts[2], "@") {
			return line
		}
	}
	if len(line) > 25 && line[19] == ' ' {
		for i := 20; i < len(line) && i < 30; i++ {
			if line[i] == '-' && i+10 < len(line) {
				sessionPart := line[i+1 : i+10]
				if strings.ContainsAny(sessionPart, "0123456789abcdefABCDEF") {
					return line
				}
			}
		}
	}

	metadata := ExtractMetadataFromLine(line, p.prefixStructure)
	if metadata == nil || (metadata.User == "" && metadata.Database == "" && metadata.Application == "" && metadata.Host == "") {
		return line
	}

	prefixStart := strings.Index(line, metadata.Prefix)
	if prefixStart == -1 {
		return line
	}

	timestampPart := line[:prefixStart]
	var normalized strings.Builder
	normalized.WriteString(timestampPart)

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
	normalized.WriteString(metadata.Message)
	return normalized.String()
}