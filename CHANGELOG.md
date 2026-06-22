# Changelog

All notable changes to this project will be documented in this file.

## [0.11.0] - 2026-06-26

### Added
- **SERVER section**: a server-lifecycle timeline — starts, restarts, shutdown types, backend crashes (by signal), crash recovery and hot configuration changes — together with replication health (stream reconnects, conflicts with recovery and the queries they cancelled, invalidated slots, terminations).
- **Split reports** (`--split <interval>`): partition an HTML report into selectable time periods (`1d`, `3h`, `5m`, …) with a period navigator and a volume heatmap, in a single self-contained file.
- **Cost map** (SQL Performance, HTML): an interactive log-log scatter of every query by execution count × average duration, coloured by cumulative time, with iso-cost and top-1%/10% Pareto reference lines.
- **Maintenance detail**: AUTOVACUUM and AUTOANALYZE as separate sections; autovacuum continuation lines parsed (buffer/WAL/tuple/system usage, per-table elapsed); per-table stats for up to 200 tables; sortable tables in HTML.
- **Prepared statements**: capture prepared-statement names and the parameters of each query's slowest run, shown in `--sql-detail`.
- **`--sql-detail` dimensions**: top databases, users, applications and hosts for the selected query.
- **Event triggering queries**: the queries that most often triggered each event pattern (Pareto), in `--event-detail` and the HTML event modal.
- **Temp files per query**: min/max/average temp-file size per query, plus the largest single temp file.
- **Multi-format export in one pass**: combine `--html`/`--md`/`--json`/`--yaml` in a single run — parsing and analysis happen once.
- **Plan shortcut**: a per-row button in the HTML query table to open a query on explain.dalibo.com.

### Changed
- **`in (...)` normalization**: long `IN ($1, $2, …)` lists collapse to `in (...)` so identical queries aggregate together.
- **Progress bar for every format**: the live progress bar now shows for `--html`/`--json`/`--yaml`/`--md` on a TTY for large inputs, not just text output.
- **Markdown tables** aligned across the report so the raw Markdown reads cleanly.
- **Normalization** preserves the case of double-quoted identifiers.

### Fixed
- **`--begin` / `--end` are wall-clock bounds**: a zoneless timestamp matches the moment the log clock reads it, in any timezone (was treated as UTC).
- **Input robustness**: strip a leading UTF-8 BOM; detect gzip/zstd by magic bytes when the extension is wrong or missing; detect tar members by content; report a truncated/partial file as a partial success (warning) instead of "no files could be parsed"; overflow-safe parsing; reject overflowing `d`/`w`/`y` durations instead of wrapping.
- **`--errors` in JSON/YAML**: restricted to the error classes, like text and Markdown.
- **`--follow`**: invalid flag combinations fail up front instead of looping every cycle.
- **HTML report**: escape log-derived content (XSS); fix a crash on very large logs, lock-duration sorting, and lock-wait formatting (now matches the CLI).
- **Determinism**: stable tie-breaks for map-derived sorts and cross-PID lock resolution.
- **Misc**: `FormatBytes` TB scale; a mislabeled `--sql-detail` average; write/close errors surfaced on output files; a JSON buffer-aliasing bug.

### Performance
- **Parallel segment parsing** of large stderr and JSON-lines inputs: up to **1.5× faster on multi-gigabyte stderr** logs and **up to 6× on gigabyte-scale JSON-lines** logs. Plus a fast-path decoder for canonical PostgreSQL timestamps, the inline analyzers sharded across goroutines and fed in batches, bounded SQL marker scans, and 1 MB CSV read buffering.

## [0.10.0] - 2026-05-07

