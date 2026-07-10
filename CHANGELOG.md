# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- **Client I/O failures**: the Connections section now reports clients that vanished mid-exchange (`could not send/receive data to/from client`), broken down by direction, reason and database.
- **Skipped autovacuums/autoanalyzes**: the maintenance sections now surface relations whose autovacuum/autoanalyze was skipped on a lock (count + affected tables), flagging tables starved of maintenance.
- **Logs with a literal prefix before the timestamp now parse**: a `log_line_prefix` with a constant literal ahead of `%t`/`%m` previously defeated format detection; the shared prefix is now detected and stripped automatically.
- **Full example report in demo mode**: a "See example report" link on the drop zone loads a bundled example log through the normal pipeline, so the report (cost map, split, every section) can be explored without supplying a file.
- **Build version on the processing line**: the CLI output now starts with the version (e.g. `quellog v0.12.0 – …`), so saved output identifies its build.

### Performance
- **Large stderr logs analyze 15-25% faster**: data-parallel analysis engine shards backends across CPU cores by PID. Engages automatically on large stderr files.
- **Large CSV logs parse ~40% faster**: a parallel segment parser splits big CSV files, mirroring the stderr/JSON parallel paths.
- **CSV parsing allocates ~half as much memory**: a single-pass, zero-copy CSV scanner replaces the standard-library reader, cutting CSV-path allocations by roughly 50%.
- **Faster reports on session- and lock-heavy logs**: anchored analyzer gates, per-worker buffer reuse and callback-free sweep-line sorts cut wall time by up to a third on large stderr files.
- **Compressed logs parse in parallel**: gzip/zstd stderr logs are parsed by a worker pool, up to ~30% faster.
- **The HTML report is a few percent smaller**: multi-line CSS comments are now stripped from the embedded stylesheet, and the compressed payload is base64url-encoded so its bytes are no longer escaped inside the template's JS string.
- **Lock and temp-file analysis retain less memory on busy logs**: lock events are stored with interned fields and no longer pin their source log line — ~30% lower peak retention on a lock-heavy capture — and temp-file events are compacted the same way. Output is unchanged.

### Changed
- **Time filter is an always-visible range slider in the Summary card**: replaces the Time dropdown and re-filters on release. Multi-day logs split the slider by day.
- **`--json` output is stable run-to-run and across machines**: event-occurrence lists are sorted ascending, and PID-sharded runs order per-execution lists canonically (timestamp, query id, duration) so they don't depend on the core count.

