//go:build !js

// Package parser provides log file parsing for PostgreSQL logs.
package parser

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
)

var errUnsupportedArchiveEntry = errors.New("unsupported archive entry")

// TarParser extracts supported log files from tar or tar.gz archives and streams entries.
type TarParser struct{}

// Parse reads a tar or tar.gz archive and parses any supported log files inside it.
func (p *TarParser) Parse(filename string, out chan<- []LogEntry) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open tar archive %s: %w", filename, err)
	}
	defer file.Close()

	// Wrap the on-disk reader so progress reports compressed bytes
	// consumed (= file size on disk = the bar's denominator).
	reader, closer, err := openTarStream(filename, WithProgress(file))
	if err != nil {
		return err
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

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if hdr.Size == 0 {
			continue
		}

		entryName := hdr.Name
		entryReader := io.LimitReader(tr, hdr.Size)

		// Path traversal protection
		if strings.Contains(entryName, "..") {
			slog.Warn("skipping tar entry with suspicious path", "entry", entryName)
			if _, err := io.Copy(io.Discard, entryReader); err != nil {
				return fmt.Errorf("discarding suspicious entry %s in %s: %w", entryName, filename, err)
			}
			continue
		}

		// Use only the base filename for extension matching
		baseName := filepath.Base(entryName)

		if isSupportedArchiveEntry(baseName) {
			if err := parseArchiveEntry(baseName, entryReader, out); err != nil {
				if errors.Is(err, errUnsupportedArchiveEntry) {
					slog.Warn("unsupported log format in archive", "entry", entryName, "archive", filename)
				} else {
					slog.Error("failed to parse entry in archive", "entry", entryName, "archive", filename, "err", err)
				}
			}
		} else {
			// Extension unrecognized — sniff the content so an extensionless
			// but valid log member (a common real-world tar layout) isn't
			// silently dropped. Genuinely unsupported/binary members are skipped.
			parsed, err := sniffAndParseArchiveEntry(baseName, entryReader, out)
			if err != nil {
				slog.Error("failed to parse entry in archive", "entry", entryName, "archive", filename, "err", err)
			} else if !parsed {
				slog.Info("skipping unsupported file in archive", "entry", entryName, "archive", filename)
			}
		}

		// Ensure the remainder of the entry is consumed.
		if _, err := io.Copy(io.Discard, entryReader); err != nil {
			return fmt.Errorf("draining entry %s in %s: %w", entryName, filename, err)
		}
	}

	return nil
}

// openTarStream wraps the raw archive reader with the decompressor its name
// implies, and returns the tar-level reader plus the closer to release (nil
// for an uncompressed .tar).
func openTarStream(filename string, r io.Reader) (io.Reader, io.Closer, error) {
	switch {
	case isGzipArchive(filename):
		gr, err := newParallelGzipReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open gzip reader for tar archive %s: %w", filename, err)
		}
		return gr, gr, nil
	case isZstdArchive(filename):
		zr, err := newZstdDecoder(r)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open zstd reader for tar archive %s: %w", filename, err)
		}
		return zr, zr, nil
	}
	return r, nil, nil
}

