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
