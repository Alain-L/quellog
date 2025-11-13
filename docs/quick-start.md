# Quick Start Guide

Get up and running with quellog in 5 minutes. This guide will walk you through installing quellog and analyzing your first PostgreSQL log file.

## Installation

=== "Linux/macOS (Binary)"

    Download the latest release for your platform:

    ```bash
    # Download (replace with latest version and your platform)
    wget https://github.com/Alain-L/quellog/releases/download/v0.x.x/quellog_Linux_x86_64.tar.gz

    # Extract
    tar -xzf quellog_Linux_x86_64.tar.gz

    # Move to PATH
    sudo mv quellog /usr/local/bin/

    # Verify installation
    quellog --version
    ```

=== "macOS (Homebrew)"

    !!! warning "Coming Soon"
        Homebrew installation is not yet available. Use the binary installation method above.

=== "Build from Source"

    ```bash
    # Clone the repository
    git clone https://github.com/Alain-L/quellog.git
    cd quellog

    # Build
    go build -o quellog .

    # Optionally move to PATH
    sudo mv quellog /usr/local/bin/
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
# Last hour of logs
quellog /var/log/postgresql/*.log --begin "2025-01-13 23:00:00"

# Specific 2-hour window
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 16:00:00"
```

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

#### 4. Check temporary file usage

```bash
# Show only tempfile section
quellog /var/log/postgresql/*.log --tempfiles
```

#### 5. Examine lock contention

```bash
# Display lock analysis
quellog /var/log/postgresql/*.log --locks
```

## Processing Multiple Files

quellog can process multiple files and directories:

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
# Extract top 5 slowest queries
quellog /var/log/postgresql/*.log --json | \
  jq '.sql.query_stats | to_entries | sort_by(-.value.max_time) | .[0:5]'

# Get checkpoint count
quellog /var/log/postgresql/*.log --json | jq '.checkpoints.complete_count'
```

### Markdown Export

Generate markdown reports for documentation:

```bash
quellog /var/log/postgresql/*.log --md > report.md
```

## Common Workflows

### Daily Performance Review

```bash
#!/bin/bash
# daily_review.sh - Analyze yesterday's logs

YESTERDAY=$(date -d "yesterday" +%Y-%m-%d)
LOG_DIR="/var/log/postgresql"

quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --json > "/reports/daily_$(date -d yesterday +%Y%m%d).json"
```

### Incident Investigation

When investigating an issue that occurred around 14:30:

```bash
# Analyze 1-hour window around incident
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-summary

# Focus on specific database that had issues
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --dbname problematic_db \
  --events --locks
```

### Finding Memory-Hungry Queries

```bash
# Show queries creating large temporary files
quellog /var/log/postgresql/*.log --tempfiles --sql-summary
```

This will display the queries that exceeded `work_mem` and had to spill to disk, sorted by total temporary file size.

## Next Steps

Now that you've run your first analysis, explore more advanced features:

- [Filtering Logs](filtering-logs.md) - Learn about all filtering options
- [SQL Analysis](sql-reports.md) - Deep dive into query performance analysis
- [Default Report](default-report.md) - Understand every section of the report
- [PostgreSQL Setup](postgresql-setup.md) - Configure PostgreSQL for comprehensive logging

## Tips & Tricks

!!! tip "Performance Tip"
    For very large log files (> 1 GB), quellog uses parallel processing automatically. You can also pre-filter logs at the shell level to reduce processing time:

    ```bash
    # Pre-filter before analysis
    grep "2025-01-13 14:" /var/log/postgresql/huge.log | quellog -
    ```

!!! warning "Timestamp Parsing"
    Make sure your log timestamps match the format you're using with `--begin` and `--end`. The format should be `YYYY-MM-DD HH:MM:SS`.

!!! info "File Detection"
    quellog automatically detects log format and compression. You don't need to specify file types manually.

## Troubleshooting

### "Unknown log format"

If quellog reports an unknown log format, it may be because:

1. The log file is empty or corrupted
2. The log format is not one of the supported types (stderr, CSV, JSON)
3. The file is binary (not a text log)

Check your `log_destination` setting in PostgreSQL:

```sql
SHOW log_destination;
```

### No SQL statistics

If you don't see SQL performance data, check that `log_min_duration_statement` is enabled:

```sql
SHOW log_min_duration_statement;
```

It should be set to `0` (log all queries) or a specific threshold (e.g., `1000` for queries over 1 second).

### No lock information

Lock analysis requires `log_lock_waits = on` in your PostgreSQL configuration:

```sql
SHOW log_lock_waits;
```

See [PostgreSQL Setup](postgresql-setup.md) for complete configuration guidance.
