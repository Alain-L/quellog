// Package parser provides log file parsing for PostgreSQL and pgBouncer logs.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
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
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) (\S+) \[(\d+)\] (LOG|WARNING|ERROR|FATAL|DEBUG|NOISE) `,
)

// pgbouncerSignatureRe identifies pgBouncer content during format
// detection. The socket markers (C-0x / S-0x) and the lowercase
// "stats:" telemetry line are unique to pgBouncer and never appear in
// PostgreSQL server logs, so either one is a reliable discriminator
// when paired with the pgBouncer prefix shape.
var pgbouncerSignatureRe = regexp.MustCompile(
	`\[\d+\] (?:LOG|WARNING|ERROR|FATAL|DEBUG|NOISE) (?:stats:|[CS]-0x[0-9a-f]+:)`,
)

// pgbouncerLinesRead and pgbouncerLinesUnrecognized record what the
// pooler parser actually saw, so the report can state how much of the
// input it failed to decode instead of publishing "parsed == parsed".
// Package-level for the same reason as the progress counters in
// progress.go: ParseFile hands the caller entries, not statistics, and
// quellog runs a single parse pass per process. The cmd layer resets
// them before the pooler pass and reads them after.
var (
	pgbouncerLinesRead         atomic.Int64
	pgbouncerLinesUnrecognized atomic.Int64
)

// ResetPgBouncerLineCounts zeroes the pooler line counters.
func ResetPgBouncerLineCounts() {
	pgbouncerLinesRead.Store(0)
	pgbouncerLinesUnrecognized.Store(0)
}

// PgBouncerLineCounts returns the lines the pooler parser scanned and,
// among them, those it could not turn into an entry.
func PgBouncerLineCounts() (read, unrecognized int64) {
	return pgbouncerLinesRead.Load(), pgbouncerLinesUnrecognized.Load()
}

// pgbouncerShapeMinLines is the number of prefix-shaped lines the shape
// vote needs before it may decide on its own, so a stray line in a text
// file cannot carry a whole file into the pgBouncer track.
const pgbouncerShapeMinLines = 2

// pgbouncerShapeRatioNum / pgbouncerShapeRatioDen express the share of the
// sampled lines that must be prefix-shaped: 90%, the same consensus bar
// detectLeadingPrefix uses.
const (
	pgbouncerShapeRatioNum = 9
	pgbouncerShapeRatioDen = 10
)

// otherLogLineRe matches a line another log writes: one that opens on a
// timestamp (PostgreSQL stderr or CSV), a syslog header, a JSON object or
// an indent (the continuation of a multi-line statement or plan), or that
// carries PostgreSQL's severity and its colon ("LOG:  ") wherever a
// log_line_prefix puts it ("< %m >", a literal in front). A pooler line
// matches too, but is counted as one first.
var otherLogLineRe = regexp.MustCompile(`^(?:\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}|<\d+>|[A-Z][a-z]{2} [ \d]\d \d{2}:\d{2}|\{|[ \t])|\b(?:LOG|ERROR|WARNING|FATAL|PANIC|NOTICE|INFO|DEBUG[1-5]?|DETAIL|HINT|STATEMENT|CONTEXT):  `)

// looksLikePgBouncer reports whether a content sample is a pgBouncer log.
// A pgBouncer-only marker (stats line or socket id) settles it, unless the
// sample holds as many lines of another log as pooler lines: a PostgreSQL
// log a stray pooler line found its way into stays a PostgreSQL log, and
// its report is not lost to the pooler track. Free text (wrapped kernel
// messages) does not count against the pooler.
// Otherwise the sample must be made, near enough entirely, of lines in the
// pgBouncer prefix shape — the discriminator being that pgBouncer writes
// "[PID] LOG " where PostgreSQL always writes a severity followed by a
// colon ("LOG:  "), so a PostgreSQL log scores zero on this vote. Without
// the shape vote, an authentic capture holding no socket line inside the
// detection sample — a restart, a fresh rotation, a supervision snapshot,
// a verbose=2 head — was rejected outright.
func looksLikePgBouncer(sample string) bool {
	var shaped, total, other int
	var marker bool
	for len(sample) > 0 {
		line := sample
		if i := strings.IndexByte(sample, '\n'); i >= 0 {
			line, sample = sample[:i], sample[i+1:]
		} else {
			sample = ""
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		total++
		if !pgbouncerPrefixRe.MatchString(line) {
			if otherLogLineRe.MatchString(line) {
				other++
			}
			continue
		}
		shaped++
		// The marker counts only when the line carrying it is
		// itself a pooler line: a PostgreSQL statement that quotes one
		// -- log_statement = all, a pasted excerpt in a support bundle
		// -- would otherwise carry the whole file onto the pooler track.
		if pgbouncerSignatureRe.MatchString(line) {
			marker = true
		}
	}
	if marker {
		return shaped > other
	}
	return shaped >= pgbouncerShapeMinLines &&
		shaped*pgbouncerShapeRatioDen >= total*pgbouncerShapeRatioNum
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

	// Tallied locally and published once, so the loop stays free of
	// atomics; every file adds its own totals to the package counters.
	var read, unrecognized int64
	for scanner.Scan() {
		line := scanner.Text()
		read++
		m := pgbouncerPrefixRe.FindStringSubmatchIndex(line)
		if m == nil {
			unrecognized++
			continue
		}
		// m captures: [0:1] whole, [2:3] datetime, [4:5] tz, [6:7] PID,
		// [8:9] severity.
		ts, ok := parsePgBouncerTimestamp(line[m[2]:m[3]], line[m[4]:m[5]])
		if !ok {
			unrecognized++
			continue
		}
		// Message starts at the severity word so the analyzer keeps the
		// severity without re-scanning the prefix.
		msg := line[m[8]:]
		bs.Send(LogEntry{Timestamp: ts, Message: msg, PID: line[m[6]:m[7]]})
	}
	pgbouncerLinesRead.Add(read)
	pgbouncerLinesUnrecognized.Add(unrecognized)
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
