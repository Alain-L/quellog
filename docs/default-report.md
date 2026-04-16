# Report Sections

When you run quellog without section flags, you get a report with all available sections. Each section can be requested individually with its flag — see [Filtering](filtering.md) for details. Some flags (e.g., `--connections`, `--tempfiles`, `--locks`) display additional detail not shown in the default report.

## Summary

```
SUMMARY

  Start date                : 2025-01-13 00:00:00 UTC
  End date                  : 2025-01-13 23:59:59 UTC
  Duration                  : 23h59m59s
  Total entries             : 1,234
  Throughput                : 8,227 entries/s
```

## SQL Summary (`--sql-summary`)

Shows query execution statistics and load distribution from the default report. For per-query analysis, see [SQL Analysis](sql-reports.md).

```
SQL SUMMARY

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

Requires `log_min_duration_statement >= 0`.

## Events (`--events`)

Log entry distribution by severity level.

```
EVENTS

  LOG     : 1,180
  WARNING : 3
  ERROR   : 1
```

Severity levels: LOG, WARNING, ERROR, FATAL, PANIC.

## Error Classes (`--errors`)

PostgreSQL error distribution by SQLSTATE class code.

```
ERROR CLASSES

  42 – Syntax Error or Access Rule Violation   : 125
  23 – Integrity Constraint Violation          : 18
  22 – Data Exception                          : 5
  53 – Insufficient Resources                  : 2
```

Common classes: **42** (syntax/permissions), **23** (constraint violations), **22** (invalid input), **53** (resources), **08** (connections), **40** (deadlocks).

!!! info
    Requires SQLSTATE codes in logs: `%e` in `log_line_prefix`, or csvlog/jsonlog format.

## Temporary Files (`--tempfiles`)

Queries that exceeded `work_mem` and spilled to disk.

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

Requires `log_temp_files >= 0`.

## Locks (`--locks`)

Lock contention, wait times, and queries involved.

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

Three query tables: **Acquired** (resolved waits, by total wait), **Still waiting** (unresolved at log end), **Most frequent** (by lock count).

Requires `log_lock_waits = on`.

## Maintenance (`--maintenance`)

Autovacuum and autoanalyze operations.

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

Tables sorted by operation count. Space recovered by VACUUM shown when available.

Requires `log_autovacuum_min_duration >= 0`.

## Checkpoints (`--checkpoints`)

Checkpoint frequency and performance.

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

Types: **time** (by `checkpoint_timeout`), **wal** (by `max_wal_size`), **shutdown**, **immediate** (manual).

Requires `log_checkpoints = on`.

## Connections (`--connections`)

Connection patterns and session durations. The default report shows summary metrics. With `--connections`, additional session analytics are displayed: duration distribution histogram, and session duration tables by user, database, and host.

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

Requires `log_connections = on`. Session durations require `log_disconnections = on`.

## Clients (`--clients`)

Unique database entities with activity counts. The default report shows the top 10 per category. With `--clients`, all entities are displayed.

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
```

Cross-tabulations (USER x DATABASE, USER x HOST) help identify access patterns and security anomalies.

Requires user/database/app info in `log_line_prefix` or csvlog/jsonlog format.
