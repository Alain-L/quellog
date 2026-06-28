//go:build !js

package parser

import (
	"os"
	"path/filepath"
	"strings"
)

// SupportsPIDSharding reports whether entries parsed from filename will carry
// the PostgreSQL backend PID in LogEntry.PID AND benefit from the analysis
// layer's PID-sharded fan-out. True for (non-syslog) stderr, including stderr
// behind gzip/zstd compression or a tar archive — the underlying format is
// detected from a decompressed sample.
//
//   - CSV/JSON return false: their wall time is dominated by parsing and
//     channel coordination, not the analysis fan-out, so sharding the analyzers
//     yields no speed-up (measured). The lever there is parallel parsing.
//   - syslog returns false: LogEntry.PID is the message sequence number, not
//     the backend PID, so PID-sharding would split a backend's correlated
//     lines (wait→acquire, error→continuation) across shards.
//   - compressed/archived stderr returns true: a decompressed sample is probed
//     for the underlying format and syslog-ness, so large compressed stderr
//     also gets the sharded pipeline. For tar archives the first supported
//     entry is sampled (archives are assumed homogeneous; a non-stderr first
//     entry conservatively disables sharding for the whole input).
//
// This file is native-only (!js): it reaches into the gzip/zstd codecs and the
// tar reader, which are not compiled for WASM. The WASM build gets a stub (see
// sharding_stub.go); it never shards anyway (single-pass) and never calls this.
func SupportsPIDSharding(filename string) bool {
	lower := strings.ToLower(filename)
	switch {
	case isTarArchiveName(lower):
		return tarFirstEntryShardable(filename)
	case strings.HasSuffix(lower, ".gz"):
		return compressedSampleShardable(filename, gzipCodec, filename[:len(filename)-3])
	case strings.HasSuffix(lower, ".zstd"):
		return compressedSampleShardable(filename, zstdCodec, filename[:len(filename)-5])
	case strings.HasSuffix(lower, ".zst"):
		return compressedSampleShardable(filename, zstdCodec, filename[:len(filename)-4])
	default:
		// Plain, uncompressed input.
		if DetectFileFormat(filename) != "stderr" {
			return false
		}
		f, err := os.Open(filename)
		if err != nil {
			return false
		}
		defer f.Close()
		return detectSyslogFormatFromFile(f) == SyslogNone
	}
}

// isTarArchiveName reports whether name is one of the tar containers we stream
// (plain or compressed). Checked before the bare .gz/.zst suffixes so that
// .tar.gz / .tar.zst route to the tar path.
func isTarArchiveName(lower string) bool {
	for _, ext := range []string{".tar", ".tgz", ".tzst", ".tar.gz", ".tar.zst", ".tar.zstd"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// sampleShardable reports whether a decompressed content sample is non-syslog
// stderr — the precondition for PID-sharding. Mirrors the parser's
// extension-then-content format detection and DetectFileFormat's classification
// (anything that is neither CSV nor JSON is stderr-family).
func sampleShardable(baseName, sample string) bool {
	if sample == "" || isBinaryContent(sample) {
		return false
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(baseName), "."))
	p := detectByExtension(baseName, ext, sample)
	if p == nil {
		p = detectByContent(baseName, sample)
	}
	switch p.(type) {
	case *CsvParser, *JsonParser, nil:
		return false
	}
	return detectSyslogFormat([]byte(sample)) == SyslogNone
}
