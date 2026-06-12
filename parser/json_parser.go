// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/tidwall/gjson"
)

// unsafeString casts a []byte to a string without copying. Used to feed
// gjson.Parse without paying the 1× input copy that gjson.ParseBytes
// performs (its implementation does string(json) up front). Safe here
// because every string we extract gets copied into msgBuf via append
// or into a fresh time.Time before the underlying scanner buffer is
// reused on the next Scan() call. Do not retain any gjson.Result
// across the scan boundary.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// errSkipEntry is returned when an entry should be silently skipped (e.g., CNPG operator logs)
var errSkipEntry = errors.New("skip entry")

// JsonParser parses PostgreSQL logs in JSON format. Supported variants:
//   - Standard PostgreSQL jsonlog (PG15+)
//   - AWS RDS jsonlog (extra fields: ps, vxid, backend_type, query_id …)
//   - Google Cloud SQL (textPayload-wrapped)
//   - CloudNative-PG (CNPG) direct (kubectl logs)
//   - CloudNative-PG wrapped (fluentd / fluentbit)
//
// Field extraction is performed via gjson.GetBytes paths, avoiding the
// per-line decodeState / map[string]interface{} / reflect cost of
// encoding/json. Critical under tinygo gc=leaking, where every
// transient allocation persists in wasm linear memory.
type JsonParser struct {
	cachedTSFmt string // last successful timestamp format (per-instance)
	msgBuf      []byte // reusable scratch for buildMessage
}

// Parse reads a JSON format log file and streams parsed entries.
// IMPORTANT: This function does NOT close the output channel.
//
// Large plain JSON-lines files take the parallel segment path —
// jsonlog decoding is CPU-bound and strictly line-delimited, so byte
// chunking parallelizes it safely (see parseJSONLinesParallel). JSON
// arrays and small files keep the sequential reader.
func (p *JsonParser) Parse(filename string, out chan<- []LogEntry) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer f.Close()

	if st, err := f.Stat(); err == nil && st.Size() >= jsonParallelMinSize {
		if workers := jsonParallelWorkers(); workers >= 2 {
			// Dispatch on structure: '[' means a JSON array (rare,
			// sequential); anything else is JSON-lines.
			br := bufio.NewReader(f)
			first, perr := peekFirstNonWhitespace(br)
			if perr == nil && first != '[' {
				return parseJSONLinesParallel(filename, st.Size(), workers, out)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("failed to rewind %s: %w", filename, err)
			}
		}
	}
	return p.parseReader(WithProgress(f), out)
}

// parseReader detects the JSON structure and dispatches to the appropriate parser.
func (p *JsonParser) parseReader(r io.Reader, out chan<- []LogEntry) error {
	bufReader := bufio.NewReader(r)
	firstByte, err := peekFirstNonWhitespace(bufReader)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("failed to read JSON stream: %w", err)
	}
	switch firstByte {
	case '[':
		return p.parseJSONArray(bufReader, out)
	default:
		return p.parseJSONLines(bufReader, out)
	}
}

// parseJSONArray parses a JSON array of log entries: [{...},{...}].
func (p *JsonParser) parseJSONArray(r io.Reader, out chan<- []LogEntry) error {
	bs := NewBatchSender(out)
	defer bs.Flush()

	// Read the entire stream so gjson.ForEachLine-style iteration over
	// the array elements works on a single buffer. JSON arrays in
	// PostgreSQL exports are typically small enough that buffering the
	// whole thing is acceptable.
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	root := gjson.ParseBytes(data)
	if !root.IsArray() {
		return fmt.Errorf("expected JSON array")
	}

	index := 0
	root.ForEach(func(_, value gjson.Result) bool {
		entry, err := p.extractFromResult(value)
		if err != nil {
			if !errors.Is(err, errSkipEntry) {
				slog.Warn("skipping malformed JSON entry", "index", index, "err", err)
			}
			index++
			return true
		}
		bs.Send(entry)
		index++
		return true
	})
	return nil
}

// parseJSONLines parses newline-delimited JSON (JSONL/NDJSON).
func (p *JsonParser) parseJSONLines(r io.Reader, out chan<- []LogEntry) error {
	bs := NewBatchSender(out)
	defer bs.Flush()
	scanner := bufio.NewScanner(r)
	// 4 MB initial buffer; grow up to math.MaxInt32 if a single jsonlog
	// entry is unusually large (verbose STATEMENT with embedded JSON,
	// long stack trace, …).
	buf := make([]byte, 4*1024*1024)
	scanner.Buffer(buf, math.MaxInt32)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s := unsafeString(line)
		if !gjson.Valid(s) {
			slog.Warn("skipping malformed JSON", "line", lineNum)
			continue
		}
		entry, err := p.extractFromResult(gjson.Parse(s))
		if err != nil {
			if !errors.Is(err, errSkipEntry) {
				slog.Warn("skipping incomplete JSON entry", "line", lineNum, "err", err)
			}
			continue
		}
		bs.Send(entry)
	}
	return scanner.Err()
}

