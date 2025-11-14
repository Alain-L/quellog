# PostgreSQL Configuration

To get the most comprehensive insights from quellog, PostgreSQL must be configured to log the right information. This guide covers the logging settings that affect quellog's analysis capabilities.

## Logging Destination

PostgreSQL can write logs to different destinations. Each has trade-offs.

### stderr (Recommended for quellog)

```ini
log_destination = 'stderr'
logging_collector = on
```

- **Pros**: Human-readable, easy to inspect manually
- **Cons**: Requires careful `log_line_prefix` configuration for structured parsing
- **Best for**: General-purpose logging, development environments

### csvlog

```ini
log_destination = 'csvlog'
logging_collector = on
```

- **Pros**: Structured format, dedicated query field for better query association
- **Cons**: Not human-readable without tools
- **Best for**: Production environments, automated analysis

### jsonlog (PostgreSQL 15+)

```ini
log_destination = 'jsonlog'
logging_collector = on
```

- **Pros**: Modern structured format, easy integration with log aggregation tools
- **Cons**: Larger file sizes, requires PostgreSQL 15+
- **Best for**: Cloud deployments, integration with modern observability stacks

### Multiple Destinations

You can log to multiple destinations simultaneously:

```ini
log_destination = 'stderr,csvlog'
```

This gives you both human-readable logs (`stderr`) and structured logs (`csvlog`) for analysis.

## Query Logging

### log_min_duration_statement

Controls which queries are logged based on execution time.

```ini
# Log all queries (use in development or short-term troubleshooting)
log_min_duration_statement = 0

# Log queries taking longer than 1 second
log_min_duration_statement = 1000

# Log queries taking longer than 100ms
log_min_duration_statement = 100

# Don't log query durations (default, not recommended for quellog)
log_min_duration_statement = -1
```

!!! warning "Performance Impact"
    Setting `log_min_duration_statement = 0` logs **every query**, which can:

    - Generate massive log files on busy databases
    - Increase I/O load
    - Impact overall performance

    Use this setting only in development or for temporary troubleshooting. In production, set a reasonable threshold (e.g., 100-1000ms).

!!! tip "Finding the Right Threshold"
    Start with a higher threshold (e.g., `1000` ms) and gradually lower it:

    ```bash
    # Analyze with 1s threshold
    quellog /var/log/postgresql/*.log --sql-summary

    # If you want more detail, lower the threshold and re-analyze
    ```

### log_statement

Controls which SQL statements are logged regardless of duration.

```ini
# Log no statements based on type (default)
log_statement = 'none'

# Log DDL statements (CREATE, ALTER, DROP)
log_statement = 'ddl'

# Log DDL + data modification statements (INSERT, UPDATE, DELETE)
log_statement = 'mod'

# Log all statements (very verbose!)
log_statement = 'all'
```

!!! tip "Combining with log_min_duration_statement"
    `log_statement = 'ddl'` combined with `log_min_duration_statement = 100` gives you:

    - All DDL statements (CREATE TABLE, etc.)
    - DML statements that take > 100ms

## Connection Logging

### Connection Events

```ini
# Log new connections
log_connections = on

# Log disconnections with session statistics
log_disconnections = on
```

When enabled, quellog can analyze:

- Connection rate over time
- Session duration distribution
- Clients connecting to the database

Example log output:

```
LOG:  connection received: host=192.168.1.100 port=5432
LOG:  connection authorized: user=myapp database=production
...
LOG:  disconnection: session time: 0:15:32.456 user=myapp database=production host=192.168.1.100 port=5432
```

### log_line_prefix

Defines the format of each log line. This is **critical** for quellog to extract user, database, application, and host information.

#### Recommended Format

```ini
log_line_prefix = '%t [%p]: db=%d,user=%u,app=%a,client=%h '
```

This produces:

```
2025-01-13 14:32:18 UTC [12345]: db=mydb,user=postgres,app=psql,client=192.168.1.100 LOG:  ...
```

#### Common Placeholders

| Placeholder | Description | Example |
|-------------|-------------|---------|
| `%t` | Timestamp without milliseconds | `2025-01-13 14:32:18 UTC` |
| `%m` | Timestamp with milliseconds | `2025-01-13 14:32:18.456 UTC` |
| `%p` | Process ID | `12345` |
| `%d` | Database name | `mydb` |
| `%u` | User name | `postgres` |
| `%a` | Application name | `psql` |
| `%h` | Remote host | `192.168.1.100` |
| `%r` | Remote host with port | `192.168.1.100:54321` |
| `%i` | Command tag | `SELECT`, `INSERT`, etc. |
| `%l` | Session line number | `1`, `2`, etc. |
| `%x` | Transaction ID | `1234` |
| `%q` | Produces no output (used as separator) |  |

#### Alternative Formats

=== "Minimal"

    ```ini
    log_line_prefix = '%t [%p] '
    ```

    Produces:
    ```
    2025-01-13 14:32:18 UTC [12345] LOG:  ...
    ```

    ✅ Works with quellog
    ❌ No database/user filtering available

=== "Detailed"

    ```ini
    log_line_prefix = '%m [%p] %q%u@%d %h '
    ```

    Produces:
    ```
    2025-01-13 14:32:18.456 UTC [12345] postgres@mydb 192.168.1.100 LOG:  ...
    ```

    ✅ Works with quellog
    ✅ Includes milliseconds

