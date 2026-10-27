package parser

import (
	"bytes"
	"strings"
	"time"
)

// pgbackrestConsoleHeaderMatch reports whether line is a pgBackRest
// console or file header (timestamp + Pnn + LEVEL). The byte gate checks
// the "Pnn " marker right after the millisecond block, so a PostgreSQL
// line, whatever its time zone ("PST", "PDT"), is rejected before any
// allocation.
func pgbackrestConsoleHeaderMatch(line []byte) (pgbrHeader, bool) {
	if len(line) < 29 || line[19] != '.' || line[23] != ' ' || line[24] != 'P' ||
		!isDigitByte(line[25]) || !isDigitByte(line[26]) ||
		(line[27] != ' ' && (!isDigitByte(line[27]) || line[28] != ' ')) {
		return pgbrHeader{}, false
	}
	// A timestamp that does not parse leaves a digit where "Pnn" must be.
	return parsePgBackRestHeader(string(line))
}

// pgbackrestNoTimestampHeaderMatch reports whether line is a pgBackRest
// header written under log-timestamp=n ("P00   INFO: text").
func pgbackrestNoTimestampHeaderMatch(line []byte) (pgbrHeader, bool) {
	if len(line) < 6 || line[0] != 'P' || !isDigitByte(line[1]) || !isDigitByte(line[2]) ||
		(line[3] != ' ' && (!isDigitByte(line[3]) || line[4] != ' ')) {
		return pgbrHeader{}, false
	}
	return parsePgBackRestHeader(string(line))
}

// pgbackrestBareHeaderMatch reports whether line is a bare pgBackRest
// stderr header, "LEVEL: [NNN]: text", with no timestamp or process
// marker. Only the coded form is recognized: the codeless one ("WARN:
// text") could be ordinary PostgreSQL continuation text.
func pgbackrestBareHeaderMatch(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	var word string
	switch line[0] {
	case 'E':
		word = "ERROR"
	case 'W':
		word = "WARN"
	case 'I':
		word = "INFO"
	case 'D':
		word = "DETAIL"
	default:
		return false
	}
	lw := len(word)
	if len(line) < lw+9 || string(line[:lw]) != word {
		return false
	}
	rest := line[lw:]
	return rest[0] == ':' && rest[1] == ' ' && rest[2] == '[' &&
		isDigitByte(rest[3]) && isDigitByte(rest[4]) && isDigitByte(rest[5]) &&
		rest[6] == ']' && rest[7] == ':' && rest[8] == ' '
}

// isBannerLine reports whether line is pgBackRest's "PROCESS START"
// banner.
func isBannerLine(line []byte) bool {
	return len(line) > 0 && line[0] == '-' && string(bytes.TrimRight(line, "\r")) == pgBackRestBanner
}

// carveHooks lets pgbrCarveOutLine act on the caller's pending
// PostgreSQL entry. parseReader and parseFromBytes hold that entry
// differently; both provide the same operations.
type carveHooks struct {
	bs *BatchSender
	// parsePG splits a PostgreSQL entry into its timestamp and message.
	parsePG func([]byte) (time.Time, string)
	// emitPG sends the pending PostgreSQL entry, if any, and clears it.
	emitPG func()
	// detachPG returns the pending PostgreSQL entry's bytes, leaving no
	// entry pending.
	detachPG func() []byte
	// restorePG makes bytes the pending PostgreSQL entry again, so the
	// next lines continue it.
	restorePG func([]byte)
	// peekPG returns the pending PostgreSQL entry's bytes, nil for none.
	peekPG func() []byte
	// skipEmpty is true when the caller drops empty lines before they
	// reach an entry (parseFromBytes); parseReader appends them.
	skipEmpty bool
}

// Hold stages, see pgbrHold.
const (
	holdNone int8 = iota
	holdBlock
	holdFailure
)

