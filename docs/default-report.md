# Default Report

This page explains each section of quellog's default report, what metrics are displayed, and how to interpret them.

## Report Structure

When you run quellog without any section flags, you get a comprehensive report with these sections:

1. [Summary](#summary) - Overview statistics
2. [SQL Performance](#sql-performance) - Query timing and distribution
3. [Events](#events) - Log severity and error classification
4. [Temporary Files](#temporary-files) - Disk spills from memory exhaustion
5. [Locks](#locks) - Lock contention and wait times
6. [Maintenance](#maintenance) - Vacuum and analyze operations
7. [Checkpoints](#checkpoints) - Checkpoint frequency and timing
8. [Connections & Sessions](#connections-sessions) - Connection patterns
9. [Clients](#clients) - Unique database entities

## Summary

The first section provides high-level statistics about the analyzed logs.

```
quellog – 1,234 entries processed in 0.15 s (45.2 MB)

SUMMARY

  Start date                : 2025-01-13 00:00:00 UTC
  End date                  : 2025-01-13 23:59:59 UTC
  Duration                  : 23h59m59s
  Total entries             : 1,234
  Throughput                : 8,227 entries/s
```

**Metrics explained**:

- **Start date**: Timestamp of the earliest log entry
- **End date**: Timestamp of the latest log entry
- **Duration**: Time span covered by the logs
- **Total entries**: Number of log lines processed
- **Throughput**: Processing speed (entries per second)

**Interpreting**:

- **Large duration, few entries**: Quiet period or filtering removed most entries
- **High throughput**: quellog processed logs quickly
- **Gap in dates**: Missing log files or log rotation

## SQL Performance

Shows query execution statistics and load distribution.

```
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
  Top 1% slow queries       : 5

  Query max duration        : 2.34 s
  Query min duration        : 12 ms
  Query median duration     : 145 ms
  Query 99% max duration    : 1.87 s
```

**Histogram**:

- Shows cumulative query execution time per time bucket
- Each `■` represents a fixed duration (shown in legend)
- Helps identify peak load periods

**Metrics explained**:

- **Total query duration**: Sum of all query execution times
- **Total queries parsed**: Number of queries logged
- **Total unique query**: Number of distinct normalized queries
- **Top 1% slow queries**: Count of queries in the slowest 1%
- **Query max duration**: Longest single query
- **Query min duration**: Fastest query
- **Query median duration**: 50th percentile
- **Query 99% max duration**: 99th percentile (typical "slow" threshold)

**Interpreting**:

- **High max, low median**: Occasional slow queries, most are fast
- **High total duration**: Database is CPU-bound or has slow queries
- **Few unique queries, many total**: Repetitive workload (good for optimization)
- **Many unique queries**: Ad-hoc workload (consider prepared statements)

**Limitations**:

- Only shows queries logged via `log_min_duration_statement`
- Queries without duration information are excluded from this section

## Events

Displays log entry distribution by severity level.

```
EVENTS

  LOG     : 1,180
  WARNING : 3
  ERROR   : 1

ERROR CLASSES (SQLSTATE)

  53100 - disk_full               : 1
```

**Severity levels**:

- **LOG**: Informational messages
- **WARNING**: Potential issues (e.g., deprecated features)
- **ERROR**: Query failures, constraint violations
- **FATAL**: Session termination errors
- **PANIC**: Server crash-level errors

**SQLSTATE classification**:

Shows error codes (SQLSTATE) and their meanings:

- **53100**: Disk full
- **42P01**: Undefined table
- **23505**: Unique violation
- **57014**: Query canceled

See [PostgreSQL Error Codes](https://www.postgresql.org/docs/current/errcodes-appendix.html) for complete list.

**Interpreting**:

- **Many WARNINGs**: Check PostgreSQL configuration or deprecated feature usage
- **ERRORs**: Application errors, missing tables, constraint violations
- **FATALs**: Connection issues, authentication failures
- **PANICs**: Critical - database crashes require investigation

## Temporary Files

Tracks queries that exceeded `work_mem` and spilled to disk.

```
TEMP FILES

  Temp file distribution | ■ = 10 MB

  00:15 - 01:11  ■■■■■■■■■■■■■■■■■■■■■■■■ 249 MB
  01:11 - 02:08  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 389 MB
  02:08 - 03:05  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 300 MB
  03:05 - 04:01  ■■■■■■■■■■■■■■■■■■■■■ 214 MB
  04:01 - 04:58  ■■■■■■■■■■■■■■■■■ 170 MB
  04:58 - 05:55  ■■■■■■■■■■■■■■■■■■■ 194 MB

  Temp file messages        : 19
  Cumulative temp file size : 1.48 GB
  Average temp file size    : 79.79 MB

  Top queries by tempfile size:

    1. se-a1b2c3d (789 MB, 8 times)
       SELECT * FROM large_table ORDER BY created_at

    2. se-x4y5z6w (456 MB, 3 times)
       SELECT * FROM users JOIN orders ON ...

    3. se-m7n8o9p (123 MB, 2 times)
       SELECT COUNT(*) FROM events GROUP BY ...
```

**Metrics explained**:

- **Temp file messages**: Number of tempfile creation events
- **Cumulative temp file size**: Total disk space used for tempfiles
- **Average temp file size**: Mean tempfile size
- **Top queries**: Queries sorted by total tempfile size (sum across all executions)

**Interpreting**:

- **Large cumulative size**: Increase `work_mem` or optimize queries
- **Many tempfiles**: Workload has many sorts/hashes exceeding `work_mem`
- **Specific queries dominate**: Optimize those queries (indexes, rewrite)
- **Tempfiles during peak hours**: Tune `work_mem` or add memory

**Tuning recommendations**:

- **Small tempfiles (< 10 MB)**: Increase `work_mem` by 2x
- **Large tempfiles (> 100 MB)**: Query optimization (indexes, JOINs)
- **Frequent tempfiles**: Consider connection pooling to limit concurrent memory usage

## Locks

Shows lock contention, wait times, and blocking queries.

```
LOCKS

  Total lock events         : 23
  Lock waiting events       : 15
  Lock acquired events      : 8
  Deadlock events           : 0

  Total lock wait time      : 34.5 s

  Lock types:

    AccessShareLock         : 12  (52.2%)
    RowExclusiveLock        : 8   (34.8%)
    ExclusiveLock           : 3   (13.0%)

  Resource types:

    relation                : 18  (78.3%)
    transaction             : 5   (21.7%)

  Top queries by total wait time:

    1. se-a1b2c3d (12.3 s total, 4 locks acquired)
       SELECT * FROM users WHERE ...

    2. se-x4y5z6w (8.9 s total, 2 locks acquired)
       UPDATE orders SET status = ...
```

**Metrics explained**:

- **Total lock events**: Total waiting + acquired + deadlock events
- **Lock waiting events**: "still waiting" messages
- **Lock acquired events**: "acquired" messages
- **Deadlock events**: Deadlock detection events
- **Total lock wait time**: Sum of time spent waiting for locks

**Lock types** (most common):

- **AccessShareLock**: Read locks (SELECT queries)
- **RowShareLock**: SELECT FOR UPDATE
- **RowExclusiveLock**: INSERT, UPDATE, DELETE
- **ShareUpdateExclusiveLock**: VACUUM, ANALYZE, CREATE INDEX CONCURRENTLY
- **ExclusiveLock**: DDL operations
- **AccessExclusiveLock**: DROP TABLE, TRUNCATE

**Resource types**:

- **relation**: Tables, indexes
- **transaction**: Transaction IDs
- **tuple**: Individual table rows
- **advisory lock**: Application-defined locks

**Interpreting**:

- **High wait time**: Lock contention is slowing queries
- **AccessShareLock waits**: Likely blocked by DDL or VACUUM
- **RowExclusiveLock waits**: Concurrent UPDATEs/DELETEs on same rows
- **Deadlocks**: Application logic issue or lock ordering problem

**Tuning recommendations**:

- **Frequent lock waits**: Review transaction isolation levels, reduce transaction duration
- **Deadlocks**: Enforce consistent lock ordering in application
- **AccessShareLock blocked by VACUUM**: Use `VACUUM` with lower `maintenance_work_mem` or schedule during off-peak

## Maintenance

Tracks autovacuum and autoanalyze operations.

```
MAINTENANCE

  Automatic vacuum count    : 12
  Automatic analyze count   : 8

  Top automatic vacuum operations per table:

    app_db.public.orders        4  33.3%       2.34 MB removed
    app_db.public.sessions      3  25.0%       1.12 MB removed
    app_db.public.users         2  16.7%       0.56 MB removed

  Top automatic analyze operations per table:

    app_db.public.orders        3  37.5%
    app_db.public.products      2  25.0%
    app_db.public.sessions      2  25.0%
```

**Metrics explained**:

- **Automatic vacuum count**: Number of autovacuum operations
- **Automatic analyze count**: Number of autoanalyze operations
- **Space removed**: Disk space recovered by VACUUM (dead tuples)

**Interpreting**:

- **Frequent vacuums on one table**: High UPDATE/DELETE rate
- **Large space removed**: Many dead tuples (consider manual VACUUM or tune autovacuum)
- **No vacuums**: Either no write activity or `log_autovacuum_min_duration` not set

**Tuning recommendations**:

- **Frequent autovacuum on large table**: Lower `autovacuum_vacuum_scale_factor` for that table
- **Autovacuum too slow**: Increase `autovacuum_max_workers` or `autovacuum_work_mem`

## Checkpoints

Displays checkpoint frequency and performance.

```
CHECKPOINTS

  Checkpoints | ■ = 1

  00:00 - 04:00  ■■■■■■■ 7
  04:00 - 08:00  ■■■■■ 5
  08:00 - 12:00  ■ 1
  12:00 - 16:00  ■■ 2
  16:00 - 20:00  ■■■ 3
  20:00 - 00:00  ■ 1

  Checkpoint count          : 19
  Avg checkpoint write time : 3.2 s
  Max checkpoint write time : 7.8 s

  Checkpoint types:

    time          14   73.7%  (0.58/h)
    wal           5    26.3%  (0.21/h)
```

**Checkpoint types**:

- **time**: Triggered by `checkpoint_timeout` (default: 5 minutes)
- **wal**: Triggered by `max_wal_size` (too much WAL generated)
- **shutdown**: Database shutdown
- **immediate**: Manual CHECKPOINT command

**Metrics explained**:

- **Checkpoint count**: Total checkpoints
- **Avg/Max write time**: Time to flush dirty buffers to disk
- **Frequency (per hour)**: Checkpoint rate

**Interpreting**:

- **Many time-based checkpoints**: Normal (every `checkpoint_timeout`)
- **Many WAL-based checkpoints**: High write load, consider increasing `max_wal_size`
- **Long write times (> 10s)**: I/O bottleneck, consider tuning `checkpoint_completion_target`

**Tuning recommendations**:

- **Frequent WAL checkpoints**: Increase `max_wal_size`
- **Long write times**: Increase `checkpoint_completion_target` (default 0.9)

## Connections & Sessions

Shows connection patterns and session durations.

```
CONNECTIONS & SESSIONS

  Connection distribution | ■ = 1

  00:00 - 00:58  ■■■■■■■■■■■■■■■ 15
  00:58 - 01:56  ■■■■■ 5
  01:56 - 02:55  ■■■■ 4
  02:55 - 03:53  ■■■■ 4
  03:53 - 04:51  ■■■■ 4
  04:51 - 05:50  ■■■■ 4

  Connection count          : 36
  Avg connections per hour  : 1.50
  Disconnection count       : 23
  Avg session time          : 1h14m7s
```

**Metrics explained**:

- **Connection count**: Total connections received
- **Avg connections per hour**: Connection rate
- **Disconnection count**: Sessions that ended
- **Avg session time**: Mean session duration

**Interpreting**:

- **High connection rate**: Consider connection pooling
- **Long avg session time**: Persistent connections (good for pooling)
- **Short avg session time**: Frequent reconnects (use pooler like pgBouncer)

## Clients

Lists unique database entities found in logs.

```
CLIENTS

  Unique DBs                : 3
  Unique Users              : 6
  Unique Apps               : 9

USERS

    admin
    analytics
    app_user
    backup_user
    batch_user
    readonly

APPS

    app_server
    batch_job
    metabase
    pg_dump
    pgadmin
    psql

DATABASES

    analytics_db
    app_db
    postgres
```

**Interpreting**:

- **Unexpected users/apps**: Security audit
- **Many unique databases**: Multi-tenant setup
- **Few unique entities**: Focused workload

## Next Steps

- [SQL Analysis Deep Dive](sql-reports.md) - Detailed per-query analysis
- [Filtering Output](filtering-output.md) - Show only specific sections
- [Export Formats](json-export.md) - JSON and Markdown output
