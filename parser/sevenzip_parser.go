//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

// SevenZipParser extracts supported log files from 7z archives and streams entries.
type SevenZipParser struct{}

// Parse reads a 7z archive and parses any supported log files inside it.
func (p *SevenZipParser) Parse(filename string, out chan<- LogEntry) error {
	r, err := sevenzip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("failed to open 7z archive %s: %w", filename, err)
	}
	defer r.Close()

	for _, f := range r.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Skip empty files
		if f.UncompressedSize == 0 {
			continue
		}

		entryName := f.Name

		// Path traversal protection
		if strings.Contains(entryName, "..") {
			log.Printf("[WARN] Skipping 7z entry with suspicious path: %s", entryName)
			continue
		}

		// Use only the base filename for extension matching
		baseName := filepath.Base(entryName)

		if !isSupportedArchiveEntry(baseName) {
			log.Printf("[INFO] Skipping unsupported file %s in archive %s", entryName, filename)
			continue
		}

		rc, err := f.Open()
		if err != nil {
			log.Printf("[ERROR] Failed to open %s in archive %s: %v", entryName, filename, err)
			continue
		}

		if err := parseArchiveEntry(baseName, rc, out); err != nil {
			if errors.Is(err, errUnsupportedArchiveEntry) {
				log.Printf("[WARN] Unsupported log format %s in archive %s", entryName, filename)
			} else {
				log.Printf("[ERROR] Failed to parse %s in archive %s: %v", entryName, filename, err)
			}
		}

		rc.Close()
	}

	return nil
}
