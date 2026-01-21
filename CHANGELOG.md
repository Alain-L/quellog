# Changelog

All notable changes to this project will be documented in this file.

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