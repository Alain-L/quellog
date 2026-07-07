// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bytes"
	"io"
)

// csvField is one field of a CSV record. raw is a zero-copy view into the
// scanner's buffer (valid only until the next Next() call); esc is set when raw
// still contains doubled quotes ("") that must be collapsed when the value is
// used. Deferring the unescape lets the common, escape-free path stay
// allocation-free and lets escaped fields be unescaped straight into the
// message buffer instead of into a throwaway string.
type csvField struct {
	raw string
	esc bool
}

// span is the in-buffer extent of a field during scanning, kept as offsets
// (not views) so a mid-record buffer compaction can shift them in place before
// they are frozen into csvField views.
type span struct {
	start, end int
	esc        bool
}

// csvScanner is a single-pass, mostly-zero-allocation CSV record scanner tuned
// for PostgreSQL CSV logs. It replaces encoding/csv on the hot path: instead of
// returning a []string with one heap string per field, it returns field views
// into its own read buffer and uses bytes.IndexByte to skip between delimiters
// in bulk (matching encoding/csv's speed). Doubled quotes are left in place and
// collapsed at use; only fields that span a buffer refill or are otherwise
// unavoidable cost an allocation.
//
// Views are valid only until the next Next() call — callers must consume them
// (copy out what they keep) before scanning on. The CSV parser does exactly
// that: buildCSVMessage copies the fields it needs into an owned message
// string, so no view escapes into a LogEntry.
//
// Supported subset (everything PostgreSQL emits): comma-separated fields,
// optional double-quoting, "" escaped quotes, newlines inside quoted fields,
// and \n or \r\n terminators. Leading and trailing spaces are trimmed to match
// encoding/csv (TrimLeadingSpace) plus the parser's historical trailing-trim.
// Malformed quoting is handled leniently rather than erroring.
type csvScanner struct {
	r     io.Reader
	buf   []byte
	start int    // parse cursor: first byte of the next record
	end   int    // bytes of valid data in buf
	eof   bool   // underlying reader drained
	spans []span // current record's field extents (offsets), reused each Next
}

// csvRecord is a view over the scanner's current record: the buffer plus the
// field extents. Fields are materialized into csvField views on demand by
// field(), so only the ~14 columns the parser actually reads pay for a view
// (PostgreSQL CSV has 23-26). Valid only until the next Next() call.
type csvRecord struct {
	buf   []byte
	spans []span
}

func (rec csvRecord) len() int { return len(rec.spans) }

// field returns record[idx], or a zero csvField (empty, unescaped) when idx is
// out of range — the bounds-safe accessor buildCSVMessage relies on.
func (rec csvRecord) field(idx int) csvField {
	if idx >= len(rec.spans) {
		return csvField{}
	}
	sp := rec.spans[idx]
	return csvField{raw: unsafeString(rec.buf[sp.start:sp.end]), esc: sp.esc}
}

// csvScannerInitialBuf mirrors the old bufio sizing: a 1 MB read buffer keeps
// the syscall count ~256x below encoding/csv's 4 KB default on big files.
const csvScannerInitialBuf = 1 << 20

func newCSVScanner(r io.Reader) *csvScanner {
	return &csvScanner{r: r, buf: make([]byte, csvScannerInitialBuf)}
}

// Reset rebinds the scanner to r and clears its cursors so the read buffer and
// span slice are reused across inputs. A parallel worker parsing many segments
// then allocates them once instead of once per segment; a buffer grown for a
// giant record is kept, amortizing across later segments.
func (s *csvScanner) Reset(r io.Reader) {
	s.r = r
	s.start = 0
	s.end = 0
	s.eof = false
	s.spans = s.spans[:0]
}

