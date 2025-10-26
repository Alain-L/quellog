// Package cmd implements the command-line interface for quellog.
package cmd

import "runtime"

// determineWorkerCount calculates the optimal number of parallel workers
// for parsing log files based on the number of files and available CPU cores.
//
// Strategy:
//   - Single file: No parallelism needed (returns 1)
//   - Multiple files: Use up to NumCPU/2 workers to avoid contention
//   - Maximum: Cap at 4 workers to prevent excessive context switching
//   - Never create more workers than files
func determineWorkerCount(numFiles int) int {
	if numFiles == 1 {
		return 1 // Single file doesn't benefit from parallelism
	}

	// Use half of available CPU cores to leave room for other goroutines
	// (filtering, analysis, etc.)
	maxWorkers := runtime.NumCPU() / 2
	if maxWorkers < 2 {
		maxWorkers = 2 // Minimum of 2 workers for parallel processing
	}
	if maxWorkers > 4 {
		maxWorkers = 4 // Cap at 4 to avoid excessive contention
	}

	// Don't create more workers than we have files
	if numFiles < maxWorkers {
		return numFiles
	}

	return maxWorkers
}
