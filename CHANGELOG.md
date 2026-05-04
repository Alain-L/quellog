# Changelog

All notable changes to this project will be documented in this file.

## [0.10.0] - unreleased

### Added
- **Per-event drill-down**: every event pattern gets a stable short id (`<sev>-<4-char-hash>`, e.g. `fa-6K1G`, `er-Qr5p`) shown in the `--events` output. Use `--event-detail` (`-E`) to open a full report for one or more patterns: full raw message, occurrences-over-time bar chart, first/last seen, frequency. Same drill-down available in the HTML report as a click-to-detail modal with uPlot sparkline + copy buttons.
- **`--last` / `--window` extended units**: `d` (days), `w` (weeks), `y` (years) in addition to the existing `s`/`m`/`h`. Example: `--last 1d`, `--last 5y`.
- **`--quiet` / `-q`**: suppress INFO logs (keep WARN/ERROR). Useful for cron and CI invocations.
- **`completion` subcommand**: `quellog completion bash|zsh|fish|powershell` writes a completion script to stdout (Cobra-generated).
- **`--open`**: launches the generated HTML report in the default browser. Cross-platform (`open` / `xdg-open` / `cmd /C start`), skipped when stderr is not a TTY or `CI=` is set.
- **`NO_COLOR` env var**: respected — disables ANSI codes in text output.
- **Live progress bar** on stderr for large parses (TTY only, > 500 MB total input).
- **Aggressive vacuum counter**: `automatic aggressive vacuum` operations counted separately, surfaced as an amber stat-card in the HTML maintenance section.
- **Live structured logging**: stdlib `log` migrated to `slog`.
- **Graceful shutdown**: `context.Context` propagated through the analysis orchestration; SIGINT no longer leaves goroutines hanging.
- **Test fixture corpus**: 29 themed fixtures + 56 JSON/MD goldens regenerable via `go test -update`.

### Changed
- **HTML report**: SQLSTATE class shown inline within the events section, grouped per severity. Click-to-detail modals for queries (cross-analyzer: SQL + locks + temp files) and events. Histogram bar charts capped at 40 chars wide regardless of terminal width. Concurrent sessions chart uses a true sweep-line peak (no more streaming-counter undercount).
- **Parser pipeline**: zero-copy byte path through `StderrParser`, `parseReader` stays in `[]byte` (no Scanner.Text, no strings.Builder); zero-copy JSON via gjson; CSV msgBuf reuse. Workers now picked from file profile, not just file count.
- **Always-on parallel analyzers**: 200 MB gate dropped — Locks, TempFiles and SQL run in dedicated goroutines for any input size.
- **Streaming JSON output**: every big section (`sql_performance.queries`, executions, lock events, temp file events, sessions, connections) streams item-by-item to the writer instead of going through `MarshalIndent` on the full slice.
- **Multiple `log.Fatalf` replaced by returned errors** so the follow-mode is resilient to transient failures (disk full, permission denied, empty time window).
- **Documentation**: events / event-detail / `--last` units / shell completion / HTML click-to-detail modal added.

