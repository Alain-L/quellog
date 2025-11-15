# PostgreSQL Configuration

This guide covers the PostgreSQL logging settings that affect quellog's analysis capabilities.

## Configuration Example

Here is a complete configuration example with all logging parameters relevant for quellog:

```ini
# postgresql.conf

# Logging destination
log_destination = 'stderr'              # or 'csvlog', 'jsonlog' (PostgreSQL 15+)
logging_collector = on

# Log line format (for stderr logs)
log_line_prefix = '%t [%p]: db=%d,user=%u,app=%a,client=%h '

# Query logging
log_min_duration_statement = 100        # Log queries > 100ms (milliseconds)
log_statement = 'ddl'                   # Log DDL statements

# Connection logging
log_connections = on
log_disconnections = on

# Operational events
log_checkpoints = on
log_autovacuum_min_duration = 0         # Log all autovacuum operations
log_temp_files = 0                      # Log all temporary files
log_lock_waits = on
deadlock_timeout = 1000                 # 1 second

# Error level
log_min_messages = warning
```

Apply configuration:

```sql
SELECT pg_reload_conf();
```

## About These Settings

The configuration above enables comprehensive logging for PostgreSQL analysis with quellog. It includes query duration tracking, connection events, checkpoint activity, autovacuum operations, temporary file usage, and lock waits. The `log_line_prefix` format captures essential metadata (timestamp, PID, database, user, application, client) needed for filtering and attribution.

quellog supports all PostgreSQL log formats: stderr/syslog (plain text), csvlog (structured CSV), and jsonlog (PostgreSQL 15+). The format is auto-detected.

!!! warning "Performance Impact"
    Setting `log_min_duration_statement = 0` logs every query, which can generate massive log files on busy databases and increase I/O load.

## Error Class Reporting (SQLSTATE Codes)

quellog can analyze errors by their SQLSTATE class (e.g., "23" for integrity constraint violations, "42" for syntax errors). This requires SQLSTATE codes in log messages.

Choose one of these methods to enable error class reporting:

### Method 1: log_error_verbosity = verbose

Add SQLSTATE codes directly in error messages:

```ini
# postgresql.conf
log_error_verbosity = 'verbose'
```

Apply configuration:

```sql
ALTER SYSTEM SET log_error_verbosity = 'verbose';
SELECT pg_reload_conf();
```

Example output:
```
ERROR: 42P01: relation "users" does not exist at character 15
LOCATION: parserOpenTable, parse_relation.c:1469
```

**Pros:** Works with stderr logs, provides detailed error context
**Cons:** More verbose logs (includes LOCATION, CONTEXT fields)

### Method 2: %e in log_line_prefix

Include SQLSTATE codes in the log line prefix:

```ini
# postgresql.conf
log_line_prefix = '%m [%p] %e '
```

Apply configuration:

```sql
ALTER SYSTEM SET log_line_prefix = '%m [%p] %e ';
SELECT pg_reload_conf();
```

Example output:
```
2025-01-01 12:00:00 CET [12345] 42P01 ERROR: relation "users" does not exist at character 15
```

**Pros:** Minimal verbosity, SQLSTATE always visible
**Cons:** Changes log_line_prefix format (may affect other tools)

### Method 3: Use CSV or JSON logs

CSV and JSON log formats include SQLSTATE codes by default:

```ini
# postgresql.conf
log_destination = 'csvlog'  # or 'jsonlog' for PostgreSQL 15+
logging_collector = on
```

Apply configuration:

```sql
-- For CSV logs
ALTER SYSTEM SET log_destination = 'csvlog';
SELECT pg_reload_conf();

-- For JSON logs (PostgreSQL 15+)
ALTER SYSTEM SET log_destination = 'jsonlog';
SELECT pg_reload_conf();
```

**Pros:** Structured format, SQLSTATE in dedicated field, best for automation
**Cons:** Requires log file management, less human-readable

### Verification

Test error class reporting:

```sql
-- Generate a test error
SELECT * FROM nonexistent_table;
```

Then run quellog:

```bash
quellog /var/log/postgresql/*.log
```

You should see an **ERROR CLASSES** section in the output:

```
ERROR CLASSES

  42 – Syntax Error or Access Rule Violation   : 1
```

If error classes don't appear, verify your PostgreSQL configuration includes SQLSTATE codes in logs.

## Next Steps

- [Understand log formats](formats.md) that quellog supports
- [Learn filtering options](filtering-logs.md) to analyze specific log subsets
- [Run your first analysis](index.md#quick-start) on your configured logs
- [Export as JSON](json-export.md) for automation and integration
