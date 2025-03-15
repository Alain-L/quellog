# TODO

## Core Features
- Detect parameter changes in logs (e.g., `ALTER SYSTEM`, config reload).
- Improve query hashing, use PostgreSQL query ID if available.
- Add security checks (password changes, plain-text password detection).
- Implement benchmark mode (`--benchmark` flag).

## Log Parsing & Detection
- Improve autodetection of log formats (stderr, syslog, CSV, JSON).
- Handle edge cases from pgBadger (orphan lines, remote files, log format distinctions).
- Parse `log_line_prefix` like `parse_log_prefix` in pgBadger.
- Enhance SQL parsing (`parse_query` from pgBadger).
- Normalize log parsing functions (`parser/normalization.go`?).
- Improve performance by reducing regex usage.

## SQL Analysis & Reporting
- Report top queries by percentile (80/90/99).
- Differentiate slowest individual, normalized, and parameterized queries.
- Identify busiest query windows.
- Group similar queries together.
- Add `--sql-detail` for in-depth query analysis.
- Include SQL hints and `auto_explain` insights.

## Checkpoints & WAL
- Distinguish ideal vs. non-ideal checkpoints (`timeout` vs. `max_wal_size`).
- Explore WAL reporting.

## API & Integration
- Evaluate REST or gRPC API for internal data access.
- Add JSON ourput for SQL report
- Consider packaging (Debian, Docker).
- Investigate compressed file support.

## Terminal UI (TUI)
- Explore `tview` or `Bubble Tea` for interactive usage.
- Add autocompletion.
- explicit format flag

## Housekeeping & Refactoring
- Finalize refactoring, standardize comments (English).
- Improve documentation (user docs, `godoc`).
- Organize test suite and add structured tests.
- Validate CLI flags.
- Add verbose / debug infos
- Errors handling

## Additional Format Support
- pgBouncer
- CNPG
- Redshift
- RDS
- Logplex
