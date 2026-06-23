# SQL Analysis

Three SQL analysis modes: `--sql-performance` for detailed performance analysis, `--sql-overview` for query type breakdowns, and `--sql-detail` for individual query inspection. `--event-detail` (covered at the bottom of this page) gives the same drill-down for individual event patterns.

## --sql-performance

Detailed statistics for all queries, including temp files and locks.

```bash
quellog /var/log/postgresql/*.log --sql-performance
```

### SQL Performance

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

  Query duration distribution | ■ = 10 req

  < 1 ms          ■■ 25 req
  < 10 ms         ■■■■■■■ 78 req
  < 100 ms        ■■■■■■■■■■■■■■ 156 req
  < 1 s           ■■■■■■■■■■ 112 req
  < 10 s          ■■■■■■■■ 89 req
  >= 10 s         ■■ 23 req
```

The HTML report adds a **cost map** — a log-log scatter of every query by execution count × average duration — to spot the heaviest queries at a glance. See [HTML Report → Cost map](html-report.md#cost-map).

### Query Tables

```
Slowest individual queries:
SQLID      Query                                                            Duration
------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customers c on o.customer_id...        2.34 s
se-x4y5z6  with user_segments as ( select user_id, case when total...        2.12 s
se-m7n8o9  select count(*) from events where user_id = ? and date...         1.98 s

Most Frequent Individual Queries:
SQLID      Query                                                            Executed
------------------------------------------------------------------------------------
se-p1q2r3  select id, name, email from users where id = ?...                    456
se-r4s5t6  select count(*) from products where category_id = ?...               234
se-u7v8w9  insert into audit_log (user_id, action, created_at) v...             178

Most time consuming queries:
SQLID      Query                                          Executed           Max           Avg         Total
------------------------------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customer...              23        2.34 s        987 ms       22.71 s
se-x4y5z6  select id, name, email from users wh...             456        456 ms         45 ms       20.52 s
se-m7n8o9  select count(*) from events where us...             178        1.98 s        112 ms       19.94 s
```

### Temp Files and Locks

When queries generated temp files or waited on locks, those sections are included:

```
TEMP FILES

SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
se-N2d0E3  select node.id as id from alf_node node where node.type_qname_id <> ?...        364     100.47 GB
se-y1z2a3  select * from large_table order by created_at desc...                            12       1.23 GB

LOCKS

Waiting queries:
SQLID      Query                                                       Acquired     Waiting       Total Wait
------------------------------------------------------------------------------------------------------------
up-bG8qBk  update alf_node set version = ? , transaction_id...              259           0          12m 25s
in-79Lxjd  insert into alf_content_url (id, content_url, co...              130           0           6m 26s
```

### Query Normalization

Queries are normalized to group similar executions:

```sql
-- Original queries
SELECT * FROM users WHERE id = 1
SELECT * FROM users WHERE id = 42

-- Normalized as
select * from users where id = ?
```

Long `IN ($1, $2, $3, …)` placeholder lists collapse to `in (...)` so the same query with different list lengths aggregates as one. Identifiers in double quotes keep their case.

### SQLID Format

Each query gets a short identifier: `se-a1b2c3` (select), `up-x4y5z6` (update), `in-m7n8o9` (insert), `de-p1q2r3` (delete). Use this ID with `--sql-detail`.

### TCL Statements

Transaction control statements (BEGIN, COMMIT, ROLLBACK, SAVEPOINT) are separated into a dedicated **TCL** tab in the query tables, keeping DML/DDL queries uncluttered.

## --sql-overview

Query type distribution across dimensions (database, user, host, application).

```bash
quellog /var/log/postgresql/*.log --sql-overview
```

### Category and Type Summary

```
  Query Category Summary

    DML          : 1,234     (78.5%)
    UTILITY      : 245       (15.6%)
    DDL          : 78        (5.0%)
    TCL          : 14        (0.9%)

  Query Type Distribution

    SELECT       : 890       (56.6%)
    INSERT       : 234       (14.9%)
    UPDATE       : 110       (7.0%)
    DELETE       : 45        (2.9%)
    BEGIN        : 78        (5.0%)
    COMMIT       : 65        (4.1%)
    CREATE TABLE : 12        (0.8%)
    VACUUM       : 23        (1.5%)
    ...
```

Categories:

- **DML** — SELECT, INSERT, UPDATE, DELETE
- **DDL** — CREATE, ALTER, DROP
- **TCL** — BEGIN, COMMIT, ROLLBACK
- **UTILITY** — VACUUM, ANALYZE, SET, COPY

### Dimension Breakdowns

Query types broken down per database, user, host, and application:

```
  Queries per Database

    mydb (1,234 queries, 45m 23s)
      SELECT         890      38m 12s
      INSERT         234       5m 45s
      UPDATE         110       1m 26s

    analytics_db (523 queries, 12m 45s)
      SELECT         487      11m 30s
      INSERT          36       1m 15s
```

Same structure for Queries per User, per Host, and per Application.

## --sql-detail

Comprehensive analysis for a specific query.

```bash
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3

# Multiple queries
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3 --sql-detail up-bG8qBk

# Short form
quellog /var/log/postgresql/*.log -Q se-N2d0E3
```

### Output

Sections appear only when relevant data exists:

```
SQL DETAILS

  Query count | ■ = 8

  09:00 - 11:00  ■■■■■■■■ 42
  11:00 - 13:00  ■■■■■■■■■■■■■■■■ 78
  13:00 - 15:00  ■■■■■■■■■■■■■■ 67
  15:00 - 17:00  ■■■■■■■■■■■■■■■■■■■■■■■ 112
  17:00 - 19:00  ■■■■■■■ 34
  19:00 - 21:00  5

  Id                   : se-a1b2c3
  Query Type           : select
  Count                : 338
  Databases            : app_db 320, reporting 18
  Users                : app_user 338
  Apps                 : webapp 300, psql 38
  Hosts                : 10.0.0.12 338
  Prepared as          : S_3, <unnamed> (2 names seen)

TIME

  Cumulative time | ■ = 45 s

  09:00 - 11:00  ■■■■■■■■ 378 s
  11:00 - 13:00  ■■■■■■■■■■■■■■■■ 756 s
  13:00 - 15:00  ■■■■■■■■■■■■ 589 s
  15:00 - 17:00  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 1834 s
  17:00 - 19:00  ■■■■■■ 312 s
  19:00 - 21:00  23 s

  Query duration distribution | ■ = 15 queries

  < 1 ms         ■■ 28 queries
  < 10 ms        ■■■■■■ 87 queries
  < 100 ms       ■■■■■■■■■■ 145 queries
  < 1 s          ■■■■■■■ 98 queries
  < 10 s         ■■■■■ 67 queries
  >= 10 s        ■ 13 queries

  Total Duration       : 1h 05m 32s
  Min Duration         : 1 ms
  Median Duration      : 234 ms
  Max Duration         : 15.23 s

TEMP FILES

  Temp files size | ■ = 250 MB

  10:15 - 12:20  ■■■■■■■■ 2.1 GB
  12:20 - 14:25  ■■■■■■■■■■■■■■■■ 4.3 GB
  14:25 - 16:30  ■■■■■■■■■■ 2.8 GB
  16:30 - 18:35  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 8.2 GB
  18:35 - 20:40  ■■■■■ 1.5 GB
  20:40 - 22:45  97 MB

  Temp files count | ■ = 5

  10:15 - 12:20  ■■■■■■■ 38
  12:20 - 14:25  ■■■■■■■■■■■■■■ 72
  14:25 - 16:30  ■■■■■■■■■ 45
  16:30 - 18:35  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 198
  18:35 - 20:40  ■■■■ 23
  20:40 - 22:45  2

  Temp Files count     : 378
  Temp File min size   : 24.00 MB
  Temp File max size   : 512.00 MB
  Temp File avg size   : 128.45 MB
  Temp Files size      : 47.43 GB

LOCKS

  Acquired Locks       : 127
  Acquired Wait Time   : 5m 42s
  Still Waiting Locks  : 3
  Still Waiting Time   : 8.45 s
  Total Wait Time      : 5m 50s

Normalized Query:

 select o.id, o.customer_id, o.total_amount, c.name
 from orders o
 join customers c
 on o.customer_id = c.id
 where o.status = ?
 and o.created_at >= ?
 and o.created_at < ?
 order by o.created_at desc
 limit ?

Example Query:

SELECT o.id, o.customer_id, o.total_amount, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.status = 'pending' AND o.created_at >= '2025-01-13 00:00:00' AND o.created_at < '2025-01-14 00:00:00' ORDER BY o.created_at DESC LIMIT 100
```

The **Query Info** block lists the top databases, users, applications and hosts for the query, and — for extended-protocol queries — the prepared-statement name(s) (`Prepared as`; `<unnamed>` when none were assigned). When parameter values are logged (via `DETAIL: parameters:` or auto_explain), a **Slowest Run** block adds that run's timestamp and PID followed by the query with its real parameter values substituted in.

### Execution Plans (auto_explain)

When `auto_explain` is enabled in PostgreSQL, execution plans are captured and displayed in the sql-detail output. In the HTML report, a **Visualize** button can be used to send the plan to [explain.dalibo.com](https://explain.dalibo.com) for interactive visualization.

See [PostgreSQL Setup](postgresql-setup.md) for auto_explain configuration.

## --event-detail

Comprehensive report for a specific event pattern, identified by the
short id displayed in the [`--events`](default-report.md#events-events)
output (`<sev>-<4-char-hash>`).

```bash
quellog /var/log/postgresql/*.log --event-detail fa-6K1G

# Multiple patterns
quellog /var/log/postgresql/*.log --event-detail fa-6K1G --event-detail er-5GIv

# Short form
quellog /var/log/postgresql/*.log -E fa-6K1G
```

### Output

```
EVENT DETAILS

  Event count | ■ = 1

  16:56 - 16:57  ■ 1
  16:57 - 16:57   -
  16:57 - 16:58   -
  16:58 - 16:59   -
  16:59 - 17:00   -
  17:00 - 17:01   -
  17:01 - 17:02   -
  17:02 - 17:03   -
  17:03 - 17:04   -
  17:04 - 17:05   -
  17:05 - 17:06  ■■■■■■■■■■■■■■■■■■■■ 20
  17:06 - 17:07  ■■■■■■■■■■■■■■■■■■■ 19

  Id                   : fa-6K1G
  Severity             : FATAL
  SQLSTATE Class       : 53 - Insufficient Resources
  Count                : 40
  First Seen           : 2026-04-02 16:56:06 CEST
  Last Seen            : 2026-04-02 17:07:14 CEST
  Frequency            : 3.59 /min

Normalized Pattern:

 sorry, too many clients already

Example:

 [157] 53300: db=app_db,user=app_user,app=[unknown],client=172.28.0.10 FATAL:  sorry, too many clients already
```

When PostgreSQL logged the statements behind the events (via `STATEMENT`
continuation lines), a **Triggering Queries** table lists them ranked by
share (Pareto):

```
Triggering Queries:
  QueryID     QUERY                                            COUNT       %
  se-Xskzeq   set statement_timeout = ?; select pg_sleep(?);       3  100.0%  ■■■■■■■■■■
```

The bar chart shows occurrences over time across 12 buckets between
the first and last occurrence — useful to spot bursts vs steady drip.
First/Last/Frequency are derived from the per-event timestamps. The
Example is the raw log message before normalization.

The HTML report exposes the same data through a click-to-detail modal
on each event row.

## Combining with Filters

```bash
# Last hour
quellog /var/log/postgresql/*.log --last 1h --sql-performance

# Specific database and time range
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --sql-performance

# Exclude monitoring
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --sql-overview
```
