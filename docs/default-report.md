# Default Report

This page explains each section of quellog's default report, what metrics are displayed, and how to interpret them.

## Report Structure

When you run quellog without any section flags, you get a comprehensive report with these sections:

1. [Summary](#summary) - Overview statistics
2. [SQL Performance](#sql-performance) - Query timing and distribution
3. [Events](#events) - Log severity distribution
4. [Error Classes](#error-classes) - SQLSTATE error classification
5. [Temporary Files](#temporary-files) - Disk spills from memory exhaustion
6. [Locks](#locks) - Lock contention and wait times
7. [Maintenance](#maintenance) - Vacuum and analyze operations
8. [Checkpoints](#checkpoints) - Checkpoint frequency and timing
9. [Connections & Sessions](#connections-sessions) - Connection patterns
10. [Clients](#clients) - Unique database entities

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

## Error Classes

Shows PostgreSQL error distribution by SQLSTATE error class code.

```
ERROR CLASSES

  42 – Syntax Error or Access Rule Violation   : 125
  23 – Integrity Constraint Violation          : 18
  22 – Data Exception                          : 5
  53 – Insufficient Resources                  : 2
```

**Common error classes**:

- **42**: Syntax errors, undefined objects, permission issues
- **23**: Foreign key violations, unique constraint violations
- **22**: Invalid input, division by zero, invalid text representation
- **53**: Out of memory, disk full, too many connections
- **08**: Connection exceptions
- **40**: Transaction rollback (deadlock, serialization failure)

!!! info "Configuration Required"
    Error classes require SQLSTATE codes in logs. Configure with `%e` in `log_line_prefix`, `log_error_verbosity = 'verbose'`, or use csvlog/jsonlog formats.

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

  Temp file messages        : 11639
  Cumulative temp file size : 48.34 GB
  Average temp file size    : 4.25 MB

Queries generating temp files:
SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
xx-Asa3KN  -- probe heap_bloat select current_database() as dbname, sum(bloat_s...       10188      16.66 GB
xx-TtcBPJ  with namespace_rels as ( select nsp.oid, nsp.nspname, array_remove(a...          28       7.37 GB
se-SunZ0F  select sit_gestion.refresh_referentiel_topo();                                 1276       6.52 GB
xx-T3SufA  close c17                                                                         4       2.56 GB
se-z3k2JB  select ?, array_agg(distinct st_srid("geom")::text || ? || upper(geo...           6       2.49 GB
```

**Metrics explained**:

- **Temp file messages**: Number of tempfile creation events
- **Cumulative temp file size**: Total disk space used for tempfiles
- **Average temp file size**: Mean tempfile size
- **Queries generating temp files**: Table showing queries sorted by total tempfile size, with count and total size per query

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

  Automatic vacuum count    : 668
  Automatic analyze count   : 353
  Top automatic vacuum operations per table:
    app_db.public.sessions                 422  63.17%
    app_db.public.audit_log                216  32.34%       8.00 KB removed
  Top automatic analyze operations per table:
    app_db.public.sessions                         300  84.99%
```

**Metrics explained**:

- **Automatic vacuum count**: Number of autovacuum operations
- **Automatic analyze count**: Number of autoanalyze operations
- **Top operations per table**: Tables sorted by operation count with percentage
- **Space removed**: Disk space recovered by VACUUM (shown when available)

## Checkpoints

Displays checkpoint frequency and performance.

```
CHECKPOINTS

  Checkpoints | ■ = 4

  00:00 - 04:00  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 159
  04:00 - 08:00  ■■■■■■■■■■■■■■■■■ 69
  08:00 - 12:00  ■■■■■■■■■■■■ 48
  12:00 - 16:00  ■ 6
  16:00 - 20:00   -
  20:00 - 00:00   -

  Checkpoint count          : 282
  Avg checkpoint write time : 29s
  Max checkpoint write time : 2m31s
  Checkpoint types:
    wal                   171   60.6%  (18.03/h)
    time                  110   39.0%  (11.60/h)
    immediate force wait    1    0.4%  (0.11/h)
```

**Checkpoint types**:

- **time**: Triggered by `checkpoint_timeout`
- **wal**: Triggered by `max_wal_size`
- **shutdown**: Database shutdown
- **immediate**: Manual CHECKPOINT command
- Types can be combined (e.g., "immediate force wait")

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
  Unique Hosts              : 4

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

HOSTS

    10.0.1.100
    10.0.1.101
    127.0.0.1
    192.168.1.50
```

## Next Steps

- [SQL Analysis Deep Dive](sql-reports.md) - Detailed per-query analysis
- [Filtering Output](filtering-output.md) - Show only specific sections
- [Export Formats](json-export.md) - JSON and Markdown output