### Fixed
- **Connection peak**: switched from streaming `len(activeConnections)` (silently undercounted re-used PIDs) to a sweep-line over session events; orphan sessions (received without a logged disconnect) flushed at Finalize so the histogram peak matches `Maximum simultaneous`.
- **`application_name=` truncation**: long values with embedded spaces were truncated at the first space when not preceded by a comma. `findSeverityMarker` extended to 14 markers + ` SSL ` for the disconnection suffix.
- **Histogram sort determinism**: time buckets in the same minute had a non-stable sort tie-break, producing different output between runs. Lexicographic fallback added.
- **`--errors --json` empty section**: the events section was hidden when `--errors` was selected; now it's surfaced (and YAML inherits).
- **Lock counting**: dedup keyed by `(process_id, blocking_pid, lock_type)` so a single real wait that PG re-logs every `deadlock_timeout` is counted once, not 3-5 times.
- **Severity counts**: `summary.error_count` / `fatal_count` etc. were always zero; now aggregated from `EventAnalyzer`.
- **Zero-offset timezones**: parser now normalizes `+0000` to UTC across platforms (was producing different goldens between macOS and Linux).
- **`--full` text output**: the flag was plumbed but the text renderer never actually read it — `--full` text now enriches the output as documented (count histogram tempfiles, queries generating temp, waiting/blocking queries, detailed connection stats).
- **WASM progress bar**: replaced the CSS-animated bar (blocked by tinygo's cooperative scheduler during the parse) with a static "Crunching log entries…" label.

### Performance
Layered improvements — measured on the J.log corpus (1 GB stderr, 5.7 M sessions, ~37 M events):

| Stage (cumulative) | RSS J.log (default GOGC) |
|---|---|
| v0.9.0 baseline | ~3000 MB |
| Drop mmap path, unify on bufio | 2029 MB |
| Compact `SQLAnalyzer.executions` (parallel slices + queryID interning) | ~1351 MB |
| Index-based sweepline + chunked compactExecutions | ~1162 MB |
| No-materialize `ConnectionMetrics` (chunks/iterators) | **583 MB** |

Net **−80 % RSS** (default) / **−87 %** with `GOGC=20`. The §2 Performance P0 of the April 2026 audit is closed.

Other perf items: zero-copy stderr parser, streaming session distribution P², streaming JSON section emitters, `madvise(DONTNEED)` on prefix mmap (later obsolete after mmap removal), `pgzip` parallel decompression, batching channel entries (256-event batches), workers heuristic adaptive to file size profile.

### Removed
- **mmap parser path**: every optim that landed since (`bytes.IndexByte`, zero-copy, batched channels, fast-path timestamp) was applied to the bufferised path which has now overtaken mmap. `parser/mmap_parser.go` (-605 LOC) gone; bug-fixes shipped along the way (`normalizeEntryBeforeParsing` was stripping timestamps; `hasTimestampString` didn't recognize the ISO `T` separator).

### Internal
- **Test corpus**: 29 themed fixtures across 9 categories (parsers, errors, connections, locks, temp_files, sql, vacuum, misc, comprehensive). Each fixture has JSON + MD goldens regenerable via `-update`.
- **CI hardening**: staticcheck step now bloquant (22 alerts cleaned at intro), CI on push + PR for `dev` branch, `gofmt -s` blocking step. `output.FormatBytes` deduplicated (was diverging between cmd/ and output/).
- **Refactor**: `LockAnalyzer.Process` split from 385 → 47 LOC + 11 helpers; PID promoted to `LogEntry` (parsed once vs 8 call sites); WASM pipeline unified on `analysis.AggregateMetrics` + `parser.FilterStream` (470 → 177 LOC, 62 % cut).

## [0.9.0] - 2026-04-16

### Added
- **auto_explain support**: Capture and display execution plans from `auto_explain` in text, markdown, JSON, and HTML (with Visualize button to explain.dalibo.com)
- **Checkpoint frequency warnings**: Parse `checkpoints are occurring too frequently` messages with interval tracking
- **WAL distance/estimate**: Extract WAL generation stats from checkpoint complete messages, with chart in HTML
- **WAL rate / flush rate**: I/O throughput metrics for checkpoint writes
- **Blocking queries**: Extract blocking PID from lock DETAIL lines, resolve to blocking query via PID tracking
- **TCL separation**: Transaction control statements (BEGIN/COMMIT/ROLLBACK) in dedicated tab
- **7z archive support**: Parse `.7z` archives with LZMA/LZMA2 compression
- **YAML export** (`--yaml`): Structured output compatible with gomplate templates
- **Duration formatting**: Sessions > 24h displayed as `5d 19h55m` instead of `139h55m`
- **All session rows**: `--connections` flag shows all rows (no top-10 limit)

### Changed
- **HTML report polish**: Improved layout, sortable tables, chart legends, scroll hints, better concurrent sessions chart
- **Documentation**: Complete rewrite — 2-tab layout (Documentation + How-tos), streamlined from 3166 to 992 lines

### Fixed
- **Lock counting**: Deduplicate lock events — count each contention once, not every repeated "still waiting" message
- **Lock query association**: Fix regression where lock query tables were empty for logs without `log_min_duration_statement`
- **JSON plan formatting**: Prevent panic on malformed auto_explain plan text
- **Session filter**: Fix concurrent sessions chart disappearing on time filter
- **Application name spaces**: Handle multi-word `application_name` in comma-separated log_line_prefix
- **Non-PostgreSQL log lines**: Skip pgBackRest/WAL-G lines captured by logging_collector via archive_command
- **Event normalization**: Handle linguistic apostrophes in message grouping
- **Archive rotated logs**: Support rotated log filenames (e.g. `postgresql.log.2026-03-23-10`) in tar archives
- **Path traversal protection**: Validate paths in tar archive entries

### Performance
- Streaming P² median for session stats (no full sort)
- Skip filter channel hop when no filters active
- Increase channel buffer from 24k to 64k entries
- Optimize isPlanMessage and LockAnalyzer fast-path

## [0.8.0] - 2026-02-08

### Added
- **ZIP archive support**: Transparent handling of `.zip` files in CLI (Go `archive/zip`) and browser, with nested compressed entries (`.gz`, `.zst`)
- **CNPG direct log format**: Parse raw `kubectl logs` output from CloudNativePG pods
- **Web Components**: Accessible, reusable UI components replacing string-based DOM generation (`<ql-tabs>`, `<ql-modal>`, `<ql-dropdown>` with search & multi-select, `<ql-tooltip>`)
- **Time filtering for HTML reports**: Client-side time range filtering with slider and date-picker modes
- **`--json-compact` flag**: Minified JSON output (~40% smaller)
- **Temp files combined chart**: Toggle between count and size views in HTML; CLI histogram for temp file size distribution
- **Vacuum space recovered in HTML**: Removed/dead tuples space in maintenance section with total recovered tile
- **Checkpoint frequency histogram**: Checkpoint rate (events/min) in CLI text output
- **PNG export for sql-detail charts**: Export individual SQL query charts as PNG images
- **Dark mode auto-detection**: Respect `prefers-color-scheme` system preference on first load

### Changed
- **Frontend modularization**: Monolithic app.js split into ES modules bundled via esbuild (Go API)
- **Build system**: Replaced Python build script with Go-native `//go:generate` pipeline + Makefile
- **Code quality**: Fixed race condition on CSV timestamp cache, fixed silent test failures, renamed Go identifiers per conventions, removed dead code, replaced deprecated APIs

### Performance
- **Streaming JSON encoder**: Reduced allocations for large JSON exports
- **WASM Uint8Array input**: `quellogParseBytes()` avoids UTF-8 round-trip for binary data
- **O(n log n) sort**: Replaced O(n²) bubble sort in syslog PID emission ordering

## [0.7.0] - 2026-01-21

### Added
- **Enhanced event hierarchy**: 3-level structure (Severity > SQLSTATE Class > Message) for better error analysis
- **SQLSTATE grouping**: Errors grouped by PostgreSQL error class (e.g., "23 - Integrity Constraint Violation")
- **Continuous monitoring mode**: `--follow` flag for real-time log surveillance with periodic refresh

### Changed
- **Events section overhaul**: Clearer display with severity percentages and message deduplication
- **Output functions refactored**: JSON and Markdown exports now accept `io.Writer` for flexible output targets
- **Parser modularization**: Split `stderr_parser.go` into focused modules for maintainability

### Improved (Accessibility)
- **Modal dialogs**: ARIA attributes (`role="dialog"`, `aria-modal`, `aria-labelledby`)
- **Focus management**: Focus trap in modals, focus restoration on close
- **Keyboard navigation**: Enhanced dropdown search with keyboard support
- **Tabs pattern**: Proper ARIA roles for tabbed interfaces

## [0.6.0] - 2025-12-20

### Added
- **Standalone HTML reports**: `--html` flag generates self-contained HTML files with embedded zstd-compressed JSON data, decoded client-side via fzstd
- **Comprehensive report mode**: `--full` flag displays all sections with detailed SQL analysis
- **CloudNative-PG (CNPG) support**: Parse Kubernetes-wrapped PostgreSQL logs from CNPG operator
- **Session events in JSON**: Raw connection start/end times for client-side concurrent sessions visualization

### Changed
- **JSON duration fields**: Replaced duration strings with `duration_ms` numeric fields for programmatic access

### Fixed
- **Event detection**: Prevented false positive ERROR counts in event analysis
- **Lock formatting**: Consistent duration formatting for lock wait times

## [0.5.0] - 2025-12-03

### Added
- **Syslog RFC5424 and BSD support**: Full parsing of RFC5424 structured syslog and BSD-style syslog formats
- **Automatic `log_line_prefix` detection**: Heuristic-based detection achieving 100% metadata extraction accuracy
- **SQL overview report**: `--sql-overview` flag with query category breakdown (DML/DDL/TCL/UTILITY) and type distribution
- **Enhanced client statistics**: `--clients` now shows activity counts, cross-tabulations, and `[X more...]` indicators
- **Concurrent sessions histogram**: Visual distribution of simultaneous database connections over time
- **Detailed session analytics**: Enriched connection statistics with session duration percentiles
- **Dedicated JSON exports for SQL analysis**: `--sql-summary --json` and `--sql-detail --json` produce focused exports

### Changed
- **Performance optimizations**:
  - CSV and temp file parsing optimizations
  - Analyzer pre-filters ~2x faster processing
  - Parallel TempFileAnalyzer with SQL analyzer
  - Fast-path event type and vacuum detection
- **Test infrastructure overhaul**: Comprehensive fixtures across 6 formats (stderr, CSV, JSON, syslog, syslog_bsd, syslog_rfc5424) with format parity validation
- **Enriched Markdown exports**: Connections and clients sections now include detailed analytics

### Fixed
- **User double-counting**: Resolved duplicate user counts in entity metrics
- **Syslog parsing**: Improved timestamp and metadata extraction for edge cases
- **Histogram consistency**: Fixed extractPrefixFields alignment issues
- **Flag validation**: `--json` and `--md` flags now properly rejected when used together

## [0.4.0] - 2025-11-21

### Added
- **Enhanced SQL summary report**: Section headers for TEMP FILES and LOCKS, improved metrics display
- **Visual histograms in SQL detail**: TIME, TEMP FILES, and LOCKS distribution charts
- **Markdown export for SQL reports**: `--sql-summary` and `--sql-detail` now support `--md` flag
- **SQL formatter**: Readable formatting for normalized queries in `--sql-detail`
- **Relative time filtering**: `--last` flag for time-based queries (e.g., `--last 24h`, `--last 7d`)
- **Error class reporting**: `--errors` flag for SQLSTATE-based error analysis
- **SQLSTATE extraction**: Support for CSV and JSON log formats
- **Stdin streaming**: Accept logs from stdin with `-` argument
- **Host/client tracking**: Entity metrics now include host information
- **Comprehensive MkDocs documentation**: Complete user guide with examples
- **Basic log format detection for cloud providers**: AWS RDS/Aurora, Azure Database, and Google Cloud SQL PostgreSQL

### Fixed
- **Query table display**: Column width calculation based on available terminal space
- **Error classes**: SQLSTATE now displayed correctly in events section
- **File access errors**: Improved handling for inaccessible files
- **JSON export**: Flag validation for incompatible combinations

## [0.3.1] - 2025-11-13
### Fixed
- **Display formatting**: Fixed tempfiles and locks table presentation
  - Fixed SQLID column alignment and table header widths
  - Fixed query tables visibility in `--sql-summary` output
- **File scanning**: Fixed zstd format detection for directory traversal
- **Code organization**: Extracted histogram computation to dedicated module (`output/histogram.go`)

## [0.3.0] - 2025-11-11
### Added
- **Lock event analysis**: Complete lock tracking with acquired/waiting events, wait times, and query association
  - Lock type and resource type distribution
  - "Acquired locks by query" and "Most frequent waiting queries" tables
  - `--locks` flag for focused lock reports

- **Temporary file analysis**: SQL query association with 99.79% coverage
  - Multi-pattern recognition across stderr, CSV, and JSON formats
  - Support for STATEMENT, QUERY field, and CONTEXT associations
  - PID-based fallback matching
  - Top queries by temp file size with cumulative statistics

- **Compression and archive support**: Transparent compressed log handling
  - gzip/pgzip (.gz), zstd (.zst, .zstd), and tar archives (.tar, .tar.gz, .tar.zst)
  - Nested compression handling with automatic format detection

- **Memory-mapped I/O**: Zero-copy stderr parsing (3% faster, 60% fewer allocations)
- **Adaptive parallelization**: File size-based worker allocation for optimal performance

### Changed
- **Performance optimizations**:
  - Parallel SQL analysis: up to 20% faster on large files
  - LRU normalization cache: Eliminates 99.97% of redundant normalizations
  - IndexByte fast-path for lock and parsing operations
  - Memory footprint reduced up to 50% on workloads with query repetition

- **SQL normalization**: Improved handling of numeric literals and edge cases
- **Test coverage**: Added format equivalence tests and SQL normalization edge case tests
- **Deterministic output**: Consistent query ordering across runs

### Fixed
- **Query normalization**: Standalone numeric literals (e.g., "id = 123") now correctly normalize to "id = ?"

## [0.2.0] - 2025-10-31
### Added
- **JSON log format support**: Native PostgreSQL jsonlog format detection and parsing
- **CSV log format support**: Full CSV log parsing capability
- **Automatic format detection**: Intelligent detection for stderr/syslog, CSV, and JSON formats
- **Streaming pipeline architecture**: ~35% performance improvement on larger files through reduced contention
- **Markdown export**: Export reports in Markdown format
- **Enhanced histograms**: Visual bar charts for query duration and load distribution
- **Checkpoint reporting**: Detailed per-event analysis with breakdown by event type
- **JSON summary export**: Complete metrics export including histogram data and autovacuum details
- **Modular text output**: Per-section flags to show/hide report sections
- **Non-regression tests**: Comprehensive test suite for reliability

### Changed
- **Major refactoring**: Reorganized cmd, parser, and analysis packages for better maintainability
- **Optimized parsing**: Multiple performance improvements including:
  - String operation optimizations (~10% faster on large files)
  - Increased bufio.Scanner buffer for better I/O performance
  - Optimized autovacuum analysis (up to 50% improvement)
  - Optimized temporary file parsing (+23% performance)
  - Memory optimization by passing data by reference
- **Improved syslog support**: Better date format parsing and handling
- **Enhanced format detection**: More reliable log format identification
- **SQL ID generation**: Switched to optimized base64 query IDs
- **Harmonized histograms**: Consistent width across all bar charts

### Fixed
- **Division by zero**: Fixed crash in query load histogram for very short time ranges
- **JSON detection**: Corrected JSONL (newline-delimited JSON) format detection
- **Last line parsing**: Fixed bug where last log line was skipped
- **Checkpoint duration**: Corrected checkpoint duration measurement
- **Table sorting**: Fixed sorting issues in maintenance reports
- **Session time extraction**: Improved connection metrics accuracy

### Removed
- `--grep` flag: Favor standard Unix tools (grep, awk) for raw log filtering

## [0.1.0] - 2025-02-17
### Added
- Initial release with PostgreSQL stderr format parsing
- CLI interface with time-based filters (begin, end, window)
- Attribute filters (database, user, application)
- General log metrics reporting (errors, warnings, vacuums, checkpoints)
- SQL performance reporting (slowest, most frequent, most time-consuming queries)
- Detailed SQL query information extraction
- Test data in `testdata/` directory