// innerFormats implements formatWrapper: it names the formats of the
// members the archive holds that we can parse, so the cmd layer routes a
// pgBouncer archive to the pooler analyzer, and a mixed one to both
// analyzers, instead of feeding pooler lines to the PostgreSQL one.
func (p *TarParser) innerFormats(filename string) (formats []string) {
	file, err := os.Open(filename)
	if err != nil {
		return nil
	}
	defer file.Close()

	reader, closer, err := openTarStream(filename, file)
	if err != nil {
		return nil
	}
	if closer != nil {
		defer closer.Close()
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err != nil || hdr == nil {
			return formats
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size == 0 || strings.Contains(hdr.Name, "..") {
			continue
		}
		formats = addFormat(formats, archiveMemberFormat(filepath.Base(hdr.Name), io.LimitReader(tr, hdr.Size)))
	}
}

// addFormat appends a member's format to the formats an archive holds,
// once each; "" (not a log) is left out.
func addFormat(formats []string, format string) []string {
	if format == "" || slices.Contains(formats, format) {
		return formats
	}
	return append(formats, format)
}

// ArchiveTrack selects the members an archive parse reads. A single run
// may analyze PostgreSQL and pgBouncer logs on two tracks; an archive
// holding both is then read once per track, each keeping its own
// members. The cmd layer sets it around each pass; the zero value reads
// every member.
type ArchiveTrack int32

const (
	AllMembers ArchiveTrack = iota
	PostgresMembers
	PoolerMembers
)

var archiveTrack atomic.Int32

// SetArchiveTrack sets the members the archive parses that follow read.
func SetArchiveTrack(t ArchiveTrack) { archiveTrack.Store(int32(t)) }

// keepMember reports whether the current track reads a member, pooler
// saying whether it is a pgBouncer log.
func keepMember(pooler bool) bool {
	switch ArchiveTrack(archiveTrack.Load()) {
	case PostgresMembers:
		return !pooler
	case PoolerMembers:
		return pooler
	}
	return true
}

// archiveMemberFormat names the format of one archive member ("csv",
// "json", "pgbouncer" or "stderr"), or "" when the member is not a log we
// can parse. It mirrors parseArchiveEntry's routing: a nested gzip/zstd
// member is decompressed first, then its head is sampled and run through
// the same content discriminators the plain path uses.
func archiveMemberFormat(name string, r io.Reader) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".gz"):
		gr, err := newParallelGzipReader(r)
		if err != nil {
			return ""
		}
		defer gr.Close()
		return archiveMemberFormat(name[:len(name)-len(".gz")], gr)
	case strings.HasSuffix(lower, ".zst"):
		return nestedArchiveMemberFormat(name, r, ".zst")
	case strings.HasSuffix(lower, ".zstd"):
		return nestedArchiveMemberFormat(name, r, ".zstd")
	}

	sample := make([]byte, sampleBufferSize)
	n, err := io.ReadFull(r, sample)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return ""
	}
	text := string(bytes.TrimPrefix(sample[:n], utf8BOM))
	if isBinaryContent(text) {
		return ""
	}
	return sampleFormat(text)
}

// nestedArchiveMemberFormat decompresses a zstd member and re-runs the
// format probe on the inner name.
func nestedArchiveMemberFormat(name string, r io.Reader, suffix string) string {
	zr, err := newZstdDecoder(r)
	if err != nil {
		return ""
	}
	defer zr.Close()
	return archiveMemberFormat(name[:len(name)-len(suffix)], zr)
}

// sampleFormat names the format of a content sample using the same
// discriminators as the plain detection path, in the same order as
// detectByContent. Unlike detectByContent it builds no parser and logs
// nothing: it only answers which analyzer the content belongs to. Returns
// "" when nothing matches.
func sampleFormat(sample string) string {
	switch {
	case looksLikePgBouncer(sample):
		return "pgbouncer"
	case isJSONContent(sample):
		return "json"
	case isCSVContent(sample):
		return "csv"
	case isLogContent(sample):
		return "stderr"
	default:
		return ""
	}
}

