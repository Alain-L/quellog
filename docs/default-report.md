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

**Basic Output (Default Report)**

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
  Avg concurrent sessions   : 13.45
  Peak concurrent sessions  : 36 (at 05:50:00)
```

**Basic metrics explained**:

- **Connection count**: Total connections received
- **Avg connections per hour**: Connection rate
- **Disconnection count**: Sessions that ended (requires `log_disconnections = on`)
- **Avg session time**: Mean session duration
- **Avg concurrent sessions**: Average number of simultaneous sessions
- **Peak concurrent sessions**: Maximum simultaneous sessions with timestamp

**Detailed Output (With `--connections` Flag)**

When using the explicit `--connections` flag, additional session analytics are displayed:

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
  Avg concurrent sessions   : 13.45
  Peak concurrent sessions  : 36 (at 05:50:00)

  Session duration distribution | ■ = 1 session

  < 1s           -
  1s - 1min      -
  1min - 30min   ■ 1
  30min - 2h     ■■■■■■■■■■■■■■■■■■■ 19
  2h - 5h        ■■■ 3
  > 5h           -

SESSION DURATION BY USER

  User                       Sessions      Min      Max      Avg   Median  Cumulated
  ---------------------------------------------------------------------------------------
  app_user                         10   31m6s  2h20m16s  1h26m59s  1h26m48s   14h29m46s
  readonly                          5   7m10s   1h3m26s   41m38s    47m30s    3h28m11s
  batch_user                        3  1h21m46s  2h0m30s  1h42m45s   1h46m0s    5h8m16s
  admin                             3  42m46s   47m31s   45m16s    45m30s    2h15m47s
  analytics                         1  1h17m45s  1h17m45s  1h17m45s  1h17m45s    1h17m45s
  backup_user                       1  1h44m45s  1h44m45s  1h44m45s  1h44m45s    1h44m45s

SESSION DURATION BY DATABASE

  Database                   Sessions      Min      Max      Avg   Median  Cumulated
  ---------------------------------------------------------------------------------------
  app_db                           16   7m10s  2h20m16s  1h19m42s  1h22m18s   21h15m18s
  postgres                          4  42m46s  1h44m45s   1h0m8s    46m31s    4h0m32s
  analytics_db                      3  47m30s  1h17m45s  1h2m54s   1h3m26s    3h8m41s

SESSION DURATION BY HOST

  Host                       Sessions      Min      Max      Avg   Median  Cumulated
  ---------------------------------------------------------------------------------------
  192.168.1.100                     3   31m6s  1h13m10s   50m40s    45m30s    2h32m1s
  10.0.1.50                         2  1h17m45s  1h52m46s  1h35m16s  1h35m16s   3h10m31s
  172.16.0.12                       2  1h44m45s  2h20m16s  2h2m31s   2h2m31s    4h5m1s
  172.16.0.30                       1  37m45s   37m45s   37m45s    37m45s    37m45s
  ...
```

**Detailed metrics explained**:

- **Session duration distribution**: Histogram showing distribution of session lengths across time buckets
- **Session tables**: Breakdown by user, database, and host showing:
  - **Sessions**: Number of sessions for this entity
  - **Min/Max**: Shortest and longest sessions
  - **Avg**: Average session duration
  - **Median**: 50th percentile (more resistant to outliers than average)
  - **Cumulated**: Total time spent in sessions for this entity

**Configuration requirements**:

- `log_connections = on` - Track connection events
- `log_disconnections = on` - Calculate session durations
- `log_line_prefix` should include `%u` (user), `%d` (database), and `%h` (host) for entity breakdowns

## Clients

Lists unique database entities found in logs with activity counts and percentages.

**Default Output (TOP 10)**

The default report shows the **top 10** most active entities per category. Use `--clients` flag to display **all** entities without limit.

```
CLIENTS

  Unique DBs                : 3
  Unique Users              : 7
  Unique Apps               : 9
  Unique Hosts              : 37

TOP USERS

  app_user                   1250   42.5%
  readonly                    856   29.1%
  batch_user                  423   14.4%
  admin                       198    6.7%
  analytics                   145    4.9%
  backup_user                  52    1.8%
  postgres                     16    0.5%

TOP APPS

  app_server                 1342   45.6%
  psql                        687   23.4%
  metabase                    456   15.5%
  pgadmin                     234    8.0%
  batch_job                   145    4.9%
  pg_dump                      52    1.8%
  pg_restore                   12    0.4%
  [3 more...]

TOP DATABASES

  app_db                     2456   83.5%
  postgres                    342   11.6%
  analytics_db                142    4.8%

TOP HOSTS

  192.168.1.100               876   29.8%
  10.0.1.50                   654   22.2%
  172.16.0.10                 543   18.5%
  10.0.1.51                   432   14.7%
  172.16.0.12                 234    8.0%
  192.168.1.101               123    4.2%
  10.0.1.52                    56    1.9%
  172.16.0.15                  22    0.7%
  [29 more...]

TOP USER × DATABASE

  app_user                  × app_db                      1856   63.1%
  readonly                  × app_db                       543   18.5%
  app_user                  × analytics_db                 123    4.2%
  batch_user                 × app_db                        98    3.3%
  readonly                  × analytics_db                  87    3.0%
  admin                     × postgres                      65    2.2%
  batch_user                × analytics_db                  45    1.5%
  analytics                 × analytics_db                  34    1.2%
  admin                     × app_db                        23    0.8%
  backup_user               × postgres                      12    0.4%
  [8 more...]

TOP USER × HOST

  app_user                  × 192.168.1.100                 654   22.2%
  readonly                  × 10.0.1.50                     432   14.7%
  app_user                  × 172.16.0.10                   345   11.7%
  batch_user                × 10.0.1.51                     234    8.0%
  app_user                  × 10.0.1.50                     187    6.4%
  readonly                  × 192.168.1.100                 156    5.3%
  admin                     × 172.16.0.12                   123    4.2%
  analytics                 × 10.0.1.52                      98    3.3%
  batch_user                × 192.168.1.101                  76    2.6%
  readonly                  × 172.16.0.10                    65    2.2%
  [42 more...]
```

