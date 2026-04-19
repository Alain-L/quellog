//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"archive/zip"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// ZipParser extracts supported log files from ZIP archives and streams entries.
type ZipParser struct{}

// Parse reads a ZIP archive and parses any supported log files inside it.
func (p *ZipParser) Parse(filename string, out chan<- LogEntry) error {
	zr, err := zip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("failed to open zip archive %s: %w", filename, err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Skip empty files
		if f.UncompressedSize64 == 0 {
			continue
		}

		entryName := f.Name

		// Path traversal protection
		if strings.Contains(entryName, "..") {
			slog.Warn("skipping zip entry with suspicious path", "entry", entryName)
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
