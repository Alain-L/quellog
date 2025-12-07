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
				// Most log entries start with: digit (timestamp), '[' (bracket), or uppercase letter (syslog month)
				if line[0] >= '0' && line[0] <= '9' || line[0] == '[' || (line[0] >= 'A' && line[0] <= 'Z') {
					// Might be a new log entry - verify with full parsing
					timestamp, _ := parseStderrLine(line)
					if timestamp.IsZero() {
						// No valid timestamp = continuation line
						isContinuation = true
					}
				} else {
					// Doesn't start with digit/bracket/uppercase = definitely continuation
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
					out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
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
				// Most log entries start with: digit (timestamp), '[' (bracket), or uppercase letter (syslog month)
				if line[0] >= '0' && line[0] <= '9' || line[0] == '[' || (line[0] >= 'A' && line[0] <= 'Z') {
					timestamp, _ := parseStderrLine(line)
					if timestamp.IsZero() {
						isContinuation = true
					}
				} else {
					// Doesn't start with digit/bracket/uppercase = definitely continuation
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
					out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
					currentEntry.Reset()
				}
				currentEntry.WriteString(line)
			}
		}
	}

	// Process final accumulated entry
	if currentEntry.Len() > 0 {
		timestamp, message := parseStderrLine(currentEntry.String())
		out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
	}

	return nil
}

// parseMmapDataOptimized is an optimized version using byte slicing instead of string conversions.
// This reduces allocations by working directly with byte slices.
//
// For syslog format, it uses per-PID tracking to handle interleaved logs from
// multiple PostgreSQL backends. Each backend (PID) maintains its own accumulated
// entry, preventing premature flushes when logs from different PIDs are interleaved.
// SyslogFormat represents the detected syslog format type
type SyslogFormat int

const (
	SyslogNone    SyslogFormat = iota // Not syslog format
	SyslogBSD                         // RFC 3164: "Nov 30 21:10:20 host ..."
	SyslogISO                         // ISO timestamp: "2025-11-30T21:10:20+00:00 host ..."
	SyslogRFC5424                     // RFC 5424: "<134>1 2025-11-30T21:10:20+00:00 host ..."
)

func parseMmapDataOptimized(data []byte, out chan<- LogEntry) error {
	// Detect syslog format by checking first non-empty line
	syslogFormat := detectSyslogFormat(data)

	if syslogFormat != SyslogNone {
		return parseMmapDataSyslog(data, out, syslogFormat)
	}
	return parseMmapDataStderr(data, out)
}

// detectSyslogFormat checks if the data appears to be syslog format and returns the type.
// Supports three syslog formats:
//   - BSD (RFC 3164): "Nov 30 21:10:20 host ..."
//   - ISO: "2025-11-30T21:10:20+00:00 host ..."
//   - RFC 5424: "<134>1 2025-11-30T21:10:20+00:00 host ..."
func detectSyslogFormat(data []byte) SyslogFormat {
	// Find first non-empty line
	start := 0
	for start < len(data) {
		i := bytes.IndexByte(data[start:], '\n')
		if i < 0 {
			i = len(data) - start
		}
		line := data[start : start+i]
		start += i + 1

		if len(line) < 15 {
			continue
		}

		// RFC 5424: starts with <priority>version (e.g., "<134>1 ")
		if line[0] == '<' {
			// Find closing '>'
			for j := 1; j < len(line) && j < 5; j++ {
				if line[j] == '>' {
					// Check for version number and space after '>'
					if j+2 < len(line) && line[j+1] >= '0' && line[j+1] <= '9' && line[j+2] == ' ' {
						return SyslogRFC5424
					}
					break
				}
			}
		}

		// ISO format: starts with "YYYY-MM-DDTHH:MM:SS" (e.g., "2025-11-30T21:10:20")
		if line[4] == '-' && line[7] == '-' && line[10] == 'T' && line[13] == ':' && line[16] == ':' {
			return SyslogISO
		}

		// BSD format: starts with month abbreviation (e.g., "Nov 30 21:10:20")
		if line[3] == ' ' && line[0] >= 'A' && line[0] <= 'Z' {
			months := [][]byte{
				[]byte("Jan"), []byte("Feb"), []byte("Mar"), []byte("Apr"),
				[]byte("May"), []byte("Jun"), []byte("Jul"), []byte("Aug"),
				[]byte("Sep"), []byte("Oct"), []byte("Nov"), []byte("Dec"),
			}
			for _, month := range months {
				if bytes.HasPrefix(line, month) {
					return SyslogBSD
				}
			}
		}

		// First non-empty line checked, return none if not syslog
		return SyslogNone
	}
	return SyslogNone
}