// fill reads more data into buf, growing it when full. It loops past
// zero-length non-EOF reads so callers never spin.
func (s *csvScanner) fill() error {
	if s.eof {
		return nil
	}
	if s.end == len(s.buf) {
		grown := make([]byte, len(s.buf)*2)
		copy(grown, s.buf[:s.end])
		s.buf = grown
	}
	for {
		n, err := s.r.Read(s.buf[s.end:])
		s.end += n
		if err == io.EOF {
			s.eof = true
			return nil
		}
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
}

// compact drops the consumed prefix [0:start) so a record straddling the buffer
// can keep growing from offset 0. It shifts the in-progress field spans by the
// same delta and returns it so the caller can fix up its local cursors.
func (s *csvScanner) compact() int {
	delta := s.start
	if delta == 0 {
		return 0
	}
	copy(s.buf, s.buf[s.start:s.end])
	s.end -= delta
	s.start = 0
	for i := range s.spans {
		s.spans[i].start -= delta
		s.spans[i].end -= delta
	}
	return delta
}

// Next scans the next record into s.fields. It returns false at EOF, or an
// error on an underlying read failure.
func (s *csvScanner) Next() (bool, error) {
	// Skip blank lines (bare \n / \r\n between records).
	for {
		if s.start >= s.end {
			if err := s.fill(); err != nil {
				return false, err
			}
			if s.start >= s.end {
				return false, nil // EOF, no more data
			}
		}
		if c := s.buf[s.start]; c == '\n' || c == '\r' {
			s.start++
			continue
		}
		break
	}

	s.spans = s.spans[:0]
	pos := s.start

	for { // one iteration per field
		// TrimLeadingSpace.
		for {
			if pos >= s.end {
				if s.eof {
					break
				}
				pos -= s.compact()
				if err := s.fill(); err != nil {
					return false, err
				}
				if pos >= s.end && s.eof {
					break
				}
				continue
			}
			if s.buf[pos] == ' ' {
				pos++
				continue
			}
			break
		}

		var sp span
		if pos < s.end && s.buf[pos] == '"' {
			pos++ // opening quote
			sp.start = pos
			for { // scan to the closing quote
				if pos >= s.end {
					if s.eof {
						break // unterminated quote at EOF (lenient)
					}
					d := s.compact()
					pos -= d
					sp.start -= d
					if err := s.fill(); err != nil {
						return false, err
					}
					if pos >= s.end && s.eof {
						break
					}
					continue
				}
				idx := bytes.IndexByte(s.buf[pos:s.end], '"')
				if idx < 0 {
					pos = s.end // need more data
					continue
				}
				pos += idx
				// Need the byte after the quote to tell "" from a closing quote.
				if pos+1 >= s.end && !s.eof {
					d := s.compact()
					pos -= d
					sp.start -= d
					if err := s.fill(); err != nil {
						return false, err
					}
				}
				if pos+1 < s.end && s.buf[pos+1] == '"' {
					sp.esc = true
					pos += 2 // escaped quote
					continue
				}
				break // closing quote at pos
			}
			sp.end = pos // exclusive, at the closing quote (or s.end at EOF)
			for sp.end > sp.start && s.buf[sp.end-1] == ' ' {
				sp.end--
			}
			if pos < s.end && s.buf[pos] == '"' {
				pos++ // consume closing quote
			}
			// Lenient: skip anything between the closing quote and the
			// delimiter. Like its sibling scan loops, refill (and compact)
			// at the buffer boundary so garbage straddling the 1 MB buffer
			// doesn't prematurely terminate the record. sp.start/sp.end are
			// already frozen here, so compaction must shift both by the same
			// delta the earlier fields (in s.spans) were shifted by.
			for {
				if pos >= s.end {
					if s.eof {
						break
					}
					d := s.compact()
					pos -= d
					sp.start -= d
					sp.end -= d
					if err := s.fill(); err != nil {
						return false, err
					}
					if pos >= s.end && s.eof {
						break
					}
					continue
				}
				if s.buf[pos] != ',' && s.buf[pos] != '\n' {
					pos++
					continue
				}
				break
			}
		} else {
			sp.start = pos
			for { // scan to ',' or '\n'
				if pos >= s.end {
					if s.eof {
						break
					}
					d := s.compact()
					pos -= d
					sp.start -= d
					if err := s.fill(); err != nil {
						return false, err
					}
					if pos >= s.end && s.eof {
						break
					}
					continue
				}
				seg := s.buf[pos:s.end]
				rel := bytes.IndexByte(seg, ',')
				if rel < 0 {
					// No comma buffered: the record ends at the next newline,
					// else refill. Scan for '\n' only over what's buffered.
					if nl := bytes.IndexByte(seg, '\n'); nl >= 0 {
						pos += nl
						break
					}
					pos = s.end // neither found yet; refill
					continue
				}
				// Comma found; a record-ending newline only matters if it falls
				// before it, so bound the '\n' scan to the field (avoids an
				// end-of-record scan per field — the source of the regression).
				if nl := bytes.IndexByte(seg[:rel], '\n'); nl >= 0 {
					pos += nl
				} else {
					pos += rel
				}
				break
			}
			sp.end = pos
			if sp.end > sp.start && s.buf[sp.end-1] == '\r' {
				sp.end-- // strip the \r of a \r\n terminator
			}
			for sp.end > sp.start && s.buf[sp.end-1] == ' ' {
				sp.end--
			}
		}
		s.spans = append(s.spans, sp)

		if pos < s.end && s.buf[pos] == ',' {
			pos++ // next field
			continue
		}
		break // '\n' or EOF: record done
	}

	// Consume the record terminator.
	if pos < s.end && s.buf[pos] == '\n' {
		s.start = pos + 1
	} else {
		s.start = pos
	}
	return true, nil
}

// record exposes the most recently scanned record. The returned view is valid
// only until the next Next() call.
func (s *csvScanner) record() csvRecord {
	return csvRecord{buf: s.buf, spans: s.spans}
}

// appendField appends f's value to b, collapsing "" to " when f.esc is set.
// The common escape-free path is a single append with no extra allocation.
func appendField(b []byte, f csvField) []byte {
	if !f.esc {
		return append(b, f.raw...)
	}
	s := f.raw
	for i := 0; i < len(s); i++ {
		b = append(b, s[i])
		if s[i] == '"' && i+1 < len(s) && s[i+1] == '"' {
			i++ // skip the second quote of the pair
		}
	}
	return b
}