**Metrics explained**:

- **Unique counts**: Total number of distinct entities
- **Entity names**: Sorted by activity (most active first)
- **Count**: Number of log entries from this entity
- **Percentage**: Proportion of total log entries
- **[X more...]**: Indicator when more than 10 entities exist (use `--clients` to see all)
- **USER × DATABASE**: Shows which users access which databases and how frequently
- **USER × HOST**: Shows which users connect from which hosts and how frequently

**Cross-tabulations** help identify:
- Access patterns (which user accesses which database)
- Connection sources (which user connects from which host)
- Security anomalies (unexpected user/database or user/host combinations)
- Load distribution across client connections

**With `--clients` Flag (ALL Entities)**

When using `--clients` explicitly, **all** entities are displayed without the 10-item limit, and headers show "USERS", "APPS", etc. (without "TOP" prefix):

```
CLIENTS

  Unique DBs                : 3
  Unique Users              : 15
  Unique Apps               : 12
  Unique Hosts              : 37

USERS

  app_user                   1250   42.5%
  readonly                    856   29.1%
  batch_user                  423   14.4%
  admin                       198    6.7%
  analytics                   145    4.9%
  backup_user                  52    1.8%
  postgres                     16    0.5%
  monitoring                   12    0.4%
  replication                   8    0.3%
  test_user                     5    0.2%
  dev_user_1                    3    0.1%
  dev_user_2                    2    0.1%
  dev_user_3                    1    0.0%
  dev_user_4                    1    0.0%
  qa_user                       1    0.0%

APPS

  app_server                 1342   45.6%
  psql                        687   23.4%
  metabase                    456   15.5%
  pgadmin                     234    8.0%
  batch_job                   145    4.9%
  pg_dump                      52    1.8%
  pg_restore                   12    0.4%
  python_script                 8    0.3%
  monitoring_tool               5    0.2%
  tableau                       3    0.1%
  dbeaver                       2    0.1%
  datagrip                      1    0.0%

DATABASES

  app_db                     2456   83.5%
  postgres                    342   11.6%
  analytics_db                142    4.8%

HOSTS

  192.168.1.100               876   29.8%
  10.0.1.50                   654   22.2%
  172.16.0.10                 543   18.5%
  10.0.1.51                   432   14.7%
  172.16.0.12                 234    8.0%
  192.168.1.101               123    4.2%
  10.0.1.52                    56    1.9%
  172.16.0.15                  22    0.7%
  10.0.1.53                    18    0.6%
  172.16.0.20                  12    0.4%
  ... (all 37 hosts listed)

USER × DATABASE

  app_user                  × app_db                      1856   63.1%
  readonly                  × app_db                       543   18.5%
  app_user                  × analytics_db                 123    4.2%
  batch_user                × app_db                        98    3.3%
  readonly                  × analytics_db                  87    3.0%
  admin                     × postgres                      65    2.2%
  batch_user                × analytics_db                  45    1.5%
  analytics                 × analytics_db                  34    1.2%
  admin                     × app_db                        23    0.8%
  backup_user               × postgres                      12    0.4%
  monitoring                × postgres                       8    0.3%
  replication               × app_db                         5    0.2%
  postgres                  × postgres                       4    0.1%
  test_user                 × app_db                         3    0.1%
  ... (all 18 combinations listed)

USER × HOST

  app_user                  × 192.168.1.100                 654   22.2%
  readonly                  × 10.0.1.50                     432   14.7%
  app_user                  × 172.16.0.10                   345   11.7%
  batch_user                × 10.0.1.51                     234    8.0%
  app_user                  × 10.0.1.50                     187    6.4%
  readonly                  × 192.168.1.100                 156    5.3%
  admin                     × 172.16.0.12                   123    4.2%
  analytics                 × 10.0.1.52                      98    3.3%
  batch_user                × 192.168.1.101                  76    2.6%
  readonly                  × 172.16.0.10                    65    2.2%
  ... (all 52 combinations listed)
```

**Key differences with `--clients`**:
- No "TOP" prefix in section headers
- **All** entities displayed (no 10-item limit)
- No `[X more...]` indicators
- Complete cross-tabulation tables

## Next Steps

- [SQL Analysis Deep Dive](sql-reports.md) - Detailed per-query analysis
- [Filtering Output](filtering-output.md) - Show only specific sections
- [Export Formats](json-export.md) - JSON and Markdown output