### Fixed
- **Analyzing multiple files at once is now deterministic**: rotated log sets were parsed with non-deterministic interleaving; files are now analyzed in order, so output is byte-stable.
- **Lock metrics count each re-lock of the same resource as its own episode**: when a backend re-locks the same object, `total_events` and `acquired_events` stay in step.
- **Lock timeline no longer repeats "still waiting" re-logs**: PostgreSQL re-logs "still waiting" once per deadlock_timeout while a backend waits; the events list held a row per re-log instead of one per wait episode (the counts were already per-episode). Now consistent.
- **Time-series charts show the date on multi-day spans**: their x-axes were time-only (`00:00`, `06:00`, …), ambiguous across days; they now add the date at each day boundary, like the concurrent-sessions chart already did.
- **Report duration tile no longer shows `0s` for spans of 24h or more**: the HTML report's duration now renders days (e.g. `1d`, `2d3h`) instead of dropping a day-formatted value.
- **Maintenance elapsed times rounded to the microsecond**: a cumulative vacuum/analyze time could display e.g. `2s` for a true `3.0s` total due to float-summation noise; the rounded value is now correct and stable.
- **Time-filtering the report kept several cards on stale or reformatted values**: after moving the time slider, SQL min/max/median/p99 lost their hour tier (a `1h 12m 50s` max showed as `72m`), temp-file totals changed number format, and the checkpoints "Too Frequent" count stayed at the full-log value while its section shrank. They are re-aggregated correctly now.
- **Connections now fully re-scope under the report's time filter**: previously only the connection count re-scoped while the session stats and the per-user/database/host tables stayed on full-log values.
- **The SQL duration-distribution band disagreed with the CLI**: the HTML re-bucketed queries by their average duration instead of using the exact per-execution distribution the report already carries; it now matches the `--full` text output.
- **The SQL duration-distribution band now re-scopes under the time filter too**: it kept showing the whole-log distribution while every other SQL card re-scoped after moving the slider; it is re-bucketed from the filtered executions now.
- **The query-detail modal's duration histogram never rendered**: it read a field that no longer exists, so every value was zero and the block was dropped.
- **Event-detail "First seen" / "Last seen" were shown in UTC**: they shifted the day for non-UTC logs; now shown in the log's own clock, like the rest of the report.
- **Split-report period-heatmap bounds could read "00:00 … 00:00"**: an intraday split spanning more than one day rendered both ends dateless; they now carry the date when the split crosses days.
- **Charts kept stale colors after a theme switch**: toggling dark/light left existing charts mixing old and new colors until the next reload; they now repaint on toggle.
- **HTML report could fail to load on older browsers**: it relied on `Intl.DurationFormat` with no fallback; a local formatter now covers browsers that lack it.
- **Standalone report: tooltips and the filter dropdown are positioned correctly again**: the standalone CSS minifier stripped the spaces inside `calc(100% + 4px)`, which Chrome then dropped, mispositioning them (the CLI report was unaffected).
- **In-browser WASM tool handles more uploads**: tar archives no longer ingest macOS `._*` sidecar files (which corrupted format detection), stream-written zips (Java `ZipOutputStream`, server-side "download as zip", …) now extract correctly by reading the zip's central directory instead of the zeroed local headers, and the dev build loads its WASM module and zstd decoder again.
- **Checkpoint chart's "Other" series now counts every non-timed/WAL trigger**: it hardcoded two trigger names, so others (e.g. `immediate force wait wal`) were dropped from the chart while the Other stat card still counted them; chart and card now agree.
- **Query-detail modal's Lock Waits block renders again**: it read per-query fields that don't exist (average/max wait, lock-type breakdown); it now shows the real acquired- and still-waiting wait times.
- **Duration/time parsing in the report UI**: the Blocking Queries table mis-read a sub-second wait (`512 ms` as 512 minutes) and dropped hours from multi-hour waits, skewing its sort and totals; microsecond/nanosecond session durations rendered as seconds and sorted as zero; and the multi-day time slider's day labels could drift up to an hour across a daylight-saving change. All corrected.
- **Analyzing three or more compressed or archived logs could hang**: the ordered multi-file fan-in bounded in-flight files with a two-slot window that workers acquired out of file order but the drain released in order, so a scheduling race could deadlock with no output and no error; files are now admitted through per-index prefetch permits tied to the drain order.
- **PID-sharding now engages for a directory argument**: the decompressed-size estimate ran on the raw arguments, so a directory measured its inode size and fell under the sharding threshold; it now measures the expanded file list, so pointing quellog at a log directory gets the same speed-up as a glob.
- **A mislabeled `.log` member in a tar archive no longer risks running out of memory**: a member named `*.log` whose content is not recognizable stderr (JSON, a shifted timestamp prefix, foreign text) was read to end-of-file into a single allocation before yielding nothing — a multi-gigabyte spike on large archives; the stream parser now caps the boundary buffer and warns once.
- **Logs mixing timezone offsets render each event in its own offset**: events were rebased into the timezone of the first event seen, shifting the displayed clock of later events across a daylight-saving change or a mixed-offset multi-file set; every event stream — locks, temp files, top events, SQL executions and connections — now renders each event in its own offset (the absolute instant was always correct).
- **More report sections re-scope under the time slider, and the rest are flagged**: the temp-file top-queries table, the top events and the SQL query mix now re-aggregate to the selected window; sections that cannot be recomputed in the browser (the events severity distribution, locks, maintenance, and the per-user/database/host tables) are marked "whole-log" instead of silently showing full-log figures as filtered.
- **The in-browser analyzer no longer silently drops rotated logs from a tar upload**: it accepted only exact `.log`/`.csv`/`.json` names, so a `/var/log/postgresql/` tarball kept the live log and dropped `postgresql.log.1`, `postgresql.log.2.gz`, the Debian `…-main.log.1`, etc.; rotated names are accepted now (matching the CLI) and any skipped entry is logged to the console.
- **The event modal's First/Last-seen and its activity sparkline now show the same clock**: the cards used the log's clock while the chart axis used the viewer's browser timezone; every time-axis chart renders in the log's clock now.
- **The time slider's connection figures now match the CLI**: orphan sessions (no disconnect line) were counted as disconnections and genuine last-second disconnects were dropped, and the peak-concurrent tie-break was inverted; the re-aggregation now uses the backend's explicit orphan flag and the backend tie-break.
- **Time-filtered temp-file totals are byte-exact**: they were rebuilt by re-parsing rounded display strings and drifted from the CLI; they now sum the exact byte sizes the payload carries.
- **Prefixed logs inside a tar archive parse again**: a log carrying a literal prefix before its timestamp parsed correctly as a plain or compressed file but yielded zero entries as a `.log` member inside a tar; the archive path now runs the same leading-prefix detection the plain path uses.

### Internal
- **Internal cleanup**: removed dead code and de-duplicated the `output/` renderers into shared helpers, with no change to any output (byte-identical on the sample matrix).
- **Web report internals restructured**: the report's JavaScript was split into per-section modules and its chart builders unified behind shared factories, under a new JS test net, with no change to the rendered report (0-pixel diff on the sample matrix).
- **CI runs the web JS test net**: the JavaScript unit tests and the window-ABI / CSS / data-key contract linters now gate merges (previously local-only via `make test-web`); the pixel-visual harness stays local (system Chrome + macOS baselines).
- **CSV scanner boundary parity**: the lenient post-quote skip now refills at the read-buffer boundary like its sibling loops, so malformed quoting that straddles the boundary no longer truncates the record.
- **Stream chunk pool releases oversized buffers**: after one giant log entry grew a chunk buffer past the base size, it is dropped instead of being returned to the pool and pinned across the in-flight window until the next GC.

## [0.11.0] - 2026-06-23

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