// pgbrHold keeps undated pgBackRest-shaped lines aside while a dated
// PostgreSQL entry is pending: they may be its text (a logged statement's
// string, dollar-quoted body or comment). Unless confirmed, they go back
// to it as the stderr parser glues undated lines. A hold ends at the next
// dated line, or at the DETAIL after an archiver failure (see segmentCut).
type pgbrHold struct {
	stage int8
	// confirmed is set by evidence that the held lines are pgBackRest's.
	confirmed bool
	// pg is the PostgreSQL entry pending when the first held line came.
	pg []byte
	// raw holds the held lines as read, for the PostgreSQL view.
	raw [][]byte
	// entries holds the pgBackRest entries and markers carved meanwhile.
	entries []LogEntry
}

// pgbrCarveOut holds the pgBackRest entry being carved out of a
// PostgreSQL stream (header plus continuation lines seen so far) and the
// state that dates these lines. Shared by StderrParser.parseReader and
// parseFromBytes through pgbrCarveOutLine.
type pgbrCarveOut struct {
	active bool
	ts     time.Time
	pid    string
	entry  []byte
	// lastKnownTS is the most recent timestamp of the stream, from either
	// program, used to date a bare header that carries none.
	lastKnownTS time.Time
	// bare is true when the open entry came from a bare header: only such
	// an entry's ts may be corrected to the line that closes it.
	bare bool
	// inPgbr is true while the previous line belonged to pgBackRest, so a
	// blank line or a signal line that follows belongs to it too.
	inPgbr bool
	// blank counts the blank pgBackRest lines not yet reported.
	blank int
	// prefix is the parser's literal log_line_prefix, learned from the
	// first dated line it was stripped from; undated lines carry it too.
	prefix []byte
	hold   pgbrHold
}

// sendDated sends a parsed PostgreSQL entry when it has a timestamp,
// which becomes the latest one of the stream.
func (c *pgbrCarveOut) sendDated(bs *BatchSender, ts time.Time, msg string) {
	if ts.IsZero() {
		return
	}
	bs.Send(NewLogEntry(ts, msg, isContinuationMessage(msg)))
	c.lastKnownTS = ts
}

// sendPG sends a PostgreSQL entry built from bytes.
func (c *pgbrCarveOut) sendPG(h *carveHooks, entry []byte) {
	if len(entry) > 0 {
		ts, msg := h.parsePG(entry)
		c.sendDated(h.bs, ts, msg)
	}
}

// takePG detaches the pending PostgreSQL entry's bytes and records its
// timestamp as the latest one.
func (c *pgbrCarveOut) takePG(h *carveHooks) []byte {
	b := h.detachPG()
	if len(b) > 0 {
		if ts, _ := h.parsePG(b); !ts.IsZero() {
			c.lastKnownTS = ts
		}
	}
	return b
}

// send routes a pgBackRest entry or marker to the batch, or to the hold.
func (c *pgbrCarveOut) send(h *carveHooks, e LogEntry) {
	if c.hold.stage != holdNone {
		c.hold.entries = append(c.hold.entries, e)
		return
	}
	h.bs.Send(e)
}

// emit sends the open entry, if any, and clears it.
func (c *pgbrCarveOut) emit(h *carveHooks) {
	if !c.active {
		return
	}
	c.send(h, LogEntry{Timestamp: c.ts, Message: string(c.entry), PID: c.pid, FromPgBackRest: true})
	c.active = false
	c.entry = c.entry[:0]
}

// closeOut sends the open entry and the blank lines that followed it.
func (c *pgbrCarveOut) closeOut(h *carveHooks) {
	c.emit(h)
	if c.blank > 0 {
		c.send(h, LogEntry{FromPgBackRest: true, PgBackRest: &PgBackRestLineInfo{BlankLines: c.blank}})
		c.blank = 0
	}
}

// open starts a new pgBackRest entry.
func (c *pgbrCarveOut) open(ts time.Time, pid, msg string, bare bool) {
	c.active, c.ts, c.pid, c.bare = true, ts, pid, bare
	c.entry = append(c.entry[:0], msg...)
	c.inPgbr = true
}

