package parser

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSyntheticStderr generates a stderr log exercising the shapes
// that matter for entry-aligned chunking: single-line entries,
// DETAIL/STATEMENT continuations carrying their own timestamp, raw
// multi-line SQL (lines without timestamps, lines starting with
// whitespace), a line inside a SQL payload that LOOKS like a
// timestamp line (the sequential parser treats it as a new entry —
// the parallel path must reproduce that, not "fix" it), and one
// entry far larger than a segment window.
func writeSyntheticStderr(t *testing.T, entries int) string {
	t.Helper()
	var b strings.Builder
	ts := func(i int) string {
		return fmt.Sprintf("2026-04-19 %02d:%02d:%02d.%03d CET", 8+(i/3600)%12, (i/60)%60, i%60, i%1000)
	}
	for i := 0; i < entries; i++ {
		switch {
		case i%89 == 7:
			// Entry with raw multi-line SQL continuation.
			fmt.Fprintf(&b, "%s [%d]: LOG:  duration: 12.3 ms  statement: SELECT a,\n", ts(i), 1000+i)
			b.WriteString("\tb, c\n")
			b.WriteString("  FROM t\n")
			b.WriteString("WHERE x = 1\n")
		case i%89 == 23:
			// ERROR + timestamped DETAIL + STATEMENT continuations.
			fmt.Fprintf(&b, "%s [%d]: ERROR:  duplicate key value violates unique constraint \"pk\"\n", ts(i), 1000+i)
			fmt.Fprintf(&b, "%s [%d]: DETAIL:  Key (id)=(42) already exists.\n", ts(i), 1000+i)
			fmt.Fprintf(&b, "%s [%d]: STATEMENT:  INSERT INTO t VALUES (42)\n", ts(i), 1000+i)
		case i%89 == 41:
			// SQL payload containing a line that looks like a new entry.
			fmt.Fprintf(&b, "%s [%d]: LOG:  duration: 5.0 ms  statement: SELECT *\n", ts(i), 1000+i)
			b.WriteString("2026-01-01 00:00:00 CET looks like an entry start inside SQL\n")
			b.WriteString("  AND y = 2\n")
		case i%89 == 59:
			// Giant entry: one statement line bigger than a segment.
			fmt.Fprintf(&b, "%s [%d]: LOG:  duration: 99.9 ms  statement: SELECT '%s'\n",
				ts(i), 1000+i, strings.Repeat("z", 3_000_000))
		default:
			fmt.Fprintf(&b, "%s [%d]: LOG:  duration: %d.%03d ms  statement: SELECT %d\n",
				ts(i), 1000+i, i%40, i%999, i)
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// parallelStderrForTest runs the parallel path with an explicit
// worker count and a tiny segment size override is not possible (the
// constant is fixed), so the synthetic file is sized to span many
// segments via the giant entries.
func parallelStderrForTest(t *testing.T, path string, workers int) []LogEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	p := &StderrParser{}
	p.detectPrefixStructure(f)
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return collectEntries(t, func(out chan<- []LogEntry) error {
		return p.parseParallel(f, st.Size(), workers, out)
	})
}

// TestStderrParallel_EquivalenceWithSequential compares the parallel
// segment path against the sequential reader on a synthetic log whose
// continuations, fake-timestamp SQL lines and giant entries land on
// segment boundaries. The streams must match entry for entry.
func TestStderrParallel_EquivalenceWithSequential(t *testing.T) {
	// ~25k entries with several 3 MB giants → ~90+ MB, >10 segments.
	path := writeSyntheticStderr(t, 25_000)

	seq := collectEntries(t, func(out chan<- []LogEntry) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		p := &StderrParser{}
		p.detectPrefixStructure(f)
		if _, err := f.Seek(0, 0); err != nil {
			return err
		}
		return p.parseReader(f, out)
	})
	if len(seq) == 0 {
		t.Fatal("sequential parse produced no entries")
	}

	for _, workers := range []int{2, 3, 8} {
		par := parallelStderrForTest(t, path, workers)
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

// readOnly hides Seek/ReaderAt so a *os.File is seen as a plain stream,
// forcing the non-seekable parseStreamParallel path (as gzip/zstd/tar do).
type readOnly struct{ r io.Reader }

func (ro readOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

// TestStderrStreamParallel_EquivalenceWithSequential covers parseStreamParallel,
// the non-seekable sibling used for compressed/tar stderr at workers>=2. The
// existing equivalence test drives the seekable parseParallel; this one drives
// the stream (in-memory 4 MB chunk) path so its chunk boundary-extension and
// ordered cross-chunk fan-in actually run. The synthetic log spans several 4 MB
// chunks with 3 MB giants straddling the boundaries; the stream must match the
// sequential reader entry for entry.
func TestStderrStreamParallel_EquivalenceWithSequential(t *testing.T) {
	path := writeSyntheticStderr(t, 1000) // giants dominate -> ~30 MB, several 4 MB chunks

	seq := collectEntries(t, func(out chan<- []LogEntry) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		p := &StderrParser{}
		p.detectPrefixStructure(f)
		if _, err := f.Seek(0, 0); err != nil {
			return err
		}
		return p.parseReader(f, out)
	})
	if len(seq) == 0 {
		t.Fatal("sequential parse produced no entries")
	}

	for _, workers := range []int{2, 3, 8} {
		par := collectEntries(t, func(out chan<- []LogEntry) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			// A fresh parser: parseStreamParallel detects the prefix from its
			// own head sample, so no pre-seek/detect is needed (and can't be).
			p := &StderrParser{}
			return p.parseStreamParallel(readOnly{f}, workers, out)
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

// TestFindEntryBoundary_Predicates pins the boundary scanner on a
// small file: boundaries must land exactly on entry-start lines and
// never on continuation lines.
func TestFindEntryBoundary_Predicates(t *testing.T) {
	content := "2026-04-19 08:00:00.000 CET [1]: LOG:  one\n" + // 0
		"  continuation line\n" +
		"2026-04-19 08:00:01.000 CET [2]: LOG:  two\n" +
		"raw sql line without timestamp\n" +
		"2026-04-19 08:00:02.000 CET [3]: LOG:  three\n"
	path := filepath.Join(t.TempDir(), "b.log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()

	// From offset 1 (inside entry one) the next boundary must be the
	// "two" line, skipping the continuation.
	idxTwo := int64(strings.Index(content, "2026-04-19 08:00:01"))
	got, err := findEntryBoundary(f, 1, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if got != idxTwo {
		t.Errorf("boundary from 1 = %d, want %d (start of entry two)", got, idxTwo)
	}

	// From inside the raw SQL line, the boundary must skip to "three".
	idxRaw := int64(strings.Index(content, "raw sql"))
	idxThree := int64(strings.Index(content, "2026-04-19 08:00:02"))
	got, err = findEntryBoundary(f, idxRaw, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if got != idxThree {
		t.Errorf("boundary from raw-sql = %d, want %d (start of entry three)", got, idxThree)
	}

	// From past the last entry start: no boundary → size.
	got, err = findEntryBoundary(f, idxThree+1, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if got != st.Size() {
		t.Errorf("boundary from tail = %d, want size %d", got, st.Size())
	}
}

// bareLinesAcross builds a stderr stream whose bare pgBackRest lines
// straddle offset at, followed by the archiver's failure and a DETAIL
// naming another program: the sequential parse gives them back to
// PostgreSQL, which a segment cut between them and the DETAIL breaks.
func bareLinesAcross(at, total int) string {
	const (
		filler = "2026-10-10 13:05:16.739 UTC [4242] LOG:  checkpoint starting: time\n"
		bare   = "ERROR: [055]: unable to load info file '/var/lib/pgbackrest/archive/main/archive.info'\n"
		fail   = "2026-10-10 13:05:17.000 UTC [999] LOG:  archive command failed with exit code 1\n"
		detail = "2026-10-10 13:05:17.000 UTC [999] DETAIL:  The failed archive command was: cp pg_wal/000000010000000000000001 /mnt/arch/000000010000000000000001\n"
	)
	var b strings.Builder
	for b.Len()+len(filler) < at-2*len(bare) {
		b.WriteString(filler)
	}
	for range 4 {
		b.WriteString(bare)
	}
	b.WriteString(fail + detail)
	for b.Len() < total {
		b.WriteString(filler)
	}
	return b.String()
}

// sameEntries fails when two parses differ, pgBackRest markers included.
func sameEntries(t *testing.T, what string, want, got []LogEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d entries, sequential had %d", what, len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if !w.Timestamp.Equal(g.Timestamp) || w.Message != g.Message || w.PID != g.PID ||
			w.IsContinuation != g.IsContinuation || w.FromPgBackRest != g.FromPgBackRest ||
			(w.PgBackRest == nil) != (g.PgBackRest == nil) || (w.PgBackRest != nil && *w.PgBackRest != *g.PgBackRest) {
			t.Fatalf("%s: entry %d differs\nseq: %+v\ngot: %+v", what, i, w, g)
		}
	}
}

// TestStderrParallel_BareLinesAcrossSegmentBoundary pins that bare lines
// held across a segment or chunk boundary parse as sequentially.
func TestStderrParallel_BareLinesAcrossSegmentBoundary(t *testing.T) {
	seqOf := func(data string) []LogEntry {
		return collectEntries(t, func(out chan<- []LogEntry) error {
			return (&StderrParser{}).parseReader(strings.NewReader(data), out)
		})
	}

	data := bareLinesAcross(stderrSegmentSize, stderrSegmentSize+1<<20)
	seq := seqOf(data)
	for _, e := range seq {
		if e.FromPgBackRest {
			t.Fatalf("sequential parse carved %+v; the DETAIL names another program", e)
		}
	}
	path := filepath.Join(t.TempDir(), "bare.log")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	sameEntries(t, "segments", seq, parallelStderrForTest(t, path, 2))

	data = bareLinesAcross(streamChunkSize, streamChunkSize+1<<20)
	par := collectEntries(t, func(out chan<- []LogEntry) error {
		return (&StderrParser{}).parseStreamParallel(readOnly{strings.NewReader(data)}, 2, out)
	})
	sameEntries(t, "stream chunks", seqOf(data), par)
}

// TestSegmentCut_SplitParsesLikeWhole pins that no carve-out state crosses
// a cut segmentCut allows: both halves parse as the whole stream, which
// holds every kind of held, carved and look-alike line.
func TestSegmentCut_SplitParsesLikeWhole(t *testing.T) {
	lines := []string{
		"2026-10-10 13:00:00.000 UTC [100] LOG:  checkpoint starting: time",
		"2026-10-10 13:00:00.100 P00   INFO: archive-push command begin 2.55.1: [pg_wal/000000010000000000000001] --stanza=main",
		"2026-10-10 13:00:00.200 P00   INFO: pushed WAL file '000000010000000000000001' to the archive",
		"",
		"2026-10-10 13:00:01.000 UTC [100] LOG:  checkpoint complete",
		"ERROR: [042]: custom archive script could not reach NFS share",
		"       HINT: check the mount",
		"2026-10-10 13:00:02.000 UTC [90] LOG:  archive command failed with exit code 42",
		"2026-10-10 13:00:02.000 UTC [90] DETAIL:  The failed archive command was: /usr/local/bin/my-archiver pg_wal/000000010000000000000002",
		"2026-10-10 13:00:03.000 UTC [100] LOG:  checkpoint starting: time",
		"ERROR: [103]: unable to find a valid repository",
		"2026-10-10 13:00:04.000 UTC [90] LOG:  archive command failed with exit code 103",
		"2026-10-10 13:00:04.000 UTC [90] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000003",
		"2026-10-10 13:00:05.000 UTC [100] LOG:  checkpoint complete",
		"2026-10-10 13:00:06.000 UTC [100] LOG:  checkpoint starting: time",
		"2026-10-10 13:00:07.000 UTC [101] LOG:  duration: 1.000 ms  statement: SELECT $$",
		"P00   WARN: hello",
		pgBackRestBanner,
		"$$",
		"2026-10-10 13:00:08.000 UTC [100] LOG:  checkpoint complete",
		pgBackRestBanner,
		"P00   INFO: archive-get command begin 2.55.1: [000000010000000000000004, pg_wal/RECOVERYXLOG] --stanza=main",
		"2026-10-10 13:00:09.000 UTC [100] LOG:  checkpoint starting: time",
		"2026-10-10 13:00:10.000 UTC [101] LOG:  duration: 1.000 ms  statement: SELECT 1 -- note",
		"P00   WARN: unable to reach repo2",
		"2026-10-10 13:00:11.000 UTC [90] LOG:  archive command failed with exit code 104",
		"2026-10-10 13:00:11.000 UTC [90] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000005",
		"2026-10-10 13:00:12.000 UTC [101] LOG:  duration: 1.000 ms  statement: SELECT 2",
		pgBackRestBanner,
		"2026-10-10 13:00:13.000 P00   INFO: archive-push command begin 2.55.1: [pg_wal/000000010000000000000006] --stanza=main",
		"2026-10-10 13:00:14.000 UTC [100] LOG:  checkpoint complete",
	}
	whole := strings.Join(lines, "\n") + "\n"
	for _, path := range []string{"reader", "bytes"} {
		want := parseBothWays(t, path, whole)
		var cut segmentCut
		cuts := 0
		for k := 1; k < len(lines); k++ {
			cut.observe([]byte(lines[k-1]), true)
			if !cut.canCut([]byte(lines[k])) {
				continue
			}
			cuts++
			head := strings.Join(lines[:k], "\n") + "\n"
			tail := strings.Join(lines[k:], "\n") + "\n"
			got := append(parseBothWays(t, path, head), parseBothWays(t, path, tail)...)
			sameEntries(t, fmt.Sprintf("%s, cut before line %d", path, k), want, got)
		}
		if cuts < 5 {
			t.Errorf("%s: only %d cuts allowed", path, cuts)
		}
	}
	var cut segmentCut
	for k, l := range lines[:8] {
		cut.observe([]byte(l), true)
		if k >= 4 && cut.canCut([]byte(lines[k+1])) {
			t.Errorf("cut allowed before line %d, inside held bare lines or right after the failure", k+1)
		}
	}
}