// peekFirstNonWhitespace returns the first non-whitespace byte without consuming it.
func peekFirstNonWhitespace(reader *bufio.Reader) (byte, error) {
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if !isWhitespace(b) {
			if err := reader.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}

func isWhitespace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}

// effectiveFields is the flat view used to build a LogEntry. Populated
// either from a top-level postgres jsonlog or from a CNPG record after
// envelope unwrapping.
type effectiveFields struct {
	timestamp   string
	user        string
	dbname      string
	pid         string
	remoteHost  string
	severity    string
	stateCode   string
	appName     string
	message     string
	detail      string
	hint        string
	query       string
	context     string
	textPayload string
}

// extractFromResult resolves the CNPG envelope (if any) and produces
// a LogEntry. Returns errSkipEntry for non-postgres CNPG noise and
// Docker fragments.
func (p *JsonParser) extractFromResult(root gjson.Result) (LogEntry, error) {
	fields, err := p.resolveFields(root)
	if err != nil {
		return LogEntry{}, err
	}
	timestamp, err := p.parseTimestamp(fields.timestamp)
	if err != nil {
		return LogEntry{}, fmt.Errorf("timestamp extraction failed: %w", err)
	}
	message := p.buildMessage(&fields)
	if message == "" {
		return LogEntry{}, fmt.Errorf("message extraction failed: no message content")
	}
	return NewLogEntry(timestamp, message, false), nil
}

// resolveFields detects CNPG envelopes (direct or fluentd-wrapped) and
// returns the flattened fields. Falls back to a top-level extraction
// for standard postgres jsonlog / RDS / Cloud SQL.
func (p *JsonParser) resolveFields(root gjson.Result) (effectiveFields, error) {
	// === Format 1: Direct CNPG (kubectl logs / cnpg report) ===
	logger := root.Get("logger").String()
	if logger == "postgres" || logger == "pgaudit" {
		if record := root.Get("record"); record.IsObject() {
			return fieldsFromRecordResult(record), nil
		}
	}
	if logger != "" && logger != "postgres" && logger != "pgaudit" {
		if root.Get("logging_pod").Exists() {
			return effectiveFields{}, errSkipEntry
		}
	}

	// === Format 2: Wrapped CNPG (fluentd / fluentbit) ===
	if msgField := root.Get("message"); msgField.IsObject() {
		innerLogger := msgField.Get("logger").String()
		hasLogtag := root.Get("logtag").Exists()
		hasInnerPod := msgField.Get("logging_pod").Exists()
		hasInnerMsg := msgField.Get("msg").Exists()
		isCNPGEnvelope := hasLogtag || hasInnerPod || hasInnerMsg

		if isCNPGEnvelope {
			if innerLogger != "" && innerLogger != "postgres" && innerLogger != "pgaudit" {
				return effectiveFields{}, errSkipEntry
			}
			innerRecord := msgField.Get("record")
			if !innerRecord.IsObject() {
				return effectiveFields{}, errSkipEntry
			}
			return fieldsFromRecordResult(innerRecord), nil
		}
		// Not a CNPG envelope — fall through to top-level handling.
	} else if root.Get("message").Type == gjson.String && root.Get("logtag").Exists() {
		// Docker log fragment with no structured postgres content.
		return effectiveFields{}, errSkipEntry
	}

	// === Format 3: Standard PostgreSQL jsonlog (RDS / Cloud SQL inclusive) ===
	return fieldsFromTopLevel(root), nil
}

// fieldsFromRecordResult flattens a CNPG record into effectiveFields,
// mapping CNPG-renamed fields (log_time, user_name, …) to the standard
// postgres jsonlog names.
func fieldsFromRecordResult(r gjson.Result) effectiveFields {
	timestamp := r.Get("log_time").String()
	if timestamp == "" {
		timestamp = r.Get("timestamp").String()
	}
	user := r.Get("user_name").String()
	if user == "" {
		user = r.Get("user").String()
	}
	dbname := r.Get("database_name").String()
	if dbname == "" {
		dbname = r.Get("dbname").String()
	}
	pid := r.Get("process_id").String()
	if pid == "" {
		pid = r.Get("pid").String()
	}
	remoteHost := extractIPHost(r.Get("connection_from").String())
	if remoteHost == "" {
		remoteHost = r.Get("remote_host").String()
	}
	stateCode := r.Get("sql_state_code").String()
	if stateCode == "" {
		stateCode = r.Get("state_code").String()
	}
	query := r.Get("query").String()
	if query == "" {
		query = r.Get("statement").String()
	}
	return effectiveFields{
		timestamp:  timestamp,
		user:       user,
		dbname:     dbname,
		pid:        pid,
		remoteHost: remoteHost,
		severity:   r.Get("error_severity").String(),
		stateCode:  stateCode,
		appName:    r.Get("application_name").String(),
		message:    r.Get("message").String(),
		detail:     r.Get("detail").String(),
		hint:       r.Get("hint").String(),
		query:      query,
		context:    r.Get("context").String(),
	}
}

