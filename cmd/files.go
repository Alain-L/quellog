// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"log"
	"os"
	"path/filepath"
)

// collectFiles gathers all log files from the provided arguments.
// Arguments can be:
//   - Individual files
//   - Glob patterns (e.g., "*.log")
//   - Directories (scans for .log files, non-recursive)
func collectFiles(args []string) []string {
	var files []string

	for _, arg := range args {
		// Check if argument is a directory
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			// Scan directory for .log files
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

// gatherLogFiles scans a directory for .log files (non-recursive).
// Only files with the .log extension are returned.
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

		// Only include .log files
		if filepath.Ext(entry.Name()) == ".log" {
			logFiles = append(logFiles, filepath.Join(dir, entry.Name()))
		}
	}

	return logFiles, nil
}
