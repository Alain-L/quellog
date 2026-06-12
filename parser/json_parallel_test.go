package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collectEntries drains a parse function into a flat slice.
func collectEntries(t *testing.T, parse func(out chan<- []LogEntry) error) []LogEntry {
	t.Helper()
	out := make(chan []LogEntry, 64)
	done := make(chan []LogEntry)
	go func() {
		var all []LogEntry
		for batch := range out {
			all = append(all, batch...)
		}
		done <- all
	}()
	if err := parse(out); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	close(out)
	return <-done
}

// writeSyntheticJSONL generates a JSON-lines file exercising the
// shapes that matter for boundary handling: normal entries, empty
// lines, malformed lines, escaped newlines inside values, a CRLF
// line, and one entry far larger than the segment reader buffer.
func writeSyntheticJSONL(t *testing.T, lines int) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch {
		case i%97 == 13:
			b.WriteString("\n") // empty line
		case i%97 == 29:
			b.WriteString("{not json at all\n")
		case i%97 == 41:
			// Escaped newlines inside the message value (RDS style).
			fmt.Fprintf(&b, `{"timestamp":"2026-04-19 17:%02d:%02d.300 UTC","pid":%d,"error_severity":"LOG","message":"duration: 12.3 ms  plan:\n\t{\n\t  \"Node\": \"Seq Scan\"\n\t}"}`,
				(i/60)%60, i%60, 1000+i)
			b.WriteString("\n")
		case i%97 == 53:
			// Giant entry: ~2.5 MB message, larger than the 1 MB
			// segment read buffer (exercises the ErrBufferFull path).
			fmt.Fprintf(&b, `{"timestamp":"2026-04-19 18:%02d:%02d.400 UTC","pid":%d,"error_severity":"LOG","message":"big: %s"}`,
				(i/60)%60, i%60, 2000+i, strings.Repeat("x", 2_500_000))
			b.WriteString("\n")
		case i%97 == 71:
			// CRLF terminator.
			fmt.Fprintf(&b, `{"timestamp":"2026-04-19 19:%02d:%02d.500 UTC","pid":%d,"error_severity":"WARNING","message":"crlf line"}`,
				(i/60)%60, i%60, 3000+i)
			b.WriteString("\r\n")
		default:
			fmt.Fprintf(&b, `{"timestamp":"2026-04-19 20:%02d:%02d.600 UTC","pid":%d,"user":"app","dbname":"appdb","error_severity":"LOG","message":"duration: %d.%03d ms  statement: SELECT %d"}`,
				(i/60)%60, i%60, 4000+i, i%50, i%1000, i)
			b.WriteString("\n")
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestJSONParallel_EquivalenceWithSequential parses the same synthetic
// file through the sequential reader and through the parallel segment
// path at several worker counts, and requires the exact same entries
// in the exact same order. Worker counts are chosen so segment
// boundaries fall mid-line, mid-giant-entry, and beyond EOF.
func TestJSONParallel_EquivalenceWithSequential(t *testing.T) {
	path := writeSyntheticJSONL(t, 5000)
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
		p := &JsonParser{}
		return p.parseReader(f, out)
	})
	if len(seq) == 0 {
		t.Fatal("sequential parse produced no entries")
	}

	for _, workers := range []int{2, 3, 7, 16} {
		par := collectEntries(t, func(out chan<- []LogEntry) error {
			return parseJSONLinesParallel(path, st.Size(), workers, out)
		})
		if len(par) != len(seq) {
			t.Fatalf("workers=%d: %d entries, sequential had %d", workers, len(par), len(seq))
		}
		for i := range seq {
			if !seq[i].Timestamp.Equal(par[i].Timestamp) ||
				seq[i].Message != par[i].Message ||
				seq[i].PID != par[i].PID ||
				seq[i].IsContinuation != par[i].IsContinuation {
				t.Fatalf("workers=%d: entry %d differs\nseq: %+v\npar: %+v", workers, i, seq[i], par[i])
			}
		}
	}
}

// TestJSONParallel_TinyFileManyWorkers pins the degenerate split:
// more workers than lines, segments smaller than single lines, and a
// last line without a trailing newline.
func TestJSONParallel_TinyFileManyWorkers(t *testing.T) {
	content := `{"timestamp":"2026-04-19 17:00:00.100 UTC","pid":1,"error_severity":"LOG","message":"a"}
{"timestamp":"2026-04-19 17:00:01.100 UTC","pid":2,"error_severity":"LOG","message":"b"}
{"timestamp":"2026-04-19 17:00:02.100 UTC","pid":3,"error_severity":"LOG","message":"c"}`
	path := filepath.Join(t.TempDir(), "tiny.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)

	par := collectEntries(t, func(out chan<- []LogEntry) error {
		return parseJSONLinesParallel(path, st.Size(), 8, out)
	})
	if len(par) != 3 {
		t.Fatalf("got %d entries, want 3", len(par))
	}
	// The parser rebuilds "[PID]: LOG: <msg>" — assert order via the
	// message tails.
	for i, want := range []string{": a", ": b", ": c"} {
		if !strings.HasSuffix(par[i].Message, want) {
			t.Errorf("entry %d message = %q, want suffix %q", i, par[i].Message, want)
		}
	}
}
