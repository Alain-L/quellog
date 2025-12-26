package parser

import (
	"fmt"
	"strings"
	"time"
)

// syslogTabMarker is the marker used in syslog format for tab characters
const syslogTabMarker = "#011"

// isSyslogContinuationLine detects syslog entries with [X-N] where N > 1
// that contain SQL continuations (not PostgreSQL log continuations like HINT).
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

		// Parse line number after "-"
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

		// Parse line number after "-"
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
			if len(content) >= 10 &&
				content[0] >= '1' && content[0] <= '2' &&
				content[4] == '-' && content[7] == '-' {
				return ""
			}
			return content
		}
	}

	return strings.TrimSpace(message)
}

// parseSyslogFormat attempts to parse the syslog BSD format: "Mon DD HH:MM:SS"
func parseSyslogFormat(line string) (time.Time, string, bool) {
	n := len(line)
	if n < 15 ||
		line[3] != ' ' ||
		line[6] != ' ' ||
		line[9] != ':' || line[12] != ':' {
		return time.Time{}, "", false
	}

	hasFrac := n > 15 && line[15] == '.'
	timestampLen := 15
	if hasFrac {
		timestampLen = 16
		for timestampLen < n && line[timestampLen] >= '0' && line[timestampLen] <= '9' {
			timestampLen++
		}
		if timestampLen <= 16 {
			return time.Time{}, "", false
		}
	}

	syslogTimestamp := line[:timestampLen]
	currentYear := time.Now().Year()
	timestampStr := fmt.Sprintf("%04d %s", currentYear, syslogTimestamp)

	var t time.Time
	var err error
	if hasFrac {
		t, err = time.Parse("2006 Jan _2 15:04:05.999999999", timestampStr)
	} else {
		t, err = time.Parse("2006 Jan _2 15:04:05", timestampStr)
	}
	if err != nil {
		return time.Time{}, "", false
	}

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
func parseSyslogFormatISO(line string) (time.Time, string, bool) {
	n := len(line)
	if n < 25 {
		return time.Time{}, "", false
	}

	if line[4] != '-' || line[7] != '-' || line[10] != 'T' || line[13] != ':' || line[16] != ':' {
		return time.Time{}, "", false
	}

	timestampEnd := 19
	if timestampEnd < n && line[timestampEnd] == '.' {
		timestampEnd++
		for timestampEnd < n && line[timestampEnd] >= '0' && line[timestampEnd] <= '9' {
			timestampEnd++
		}
	}

	if timestampEnd < n {
		if line[timestampEnd] == 'Z' {
			timestampEnd++
		} else if line[timestampEnd] == '+' || line[timestampEnd] == '-' {
			if timestampEnd+6 <= n {
				timestampEnd += 6
			}
		}
	}

	timestampStr := line[:timestampEnd]
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	rest := line[timestampEnd:]
	colonBracket := strings.Index(rest, "]: ")
	if colonBracket == -1 {
		return time.Time{}, "", false
	}

	message := rest[colonBracket+3:]
	return t, message, true
}

// parseSyslogFormatRFC5424 parses a syslog line with RFC 5424 format.
func parseSyslogFormatRFC5424(line string) (time.Time, string, bool) {
	n := len(line)
	if n < 30 || line[0] != '<' {
		return time.Time{}, "", false
	}

	priorityEnd := strings.IndexByte(line, '>')
	if priorityEnd == -1 || priorityEnd > 5 {
		return time.Time{}, "", false
	}

	pos := priorityEnd + 1
	if pos >= n || line[pos] < '0' || line[pos] > '9' {
		return time.Time{}, "", false
	}
	pos++
	if pos >= n || line[pos] != ' ' {
		return time.Time{}, "", false
	}
	pos++

	if pos+19 > n {
		return time.Time{}, "", false
	}

	timestampStart := pos
	timestampEnd := pos + 19

	if timestampEnd < n && line[timestampEnd] == '.' {
		timestampEnd++
		for timestampEnd < n && line[timestampEnd] >= '0' && line[timestampEnd] <= '9' {
			timestampEnd++
		}
	}

	if timestampEnd < n {
		if line[timestampEnd] == 'Z' {
			timestampEnd++
		} else if line[timestampEnd] == '+' || line[timestampEnd] == '-' {
			if timestampEnd+6 <= n {
				timestampEnd += 6
			}
		}
	}

	timestampStr := line[timestampStart:timestampEnd]
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Time{}, "", false
		}
	}

	rest := line[timestampEnd:]
	bracketIdx := strings.Index(rest, " [")
	if bracketIdx == -1 {
		return time.Time{}, "", false
	}

	checkPos := bracketIdx + 2
	if checkPos < len(rest) && rest[checkPos] >= '0' && rest[checkPos] <= '9' {
		message := rest[bracketIdx+1:]
		return t, message, true
	}

	return time.Time{}, "", false
}
