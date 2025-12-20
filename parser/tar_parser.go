//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

var errUnsupportedTarEntry = errors.New("unsupported tar entry")

// TarParser extracts supported log files from tar or tar.gz archives and streams entries.
type TarParser struct{}

// Parse reads a tar or tar.gz archive and parses any supported log files inside it.
func (p *TarParser) Parse(filename string, out chan<- LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open tar archive %s: %w", filename, err)
	}
	defer file.Close()

	var reader io.Reader = file
	var closer io.Closer

	if isGzipArchive(filename) {
		gr, gzipErr := newParallelGzipReader(file)
		if gzipErr != nil {
			return fmt.Errorf("failed to open gzip reader for tar archive %s: %w", filename, gzipErr)
		}
		reader = gr
		closer = gr
	} else if isZstdArchive(filename) {
		zr, zstdErr := newZstdDecoder(file)
		if zstdErr != nil {
			return fmt.Errorf("failed to open zstd reader for tar archive %s: %w", filename, zstdErr)
		}
		reader = zr
		closer = zr
	}

	if closer != nil {
		defer closer.Close()
	}

	tr := tar.NewReader(reader)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading archive %s: %w", filename, err)
		}

		if hdr == nil {
			continue
		}

		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}

		if hdr.Size == 0 {
			continue
		}

		entryName := hdr.Name
		entryReader := io.LimitReader(tr, hdr.Size)

		if !isSupportedTarEntry(entryName) {
			// Drain entry to reach next header.
			if _, err := io.Copy(io.Discard, entryReader); err != nil {
				return fmt.Errorf("discarding unsupported entry %s in %s: %w", entryName, filename, err)
			}
			log.Printf("[INFO] Skipping unsupported file %s in archive %s", entryName, filename)
			continue
		}

		if err := parseTarEntry(entryName, entryReader, out); err != nil {
			if errors.Is(err, errUnsupportedTarEntry) {
				log.Printf("[WARN] Unsupported log format %s in archive %s", entryName, filename)
			} else {
				log.Printf("[ERROR] Failed to parse %s in archive %s: %v", entryName, filename, err)
			}
		}

		// Ensure the remainder of the entry is consumed.
		if _, err := io.Copy(io.Discard, entryReader); err != nil {
			return fmt.Errorf("draining entry %s in %s: %w", entryName, filename, err)
		}
	}

	return nil
}

// isSupportedTarEntry reports whether the archive entry should be parsed.
func isSupportedTarEntry(name string) bool {
	lower := strings.ToLower(name)
	supported := []string{
		".log",
		".csv",
		".json",
		".log.gz",
		".csv.gz",
		".json.gz",
		".log.zst",
		".csv.zst",
		".json.zst",
		".log.zstd",
		".csv.zstd",
		".json.zstd",
	}

	for _, ext := range supported {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// parseTarEntry selects the correct parser for an archive entry.
func parseTarEntry(name string, r io.Reader, out chan<- LogEntry) error {
	lower := strings.ToLower(name)

	switch {
	case strings.HasSuffix(lower, ".log"):
		parser := &StderrParser{}
		return parser.parseReader(r, out)
	case strings.HasSuffix(lower, ".csv"):
		parser := &CsvParser{}
		return parser.parseReader(r, out)
	case strings.HasSuffix(lower, ".json"):
		parser := &JsonParser{}
		return parser.parseReader(r, out)
	case strings.HasSuffix(lower, ".gz"):
		// Handle nested gzip-compressed files.
		gzReader, err := newParallelGzipReader(r)
		if err != nil {
			return fmt.Errorf("failed to decompress %s: %w", name, err)
		}
		defer gzReader.Close()

		trimmedName := name[:len(name)-3]
		return parseTarEntry(trimmedName, gzReader, out)
	case strings.HasSuffix(lower, ".zst"):
		return parseZstdTarEntry(name, r, ".zst", out)
	case strings.HasSuffix(lower, ".zstd"):
		return parseZstdTarEntry(name, r, ".zstd", out)
	default:
		return errUnsupportedTarEntry
	}
}

func parseZstdTarEntry(name string, r io.Reader, suffix string, out chan<- LogEntry) error {
	zr, err := newZstdDecoder(r)
	if err != nil {
		return fmt.Errorf("failed to decompress %s: %w", name, err)
	}
	defer zr.Close()

	trimmedName := name[:len(name)-len(suffix)]
	return parseTarEntry(trimmedName, zr, out)
}

// isGzipArchive reports whether the archive is gzip-compressed.
func isGzipArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

// isZstdArchive reports whether the archive is zstd-compressed.
func isZstdArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".tar.zst") ||
		strings.HasSuffix(lower, ".tar.zstd") ||
		strings.HasSuffix(lower, ".tzst")
}
