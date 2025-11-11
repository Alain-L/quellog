// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// collectFiles gathers all log files from the provided arguments.
// Arguments can be:
//   - Individual files
//   - Glob patterns (e.g., "*.log")
//   - Directories (scans for supported log files, non-recursive)
func collectFiles(args []string) []string {
	var files []string

	for _, arg := range args {
		// Check if argument is a directory
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			// Scan directory for supported log files
			dirFiles, err := gatherLogFiles(arg)
			if err != nil {
				log.Printf("[WARN] Failed to read directory %s: %v", arg, err)
				continue
			}
			files = append(files, dirFiles...)
			continue
		}

		// Try to expand as glob pattern
		matches, err := filepath.Glob(arg)
		if err != nil {
			log.Printf("[WARN] Invalid pattern %s: %v", arg, err)
			continue
		}

		if len(matches) == 0 {
			log.Printf("[WARN] No files match pattern: %s", arg)
			continue
		}

		files = append(files, matches...)
	}

	return files
}

// gatherLogFiles scans a directory for supported log files (non-recursive).
func gatherLogFiles(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	var logFiles []string
	for _, entry := range entries {
		// Skip subdirectories
		if entry.IsDir() {
			continue
		}

		if isSupportedLogFile(entry.Name()) {
			logFiles = append(logFiles, filepath.Join(dir, entry.Name()))
		}
	}

	return logFiles, nil
}

// isSupportedLogFile reports whether the file name looks like a supported log format.
// Accepted extensions:
//   - .log, .csv, .json
//   - .log.gz, .csv.gz, .json.gz
//   - .log.zst, .log.zstd, .csv.zst, .csv.zstd, .json.zst, .json.zstd
//   - .tar, .tar.gz, .tgz, .tar.zst, .tar.zstd, .tzst
func isSupportedLogFile(name string) bool {
	lower := strings.ToLower(name)
	supported := []string{
		".log",
		".csv",
		".json",
		".log.gz",
		".csv.gz",
		".json.gz",
		".log.zst",
		".log.zstd",
		".csv.zst",
		".csv.zstd",
		".json.zst",
		".json.zstd",
		".tar",
		".tar.gz",
		".tgz",
		".tar.zst",
		".tar.zstd",
		".tzst",
	}

	for _, ext := range supported {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
