package parser

import (
	"strings"
	"time"
)

// parseRDSFormat attempts to parse the AWS RDS PostgreSQL log format:
// "YYYY-MM-DD HH:MM:SS TZ:host(port):user@db:[pid]:severity: message..."
func parseRDSFormat(line string) (time.Time, string, bool) {
	n := len(line)

	// Quick positional validation
	if n < 40 ||
		line[4] != '-' || line[7] != '-' ||
		line[10] != ' ' ||
		line[13] != ':' || line[16] != ':' {
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

	for i < n && line[i] != ':' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	if i >= n || line[i] != ':' {
		return time.Time{}, "", false
	}

	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	i++ // Skip the colon after timezone

	hostStart := i
	for i < n && line[i] != ':' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	hostAndPort := line[hostStart:i]
	i++ // Skip colon

	userDbStart := i
	for i < n && line[i] != ':' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	userDb := line[userDbStart:i]
	i++ // Skip colon

	if i < n && line[i] == '[' {
		for i < n && line[i] != ']' {
			i++
		}
		if i < n {
			i++
		}
		if i < n && line[i] == ':' {
			i++
		}
	}

	message := ""
	if i < n {
		message = line[i:]
	}

	host := hostAndPort
	if idx := strings.Index(hostAndPort, "("); idx != -1 {
		host = hostAndPort[:idx]
	}

	user := ""
	database := ""
	if idx := strings.Index(userDb, "@"); idx != -1 {
		user = userDb[:idx]
		database = userDb[idx+1:]
	}

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
func parseAzureFormat(line string) (time.Time, string, bool) {
	n := len(line)

	if n < 30 ||
		line[4] != '-' || line[7] != '-' ||
		line[10] != ' ' ||
		line[13] != ':' || line[16] != ':' {
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

	for i < n && line[i] != '-' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	tzEnd := i

	if i >= n || line[i] != '-' {
		return time.Time{}, "", false
	}

	timestampStr := line[:tzEnd]
	t, err := time.Parse("2006-01-02 15:04:05 MST", timestampStr)
	if err != nil {
		return time.Time{}, "", false
	}

	i++ // Skip the dash after timezone

	for i < n && line[i] != '-' {
		i++
	}
	if i >= n {
		return time.Time{}, "", false
	}
	i++

	message := ""
	if i < n {
		message = line[i:]
	}

	return t, message, true
}
