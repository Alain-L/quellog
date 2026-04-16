# PostgreSQL Setup

## Recommended Configuration

```ini
# postgresql.conf

# Logging destination
log_destination = 'stderr'              # or 'csvlog', 'jsonlog' (PG 15+)
logging_collector = on

# Log line format (stderr only)
log_line_prefix = '%t [%p] %e: db=%d,user=%u,app=%a,client=%h '

# Query logging
log_min_duration_statement = 100        # Log queries > 100ms
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

# Optional: execution plans for slow queries
# shared_preload_libraries = 'auto_explain'
# auto_explain.log_min_duration = 1000    # Log plans for queries > 1s
# auto_explain.log_format = 'json'        # JSON for structured parsing
# auto_explain.log_analyze = on           # Include actual row counts
```

Apply with `SELECT pg_reload_conf();`

!!! tip "Automatic log_line_prefix detection"
    The format above is recommended, but **quellog adapts to most `log_line_prefix` configurations**. CSV and JSON log formats include all metadata by default.

!!! warning "Performance impact"
    `log_min_duration_statement = 0` logs every query, which generates large log files on busy databases. Start with `100` (ms) and lower if needed.


## What Each Setting Enables

| Setting | quellog section | Notes |
|---------|----------------|-------|
| `log_min_duration_statement` | SQL Performance, SQL Overview | `0` = all queries, `100` = queries > 100ms |
| `log_connections` | Connections | Connection counts and rates |
| `log_disconnections` | Connections | Session durations, concurrent sessions chart |
| `log_checkpoints` | Checkpoints | Checkpoint frequency, WAL distance, I/O stats |
| `log_autovacuum_min_duration` | Maintenance | Vacuum/analyze frequency, table stats |
| `log_temp_files` | Temp Files | Temp file count and sizes per query |
| `log_lock_waits` | Locks | Lock contention, deadlocks, blocking queries |
| `log_line_prefix` with `%e` | Events | SQLSTATE error class reporting |
| `log_line_prefix` with `%d,%u,%a,%h` | Clients, Filtering | Per-database/user/app/host breakdown |
| `auto_explain` extension | SQL Analysis | Execution plans attached to slow queries |
