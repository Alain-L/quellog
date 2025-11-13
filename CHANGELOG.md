# Changelog

All notable changes to this project will be documented in this file.

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