// finish closes the stream: held lines are settled, then the pending
// PostgreSQL entry and the open pgBackRest entry are sent.
func (c *pgbrCarveOut) finish(h *carveHooks) {
	if c.hold.stage != holdNone {
		c.closeOut(h)
		// An archiver failure with no DETAIL names no other program.
		c.hold.confirmed = c.hold.confirmed || c.hold.stage == holdFailure
		c.settleHold(h)
	}
	h.emitPG()
	c.closeOut(h)
}

// settleHold ends a hold: in pgBackRest's favor when confirmed, else the
// held lines go back to the PostgreSQL entry they followed.
func (c *pgbrCarveOut) settleHold(h *carveHooks) {
	switch {
	case c.hold.confirmed:
		c.commitHold(h)
	case c.hold.stage == holdFailure:
		// The archiver's failure line is the pending entry now.
		c.sendPG(h, c.dropHold(h))
	default:
		h.restorePG(c.dropHold(h))
	}
}

// commitHold ends a hold in pgBackRest's favor: the PostgreSQL entry the
// held lines followed is sent, then the pgBackRest entries carved
// meanwhile.
func (c *pgbrCarveOut) commitHold(h *carveHooks) {
	hold := c.hold
	c.hold = pgbrHold{}
	c.sendPG(h, hold.pg)
	for _, e := range hold.entries {
		h.bs.Send(e)
	}
}

// dropHold ends a hold in PostgreSQL's favor and returns the entry the
// held lines followed, with these lines glued back as the stderr parser
// glues a line without a timestamp. What was carved meanwhile is dropped.
func (c *pgbrCarveOut) dropHold(h *carveHooks) []byte {
	hold := c.hold
	c.hold = pgbrHold{}
	c.active, c.entry, c.blank, c.inPgbr = false, c.entry[:0], 0, false
	b := hold.pg
	for _, line := range hold.raw {
		b = appendPGContinuation(b, line, h.skipEmpty)
	}
	return b
}

// startHold begins a hold at an undated pgBackRest line when a dated
// PostgreSQL entry is pending, the only entry such a line could still
// belong to. A bare header always starts one, for the archiver's DETAIL;
// with no dated entry pending, nothing could take it back but that
// DETAIL, so the hold starts confirmed.
func (c *pgbrCarveOut) startHold(h *carveHooks, raw []byte, bare bool) {
	c.closeOut(h)
	if c.hold.stage != holdNone {
		return
	}
	dated := hasTimestampBytes(c.unprefixed(h.peekPG()))
	if !bare && !dated {
		h.emitPG()
		return
	}
	c.hold = pgbrHold{stage: holdBlock, confirmed: !dated, pg: c.takePG(h)}
	c.hold.raw = append(c.hold.raw, bytes.Clone(raw))
}

// appendPGContinuation adds line to entry the way the stderr parser does
// for a line without a timestamp.
func appendPGContinuation(entry, line []byte, skipEmpty bool) []byte {
	if len(line) == 0 && skipEmpty {
		return entry
	}
	leading := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
	if len(entry) == 0 && !leading {
		return append(entry, line...)
	}
	if len(entry) > 0 {
		entry = append(entry, ' ')
	}
	return append(entry, trimSpaceBytes(line)...)
}

// PostgreSQL archiver's wording for a failed archive_command, shared with
// the analysis stage: the failure line, then its DETAIL naming the
// command.
const (
	ArchiveFailedExitPrefix    = "archive command failed with exit code "
	ArchiveFailedSignalPrefix  = "archive command was terminated by signal "
	FailedArchiveCommandPrefix = "The failed archive command was: "
)

// ArchiveCommandIsPgBackRest reports whether an archive_command runs
// pgBackRest, whatever the case of its name.
func ArchiveCommandIsPgBackRest(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "pgbackrest")
}

