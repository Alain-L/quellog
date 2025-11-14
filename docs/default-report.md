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

## SQL Performance

Shows query execution statistics and load distribution.

!!! note
    Query details (list of slowest queries, most time-consuming queries) are only shown when using `--sql-summary`. The default report shows only summary metrics.

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
```

**Severity levels**:

- **LOG**: Informational messages
- **WARNING**: Potential issues (e.g., deprecated features)
- **ERROR**: Query failures, constraint violations
- **FATAL**: Session termination errors
- **PANIC**: Server crash-level errors

## Temporary Files

Tracks queries that exceeded `work_mem` and spilled to disk.

!!! note
    Query details ("Top queries by tempfile size") are shown when using `--tempfiles` or `--sql-summary`. The default report shows only summary metrics.

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

## Locks

Shows lock contention, wait times, and queries involved in lock waits.

!!! note
    Query details (the three query tables) are shown when using `--locks` or `--sql-summary`. The default report shows only summary metrics.

```
LOCKS

  Total lock events         : 194
  Waiting events            : 171
  Acquired events           : 23
  Avg wait time             : 54.92 s
  Total wait time           : 2h 57m 34s
  Lock types:
    AccessShareLock              194  100.0%
  Resource types:
    relation                     194  100.0%

Acquired locks by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
se-uD4Xj2  select count(*) as gt_result_ from (select * fro...           3          15m 08s          45m 26s
se-jStrWD  select count(*) as gt_result_ from (select * fro...           3          12m 48s          38m 24s
se-kvbfUA  select count(*) as gt_result_ from (select * fro...           3          10m 50s          32m 30s

Locks still waiting by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
xx-AmtyJ1  -- probe btree_bloat select current_database() a...          89           8.14 s          12m 04s
xx-Asa3KN  -- probe heap_bloat select current_database() as...          24           1.00 s          24.00 s

Most frequent waiting queries:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
xx-AmtyJ1  -- probe btree_bloat select current_database() a...          94           8.80 s          13m 47s
se-uD4Xj2  select count(*) as gt_result_ from (select * fro...           3          15m 08s          45m 26s
se-jStrWD  select count(*) as gt_result_ from (select * fro...           3          12m 48s          38m 24s
```

**Metrics explained**:

- **Total lock events**: Number of lock wait log messages
- **Waiting events**: "still waiting" messages (lock not yet acquired)
- **Acquired events**: "acquired" messages (lock eventually granted)
- **Avg wait time**: Average duration of lock waits
- **Total wait time**: Sum of all lock wait durations

**Query tables**:

- **Acquired locks by query**: Queries that eventually acquired locks (sorted by total wait time)
- **Locks still waiting by query**: Queries still waiting when logs ended
- **Most frequent waiting queries**: All queries that waited for locks (sorted by lock count)

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

## Next Steps

- [SQL Analysis Deep Dive](sql-reports.md) - Detailed per-query analysis
- [Filtering Output](filtering-output.md) - Show only specific sections
- [Export Formats](json-export.md) - JSON and Markdown output
