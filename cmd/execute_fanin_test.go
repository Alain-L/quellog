package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// TestParseFilesAsyncOrderedFanInNoDeadlock guards the ordered multi-file
// fan-in against R1: a regression where the shared window semaphore was
// acquired by workers out of index order but released by the drain in strict
// index order, so with >2 workers the lowest un-drained index could block
// before parsing while higher indices held both slots — a circular wait that
// hung the pipeline with no output and no error.
//
// The fix replaces the shared semaphore with per-index start permits (gate[]).
// This test drives the real parseFilesAsync over several small files with a
// worker pool larger than the 2-slot window, under a watchdog, and asserts it
// completes AND that the fan-in preserves file-list order. It is deterministic
// only for the fixed code; the deadlock proof for the buggy code lives in the
// scheduling model. Run under -race to also catch data races on the gates.
func TestParseFilesAsyncOrderedFanInNoDeadlock(t *testing.T) {
	// Force a worker pool wider than the 2-file window so the ordered fan-in
	// (numWorkers != 1) path is exercised with contention on the gates.
	t.Setenv("QUELLOG_WORKERS", "5")

	dir := t.TempDir()
	const n = 5
	var files []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("postgresql-%d.log", i))
		// Several identical stderr lines so format detection is unambiguous;
		// the "SELECT <i>;" marker identifies which file each entry came from.
		var b strings.Builder
		for l := 0; l < 4; l++ {
			fmt.Fprintf(&b, "2025-01-15 10:00:%02d.100 UTC [10%02d] LOG:  duration: 1.000 ms  statement: SELECT %d;\n", l, i, i)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	// Sanity: the fixed code must take the multi-file worker-pool branch.
	if got := determineWorkerCount(files); got <= 2 {
		t.Fatalf("determineWorkerCount = %d, want > 2 to exercise the ordered fan-in", got)
	}

	out := make(chan []parser.LogEntry, 64)
	var parsedAny atomic.Bool
	done := make(chan struct{})
	var messages []string
	go func() {
		defer close(done)
		// pb is nil: all progressBar methods are nil-safe no-ops.
		go parseFilesAsync(context.Background(), files, out, &parsedAny, nil)
		for batch := range out {
			for _, e := range batch {
				messages = append(messages, e.Message)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parseFilesAsync deadlocked: ordered fan-in never completed")
	}

	if !parsedAny.Load() {
		t.Fatal("parseFilesAsync reported no files parsed")
	}

	// The ordered fan-in guarantees each file's entries arrive contiguously in
	// file-list order, so the first appearance of each marker must be exactly
	// 0,1,2,3,4 in that sequence.
	var order []int
	seen := make(map[int]bool)
	for _, m := range messages {
		for i := 0; i < n; i++ {
			if strings.Contains(m, fmt.Sprintf("SELECT %d;", i)) {
				if !seen[i] {
					seen[i] = true
					order = append(order, i)
				}
				break
			}
		}
	}
	want := []int{0, 1, 2, 3, 4}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("fan-in marker order = %v, want %v (file order not preserved)", order, want)
	}
}

// TestShardDecisionUsesExpandedFiles guards R4 finding 11: the analysis
// PID-shard decision was made from the RAW command-line args. A directory (or
// glob) argument stats to a tiny inode size, always falling below the 256 MB
// gate, so `quellog /var/log/postgresql/` never sharded even when the directory
// held gigabytes of plain stderr. The fix threads the EXPANDED file list
// (collectFiles) into shardWorkers, which is what runAnalysisCycle now does.
//
// The test builds a directory of two large (sparse) plain-stderr logs whose
// combined size crosses the gate and asserts:
//   - the raw directory arg estimates to a tiny inode and does NOT shard,
//   - the expanded file list estimates to the real byte volume and DOES shard,
//   - the estimate over the expansion equals the sum of the per-file estimates,
//   - a plain-file arg is unaffected (no double-expansion).
func TestShardDecisionUsesExpandedFiles(t *testing.T) {
	os.Unsetenv("QUELLOG_SHARD_WORKERS")
	os.Unsetenv("QUELLOG_WORKERS")

	const minSize = 256 << 20 // must mirror shardWorkers' gate

	dir := t.TempDir()
	head := strings.Repeat(
		"2025-01-15 10:00:00.100 UTC [1234] LOG:  duration: 1.000 ms  statement: SELECT 1;\n",
		4000)
	var files []string
	for _, name := range []string{"postgresql-a.log", "postgresql-b.log"} {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(head); err != nil {
			t.Fatal(err)
		}
		// Sparse extend to 200 MB logical (~0 real disk); os.Stat reports the
		// logical size, so two files clear the 256 MB gate. The head keeps the
		// format detectable as plain, non-syslog stderr (PID-shardable).
		if err := f.Truncate(200 << 20); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	// The raw directory arg is the buggy path: a tiny inode below the gate.
	dirSize := estimatedDecompressedSize([]string{dir})
	if dirSize >= minSize {
		t.Fatalf("directory arg estimate = %d, expected a tiny inode below the %d gate", dirSize, minSize)
	}
	if got := shardWorkers([]string{dir}); got != 1 {
		t.Fatalf("shardWorkers(raw dir) = %d, want 1 (the pre-fix behavior)", got)
	}

	// The expanded file list is the fixed path (what runAnalysisCycle feeds in).
	expanded := collectFiles([]string{dir})
	if !reflect.DeepEqual(expanded, files) {
		t.Fatalf("collectFiles(dir) = %v, want %v", expanded, files)
	}
	expSize := estimatedDecompressedSize(expanded)
	if expSize < minSize {
		t.Fatalf("expanded estimate = %d, expected >= %d gate", expSize, minSize)
	}
	if got := shardWorkers(expanded); got <= 1 {
		t.Fatalf("shardWorkers(expanded) = %d, want > 1 (sharding must now fire for a directory)", got)
	}

	// The estimate over the expansion equals the sum of the per-file estimates.
	var sum int64
	for _, f := range files {
		sum += estimatedDecompressedSize([]string{f})
	}
	if expSize != sum {
		t.Fatalf("estimatedDecompressedSize(expanded) = %d, want sum-of-files %d", expSize, sum)
	}

	// A plain-file arg is not a directory or glob: collectFiles maps it to
	// itself, so raw and expanded estimates match (no double-expansion).
	single := files[0]
	if raw, exp := estimatedDecompressedSize([]string{single}), estimatedDecompressedSize(collectFiles([]string{single})); raw != exp {
		t.Fatalf("plain-file estimate changed under expansion: raw=%d expanded=%d", raw, exp)
	}
}

// csvRecord returns one valid PostgreSQL csvlog record (28 fields) whose
// message carries a "SELECT <marker>;" tag, so per-file entries stay
// identifiable after the CSV parser folds the message field into LogEntry.Message.
func csvRecord(sec, marker int) string {
	return fmt.Sprintf(
		"2025-01-15 10:00:%02d.100 UTC,,,55,,692cb2bb.37,%d,,2025-01-15 10:00:00 UTC,,0,LOG,00000,\"duration: 1.000 ms  statement: SELECT %d;\",,,,,,,,,\"\",\"client backend\",,0\n",
		sec, sec+1, marker)
}

// TestFanInWindowByFormat guards R2: the ordered multi-file fan-in throttled
// EVERY input set to 2 files in flight. That is correct only for self-parallel
// stderr, where each file already saturates the CPU through its own intra-file
// parallelism. Compressed CSV, compressed JSON and prefixed compressed stderr
// all fall back to single-goroutine parsing, so a rotation of e.g. *.csv.gz ran
// 2 concurrent parses where v0.11.0 ran up to numWorkers — a several-fold
// slowdown with the other workers idle on their gates.
//
// The fix sizes the window by format (fanInWindow). This asserts the decision
// directly — no heavy parsing — for both regimes.
func TestFanInWindowByFormat(t *testing.T) {
	dir := t.TempDir()

	writeFiles := func(prefix, ext, content string, n int) []string {
		var files []string
		for i := 0; i < n; i++ {
			p := filepath.Join(dir, fmt.Sprintf("%s-%d.%s", prefix, i, ext))
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			files = append(files, p)
		}
		return files
	}

	// Valid plain, non-syslog stderr: SupportsPIDSharding is true, so these take
	// the self-parallel segment engine and must keep the small RSS-bounding window.
	var stderrBuf strings.Builder
	for l := 0; l < 4; l++ {
		fmt.Fprintf(&stderrBuf, "2025-01-15 10:00:%02d.100 UTC [10%02d] LOG:  duration: 1.000 ms  statement: SELECT 1;\n", l, l)
	}
	stderrFiles := writeFiles("stderr", "log", stderrBuf.String(), 4)

	// Valid plain CSV: SupportsPIDSharding is false (CSV parses on a single
	// goroutine), so up to numWorkers files must be allowed to run concurrently.
	var csvBuf strings.Builder
	for l := 0; l < 4; l++ {
		csvBuf.WriteString(csvRecord(l, 1))
	}
	csvFiles := writeFiles("csvlog", "csv", csvBuf.String(), 4)

	const numWorkers = 6

	// Sanity: window sizing keys off exactly this classification.
	for _, f := range stderrFiles {
		if !parser.SupportsPIDSharding(f) {
			t.Fatalf("expected %s to be self-parallel stderr (SupportsPIDSharding true)", f)
		}
	}
	for _, f := range csvFiles {
		if parser.SupportsPIDSharding(f) {
			t.Fatalf("expected %s to be single-goroutine (SupportsPIDSharding false)", f)
		}
	}

	// A genuinely large plain stderr file, above the stream-parallel size gate.
	// ~1 MB of real stderr content at the head (so format detection samples
	// stderr, not the sparse tail), then extended sparse to 128 MB so os.Stat
	// clears the gate without writing 128 MB. This one self-parallelizes.
	largeStderr := filepath.Join(dir, "large-stderr.log")
	if err := os.WriteFile(largeStderr, []byte(strings.Repeat(stderrBuf.String(), 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(largeStderr, 128<<20); err != nil {
		t.Fatal(err)
	}

	// Small stderr and small CSV are all BELOW the gate: they parse on a single
	// goroutine, so the window must open to numWorkers (v0.11.0 throughput).
	// This is the R2 fix and the RSS size-gate working together — a rotation of
	// small compressed/plain logs is not throttled to 2.
	if got := fanInWindow(stderrFiles, numWorkers); got != numWorkers {
		t.Fatalf("fanInWindow(small stderr) = %d, want %d (below the gate: parses sequentially, must not be throttled)", got, numWorkers)
	}
	if got := fanInWindow(csvFiles, numWorkers); got != numWorkers {
		t.Fatalf("fanInWindow(csv) = %d, want %d (single-goroutine inputs must not be throttled)", got, numWorkers)
	}

	// The large stderr file self-parallelizes (chunked segment engine), so the
	// window stays small to bound RSS; a mixed set containing it is pulled down.
	if got := fanInWindow([]string{largeStderr}, numWorkers); got != smallFanInWindow {
		t.Fatalf("fanInWindow(large stderr) = %d, want %d (above the gate: self-parallel, small window bounds RSS)", got, smallFanInWindow)
	}
	mixed := append(append([]string{}, csvFiles...), largeStderr)
	if got := fanInWindow(mixed, numWorkers); got != smallFanInWindow {
		t.Fatalf("fanInWindow(mixed with large stderr) = %d, want %d (a self-parallel file forces the small window)", got, smallFanInWindow)
	}
}

// TestParseFilesAsyncNumWorkersWindowNoDeadlock drives the real fan-in over a
// single-goroutine (CSV) input set wide enough that the numWorkers window is
// smaller than the file count, so the drain must post the later gates
// (gate[i+window]) as it advances. It asserts the generalized window neither
// deadlocks nor reorders output — the deadlock-freedom the per-index gates give
// the small window must survive the larger window too.
func TestParseFilesAsyncNumWorkersWindowNoDeadlock(t *testing.T) {
	// Force a pool of 5 over 8 CSV files: fanInWindow opens to numWorkers (5),
	// which is < the file count, exercising the drain's gate-posting path.
	t.Setenv("QUELLOG_WORKERS", "5")

	dir := t.TempDir()
	const n = 8
	var files []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("postgresql-%d.csv", i))
		var b strings.Builder
		for l := 0; l < 4; l++ {
			b.WriteString(csvRecord(l, i))
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	numWorkers := determineWorkerCount(files)
	if numWorkers <= 1 {
		t.Fatalf("determineWorkerCount = %d, want > 1 to exercise the ordered fan-in", numWorkers)
	}
	if got := fanInWindow(files, numWorkers); got != numWorkers {
		t.Fatalf("fanInWindow = %d, want %d (numWorkers window for single-goroutine CSV)", got, numWorkers)
	}
	if numWorkers >= n {
		t.Fatalf("numWorkers = %d, want < %d so the drain must post later gates", numWorkers, n)
	}

	out := make(chan []parser.LogEntry, 64)
	var parsedAny atomic.Bool
	done := make(chan struct{})
	var messages []string
	go func() {
		defer close(done)
		// pb is nil: all progressBar methods are nil-safe no-ops.
		go parseFilesAsync(context.Background(), files, out, &parsedAny, nil)
		for batch := range out {
			for _, e := range batch {
				messages = append(messages, e.Message)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parseFilesAsync deadlocked: numWorkers-window fan-in never completed")
	}

	if !parsedAny.Load() {
		t.Fatal("parseFilesAsync reported no files parsed")
	}

	// The ordered fan-in guarantees each file's entries arrive contiguously in
	// file-list order, so the first appearance of each marker must be 0..7.
	var order []int
	seen := make(map[int]bool)
	for _, m := range messages {
		for i := 0; i < n; i++ {
			if strings.Contains(m, fmt.Sprintf("SELECT %d;", i)) {
				if !seen[i] {
					seen[i] = true
					order = append(order, i)
				}
				break
			}
		}
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("fan-in marker order = %v, want %v (file order not preserved at numWorkers window)", order, want)
	}
}
