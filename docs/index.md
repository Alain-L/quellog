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

## Quick Start

Get up and running with quellog in 5 minutes.

### Installation

=== "Debian/Ubuntu (.deb)"

    Download and install the package:

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.deb"

    # Install
    sudo dpkg -i quellog_${LATEST_VERSION#v}_amd64.deb

    # Verify
    quellog --version
    ```

=== "Red Hat/Fedora (.rpm)"

    Download and install the package:

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.rpm"

    # Install
    sudo dnf install quellog_${LATEST_VERSION#v}_amd64.rpm

    # Verify
    quellog --version
    ```

=== "Linux/macOS (tar.gz)"

    Download and extract the binary:

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    # Linux amd64:
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_linux_amd64.tar.gz"
    tar -xzf quellog_${LATEST_VERSION}_linux_amd64.tar.gz

    # macOS (darwin) amd64:
    # curl -LO "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_darwin_amd64.tar.gz"
    # tar -xzf quellog_${LATEST_VERSION}_darwin_amd64.tar.gz

    # Move to PATH
    sudo install -m 755 quellog /usr/local/bin/

    # Verify installation
    quellog --version
    ```

=== "macOS (Homebrew)"

    !!! warning "Coming Soon"
        Homebrew installation is not yet available. Use the tar.gz installation method above.

=== "Build from Source"

    ```bash
    # Clone the repository
    git clone https://github.com/Alain-L/quellog.git
    cd quellog

    # Build
    go build -o quellog .

    # Optionally move to PATH
    sudo install -m 755 quellog /usr/local/bin/
    ```

For detailed installation instructions for all platforms, see the [Installation Guide](installation.md).

### Your First Analysis

The simplest way to use quellog is to point it at a log file:

```bash
quellog /var/log/postgresql/postgresql-15-main.log
```

quellog will automatically:

1. Detect the log format (stderr, CSV, or JSON)
2. Parse all entries
3. Aggregate metrics across all analysis categories
4. Display a comprehensive report

!!! info "Automatic Format Detection"
    quellog automatically detects log format and compression. You don't need to specify file types manually.

### Common First Steps

#### 1. Analyze a specific time window

```bash
# Specific 2-hour window
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 16:00:00"

# Last hour of logs (example with specific end time)
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 23:00:00" \
  --end "2025-01-14 00:00:00"
```

!!! info "Time Window Filtering"
    Use `--begin` and `--end` together to specify a time range. A standalone `--window` flag for relative time ranges is coming soon.

!!! warning "Timestamp Format"
    Make sure your log timestamps match the format you're using with `--begin` and `--end`. The format should be `YYYY-MM-DD HH:MM:SS`.

#### 2. Focus on a specific database

```bash
# Production database only
quellog /var/log/postgresql/*.log --dbname production

# Multiple databases
quellog /var/log/postgresql/*.log --dbname app_db --dbname analytics_db
```

#### 3. Analyze SQL performance

```bash
# Show SQL summary with top queries
quellog /var/log/postgresql/*.log --sql-summary

# Get details for a specific slow query
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3d
```

#### 4. Focus on specific sections

You can display only specific sections of the report:

```bash
# Show only tempfile section
quellog /var/log/postgresql/*.log --tempfiles

# Show only lock analysis
quellog /var/log/postgresql/*.log --locks

# Combine multiple sections
quellog /var/log/postgresql/*.log --tempfiles --locks
```

### Processing Multiple Files

quellog can process multiple files and directories:

!!! tip "Performance"
    When processing multiple files, quellog uses parallel workers automatically (up to 8 workers based on CPU cores) to maximize throughput.

```bash
# Multiple files
quellog postgresql-2025-01-12.log postgresql-2025-01-13.log

# Entire directory
quellog /var/log/postgresql/

# Glob patterns
quellog /var/log/postgresql/postgresql-*.log

# Compressed archives
quellog /backups/logs/postgresql-2025-01.tar.gz
```

### Exporting Results

#### JSON Export

Export results as JSON for further processing:

```bash
quellog /var/log/postgresql/*.log --json > report.json
```

Use with `jq` for specific queries:

```bash
# Get checkpoint count
quellog /var/log/postgresql/*.log --json | jq '.checkpoints.total_checkpoints'
# Output: 19

# Get total queries parsed
quellog /var/log/postgresql/*.log --json | jq '.sql_performance.total_queries_parsed'
# Output: 456

# Get query median duration
quellog /var/log/postgresql/*.log --json | jq '.sql_performance.query_median_duration'
# Output: "145 ms"
```

#### Markdown Export

Generate markdown reports for documentation:

```bash
quellog /var/log/postgresql/*.log --md > report.md
```

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

## Next Steps

Now that you're familiar with quellog, explore more advanced features:

- [Installation Guide](installation.md) - Detailed installation for all platforms
- [PostgreSQL Setup](postgresql-setup.md) - Configure PostgreSQL for optimal logging
- [Default Report](default-report.md) - Understanding all report sections
- [SQL Analysis](sql-reports.md) - Deep dive into query performance analysis
- [Filtering Logs](filtering-logs.md) - Advanced filtering techniques
- [JSON Export](json-export.md) - Structured data export for automation
- [Markdown Export](markdown-export.md) - Generate documentation-ready reports

## License

quellog is open source software licensed under the PostgreSQL License.

## Community

- **Issues**: Report bugs, request features, or share non-standard log formats on [GitHub Issues](https://github.com/Alain-L/quellog/issues)
- **Contributing**: Contributions are welcome! See [CONTRIBUTING.md](https://github.com/Alain-L/quellog/blob/main/CONTRIBUTING.md)

!!! info "Help Us Improve"
    If quellog doesn't support your specific `log_line_prefix` configuration, please open an issue with your settings and a sample log. We regularly add support for new formats based on community feedback!