// isArchiveFailureLine reports whether a PostgreSQL line is the
// archiver's report of a failed archive_command.
func isArchiveFailureLine(line []byte) bool {
	return bytes.Contains(line, []byte(ArchiveFailedExitPrefix)) ||
		bytes.Contains(line, []byte(ArchiveFailedSignalPrefix))
}

// isUndatedPgbrLine reports whether line, not indented and without a
// timestamp, is one pgBackRest writes: a header, a banner, or a signal
// line right after pgBackRest.
func (c *pgbrCarveOut) isUndatedPgbrLine(line []byte) bool {
	return isUndatedPgbrShape(line) || (c.inPgbr && bytes.HasPrefix(line, []byte(pgBackRestSignalPrefix)))
}

// isUndatedPgbrShape reports whether line has the shape of an undated
// pgBackRest line whatever the state: a bare stderr header, a banner, or
// a header written under log-timestamp=n.
func isUndatedPgbrShape(line []byte) bool {
	if pgbackrestBareHeaderMatch(line) || isBannerLine(line) {
		return true
	}
	_, ok := pgbackrestNoTimestampHeaderMatch(line)
	return ok
}

// holdBlockLine handles a line while lines are held. It returns true
// when the line is PostgreSQL's, false when it goes on to
// pgbrCarveOutLine's ordinary rules.
func (c *pgbrCarveOut) holdBlockLine(h *carveHooks, raw, line, stripped []byte, leadingSpace bool) bool {
	blank := isBlankLine(line)
	if (leadingSpace && c.active) || (blank && c.inPgbr) || (!blank && !leadingSpace && c.isUndatedPgbrLine(line)) {
		c.hold.raw = append(c.hold.raw, bytes.Clone(raw))
		return false
	}
	if blank || leadingSpace || !hasTimestampBytes(stripped) {
		// A raw line that continues the PostgreSQL entry: the held lines
		// were its text too.
		h.restorePG(c.dropHold(h))
		return true
	}
	// pgBackRest writes its stderr output before PostgreSQL logs the
	// failure, so the next dated line dates the last bare entry.
	hdr, console := pgbackrestConsoleHeaderMatch(stripped)
	if c.active && c.bare {
		if console {
			c.ts = hdr.ts
		} else if ts, _, ok := parseStderrFormatFromBytes(stripped); ok {
			c.ts = ts
		}
	}
	c.closeOut(h)
	c.inPgbr = false
	if isArchiveFailureLine(stripped) {
		// Its DETAIL decides.
		c.hold.stage = holdFailure
		return false
	}
	c.hold.confirmed = c.hold.confirmed || console
	c.settleHold(h)
	return false
}

// holdFailureLine looks at a line while the archiver's failure line
// follows held lines: the first dated or pgBackRest-shaped line settles
// the hold. A DETAIL naming another program gives the lines back; one
// naming pgBackRest, or none (log_error_verbosity = terse), confirms them.
func (c *pgbrCarveOut) holdFailureLine(h *carveHooks, line, stripped []byte, leadingSpace bool) {
	if leadingSpace || isBlankLine(line) {
		return
	}
	if !hasTimestampBytes(stripped) && !c.isUndatedPgbrLine(line) {
		return
	}
	i := bytes.Index(stripped, []byte(FailedArchiveCommandPrefix))
	c.hold.confirmed = i < 0 || ArchiveCommandIsPgBackRest(string(stripped[i+len(FailedArchiveCommandPrefix):]))
	c.settleHold(h)
}

// isCommandBoundary reports whether a header message is pgBackRest's
// "<command> command begin" or "<command> command end:" line.
func isCommandBoundary(msg string) bool {
	_, rest, ok := strings.Cut(msg, ": ")
	if !ok {
		return false
	}
	cmd, rest, ok := strings.Cut(rest, " command ")
	if !ok || cmd == "" || strings.Trim(cmd, "abcdefghijklmnopqrstuvwxyz-:") != "" {
		return false
	}
	return strings.HasPrefix(rest, "begin ") || strings.HasPrefix(rest, "end: ")
}

