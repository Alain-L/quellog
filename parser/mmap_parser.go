//go:build (linux || darwin) && !wasm
// +build linux darwin
// +build !wasm

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// MmapStderrParser parses PostgreSQL logs using memory-mapped I/O.
// This eliminates syscall overhead by mapping the file directly into memory.
//
// Performance characteristics:
//   - No read() syscalls (kernel handles page faults)
//   - Sequential access benefits from kernel prefetching
//   - ~3% faster on large files (>10GB)
//
// Robustness:
//   - Automatically falls back to buffered I/O if mmap fails
//   - Handles special files (pipes, network filesystems, etc.)
type MmapStderrParser struct{}

// Parse reads a PostgreSQL stderr/syslog format log file using mmap.
// If mmap fails (network filesystem, special file, permissions, etc.),
// it automatically falls back to buffered I/O parsing.
func (p *MmapStderrParser) Parse(filename string, out chan<- LogEntry) error {
	// Try mmap first
	err := p.parseWithMmap(filename, out)
	if err != nil {
		// mmap failed, fallback to buffered I/O
		// This handles: pipes, network filesystems, special files, etc.
		fallbackParser := &StderrParser{}
		return fallbackParser.Parse(filename, out)
	}
	return nil
}

// parseWithMmap attempts to parse the file using memory-mapped I/O.
// Returns an error if mmap fails, triggering fallback to buffered I/O.
func (p *MmapStderrParser) parseWithMmap(filename string, out chan<- LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer file.Close()

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file %s: %w", filename, err)
	}
	size := stat.Size()

	// Handle empty files
	if size == 0 {
		return nil
	}

	// Memory-map the file
	data, err := syscall.Mmap(int(file.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		// mmap failed (could be network filesystem, pipe, etc.)
		return fmt.Errorf("mmap failed: %w", err)
	}
	defer syscall.Munmap(data)

	// Parse the mapped data line by line (optimized version with zero-copy byte slicing)
	return parseMmapDataOptimized(data, out)
}

// parseMmapData parses log data from a memory-mapped buffer.
// It scans for newlines and assembles multi-line entries.
func parseMmapData(data []byte, out chan<- LogEntry) error {
	var currentEntry strings.Builder
	currentEntry.Grow(1024) // Pre-allocate for typical log line

	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			// Extract line (without newline)
			line := string(data[start:i])
			start = i + 1

			// Handle syslog tab markers
			if idx := strings.Index(line, syslogTabMarker); idx != -1 {
				line = " " + line[idx+len(syslogTabMarker):]
			}

			// Check if this is a continuation line
			// Fast path: starts with whitespace (most continuation lines)
			isContinuation := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')

			// Fallback: if not indented AND we have a current entry, check for timestamp
			// This handles cases like GCP where SQL continuation lines are not indented
			if !isContinuation && len(line) > 0 && currentEntry.Len() > 0 {
				// Fast path: check if line could possibly start a log entry
				if line[0] >= '0' && line[0] <= '9' || line[0] == '[' {
					// Might be a new log entry - verify with full parsing
					timestamp, _ := parseStderrLine(line)
					if timestamp.IsZero() {
						// No valid timestamp = continuation line
						isContinuation = true
					}
				} else {
					// Doesn't start with digit/bracket = definitely continuation
					isContinuation = true
				}
			}

			if isContinuation {
				// Append to current entry
				if currentEntry.Len() > 0 {
					currentEntry.WriteByte(' ')
				}
				currentEntry.WriteString(strings.TrimSpace(line))
			} else {
				// This is a new entry, process the previous one
				if currentEntry.Len() > 0 {
					timestamp, message := parseStderrLine(currentEntry.String())
					out <- LogEntry{Timestamp: timestamp, Message: message}
					currentEntry.Reset()
				}
				// Start accumulating new entry
				currentEntry.WriteString(line)
			}
		}
	}

	// Handle last line if file doesn't end with newline
	if start < len(data) {
		line := string(data[start:])
		if len(line) > 0 {
			// Check if this is a continuation line
			isContinuation := line[0] == ' ' || line[0] == '\t'

			// Fallback: if not indented AND we have a current entry, check for timestamp
			if !isContinuation && currentEntry.Len() > 0 {
				// Fast path: check if line could possibly start a log entry
				if line[0] >= '0' && line[0] <= '9' || line[0] == '[' {
					timestamp, _ := parseStderrLine(line)
					if timestamp.IsZero() {
						isContinuation = true
					}
				} else {
					// Doesn't start with digit/bracket = definitely continuation
					isContinuation = true
				}
			}

			if isContinuation {
				if currentEntry.Len() > 0 {
					currentEntry.WriteByte(' ')
				}
				currentEntry.WriteString(strings.TrimSpace(line))
			} else {
				if currentEntry.Len() > 0 {
					timestamp, message := parseStderrLine(currentEntry.String())
					out <- LogEntry{Timestamp: timestamp, Message: message}
					currentEntry.Reset()
				}
				currentEntry.WriteString(line)
			}
		}
	}

	// Process final accumulated entry
	if currentEntry.Len() > 0 {
		timestamp, message := parseStderrLine(currentEntry.String())
		out <- LogEntry{Timestamp: timestamp, Message: message}
	}

	return nil
}

