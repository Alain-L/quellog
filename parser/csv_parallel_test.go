package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csvRow builds a valid 23-field PostgreSQL csvlog record. The message (field
// 13) is quoted with "" escaping, so it may contain commas, quotes and embedded
// newlines.
func csvRow(ts string, pid int, message string) string {
	q := `"` + strings.ReplaceAll(message, `"`, `""`) + `"`
	return fmt.Sprintf("%s,app,appdb,%d,10.0.0.1,sess,1,SELECT,%s,1/2,0,LOG,00000,%s,,,,,,,,,myapp\n",
		ts, pid, ts, q)
}

// writeSyntheticCSV generates a >16 MB CSV (so several 8 MB segments form)
// exercising the shapes that matter for record-boundary detection: plain
// records, messages with embedded newlines (multi-physical-line records),
// embedded "\n<digits>" that look like a timestamp but fail the calendar range
// check, escaped quotes, a record far larger than the 1 MB scanner buffer, and
// a final record without a trailing newline.
func writeSyntheticCSV(t *testing.T, target int) string {
	t.Helper()
	var b strings.Builder
	ts := func(i int) string {
		return fmt.Sprintf("2026-01-02 %02d:%02d:%02d.%03d UTC", (i/3600)%24, (i/60)%60, i%60, i%1000)
	}
	i := 0
	for b.Len() < target {
		switch {
		case i%101 == 7:
			// Message with embedded newlines (record spans physical lines).
			b.WriteString(csvRow(ts(i), 1000+i, "duration: 5 ms  statement:\nSELECT *\nFROM t\nWHERE id = 1"))
		case i%101 == 23:
			// Quoted text that looks like a record start but has an
			// out-of-range date/time — must NOT be taken as a boundary.
			b.WriteString(csvRow(ts(i), 1000+i, "oops\n2026-13-45 99:99:99 UTC,not,a,real,record"))
		case i%101 == 37:
			// Quoted text with a calendar-valid-looking but still in-quote
			// line that lacks the trailing field-0 comma right after the TZ.
			b.WriteString(csvRow(ts(i), 1000+i, "see line\n2026-01-02 03:04:05 UTC continues here"))
		case i%101 == 53:
			// Escaped quotes inside the message.
			b.WriteString(csvRow(ts(i), 1000+i, `he said "hello", then "bye"`))
		case i%101 == 67:
			// Giant record: ~2.5 MB message, beyond the 1 MB scanner buffer.
			b.WriteString(csvRow(ts(i), 1000+i, "big: "+strings.Repeat("x", 2_500_000)))
		default:
			b.WriteString(csvRow(ts(i), 1000+i, fmt.Sprintf("duration: %d.%03d ms  statement: SELECT %d", i%50, i%1000, i)))
		}
		i++
	}
	// Final record with NO trailing newline.
	s := b.String()
	s = strings.TrimRight(s, "\n") + "\n" + strings.TrimRight(csvRow(ts(i), 9999, "last record no newline"), "\n")

	path := filepath.Join(t.TempDir(), "synthetic.csv")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCSVEquivalent(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	seq := collectEntries(t, func(out chan<- []LogEntry) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return (&CsvParser{}).parseReader(WithProgress(f), out)
	})
	if len(seq) == 0 {
		t.Fatal("sequential parse produced no entries")
	}
	for _, workers := range []int{2, 3, 7, 16} {
		par := collectEntries(t, func(out chan<- []LogEntry) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return (&CsvParser{}).parseParallel(f, st.Size(), workers, out)
		})
		if len(par) != len(seq) {
			t.Fatalf("workers=%d: %d entries, sequential had %d", workers, len(par), len(seq))
		}
		for i := range seq {
			if !seq[i].Timestamp.Equal(par[i].Timestamp) ||
				seq[i].Message != par[i].Message ||
				seq[i].PID != par[i].PID ||
				seq[i].IsContinuation != par[i].IsContinuation {
				t.Fatalf("workers=%d: entry %d differs\nseq: %.120q\npar: %.120q", workers, i, seq[i].Message, par[i].Message)
			}
		}
	}
}

// TestCSVParallel_EquivalenceWithSequential requires the parallel record-aligned
// path to produce exactly the sequential entries, in order, at several worker
// counts (so boundaries fall mid-record, mid-giant, near quoted newlines).
func TestCSVParallel_EquivalenceWithSequential(t *testing.T) {
	assertCSVEquivalent(t, writeSyntheticCSV(t, 18<<20))
}

