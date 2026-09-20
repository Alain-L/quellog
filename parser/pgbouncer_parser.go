// Package parser provides log file parsing for PostgreSQL and pgBouncer logs.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// PgBouncerParser parses pgBouncer connection-pooler logs. Unlike
// PostgreSQL logs, pgBouncer emits strictly single-line records with a
// fixed prefix "YYYY-MM-DD HH:MM:SS.fff TZ [PID] SEVERITY ", so there
// are no DETAIL/HINT continuation lines to stitch. Each emitted
// LogEntry carries the text from the severity word onward as its
// Message (e.g. "LOG stats: ...", "WARNING C-0x..: pooler error: ..."),
// which PgBouncerAnalyzer then classifies.
type PgBouncerParser struct{}

// pgbouncerPrefixRe matches the canonical pgBouncer log prefix and
// captures the severity word. pgBouncer uses a space (not a colon)
// after the bracketed PID, and the severity is not followed by a colon
// — both differ from PostgreSQL's "[PID]: LOG:" / "[PID] LOG:" shapes.
var pgbouncerPrefixRe = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) (\S+) \[\d+\] (LOG|WARNING|ERROR|FATAL|DEBUG|NOISE) `,
)

// pgbouncerSignatureRe identifies pgBouncer content during format
// detection. The socket markers (C-0x / S-0x) and the lowercase
// "stats:" telemetry line are unique to pgBouncer and never appear in
// PostgreSQL server logs, so either one is a reliable discriminator
// when paired with the pgBouncer prefix shape.
var pgbouncerSignatureRe = regexp.MustCompile(
	`\[\d+\] (?:LOG|WARNING|ERROR|FATAL|DEBUG|NOISE) (?:stats:|[CS]-0x[0-9a-f]+:)`,
)

// looksLikePgBouncer reports whether a content sample is a pgBouncer
// log. It requires both the pgBouncer prefix shape and a pgBouncer-only
// marker (stats line or socket id) so ordinary PostgreSQL logs — which
// share the timestamp/PID shape — are never misclassified.
func looksLikePgBouncer(sample string) bool {
	return pgbouncerSignatureRe.MatchString(sample)
}

// Parse reads a pgBouncer log file and streams batches of LogEntry
// through out. The caller owns the channel lifecycle. Lines that do not
// match the pgBouncer prefix (blank lines, wrapped kernel messages) are
// skipped rather than failing the parse.
func (p *PgBouncerParser) Parse(filename string, out chan<- []LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	if err := p.parseReader(WithProgress(file), out); err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	return nil
}

// parseReader runs the pgBouncer parsing logic against any io.Reader, so
// the compressed (gzip/zstd) and archive (tar/zip/7z) routers can feed it
// a decompressed stream exactly as they do for the other formats. Like
// every other parseReader path it strips a leading UTF-8 BOM, which would
// otherwise push the first record off the prefix regex.
func (p *PgBouncerParser) parseReader(r io.Reader, out chan<- []LogEntry) error {
	bs := NewBatchSender(out)
	defer bs.Flush()

	scanner := bufio.NewScanner(skipBOM(r))
	scanner.Buffer(make([]byte, 0, 256*1024), scannerBuffer)

	for scanner.Scan() {
		line := scanner.Text()
		m := pgbouncerPrefixRe.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		// m captures: [0:1] whole, [2:3] datetime, [4:5] tz, [6:7] severity.
		ts, ok := parsePgBouncerTimestamp(line[m[2]:m[3]], line[m[4]:m[5]])
		if !ok {
			continue
		}
		// Message starts at the severity word so the analyzer keeps the
		// severity without re-scanning the prefix.
		msg := line[m[6]:]
		bs.Send(LogEntry{Timestamp: ts, Message: msg})
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading pgBouncer log: %w", err)
	}
	return nil
}

// parsePgBouncerTimestamp decodes the "YYYY-MM-DD HH:MM:SS[.fff]" field
// plus its separate zone token into a normalized time. It reuses the
// canonical fast-path decoder by re-joining the two fields with a single
// space, which is exactly the layout fastParsePGTimestamp expects.
func parsePgBouncerTimestamp(datetime, zone string) (time.Time, bool) {
	joined := datetime + " " + zone
	tzStart := len(datetime) + 1
	if t, ok := fastParsePGTimestamp(joined, tzStart, len(joined)); ok {
		return t, true
	}
	// Slow fallback keeps parity with exotic zone names the fast path
	// declines (numeric offsets, localized abbreviations).
	layout := "2006-01-02 15:04:05 MST"
	if strings.IndexByte(datetime, '.') != -1 {
		layout = "2006-01-02 15:04:05.999999 MST"
	}
	t, err := parseTime(layout, joined)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
