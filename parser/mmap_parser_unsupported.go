//go:build !(linux || darwin) || wasm
// +build !linux,!darwin wasm

// Package parser provides log file parsing for PostgreSQL logs.
package parser

// MmapStderrParser is a stub for platforms that don't support mmap.
// On unsupported platforms, it always falls back to buffered I/O.
type MmapStderrParser struct{}

// Parse falls back to buffered I/O on unsupported platforms.
func (p *MmapStderrParser) Parse(filename string, out chan<- LogEntry) error {
	// Fall back to buffered I/O parser
	bufParser := &StderrParser{}
	return bufParser.Parse(filename, out)
}
