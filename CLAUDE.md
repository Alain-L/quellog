# CLAUDE.md

This guide helps Claude Code (claude.ai/code) work on **quellog**.

## Project Overview

quellog is a high-performance PostgreSQL log parser and analyzer written in Go. It processes gigabytes of archives in seconds while producing synthetic overviews, SQL performance analysis, and operational insights.

Key capabilities:
- Multi-format log parsing: stderr/syslog, CSV, JSON with automatic detection
- Transparent compression & archive handling: plain files, gzip/pgzip, zstd, and tar/tgz/tzst (including nested compressed entries)
- Streaming architecture with concurrent parsing for large inputs
- Time-based and attribute-based filtering (database, user, application)
- Comprehensive analysis: SQL performance, events, checkpoints, vacuum operations, connections, temp files

## Development Commands

### Building
```bash
# Build development binary
go build -o bin/quellog .

# Build for testing
go build -o bin/quellog_test .

# Run the development binary against a fixture
bin/quellog test/testdata/test_summary.log
```

### Testing
```bash
# Run all tests with coverage
go test ./... -cover

# Run the integration summary test
go test ./test/summary_test.go

# Run tests in verbose mode
go test -v ./...

# Run code vetting
go vet ./...
```

### Running
```bash
# Basic usage
quellog /path/to/logs

# With filters
quellog /path/to/logs --dbname mydb --dbuser myuser --begin "2025-01-01 00:00:00"

# SQL analysis
quellog /path/to/logs --sql-summary
quellog /path/to/logs --sql-detail <query_id>

# Export formats
quellog /path/to/logs --json
quellog /path/to/logs --md
```

## Compression & Archive Support

- `parser/autodetect.go` chooses readers based on extensions (`.gz`, `.zst`, `.zstd`, `.tar*`) before falling back to content heuristics. Plain parsers remain unchanged.
- `newParallelGzipReader` (pgzip) and `newZstdDecoder` (klauspost/zstd) provide streaming decompression with bounded concurrency.
- `parser/tar_parser.go` streams tar/tgz/tzst archives and recursively handles nested gzip/zstd members. Unsupported entries are drained and skipped.
- Keep the fallback stderr parser intact; channel ownership stays with callers.

## Tests & Fixtures

- `test/summary_test.go` builds a fresh `quellog_test` binary and asserts JSON output for a matrix of inputs: raw, gzip, zstd (`.zst`, `.zstd`), tar/tgz/tzst.
- Fixtures live in `test/testdata/`. Every base file (e.g. `test_summary.log`) now has gzip and zstd variants plus tar archives holding the same inputs.
- Golden JSON lives at `test/testdata/test_summary.golden.json`. Regenerate carefully when analysis output changes.

## Package Structure

- `main.go`: Entry point delegating to `cmd`.
- `cmd/`: Cobra CLI (`root.go`, `execute.go`, `parsing.go`, `workers.go`, `files.go`).
- `parser/`: Format detection and parsing (`autodetect.go`, `stderr_parser.go`, `csv_parser.go`, `json_parser.go`, `filter.go`, `tar_parser.go`).
- `analysis/`: Metric aggregation (`summary.go`, `sql.go`, `events.go`, `checkpoints.go`, `vacuum.go`, `connections.go`, `temp_files.go`, `errclass.go`, `utils.go`).
- `output/`: Rendering (`text.go`, `json.go`, `markdown.go`, `formatter.go`).

## Dependencies

- Compression relies on `github.com/klauspost/compress/pgzip` and `github.com/klauspost/compress/zstd`. They are direct dependencies; keep them updated together.
- JS bundling uses `github.com/evanw/esbuild` (Go API) - no Node.js required.
- Standard Go tooling only; avoid introducing heavy external libs for decompression—extend `compressionCodec` if new formats are needed.

## Output Formatting

- `output/text.go` now routes everything through a `textPrinter` helper that handles ANSI styling and terminal width detection. Reuse its helpers (`renderMetrics`, `renderHistogram`, `printTopTables`, etc.) instead of duplicating spacing logic.
- The refactor restored byte-for-byte parity with the legacy formatter. When tweaking text output, diff `bin/quellog test/testdata/test_summary.log` against a known-good binary (for example `bin/quellog_dev`) to keep blank lines and column widths identical.
- Histograms and tabular sections share alignment helpers; add new sections by following the existing indentation patterns (two spaces before labels, padded columns with `%-25s`).

## Web Assets & HTML Report Template

The web interface uses ES modules bundled with esbuild (Go API):

**Source files (versioned):**
- `web/app.js` - Entry point ES module
- `web/js/*.js` - Modules (utils, state, theme, compression, filters, file-handler, charts)
- `web/index.html` - HTML template
- `web/styles.css` - CSS styles
- `web/uplot.min.js` - Chart library
- `web/fzstd.min.js` - Zstd decompressor

**Generated files (gitignored):**
- `web/app.bundle.js` - Bundled JS (IIFE format)
- `output/assets/*` - Assets copied for Go embedding

**When modifying web assets:**
1. Edit the source files in `web/`
2. Run `go generate ./web/...` to bundle JS and copy assets
3. Run `go build` to compile with updated assets

Or simply: `go generate ./web/... && go build -o bin/quellog .`

The assets are embedded via `//go:embed` and assembled into the HTML template at runtime.

## Important Development Notes

- **Language**: All code comments, doc-comments, commit messages, and identifiers must be in English. No French comments.
- **Format Detection**: Sample up to 32 KB (extend for newline scarcity). CSV/JSON detection uses regex heuristics; stderr/syslog detection has multiple patterns. Binary detection prevents misclassification. Compression choice happens before sampling for tar archives and known extensions.
- **Message Parsing**: Continuation lines (DETAIL/HINT/STATEMENT/CONTEXT) must be appended to the previous entry. Respect `LogParser.Parse` contract—no channel closing.
- **Performance**: Buffered channels sized at 65 536 keep workers busy. Avoid recompiling regex in hot paths. Worker count = `min(numFiles, min(numCPU, 8))`.

## Common Pitfalls

1. Do not close shared channels inside parsers.
2. Guard against zero timestamps—skip or log warnings.
3. Always drain unsupported tar entries so the reader can advance.
4. Remember tests build a binary; clean up artifacts or reuse `bin/` when possible.
5. Update fixtures/goldens together when changing analysis output or adding formats.
- 1 still waiting ou 1 acquired tant que c'est la même attente. Compris ?