// fieldsFromTopLevel flattens a top-level postgres jsonlog into effectiveFields.
func fieldsFromTopLevel(r gjson.Result) effectiveFields {
	dbname := r.Get("dbname").String()
	if dbname == "" {
		dbname = r.Get("database").String()
	}
	timestamp := r.Get("timestamp").String()
	if timestamp == "" {
		timestamp = r.Get("time").String()
	}
	if timestamp == "" {
		timestamp = r.Get("ts").String()
	}
	if timestamp == "" {
		timestamp = r.Get("@timestamp").String()
	}
	var msg string
	if mf := r.Get("message"); mf.Type == gjson.String {
		msg = mf.String()
	}
	query := r.Get("query").String()
	if query == "" {
		query = r.Get("statement").String()
	}
	return effectiveFields{
		timestamp:   timestamp,
		user:        r.Get("user").String(),
		dbname:      dbname,
		pid:         r.Get("pid").String(),
		remoteHost:  r.Get("remote_host").String(),
		severity:    r.Get("error_severity").String(),
		stateCode:   r.Get("state_code").String(),
		appName:     r.Get("application_name").String(),
		message:     msg,
		detail:      r.Get("detail").String(),
		hint:        r.Get("hint").String(),
		query:       query,
		context:     r.Get("context").String(),
		textPayload: r.Get("textPayload").String(),
	}
}

// extractIPHost mirrors the legacy CNPG handling: strip the port from
// "10.131.3.19:58258" to leave "10.131.3.19". Pass-through for "[local]".
func extractIPHost(s string) string {
	if s == "" {
		return ""
	}
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		return s[:idx]
	}
	return s
}

// buildMessage rebuilds a stderr-style log line from the flattened fields,
// writing into the parser's reusable scratch buffer.
func (p *JsonParser) buildMessage(f *effectiveFields) string {
	if f.textPayload != "" {
		return f.textPayload
	}

	if cap(p.msgBuf) < 512 {
		p.msgBuf = make([]byte, 0, 512)
	} else {
		p.msgBuf = p.msgBuf[:0]
	}
	b := p.msgBuf

	wrote := false
	if f.pid != "" {
		b = append(b, '[')
		b = append(b, f.pid...)
		b = append(b, ']', ':')
		wrote = true
	}
	if f.user != "" || f.dbname != "" || f.appName != "" || f.remoteHost != "" {
		if wrote {
			b = append(b, ' ')
		}
		hasField := false
		if f.user != "" {
			b = append(b, "user="...)
			b = append(b, f.user...)
			hasField = true
		}
		if f.dbname != "" {
			if hasField {
				b = append(b, ',')
			}
			b = append(b, "db="...)
			b = append(b, f.dbname...)
			hasField = true
		}
		if f.appName != "" {
			if hasField {
				b = append(b, ',')
			}
			b = append(b, "app="...)
			b = append(b, f.appName...)
			hasField = true
		}
		if f.remoteHost != "" {
			if hasField {
				b = append(b, ',')
			}
			b = append(b, "client="...)
			b = append(b, f.remoteHost...)
		}
		wrote = true
	}
	if f.severity != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, f.severity...)
		b = append(b, ':')
		wrote = true
	}
	if f.message != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, f.message...)
		wrote = true
	}
	if f.detail != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, "DETAIL: "...)
		b = append(b, f.detail...)
		wrote = true
	}
	if f.hint != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, "HINT: "...)
		b = append(b, f.hint...)
		wrote = true
	}
	if f.query != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, "STATEMENT: "...)
		b = append(b, f.query...)
		wrote = true
	}
	if f.context != "" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, "CONTEXT: "...)
		b = append(b, f.context...)
		wrote = true
	}
	if f.stateCode != "" && f.stateCode != "00000" {
		if wrote {
			b = append(b, ' ')
		}
		b = append(b, "SQLSTATE = '"...)
		b = append(b, f.stateCode...)
		b = append(b, '\'')
	}

	p.msgBuf = b
	return string(b)
}

// jsonTimestampFormats lists the formats we observe in PostgreSQL JSON
// logs and CNPG records, ordered by likelihood.
var jsonTimestampFormats = [...]string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999 MST",
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05.999",
	"2006-01-02 15:04:05",
}

// parseTimestamp parses a timestamp from any of the supported formats.
// Caches the most recently successful format so subsequent lines hit
// the fast path without burning per-line errors on the wrong layouts.
func (p *JsonParser) parseTimestamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("no timestamp field found")
	}
	if p.cachedTSFmt != "" {
		if t, err := parseTime(p.cachedTSFmt, s); err == nil {
			return t, nil
		}
	}
	for _, f := range jsonTimestampFormats {
		if t, err := parseTime(f, s); err == nil {
			p.cachedTSFmt = f
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", s)
}
