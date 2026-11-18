package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// PatroniParser reads Patroni's own log: the default text format, optionally
// behind a journalctl prefix, or its JSON format (log.type: json). Entries
// carry "LEVEL: message"; the timestamp has no zone and is kept as the wall
// clock Patroni wrote.
type PatroniParser struct{}

var (
	// The optional head is a journalctl prefix ("Sep 24 13:44:33 db1 patroni[812]: "):
	// variable-length, unlike the constant literal detectLeadingPrefix strips.
	patroniLineRe = regexp.MustCompile(
		`^(?:.*?: )?(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}),(\d{3}) (DEBUG|INFO|WARNING|ERROR|CRITICAL): `,
	)
	patroniJSONRe = regexp.MustCompile(`^\{.*"asctime": ?"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3}"`)
)

// looksLikePatroni requires most timestamped sample lines to have Patroni's
// shape: its comma-millisecond timestamp never appears in PostgreSQL logs.
// Lines behind a syslog prefix vote too. Untimestamped lines (pg_controldata
// output, tracebacks) do not.
func looksLikePatroni(sample string) bool {
	var shaped, records int
	for _, line := range strings.Split(sample, "\n") {
		switch {
		case patroniLineRe.MatchString(line), patroniJSONRe.MatchString(line):
			shaped++
			records++
		case recordStartRe.MatchString(line):
			records++
		}
	}
	return shaped >= 2 && shaped*2 > records
}

var recordStartRe = regexp.MustCompile(`^(?:\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}|[A-Z][a-z]{2} [ \d]\d \d{2}:\d{2}:\d{2})`)

func (p *PatroniParser) Parse(filename string, out chan<- []LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer f.Close()
	skipped, err := p.parseReader(WithProgress(f), out)
	warnSkippedPatroni(filename, skipped)
	return err
}

// warnSkippedPatroni says how many timestamped lines of a file read as a
// Patroni log were not Patroni's, such as PostgreSQL's in a shared stream.
func warnSkippedPatroni(filename string, skipped int) {
	if skipped == 0 {
		return
	}
	if filename == "" {
		slog.Warn("Lines that are not Patroni's are skipped in a Patroni log", "lines", skipped)
		return
	}
	slog.Warn("Lines that are not Patroni's are skipped in a Patroni log", "file", filename, "lines", skipped)
}

// parseReader emits one entry per timestamped line. Untimestamped lines
// (Python tracebacks) follow as continuations of the previous entry. It
// returns the count of other timestamped lines, which it skips.
func (p *PatroniParser) parseReader(r io.Reader, out chan<- []LogEntry) (int, error) {
	bs := NewBatchSender(out)
	defer bs.Flush()
	sc := bufio.NewScanner(skipBOM(r))
	sc.Buffer(make([]byte, 0, 256*1024), scannerBuffer)
	var seen bool
	var skipped int
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "{") {
			if e, ok := parsePatroniJSON(line); ok {
				seen = true
				bs.Send(e)
			}
			continue
		}
		m := patroniLineRe.FindStringSubmatchIndex(line)
		if m == nil {
			if recordStartRe.MatchString(line) {
				skipped++
				continue
			}
			if seen && strings.TrimSpace(line) != "" {
				bs.Send(LogEntry{Message: line, IsContinuation: true})
			}
			continue
		}
		ts, err := parseTime("2006-01-02 15:04:05.000", line[m[2]:m[3]]+"."+line[m[4]:m[5]])
		if err != nil {
			continue
		}
		seen = true
		bs.Send(LogEntry{Timestamp: ts, Message: line[m[6]:]})
	}
	return skipped, sc.Err()
}

func parsePatroniJSON(line string) (LogEntry, bool) {
	var j struct{ Asctime, Levelname, Message string }
	if json.Unmarshal([]byte(line), &j) != nil || j.Levelname == "" {
		return LogEntry{}, false
	}
	ts, err := parseTime("2006-01-02 15:04:05,000", j.Asctime)
	if err != nil {
		return LogEntry{}, false
	}
	return LogEntry{Timestamp: ts, Message: j.Levelname + ": " + j.Message}, true
}