// isSupportedArchiveEntry reports whether the archive entry should be parsed.
func isSupportedArchiveEntry(name string) bool {
	lower := strings.ToLower(name)
	supported := []string{
		".log",
		".csv",
		".json",
		".jsonl",
		".log.gz",
		".csv.gz",
		".json.gz",
		".jsonl.gz",
		".log.zst",
		".csv.zst",
		".json.zst",
		".jsonl.zst",
		".log.zstd",
		".csv.zstd",
		".json.zstd",
		".jsonl.zstd",
	}

	for _, ext := range supported {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	// Support rotated PostgreSQL log files (e.g. postgresql.log.2026-03-23-10)
	if isRotatedLogFile(lower) {
		return true
	}

	return false
}

// hasRotatedBase reports whether lower carries a rotation suffix on the base
// extension: the base immediately followed by a separator ('.' for numeric or
// date-dot rotation, '-' for logrotate "dateext") then a digit — e.g.
// postgresql.log.1, postgresql.log.2026-03-23-10, postgresql.log-20260320.
func hasRotatedBase(lower, base string) bool {
	for from := 0; ; {
		idx := strings.Index(lower[from:], base)
		if idx == -1 {
			return false
		}
		idx += from
		rest := lower[idx+len(base):]
		if len(rest) >= 2 && (rest[0] == '.' || rest[0] == '-') && rest[1] >= '0' && rest[1] <= '9' {
			return true
		}
		from = idx + 1
	}
}

// isRotatedLogFile detects PostgreSQL rotated log/csv files (any rotation
// naming hasRotatedBase recognizes), so a rotated member is accepted for
// parsing instead of being dropped as an unsupported archive entry.
func isRotatedLogFile(lower string) bool {
	return hasRotatedBase(lower, ".log") || hasRotatedBase(lower, ".csv")
}

// sniffAndParseArchiveEntry handles an archive member whose name carries no
// recognized extension. It buffers a sample, detects the format from content,
// and parses the member (sample + remaining stream) when it looks like a log.
// Returns parsed=false for binary or unrecognized content, so the caller skips
// it. Compressed members without an extension are treated as binary and skipped.
func sniffAndParseArchiveEntry(name string, r io.Reader, out chan<- []LogEntry) (bool, error) {
	sample := make([]byte, sampleBufferSize)
	n, err := io.ReadFull(r, sample)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	sample = bytes.TrimPrefix(sample[:n], utf8BOM)

	if isBinaryContent(string(sample)) {
		return false, nil
	}
	parser := detectByContent(name, string(sample))
	if parser == nil {
		return false, nil
	}

	// Replay the buffered sample ahead of the rest of the entry.
	full := io.MultiReader(bytes.NewReader(sample), r)
	if _, pooler := parser.(*PgBouncerParser); !keepMember(pooler) {
		return true, nil // the other track's member; the caller drains it
	}
	switch src := parser.(type) {
	case *CsvParser:
		return true, (&CsvParser{}).parseReader(full, out)
	case *JsonParser:
		return true, (&JsonParser{}).parseReader(full, out)
	case *PgBouncerParser:
		return true, (&PgBouncerParser{}).parseReader(full, out)
	default:
		// Carry the detected leading-prefix offset so a prefixed log inside
		// an archive parses like its plain counterpart instead of silently
		// yielding zero entries.
		sp, _ := src.(*StderrParser)
		prefixLen := 0
		if sp != nil {
			prefixLen = sp.prefixLen
		}
		return true, routeStderrStream(full, prefixLen, out)
	}
}

// routeStderrStream parses a fully-buffered stderr stream. A prefix-less member
// takes the parallel stream-chunked fast path (same as compressed plain
// streams); a detected literal prefix keeps the sequential prefix-aware reader,
// which mirrors wrapCompressedParser's routing.
func routeStderrStream(full io.Reader, prefixLen int, out chan<- []LogEntry) error {
	parser := &StderrParser{prefixLen: prefixLen}
	if workers := parallelWorkers(); workers >= 2 && prefixLen == 0 {
		return parser.parseStreamParallel(full, workers, out)
	}
	return parser.parseReader(full, out)
}

// parseLogArchiveEntry parses a text-log archive member (a ".log" or rotated
// ".log.<date>" entry), running the same probes the plain/compressed path
// runs in autodetect.go's detectByExtension: pgBouncer content first, then a
// literal log_line_prefix salvage for stderr. Archive members are
// non-seekable, so it buffers a head sample, probes it, then replays the
// sample ahead of the rest of the stream so no bytes are lost. Without the
// pgBouncer probe a pooler log inside an archive was parsed as PostgreSQL;
// without the prefix salvage a prefixed log yielded zero entries.
func parseLogArchiveEntry(r io.Reader, out chan<- []LogEntry) error {
	sample := make([]byte, sampleBufferSize)
	n, err := io.ReadFull(r, sample)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return err
	}
	sample = bytes.TrimPrefix(sample[:n], utf8BOM)

	// Replay the buffered sample ahead of the remaining stream, then route
	// it in detectByExtension's own order.
	s := string(sample)
	full := io.MultiReader(bytes.NewReader(sample), r)

	// pgBouncer first: a pooler log shares PostgreSQL's timestamp/PID shape,
	// so the stderr reader would happily swallow it and report it as a
	// PostgreSQL log.
	if pooler := looksLikePgBouncer(s); !keepMember(pooler) {
		return nil // the other track's member; the caller drains it
	} else if pooler {
		return (&PgBouncerParser{}).parseReader(full, out)
	}

	// Only probe for a literal prefix when the sample doesn't already look like
	// a normal PostgreSQL log — mirrors detectByExtension's "log" branch, so a
	// well-formed member keeps prefixLen 0 and the common no-prefix fast path.
	prefixLen := 0
	if !isLogContent(s) {
		prefixLen = detectLeadingPrefix(s)
	}
	return routeStderrStream(full, prefixLen, out)
}