// isBlankLine reports whether line holds only spaces, tabs or a CR.
func isBlankLine(line []byte) bool {
	for _, b := range line {
		if b != ' ' && b != '\t' && b != '\r' {
			return false
		}
	}
	return true
}

// unprefixed returns line without the literal log_line_prefix, when the
// line starts with it.
func (c *pgbrCarveOut) unprefixed(line []byte) []byte {
	if len(c.prefix) > 0 && bytes.HasPrefix(line, c.prefix) {
		return line[len(c.prefix):]
	}
	return line
}

// pgbrCarveOutLine decides whether a line of a PostgreSQL stream is
// pgBackRest's (header of any form, banner, signal line, continuation or
// blank line after one) and returns false when the caller must handle it
// as PostgreSQL. Each test starts with a byte comparison. strippedLine is
// rawLine without the parser's literal prefix, which undated lines lose
// too.
func pgbrCarveOutLine(c *pgbrCarveOut, h *carveHooks, rawLine, strippedLine []byte) bool {
	if len(strippedLine) < len(rawLine) && c.prefix == nil {
		c.prefix = bytes.Clone(rawLine[:len(rawLine)-len(strippedLine)])
	}
	lineBytes := c.unprefixed(rawLine)
	leadingSpace := len(lineBytes) > 0 && (lineBytes[0] == ' ' || lineBytes[0] == '\t')
	switch c.hold.stage {
	case holdBlock:
		if c.holdBlockLine(h, rawLine, lineBytes, strippedLine, leadingSpace) {
			return false
		}
	case holdFailure:
		c.holdFailureLine(h, lineBytes, strippedLine, leadingSpace)
	}
	if c.inPgbr && isBlankLine(lineBytes) {
		c.blank++
		return true
	}
	if leadingSpace {
		if !c.active {
			c.inPgbr = false
			return false
		}
		c.entry = append(c.entry, '\n')
		c.entry = append(c.entry, trimSpaceBytes(lineBytes)...)
		return true
	}
	if len(lineBytes) > 0 {
		switch lineBytes[0] {
		case '-':
			if isBannerLine(lineBytes) {
				c.startHold(h, rawLine, false)
				c.send(h, LogEntry{FromPgBackRest: true, PgBackRest: &PgBackRestLineInfo{ProcessStart: true}})
				c.inPgbr = true
				return true
			}
		case 't':
			if c.inPgbr && bytes.HasPrefix(lineBytes, []byte(pgBackRestSignalPrefix)) {
				c.closeOut(h)
				sig := strings.TrimSpace(string(lineBytes[len(pgBackRestSignalPrefix):]))
				c.send(h, LogEntry{FromPgBackRest: true, PgBackRest: &PgBackRestLineInfo{Signal: sig}})
				return true
			}
		case 'P':
			if hdr, ok := pgbackrestNoTimestampHeaderMatch(lineBytes); ok {
				c.startHold(h, rawLine, false)
				c.open(time.Time{}, hdr.pid, hdr.msg, false)
				if c.hold.stage != holdNone && isCommandBoundary(hdr.msg) {
					c.hold.confirmed = true
				}
				return true
			}
		}
	}
	if hdr, ok := pgbackrestConsoleHeaderMatch(strippedLine); ok {
		h.emitPG()
		if c.active && c.bare {
			// A bare entry is dated by the next dated line of the stream.
			c.ts = hdr.ts
		}
		c.closeOut(h)
		c.open(hdr.ts, hdr.pid, hdr.msg, false)
		c.lastKnownTS = hdr.ts
		return true
	}
	if pgbackrestBareHeaderMatch(lineBytes) {
		c.startHold(h, rawLine, true)
		c.open(c.lastKnownTS, "", string(lineBytes), true)
		return true
	}
	c.inPgbr = false
	c.closeOut(h)
	return false
}