### Added
- **Per-event drill-down**: every event pattern gets a stable short id (`<sev>-<4-char-hash>`, e.g. `fa-6K1G`, `er-Qr5p`) shown in the `--events` output. Use `--event-detail` (`-E`) to open a full report for one or more patterns: full raw message, occurrences-over-time bar chart, first/last seen, frequency. Same drill-down available in the HTML report as a click-to-detail modal with uPlot sparkline + copy buttons.
- **Aggressive vacuum counter**: `automatic aggressive vacuum` operations counted separately, surfaced as an amber stat-card in the HTML maintenance section.
- **`--last` / `--window` extended units**: `d` (days), `w` (weeks), `y` (years) in addition to the existing `s`/`m`/`h`. Example: `--last 1d`, `--last 5y`.
- **Live progress bar** on stderr for large parses (TTY only, > 500 MB total input).
- **`--open`**: launches the generated HTML report in the default browser. Cross-platform (`open` / `xdg-open` / `cmd /C start`), skipped when stderr is not a TTY or `CI=` is set.
- **`completion` subcommand**: `quellog completion bash|zsh|fish|powershell` writes a completion script to stdout (Cobra-generated).
- **`--quiet` / `-q`**: suppress INFO logs (keep WARN/ERROR). Useful for cron and CI invocations.
- **`NO_COLOR` env var**: respected — disables ANSI codes in text output.

### Changed
- **HTML report**: per-event click-to-detail modal with occurrences-over-time sparkline + copy-id and copy-message buttons.
- **Parser & analyzer pipeline**: zero-copy `[]byte` through `StderrParser`, zero-copy JSON via gjson, msgBuf reuse for CSV. Locks/TempFiles/SQL always run in dedicated goroutines (200 MB gate dropped). Worker count picked from file size profile.
- **Streaming JSON output**: big sections (`sql_performance.queries`, executions, lock and temp-file events, sessions, connections) stream item-by-item instead of `MarshalIndent` on the full slice.

### Fixed
- **Connection peak**: now computed via sweep-line over session events instead of a streaming counter that undercounted re-used PIDs. Orphan sessions (received with no logged disconnect) flushed at Finalize.
- **`application_name=` truncation**: long values with embedded spaces were cut at the first space.
- **Histogram sort**: deterministic across runs (lexicographic tie-break on equal-minute buckets).
- **`--errors --json` empty section**: the events section was hidden when `--errors` was selected; now it's surfaced (and YAML inherits).
- **HTML Blocking Queries dedup**: the table aggregated raw lock events, so a single wait re-logged every `deadlock_timeout` (default 1s) appeared multiple times. Deduped by unique wait.
- **Severity counts**: `summary.error_count` / `fatal_count` etc. were always zero; now aggregated from `EventAnalyzer`.
- **Zero-offset timezones**: parser now normalizes `+0000` to UTC across platforms (was producing different goldens between macOS and Linux).
- **`--full` text output**: the flag was plumbed but the text renderer never read it. Now it does.
- **WASM progress bar**: replaced the CSS-animated bar (blocked by tinygo's cooperative scheduler during the parse) with a static "Crunching log entries…" label.
- **HTML chart dblclick reset**: uPlot's built-in dblclick auto-fitted to the current data extent, which after a zoom equals the zoomed range — so dblclick "reset" stayed stuck. Replaced with an explicit handler that mirrors the Reset button.
- **HTML SQL chart tooltip**: missing `fmt` import surfaced as `ReferenceError` on every cursor move over the SQL Performance chart (silent in production, visible only in DevTools).

### Performance
- **Memory footprint**: roughly −80 % RSS on multi-gigabyte stderr corpora (−87 % with `GOGC=20`). Heavy analyzers (SQL, connections) moved to chunked parallel-slice storage; connection metrics expose iterators instead of materializing slices.
- **Streaming session distribution**: P² sketch replaces the materialized duration array — constant memory regardless of session count.
- **WASM**: tinygo linear-memory ceiling pressure cut on big browser logs.

### Removed
- **mmap parser path** (`parser/mmap_parser.go`, -605 LOC): the buffered path took over after a year of optims focused on it.

### Internal
- **Structured logging**: `log` → `slog` (enables `--quiet`).
- **Graceful shutdown**: `context.Context` propagated through the analysis orchestration; SIGINT no longer leaves goroutines hanging.
- **Test corpus**: 29 themed fixtures + JSON/MD goldens regenerable via `go test -update`.
- **CI hardening**: blocking `staticcheck` and `gofmt -s` steps; runs on push and PR for `dev`.
- **Refactor**: `LockAnalyzer.Process` split (385 → 47 LOC + 11 helpers); WASM pipeline unified on `analysis.AggregateMetrics` + `parser.FilterStream` (-62 %).

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