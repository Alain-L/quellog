//go:build js

// Package parser provides log file parsing for PostgreSQL logs.
// This file provides stub implementations for WASM builds where compression is not supported.
package parser

// detectCompressedFile is a stub for WASM builds.
// Compression is handled by the browser before passing content to WASM.
// Returns (nil, nil, false) to indicate the file was not handled.
func detectCompressedFile(filename string) (LogParser, error, bool) {
	return nil, nil, false
}
