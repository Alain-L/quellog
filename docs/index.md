# quellog

**quellog** is a high-performance PostgreSQL log analyzer designed to help database administrators and developers understand their PostgreSQL instances through comprehensive log analysis. Built with Go, it processes gigabytes of log data in seconds, providing actionable insights about query performance, database operations, and system health.

```console
$ quellog /var/log/postgresql/postgresql.log
quellog – 835,059 entries processed in 0.90 s (100 MB)

SUMMARY

  Start date                : 2024-12-31 23:00:08 UTC
  End date                  : 2025-02-15 04:05:38 UTC
  Duration                  : 1085h5m30s
  Total entries             : 835059
  Throughput                : 927843 entries/s
...
```

## Why quellog?

PostgreSQL generates rich logs that contain invaluable information about database behavior, but analyzing these logs manually is time-consuming and error-prone. quellog automates this process, transforming raw log files into clear, structured reports that help you:

- **Identify performance bottlenecks** through SQL query analysis and timing metrics
- **Understand database workload** via connection patterns and query distribution
- **Detect operational issues** by tracking errors, warnings, and system events
- **Monitor maintenance operations** including vacuum, analyze, and checkpoint activities
- **Analyze lock contention** to identify blocking queries and resource conflicts
- **Track temporary file usage** to find queries exceeding `work_mem` limits

## Key Features

### Multi-Format Support

quellog automatically detects and parses PostgreSQL logs in multiple formats:

- **stderr/syslog format** - Traditional PostgreSQL text logs
- **CSV format** - Structured comma-separated value logs
- **JSON format** - Modern JSON logging output (including Google Cloud SQL and Azure Database for PostgreSQL)

### Compression & Archive Handling

Process logs directly without manual decompression:

- **gzip** (`.gz`) - Parallel decompression for faster processing
- **zstd** (`.zst`, `.zstd`) - High-compression ratio support
- **tar archives** (`.tar`, `.tar.gz`, `.tar.zst`, `.tgz`, `.tzst`) - Recursive archive processing

### Comprehensive Analysis

quellog extracts and aggregates metrics across multiple dimensions:

- **SQL Performance** - Query durations, execution counts, percentiles (median, p99)
- **Temporary Files** - Size tracking with query association
- **Lock Events** - Wait times, lock types, deadlock detection
- **Connections** - Session durations, connection rates, client distribution
- **Checkpoints** - Frequency, types, write times
- **Vacuum Operations** - Autovacuum/autoanalyze activity, space recovery
- **Error Analysis** - Severity distribution

### Powerful Filtering

Focus on specific subsets of your logs:

- **Time-based filtering** - Analyze specific date ranges or time windows
- **Attribute filtering** - Filter by database, user, application, or host
- **Exclusion filters** - Exclude specific users or patterns
- **Section filtering** - Display only the sections you need

### Flexible Output

Export results in the format that works for your workflow:

- **Text** - Human-readable terminal output with ANSI colors
- **JSON** - Structured data for automation and integration
- **Markdown** - Documentation-friendly format for reports

## Performance

quellog is built for speed, utilizing:

- **Streaming architecture** - Processes logs without loading everything into memory
- **Concurrent parsing** - Parallel processing of multiple log files
- **Memory-mapped I/O** - Fast file access for stderr/syslog formats
- **Optimized algorithms** - Query normalization caching, efficient pattern matching

### Benchmark Results

| Log Type | Size | Processing Time | Throughput | Notes |
|----------|------|-----------------|------------|-------|
| **stderr** | 54 MB | 0.34 s | ~159 MB/s | Memory < 100 MB |
| **stderr** | 1.0 GB | 3.41 s | ~300 MB/s | |
| **CSV** | 1.2 GB | 5.50 s | ~218 MB/s | |
| **tar.gz archive** | 60 GB | 4m 34s | ~225 MB/s | Parallel decompression |

quellog can process typical production log files (100 MB - 1 GB) in seconds, making it suitable for both ad-hoc analysis and automated reporting pipelines.

## Architecture

quellog's design prioritizes both performance and accuracy:

1. **Format Detection** - Automatic identification via file extension and content sampling
2. **Streaming Parsing** - Log entries are processed one at a time through buffered channels
3. **Concurrent Analysis** - Multiple specialized analyzers process entries in parallel
4. **Query Normalization** - SQL queries are parameterized for aggregation (e.g., `WHERE id = 1` → `WHERE id = $1`)
5. **Association Logic** - Advanced algorithms link queries to tempfiles and locks across log formats

## Use Cases

### Development

- **Query optimization** - Identify slow queries and analyze execution patterns
- **Memory tuning** - Find queries that exceed `work_mem` and generate temporary files
- **Lock analysis** - Detect blocking queries and deadlock conditions

### Operations

- **Incident analysis** - Review logs around specific timeframes to understand issues
- **Capacity planning** - Analyze connection patterns and checkpoint frequency
- **Performance monitoring** - Track SQL performance trends over time

### Compliance & Auditing

- **Connection tracking** - Monitor database access by user, application, and host
- **Query auditing** - Review what queries were executed and when
- **Error reporting** - Aggregate and classify database errors

## Getting Started

Ready to analyze your PostgreSQL logs? Check out the [Quick Start Guide](quick-start.md) to get up and running in minutes.

For detailed installation instructions, see the [Installation Guide](installation.md).

## License

quellog is open source software licensed under the PostgreSQL License.

## Community

- **Issues**: Report bugs, request features, or share non-standard log formats on [GitHub Issues](https://github.com/Alain-L/quellog/issues)
- **Contributing**: Contributions are welcome! See [CONTRIBUTING.md](https://github.com/Alain-L/quellog/blob/main/CONTRIBUTING.md)

!!! info "Help Us Improve"
    If quellog doesn't support your specific `log_line_prefix` configuration, please open an issue with your settings and a sample log. We regularly add support for new formats based on community feedback!

---

!!! tip "Next Steps"

    - [Quick Start Guide](quick-start.md) - Get started in 5 minutes
    - [Installation](installation.md) - Detailed installation instructions
    - [PostgreSQL Setup](postgresql-setup.md) - Configure PostgreSQL for optimal logging
