# Quick Start Guide

Get up and running with quellog in 5 minutes. This guide will walk you through installing quellog and analyzing your first PostgreSQL log file.

## Installation

=== "Debian/Ubuntu (.deb)"

    Download and install the package:

    ```bash
    # Download (replace 0.1.0 with latest version)
    wget https://github.com/Alain-L/quellog/releases/download/v0.1.0/quellog_0.1.0_amd64.deb

    # Install
    sudo dpkg -i quellog_0.1.0_amd64.deb

    # Verify
    quellog --version
    ```

=== "Red Hat/Fedora (.rpm)"

    Download and install the package:

    ```bash
    # Download (replace 0.1.0 with latest version)
    wget https://github.com/Alain-L/quellog/releases/download/v0.1.0/quellog_0.1.0_amd64.rpm

    # Install
    sudo dnf install quellog_0.1.0_amd64.rpm

    # Verify
    quellog --version
    ```

=== "Linux/macOS (tar.gz)"

    Download and extract the binary:

    ```bash
    # Download (replace v0.1.0 with latest version and adjust platform)
    # Linux amd64:
    wget https://github.com/Alain-L/quellog/releases/download/v0.1.0/quellog_v0.1.0_linux_amd64.tar.gz
    tar -xzf quellog_v0.1.0_linux_amd64.tar.gz

    # macOS (darwin) amd64:
    # wget https://github.com/Alain-L/quellog/releases/download/v0.1.0/quellog_v0.1.0_darwin_amd64.tar.gz

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

## Your First Analysis

### Basic Usage

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

### Example Output

Here's what you'll see when running quellog:

```
quellog – 1,234 entries processed in 0.15 s (45.2 MB)

SUMMARY

  Start date                : 2025-01-13 00:00:00 UTC
  End date                  : 2025-01-13 23:59:59 UTC
  Duration                  : 23h59m59s
  Total entries             : 1,234
  Throughput                : 8,227 entries/s

SQL PERFORMANCE

  Query load distribution | ■ = 10 s

  00:00 - 03:59  ■■■■■■■■■■ 105 s
  04:00 - 07:59  ■■■■■■■ 78 s
  08:00 - 11:59  ■■■■■■■■■■■■■■ 145 s
  12:00 - 15:59  ■■■■■■■■ 89 s
  16:00 - 19:59  ■■■■■■ 67 s
  20:00 - 23:59  ■■■■ 42 s

  Total query duration      : 8m 46s
  Total queries parsed      : 456
  Total unique query        : 127

  Query max duration        : 2.34 s
  Query min duration        : 12 ms
  Query median duration     : 145 ms
  Query 99% max duration    : 1.87 s

EVENTS

  LOG     : 1,180
  WARNING : 3
  ERROR   : 1

...
```

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

## Processing Multiple Files

quellog can process multiple files and directories:

!!! tip "Performance"
    When processing multiple files, quellog uses parallel workers automatically (up to 4 workers based on CPU cores) to maximize throughput.

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

## Exporting Results

### JSON Export

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

### Markdown Export

Generate markdown reports for documentation:

```bash
quellog /var/log/postgresql/*.log --md > report.md
```


## Next Steps

You're now familiar with the basics of quellog! Explore more:

- [Default Report](default-report.md) - Understanding all report sections
- [SQL Analysis](sql-reports.md) - Deep dive into query performance
- [Filtering Logs](filtering-logs.md) - Focus on specific time ranges and attributes
- [PostgreSQL Setup](postgresql-setup.md) - Optimize your PostgreSQL logging configuration
