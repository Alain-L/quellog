// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"os"
	"runtime"
	"strconv"

	"github.com/Alain-L/quellog/parser"
)

// rawParallelismThreshold is the per-file size above which uncompressed
// log files start benefiting from inter-file parallelism. Below it, the
// added pthread_cond_signal contention from multiple producers on the
// shared rawLogs channel costs more than the parsing it saves.
//
// Calibrated against five corpora (April 2026):
//   - wasac (24 × 4 MB raw):       parallelism neutral
//   - ensam (9 × 190 MB raw):      parallelism -8% (worse)
//   - agirc-itesoft (19 × 250 MB): parallelism -2% (worse)
//   - H.log split (6 × 1.8 GB):    parallelism -26% (better)
//   - Z (7 × 440 MB compressed):   parallelism -46% (better)
//
// 500 MB sits comfortably above the agirc median and below the H.log
// fragments, capturing the observed crossover.
const rawParallelismThreshold = 500 * 1024 * 1024

// determineWorkerCount picks a worker pool size for parsing the given
// input files. The decision is made up-front from the file list because
// the right answer depends on:
//
//   - Whether the parser is CPU-bound (compressed: gzip/zstd/zip/7z/tar)
//     or stream-bound (raw .log/.csv/.json on mmap or bufio.Scanner).
//   - The average per-file size for raw inputs — small/medium raw files
//     suffer from channel contention when parsed in parallel; large
//     ones overcome that overhead.
//
// Heuristic:
//   - Single file → 1 worker (no pool needed).
//   - All inputs compressed → up to min(NumCPU, numFiles, 8). Decompression
//     is CPU-isolated per file and scales linearly.
//   - Mixed or all-raw with avg file size > rawParallelismThreshold →
//     min(NumCPU/2, numFiles, 4). Conservative cap because the
//     scheduler/channel costs grow faster than the parsing wins.
//   - Otherwise → 1 worker. Serial wins on logs with many small/medium raw
//     files (most production rotation patterns: 24 hourly files, etc.).
//
// The QUELLOG_WORKERS env var, if set to a positive integer, overrides
// the heuristic — useful when re-tuning or working around an outlier.
func determineWorkerCount(files []string) int {
	if v := os.Getenv("QUELLOG_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > len(files) {
				return len(files)
			}
			return n
		}
	}

	if len(files) <= 1 {
		return 1
	}

	totalSize := int64(0)
	compressedCount := 0
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			totalSize += st.Size()
		}
		if parser.IsCompressed(f) {
			compressedCount++
		}
	}

	cpu := runtime.NumCPU()

	// All compressed: CPU-bound decompression scales near-linearly.
	if compressedCount == len(files) {
		return capWorkers(cpu, len(files), 8)
	}

	// Mixed or raw: parallelism only pays off on large per-file sizes.
	if totalSize > 0 {
		avgSize := totalSize / int64(len(files))
		if avgSize > rawParallelismThreshold {
			return capWorkers(cpu/2, len(files), 4)
		}
	}

	return 1
}

// capWorkers returns max(1, min(want, fileCount, ceiling)). Wraps the
// repetitive bound logic so the heuristic above stays readable.
func capWorkers(want, fileCount, ceiling int) int {
	if want < 1 {
		want = 1
	}
	if want > ceiling {
		want = ceiling
	}
	if want > fileCount {
		want = fileCount
	}
	return want
}
