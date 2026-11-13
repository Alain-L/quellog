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
type ZipParser struct{ track ArchiveTrack }

// Parse reads a ZIP archive and parses any supported log files inside it.
func (p *ZipParser) Parse(filename string, out chan<- []LogEntry) error {
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

		out <- []LogEntry{PgBackRestSourceMarker(entryName, true)}
		if err := parseArchiveEntry(baseName, rc, out, p.track); err != nil {
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

// walkMembers implements memberWalker: it considers exactly the members
// Parse parses.
func (p *ZipParser) walkMembers(filename string, visit func(name, format string)) {
	zr, err := zip.OpenReader(filename)
	if err != nil {
		return
	}
	defer zr.Close()

	for _, f := range zr.File {
		visit(f.Name, memberFormat(f.Name, uint64(f.UncompressedSize64), f.FileInfo().IsDir(), f.Open))
	}
}

// innerFormats implements formatWrapper.
func (p *ZipParser) innerFormats(filename string) []string { return memberFormats(p, filename) }
