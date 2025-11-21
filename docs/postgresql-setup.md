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
log_line_prefix = '%t [%p] %e: db=%d,user=%u,app=%a,client=%h '

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

The configuration above enables comprehensive logging for PostgreSQL analysis with quellog. It includes query duration tracking, connection events, checkpoint activity, autovacuum operations, temporary file usage, and lock waits. The `log_line_prefix` format captures essential metadata (timestamp, PID, SQLSTATE, database, user, application, client) needed for filtering and attribution.

quellog supports all PostgreSQL log formats: stderr/syslog (plain text), csvlog (structured CSV), and jsonlog (PostgreSQL 15+). The format is auto-detected.

!!! info "Error Class Reporting (SQLSTATE codes)"
    The `%e` in `log_line_prefix` includes SQLSTATE error codes for classification. Alternatively, use `log_error_verbosity = 'verbose'` for detailed error context, or csvlog/jsonlog which include SQLSTATE codes by default in dedicated fields.

!!! warning "Performance Impact"
    Setting `log_min_duration_statement = 0` logs every query, which can generate massive log files on busy databases and increase I/O load.

## Next Steps

- [Understand log formats](formats.md) that quellog supports
- [Learn filtering options](filtering-logs.md) to analyze specific log subsets
- [Run your first analysis](index.md#quick-start) on your configured logs