// parseMmapDataSyslog parses syslog format with per-PID tracking.
// This handles interleaved logs from multiple PostgreSQL backends correctly.
//
// Key insight: syslog uses [statement_id-line_number] pattern where:
//   - [X-1] = first line of statement X (new entry)
//   - [X-N] where N>1 = continuation line (append to entry)
//
// Strategy: Track entries per PID for correct multi-line SQL assembly.
// When a new entry arrives for a PID, flush only that PID's previous entry.
// At the end, flush remaining entries sorted by timestamp for correct analyzer order.
func parseMmapDataSyslog(data []byte, out chan<- LogEntry, format SyslogFormat) error {
	// Per-PID entry tracking: each backend accumulates its own entry
	// We store both the entry data and its original line number for stable sorting
	type pidEntry struct {
		lineNum int
		data    []byte
	}
	perPIDEntries := make(map[string]pidEntry)

	// Track emission order for deterministic output
	type orderedEntry struct {
		lineNum int // Original file line number for stable sorting
		entry   LogEntry
	}
	var emissionOrder []orderedEntry
	lineNum := 0

	// Helper to emit an entry with its original line number
	// Uses the correct parser based on the detected syslog format
	emit := func(ln int, entry []byte) {
		var ts time.Time
		var msg string

		switch format {
		case SyslogBSD:
			ts, msg = parseStderrLineBytes(entry)
		case SyslogISO:
			ts, msg, _ = parseSyslogFormatISO(string(entry))
		case SyslogRFC5424:
			ts, msg, _ = parseSyslogFormatRFC5424(string(entry))
		default:
			ts, msg = parseStderrLineBytes(entry)
		}

		emissionOrder = append(emissionOrder, orderedEntry{
			lineNum: ln,
			entry:   LogEntry{Timestamp: ts, Message: msg, IsContinuation: isContinuationMessage(msg)},
		})
	}

	// Helper to parse syslog line based on detected format
	parseLine := func(line string) (time.Time, string, bool) {
		switch format {
		case SyslogBSD:
			return parseSyslogFormat(line)
		case SyslogISO:
			return parseSyslogFormatISO(line)
		case SyslogRFC5424:
			return parseSyslogFormatRFC5424(line)
		default:
			return parseSyslogFormat(line)
		}
	}

	start := 0
	for start < len(data) {
		// Find next newline
		i := bytes.IndexByte(data[start:], '\n')
		if i < 0 {
			break
		}

		i += start
		line := data[start:i]
		start = i + 1
		lineNum++

		if len(line) == 0 {
			continue
		}

		// Parse syslog line to extract timestamp and message part
		lineStr := string(line)
		ts, message, ok := parseLine(lineStr)
		if !ok {
			// Not a valid syslog line - skip
			continue
		}

		// Extract PID from the full line (header contains postgres[PID])
		// For continuation lines, the message after "]: " doesn't have the PID,
		// but the syslog header does (e.g., "...postgres[108]: [10-2] ...")
		pid := extractSyslogPID(lineStr)
		if pid == "" {
			// No PID found - emit as standalone entry using already-parsed values
			emissionOrder = append(emissionOrder, orderedEntry{
				lineNum: lineNum,
				entry:   LogEntry{Timestamp: ts, Message: message, IsContinuation: isContinuationMessage(message)},
			})
			continue
		}

		// Check if this is a continuation line ([X-N] where N > 1)
		if isSyslogContinuationLine(message) {
			// Continuation: append to this PID's entry
			if pe, exists := perPIDEntries[pid]; exists && len(pe.data) > 0 {
				content := extractSyslogContinuationContent(message)
				if content != "" {
					pe.data = append(pe.data, ' ')
					pe.data = append(pe.data, content...)
					perPIDEntries[pid] = pe
				}
			}
			// If no entry for this PID, it's an orphan continuation - skip
		} else {
			// New entry [X-1]: flush previous entry for THIS PID only
			if pe, exists := perPIDEntries[pid]; exists && len(pe.data) > 0 {
				emit(pe.lineNum, pe.data)
			}
			// Start accumulating this entry for this PID (with current line number)
			perPIDEntries[pid] = pidEntry{lineNum: lineNum, data: append([]byte(nil), line...)}
		}
	}

	// Handle remaining data after last newline
	if start < len(data) {
		line := data[start:]
		lineNum++
		if len(line) > 0 {
			lineStr := string(line)
			_, message, ok := parseLine(lineStr)
			if ok {
				pid := extractSyslogPID(lineStr)
				if pid != "" {
					if isSyslogContinuationLine(message) {
						if pe, exists := perPIDEntries[pid]; exists && len(pe.data) > 0 {
							content := extractSyslogContinuationContent(message)
							if content != "" {
								pe.data = append(pe.data, ' ')
								pe.data = append(pe.data, content...)
								perPIDEntries[pid] = pe
							}
						}
					} else {
						if pe, exists := perPIDEntries[pid]; exists && len(pe.data) > 0 {
							emit(pe.lineNum, pe.data)
						}
						perPIDEntries[pid] = pidEntry{lineNum: lineNum, data: append([]byte(nil), line...)}
					}
				}
			}
		}
	}

	// Flush remaining entries
	for _, pe := range perPIDEntries {
		if len(pe.data) > 0 {
			emit(pe.lineNum, pe.data)
		}
	}

	// Now sort ALL emitted entries by timestamp (stable sort preserving file line order)
	// This ensures correct order for analyzers that depend on temporal ordering
	for i := 0; i < len(emissionOrder)-1; i++ {
		for j := i + 1; j < len(emissionOrder); j++ {
			ei, ej := emissionOrder[i], emissionOrder[j]
			// Sort by timestamp first, then by original line number for stability
			if ej.entry.Timestamp.Before(ei.entry.Timestamp) ||
				(ej.entry.Timestamp.Equal(ei.entry.Timestamp) && ej.lineNum < ei.lineNum) {
				emissionOrder[i], emissionOrder[j] = emissionOrder[j], emissionOrder[i]
			}
		}
	}
	for _, e := range emissionOrder {
		out <- e.entry
	}

	return nil
}