// writePathologicalCSV builds the R-1 regression fixture: filler records, then
// one huge record whose quoted, multi-line message (an echoed COPY payload) has
// EVERY physical line begin with a valid PostgreSQL timestamp + comma — each one
// a false positive for the retired isCSVRecordStart heuristic. The huge record
// is sized (~10 MB) and positioned (after ~10 MB of fillers) to straddle an
// 8 MB segment boundary, so a mid-field split is forced if boundaries are not
// quote-aware. It returns the fixture path and the unique tail marker that sits
// at the very end of the huge record's message (the field value a mid-field
// split would drop).
func writePathologicalCSV(t *testing.T, msgSize int) (path, tailMarker string) {
	t.Helper()
	tailMarker = "END_OF_COPY_TAIL_MARKER_7f3a9c"
	ts := "2026-01-02 03:04:05.123 UTC"
	var b strings.Builder

	i := 0
	for b.Len() < 10<<20 { // fillers so the huge record crosses the 16 MB (2×8 MB) boundary
		b.WriteString(csvRow(ts, 1000+i, fmt.Sprintf("duration: %d.%03d ms  statement: SELECT %d", i%50, i%1000, i)))
		i++
	}

	var msg strings.Builder
	msg.WriteString("duration: 12.500 ms  statement: COPY events FROM stdin WITH (FORMAT csv);")
	for msg.Len() < msgSize {
		// A full 23-field csvlog-shaped line living INSIDE the quoted message:
		// it starts with a valid timestamp + comma (old-heuristic false positive)
		// and has >= 14 fields (would be counted as a record if mis-split).
		fmt.Fprintf(&msg, "\n2026-01-01 12:00:00.000 UTC,evil,evildb,9999,10.9.9.9,x,1,INSERT,%s,1/9,0,LOG,00000,injected row %d,,,,,,,,,evilapp", ts, msg.Len())
	}
	msg.WriteString("\n" + tailMarker)
	b.WriteString(csvRow(ts, 424242, msg.String()))

	for j := 0; j < 5000; j++ { // fillers after the huge record
		b.WriteString(csvRow(ts, 2000+i, fmt.Sprintf("duration: %d.%03d ms  statement: UPDATE t SET x=%d", i%50, i%1000, i)))
		i++
	}

	path = filepath.Join(t.TempDir(), "pathological.csv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, tailMarker
}

func countMessagesContaining(entries []LogEntry, sub string) int {
	n := 0
	for _, e := range entries {
		if strings.Contains(e.Message, sub) {
			n++
		}
	}
	return n
}

// TestCSVParallel_QuotedMultilineTimestampBoundary is the R-1 regression guard.
// The huge record's quoted message is a multi-line COPY echo whose every line
// begins with a valid PostgreSQL timestamp + comma. With the old
// timestamp-shaped-line boundary heuristic a segment boundary landed inside that
// quoted field: the record lost its tail and the embedded lines became thousands
// of bogus records, silently corrupting the analysis of any CSV ≥ csvParallelMinSize.
// The quote-parity-aware boundary computation keeps the record whole, so the
// parallel parse matches the sequential (stdlib-equivalent, v0.11.0) parse in
// both record count and content.
func TestCSVParallel_QuotedMultilineTimestampBoundary(t *testing.T) {
	path, tailMarker := writePathologicalCSV(t, 10<<20)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	seq := collectEntries(t, func(out chan<- []LogEntry) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return (&CsvParser{}).parseReader(WithProgress(f), out)
	})
	// Oracle: the whole quoted field is ONE record, so its tail marker is present
	// in exactly one entry.
	if got := countMessagesContaining(seq, tailMarker); got != 1 {
		t.Fatalf("oracle: tail marker in %d entries, want 1", got)
	}

	for _, workers := range []int{2, 3, 7, 16} {
		par := collectEntries(t, func(out chan<- []LogEntry) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return (&CsvParser{}).parseParallel(f, st.Size(), workers, out)
		})
		if len(par) != len(seq) {
			t.Fatalf("workers=%d: parallel produced %d records, oracle has %d (boundary split a quoted field)",
				workers, len(par), len(seq))
		}
		// The huge record's tail must survive intact in exactly one entry.
		if got := countMessagesContaining(par, tailMarker); got != 1 {
			t.Fatalf("workers=%d: tail marker in %d entries, want 1 (huge record lost its tail)",
				workers, got)
		}
	}
}

// TestCSVParallel_RealFile validates equivalence on a real CSV when PARSEBENCH
// points at one (gated so it never runs in CI):
//
//	PARSEBENCH=_samples/C.csv go test ./parser/ -run TestCSVParallel_RealFile -count=1
func TestCSVParallel_RealFile(t *testing.T) {
	path := os.Getenv("PARSEBENCH")
	if path == "" {
		t.Skip("set PARSEBENCH=<file.csv>")
	}
	assertCSVEquivalent(t, path)
}