=== "With Transaction ID"

    ```ini
    log_line_prefix = '%t [%p]: [%l-1] user=%u,db=%d,app=%a,host=%h,xid=%x '
    ```

    Produces:
    ```
    2025-01-13 14:32:18 UTC [12345]: [1-1] user=postgres,db=mydb,app=psql,host=192.168.1.100,xid=5678 LOG:  ...
    ```

    ✅ Works with quellog
    ✅ Includes transaction ID for correlation

!!! info "CSV Log Format"
    When using `log_destination = 'csvlog'`, CSV logs have a fixed structure with dedicated fields for user, database, application, etc.

## Temporary File Logging

```ini
# Log all temporary files
log_temp_files = 0

# Log only tempfiles larger than 10 MB
log_temp_files = 10240

# Don't log temporary files (default)
log_temp_files = -1
```

Temporary files are created when queries exceed `work_mem`. Logging them helps identify memory-hungry queries.

Example log output:

```
LOG:  temporary file: path "base/pgsql_tmp/pgsql_tmp12345.0", size 104857600
STATEMENT:  SELECT * FROM large_table ORDER BY created_at
```

quellog associates temporary files with their queries and reports:

- Total tempfile count and size
- Top queries by tempfile size
- Tempfile size distribution over time

## Lock Wait Logging

```ini
# Log lock waits
log_lock_waits = on

# How long to wait before logging (default: 1s = 1000ms)
deadlock_timeout = 1000
```

When a query waits for a lock longer than `deadlock_timeout`, PostgreSQL logs it:

```
LOG:  process 12345 still waiting for AccessShareLock on relation 16384 of database 13445 after 1000.072 ms
STATEMENT:  SELECT * FROM users WHERE id = 42

LOG:  process 12345 acquired AccessShareLock on relation 16384 of database 13445 after 2468.117 ms
```

quellog analyzes lock events to show:

- Total lock wait time
- Most frequent blocking queries
- Lock types and resources

## Autovacuum Logging

```ini
# Log all autovacuum operations
log_autovacuum_min_duration = 0

# Log only autovacuum operations taking > 1 second
log_autovacuum_min_duration = 1000

# Don't log autovacuum (default: -1)
log_autovacuum_min_duration = -1
```

Example log output:

```
LOG:  automatic vacuum of table "mydb.public.users": index scans: 0
    pages: 123 removed, 4567 remain, 0 skipped due to pins, 0 skipped frozen
    tuples: 12345 removed, 456789 remain, 0 are dead but not yet removable
    buffer usage: 890 hits, 12 misses, 3 dirtied
    avg read rate: 0.123 MB/s, avg write rate: 0.045 MB/s
    system usage: CPU: user: 0.12 s, system: 0.03 s, elapsed: 2.45 s
```

quellog tracks:

- Autovacuum/autoanalyze frequency by table
- Space recovered by vacuum
- Tables requiring frequent vacuuming

## Checkpoint Logging

```ini
# Log checkpoints
log_checkpoints = on
```

Example log output:

```
LOG:  checkpoint starting: time
LOG:  checkpoint complete: wrote 12345 buffers (75.5%); 0 WAL file(s) added, 0 removed, 1 recycled; write=0.123 s, sync=0.045 s, total=0.168 s; sync files=10, longest=0.012 s, average=0.005 s
```

quellog analyzes:

- Checkpoint frequency (time-based vs. WAL-based)
- Write times
- Checkpoint distribution over time

## Error and Warning Logging

```ini
# Minimum severity to log (default: WARNING)
log_min_messages = warning

# Options: debug5, debug4, debug3, debug2, debug1, info, notice, warning, error, log, fatal, panic
```

For production, `warning` or `error` is typically appropriate. For development or troubleshooting, `info` or `log` provides more detail.

## Applying Configuration Changes

### Method 1: Reload (No Restart Required)

Most logging settings can be reloaded without restarting PostgreSQL:

```sql
-- Reload configuration
SELECT pg_reload_conf();

-- Verify changes
SHOW log_min_duration_statement;
SHOW log_connections;
```

### Method 2: Restart (Required for Some Settings)

Some settings require a full restart:

```bash
# Linux (systemd)
sudo systemctl restart postgresql

# Linux (sysvinit)
sudo service postgresql restart

# macOS (Homebrew)
brew services restart postgresql

# Windows
net stop postgresql-x64-15
net start postgresql-x64-15
```

Settings requiring restart include:

- `logging_collector`
- `log_destination` (in some cases)

## Verifying Configuration

Check your current logging configuration:

```sql
-- Show log destination
SHOW log_destination;

-- Show log directory
SHOW log_directory;

-- Show query duration threshold
SHOW log_min_duration_statement;

-- Show log line prefix
SHOW log_line_prefix;

-- Show all log settings
SELECT name, setting, unit, context
FROM pg_settings
WHERE name LIKE 'log%'
ORDER BY name;
```

## Next Steps

- [Understand log formats](formats.md) that quellog supports
- [Learn filtering options](filtering-logs.md) to analyze specific log subsets
- [Run your first analysis](quick-start.md) on your configured logs
