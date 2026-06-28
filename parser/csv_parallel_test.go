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
