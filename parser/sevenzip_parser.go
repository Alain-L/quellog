//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"errors"
	"fmt"
	"log/slog"
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
			slog.Warn("skipping 7z entry with suspicious path", "entry", entryName)
			continue
		}

		// Use only the base filename for extension matching
		baseName := filepath.Base(entryName)

		if !isSupportedArchiveEntry(baseName) {
			slog.Info("skipping unsupported file in archive", "entry", entryName, "archive", filename)
			continue
		}

		rc, err := f.Open()
		if err != nil {
			slog.Error("failed to open entry in archive", "entry", entryName, "archive", filename, "err", err)
			continue
		}

		if err := parseArchiveEntry(baseName, rc, out); err != nil {
			if errors.Is(err, errUnsupportedArchiveEntry) {
				slog.Warn("unsupported log format in archive", "entry", entryName, "archive", filename)
			} else {
				slog.Error("failed to parse entry in archive", "entry", entryName, "archive", filename, "err", err)
			}
		}

		rc.Close()
	}

	return nil
}