// parseMmapDataOptimized is an optimized version using byte slicing instead of string conversions.
// This reduces allocations by working directly with byte slices.
func parseMmapDataOptimized(data []byte, out chan<- LogEntry) error {
	var currentEntry []byte
	currentEntry = make([]byte, 0, 1024) // Pre-allocate

	start := 0
	// OPTIMIZATION: Use bytes.IndexByte to jump directly to newlines
	// instead of scanning byte-by-byte. This is ~10x faster for finding '\n'.
	for start < len(data) {
		// Find next newline
		i := bytes.IndexByte(data[start:], '\n')
		if i < 0 {
			// No more newlines, handle remaining data
			break
		}

		// Extract line (without newline)
		i += start // Convert relative index to absolute
		line := data[start:i]
		start = i + 1

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// Check if this is a continuation line
		// Fast path: starts with whitespace (most continuation lines)
		isContinuation := line[0] == ' ' || line[0] == '\t'

		// Fallback: if not indented AND we have a current entry, check for timestamp
		// This handles cases like GCP where SQL continuation lines are not indented
		if !isContinuation && len(currentEntry) > 0 {
			// Fast path: check if line could possibly start a log entry
			// Most log entries start with: digit (timestamp) or '[' (bracket timestamp)
			// This avoids expensive parseStderrLineBytes call for obvious continuation lines
			if line[0] >= '0' && line[0] <= '9' || line[0] == '[' {
				// Might be a new log entry - verify with full parsing
				timestamp, _ := parseStderrLineBytes(line)
				if timestamp.IsZero() {
					// No valid timestamp = continuation line
					isContinuation = true
				}
			} else {
				// Doesn't start with digit/bracket = definitely continuation
				isContinuation = true
			}
		}

		if isContinuation {
			// Append to current entry
			if len(currentEntry) > 0 {
				currentEntry = append(currentEntry, ' ')
			}
			currentEntry = append(currentEntry, bytes.TrimSpace(line)...)
		} else {
			// This is a new entry, process the previous one
			if len(currentEntry) > 0 {
				timestamp, message := parseStderrLineBytes(currentEntry)
				out <- LogEntry{Timestamp: timestamp, Message: message}
				currentEntry = currentEntry[:0] // Reset but keep capacity
			}
			// Start accumulating new entry
			currentEntry = append(currentEntry[:0], line...)
		}
	}

	// Handle last line if file doesn't end with newline
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			// Check if this is a continuation line
			isContinuation := line[0] == ' ' || line[0] == '\t'

			// Fallback: if not indented AND we have a current entry, check for timestamp
			if !isContinuation && len(currentEntry) > 0 {
				// Fast path: check if line could possibly start a log entry
				if line[0] >= '0' && line[0] <= '9' || line[0] == '[' {
					timestamp, _ := parseStderrLineBytes(line)
					if timestamp.IsZero() {
						isContinuation = true
					}
				} else {
					// Doesn't start with digit/bracket = definitely continuation
					isContinuation = true
				}
			}

			if isContinuation {
				if len(currentEntry) > 0 {
					currentEntry = append(currentEntry, ' ')
				}
				currentEntry = append(currentEntry, bytes.TrimSpace(line)...)
			} else {
				if len(currentEntry) > 0 {
					timestamp, message := parseStderrLineBytes(currentEntry)
					out <- LogEntry{Timestamp: timestamp, Message: message}
					currentEntry = currentEntry[:0]
				}
				currentEntry = append(currentEntry[:0], line...)
			}
		}
	}

	// Process final accumulated entry
	if len(currentEntry) > 0 {
		timestamp, message := parseStderrLineBytes(currentEntry)
		out <- LogEntry{Timestamp: timestamp, Message: message}
	}

	return nil
}

// parseStderrLineBytes is the byte-slice version of parseStderrLine.
// It converts to string only at the last moment to reduce allocations.
func parseStderrLineBytes(line []byte) (time.Time, string) {
	// For now, convert to string and reuse existing parser
	// TODO: Could be optimized further by parsing directly from bytes
	return parseStderrLine(string(line))
}