// parseMmapDataStderr parses stderr format (non-syslog) using the original logic.
func parseMmapDataStderr(data []byte, out chan<- LogEntry) error {
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
			// Most log entries start with: digit (timestamp), '[' (bracket), or uppercase letter (syslog month)
			// This avoids expensive parseStderrLineBytes call for obvious continuation lines
			if line[0] >= '0' && line[0] <= '9' || line[0] == '[' || (line[0] >= 'A' && line[0] <= 'Z') {
				// Might be a new log entry - verify with full parsing
				timestamp, _ := parseStderrLineBytes(line)
				if timestamp.IsZero() {
					// No valid timestamp = continuation line
					isContinuation = true
				}
			} else {
				// Doesn't start with digit/bracket/uppercase = definitely continuation
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
				out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
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
					out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
					currentEntry = currentEntry[:0]
				}
				currentEntry = append(currentEntry[:0], line...)
			}
		}
	}

	// Process final accumulated entry
	if len(currentEntry) > 0 {
		timestamp, message := parseStderrLineBytes(currentEntry)
		out <- LogEntry{Timestamp: timestamp, Message: message, IsContinuation: isContinuationMessage(message)}
	}

	return nil
}

// parseStderrLineBytes converts a byte slice to string and parses it.
// For the native CLI with Go's GC, this is faster than byte-level parsing
// because it avoids goroutine contention. The WASM path uses parseFromBytes
// in stderr_parser.go which benefits from zero-copy parsing with leaking GC.
func parseStderrLineBytes(line []byte) (time.Time, string) {
	return parseStderrLine(string(line))
}