// parseArchiveEntry selects the correct parser for an archive entry.
func parseArchiveEntry(name string, r io.Reader, out chan<- []LogEntry) error {
	lower := strings.ToLower(name)

	switch {
	case strings.HasSuffix(lower, ".log"):
		// Buffer a head sample so a literal log_line_prefix is salvaged the
		// same way the plain/compressed path does, then parse (parallel when
		// prefix-less, sequential when a prefix was detected).
		return parseLogArchiveEntry(r, out)
	case strings.HasSuffix(lower, ".csv"):
		if !keepMember(false) {
			return nil
		}
		parser := &CsvParser{}
		return parser.parseReader(r, out)
	case strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".jsonl"):
		if !keepMember(false) {
			return nil
		}
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
		return parseArchiveEntry(trimmedName, gzReader, out)
	case strings.HasSuffix(lower, ".zst"):
		return parseZstdArchiveEntry(name, r, ".zst", out)
	case strings.HasSuffix(lower, ".zstd"):
		return parseZstdArchiveEntry(name, r, ".zstd", out)
	case hasRotatedBase(lower, ".log"):
		// Rotated PostgreSQL log files, including logrotate dateext
		// (e.g. postgresql.log.2026-03-23-10, postgresql.log-20260320 after
		// nested decompression). Same literal-prefix salvage as ".log" above.
		return parseLogArchiveEntry(r, out)
	case hasRotatedBase(lower, ".csv"):
		// Rotated CSV log files (numeric, date-dot or dateext).
		if !keepMember(false) {
			return nil
		}
		parser := &CsvParser{}
		return parser.parseReader(r, out)
	default:
		return errUnsupportedArchiveEntry
	}
}

func parseZstdArchiveEntry(name string, r io.Reader, suffix string, out chan<- []LogEntry) error {
	zr, err := newZstdDecoder(r)
	if err != nil {
		return fmt.Errorf("failed to decompress %s: %w", name, err)
	}
	defer zr.Close()

	trimmedName := name[:len(name)-len(suffix)]
	return parseArchiveEntry(trimmedName, zr, out)
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

// tarFirstEntryShardable reports whether the first supported entry of a tar
// archive (plain or gzip/zstd-compressed) is non-syslog stderr — the
// PID-sharding precondition. Archives are assumed homogeneous: the first
// entry's format stands in for the whole input; anything else (CSV/JSON,
// syslog, unreadable) conservatively returns false.
func tarFirstEntryShardable(filename string) bool {
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()

	var reader io.Reader = file
	if isGzipArchive(filename) {
		gr, gzErr := newParallelGzipReader(file)
		if gzErr != nil {
			return false
		}
		defer gr.Close()
		reader = gr
	} else if isZstdArchive(filename) {
		zr, zErr := newZstdDecoder(file)
		if zErr != nil {
			return false
		}
		defer zr.Close()
		reader = zr
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return false // EOF or read error: no shardable entry found
		}
		if hdr.Typeflag != tar.TypeReg || !isSupportedArchiveEntry(hdr.Name) {
			continue
		}
		// Sample this entry, transparently decompressing a nested member.
		name := hdr.Name
		var er io.Reader = tr
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".gz"):
			gr, gzErr := newParallelGzipReader(tr)
			if gzErr != nil {
				return false
			}
			defer gr.Close()
			er, name = gr, name[:len(name)-3]
		case strings.HasSuffix(lower, ".zstd"):
			zr, zErr := newZstdDecoder(tr)
			if zErr != nil {
				return false
			}
			defer zr.Close()
			er, name = zr, name[:len(name)-5]
		case strings.HasSuffix(lower, ".zst"):
			zr, zErr := newZstdDecoder(tr)
			if zErr != nil {
				return false
			}
			defer zr.Close()
			er, name = zr, name[:len(name)-4]
		}
		buf := make([]byte, sampleBufferSize)
		n, _ := io.ReadFull(er, buf)
		sample := strings.TrimPrefix(string(buf[:n]), string(utf8BOM))
		return sampleShardable(filepath.Base(name), sample)
	}
}
