package parser

import (
	"path/filepath"
	"strings"
	"time"
)

// pgBackRestBanner is the literal pgBackRest writes to a log file before
// the first line of every OS process that opens it (never to console or
// stderr).
const pgBackRestBanner = "-------------------PROCESS START-------------------"

// pgBackRestSignalPrefix starts the bare line pgBackRest's signal handler
// writes when a signal stops the process ("terminated on signal
// SIGTERM"): no timestamp, no process id, no level.
const pgBackRestSignalPrefix = "terminated on signal "

// pgbackrestLevelNames are the level strings pgBackRest's log.c can emit
// as a line header, right-justified to width 6 (OFF never appears as a
// header): the one catalog the header decoder and analysis
// (IsPgBackRestLevel) both derive from.
var pgbackrestLevelNames = []string{"ERROR", "WARN", "INFO", "DETAIL", "DEBUG", "TRACE", "ASSERT"}

var pgbackrestLevels = func() map[string]bool {
	m := make(map[string]bool, len(pgbackrestLevelNames))
	for _, l := range pgbackrestLevelNames {
		m[l] = true
	}
	return m
}()

// IsPgBackRestLevel reports whether level is one of pgBackRest's own
// line-header levels.
func IsPgBackRestLevel(level string) bool {
	return pgbackrestLevels[level]
}

// pgbackrestShapeMinLines and the ratio below guard the format vote: the
// sample must hold a few pgBackRest-shaped lines and be made almost
// entirely of them.
const pgbackrestShapeMinLines = 2

const (
	pgbackrestShapeRatioNum = 9
	pgbackrestShapeRatioDen = 10
)

// looksLikePgBackRest reports whether a content sample is made of
// pgBackRest's own log lines. Detection asks only after the PostgreSQL
// patterns failed, so that such an input is accepted: the stderr parser
// then carves every line out as pgBackRest's. A banner line settles it
// outright. Lines that are a header (with or without a timestamp) or a
// space-indented continuation count as "shaped"; the sample must be almost
// entirely shaped lines.
func looksLikePgBackRest(sample string) bool {
	var shaped, total int
	for len(sample) > 0 {
		line := sample
		if i := strings.IndexByte(sample, '\n'); i >= 0 {
			line, sample = sample[:i], sample[i+1:]
		} else {
			sample = ""
		}
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line == pgBackRestBanner {
			return true
		}
		total++
		if _, ok := parsePgBackRestHeader(line); ok {
			shaped++
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && shaped > 0 {
			// A continuation line only counts after a header: an
			// arbitrary indented text file would otherwise score 100%.
			shaped++
		}
	}
	return shaped >= pgbackrestShapeMinLines &&
		shaped*pgbackrestShapeRatioDen >= total*pgbackrestShapeRatioNum
}

// pgbrFileCommands are the command parts of pgBackRest's log file names
// ("{stanza}-{command}[-{role}-{NNN}].log").
var pgbrFileCommands = []string{
	"archive-push-async", "archive-get-async", "archive-push", "archive-get",
	"stanza-create", "stanza-upgrade", "stanza-delete",
	"backup", "expire", "restore", "check", "verify", "info", "start", "stop",
}

// StanzaHintFromFilename returns the stanza part of a pgBackRest log file
// name, matched by its known command suffix so a stanza holding a "-"
// survives ("db-prod-backup.log" gives "db-prod"). Rotation and
// compression suffixes are ignored. Returns "" for a name with no known
// command suffix, or for the "all" prefix pgBackRest uses without a
// stanza. Only a fallback: the stanza named in the content wins.
func StanzaHintFromFilename(filename string) string {
	base := filepath.Base(filename)
	if i := strings.LastIndex(base, ".log"); i > 0 {
		base = base[:i]
	}
	base = trimProcessSuffix(base)
	for _, cmd := range pgbrFileCommands {
		if len(base) > len(cmd)+1 && strings.HasSuffix(base, "-"+cmd) {
			if stanza := base[:len(base)-len(cmd)-1]; stanza != "all" {
				return stanza
			}
			return ""
		}
	}
	return ""
}

// trimProcessSuffix drops a "-local-NNN" or "-remote-NNN" worker suffix.
func trimProcessSuffix(base string) string {
	i := strings.LastIndexByte(base, '-')
	if i < 0 || i == len(base)-1 {
		return base
	}
	for _, c := range []byte(base[i+1:]) {
		if !isDigitByte(c) {
			return base
		}
	}
	rest := base[:i]
	for _, role := range []string{"-local", "-remote"} {
		if strings.HasSuffix(rest, role) {
			return rest[:len(rest)-len(role)]
		}
	}
	return base
}

// pgbrHeader is the decoded form of one pgBackRest header line.
type pgbrHeader struct {
	ts  time.Time // zero when log-timestamp=n (do not invent one)
	pid string    // process marker, "P00"
	// msg is the line from the level word on ("ERROR: [103]: text"),
	// the Message of the entry it starts; analysis decodes the code.
	msg string
}

// parsePgBackRestHeader decodes one physical line as a pgBackRest header
// (timestamp + Pnn + level, or just Pnn + level under log-timestamp=n),
// with byte comparisons only.
func parsePgBackRestHeader(line string) (pgbrHeader, bool) {
	var h pgbrHeader
	i := 0
	n := len(line)
	if n >= 24 && isDigitByte(line[0]) && line[4] == '-' && line[7] == '-' &&
		line[10] == ' ' && line[13] == ':' && line[16] == ':' && line[19] == '.' && line[23] == ' ' {
		if ts, err := time.Parse("2006-01-02 15:04:05.000", line[:23]); err == nil {
			h.ts = ts
			i = 24
		}
	}
	if i >= n || line[i] != 'P' {
		return h, false
	}
	pidStart := i
	i++
	for i < n && isDigitByte(line[i]) {
		i++
	}
	if d := i - pidStart - 1; d < 2 || d > 3 {
		return h, false
	}
	h.pid = processMarker(line[pidStart:i])
	spaces := 0
	for i < n && line[i] == ' ' {
		i++
		spaces++
	}
	if spaces == 0 {
		return h, false
	}
	levelStart := i
	for i < n && line[i] >= 'A' && line[i] <= 'Z' {
		i++
	}
	if !pgbackrestLevels[line[levelStart:i]] || i >= n || line[i] != ':' {
		return h, false
	}
	h.msg = line[levelStart:]
	return h, true
}

// processMarker returns pid ("P00", "P01", …) without allocating for the
// main process's marker.
func processMarker(pid string) string {
	if pid == "P00" {
		return "P00"
	}
	return strings.Clone(pid)
}

func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}
