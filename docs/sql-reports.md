# SQL Analysis

quellog provides three SQL analysis modes: `--sql-performance` for detailed performance analysis, `--sql-overview` for query type breakdowns, and `--sql-detail` for individual query inspection.

## --sql-performance

Shows detailed statistics for all queries found in logs, including SQL performance, temp files, and locks.

```bash
quellog /var/log/postgresql/*.log --sql-performance
```

### Output Format

**SQL PERFORMANCE section**

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

Histograms show query load over time and distribution by duration. Metrics show totals, counts, and percentiles.

**Slowest individual queries**

```
Slowest individual queries:
SQLID      Query                                                            Duration
------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customers c on o.customer_id...        2.34 s
se-x4y5z6  with user_segments as ( select user_id, case when total...        2.12 s
se-m7n8o9  select count(*) from events where user_id = ? and date...         1.98 s
```

Top queries sorted by maximum single execution time (**Duration**: slowest execution observed).

**Most Frequent Individual Queries**

```
Most Frequent Individual Queries:
SQLID      Query                                                            Executed
------------------------------------------------------------------------------------
se-p1q2r3  select id, name, email from users where id = ?...                    456
se-r4s5t6  select count(*) from products where category_id = ?...               234
se-u7v8w9  insert into audit_log (user_id, action, created_at) v...             178
```

Top queries sorted by execution count (**Executed**: number of times the query ran).

**Most time consuming queries**

```
Most time consuming queries:
SQLID      Query                                          Executed           Max           Avg         Total
------------------------------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customer...              23        2.34 s        987 ms       22.71 s
se-x4y5z6  select id, name, email from users wh...             456        456 ms         45 ms       20.52 s
se-m7n8o9  select count(*) from events where us...             178        1.98 s        112 ms       19.94 s
```

Top queries by total cumulative time. **Executed**: number of times run. **Max**: slowest execution. **Avg**: average duration. **Total**: sum of all executions.

**TEMP FILES section**

```
TEMP FILES

SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
se-N2d0E3  select node.id as id from alf_node node where node.type_qname_id <> ?...        364     100.47 GB
se-y1z2a3  select * from large_table order by created_at desc...                            12       1.23 GB
se-b4c5d6  with recursive categories as ( select id, parent_id from...                       8     456.78 MB
```

Queries that created temporary files, sorted by total tempfile size. **Count**: number of tempfile creations. **Total Size**: cumulative size of all tempfiles created by this query.

**LOCKS section**

```
LOCKS

Acquired locks by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-bG8qBk  update alf_node set version = ? , transaction_id...        259           2.88 s          12m 25s
in-79Lxjd  insert into alf_content_url (id, content_url, co...        130           2.97 s           6m 26s
up-Yd6ZIK  update act_ru_task set rev_ = ?, name_ = ?, pare...         19           5.68 s           1m 47s

Locks still waiting by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
se-q1r2s3  select * from products where category_id = ? for...         3           1.05 s             3.15 s
up-t4u5v6  update users set last_login = now() where id = ?...         2           825 ms             1.65 s

Most frequent waiting queries:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-bG8qBk  update alf_node set version = ? , transaction_id...        259           2.88 s           12m 25s
in-79Lxjd  insert into alf_content_url (id, content_url, co...        130           2.97 s            6m 26s
up-Yd6ZIK  update act_ru_task set rev_ = ?, name_ = ?, pare...         19           5.68 s            1m 47s
```

Three tables showing queries involved in lock waits:

- **Acquired locks by query**: Locks that were eventually granted (sorted by total wait time)
- **Locks still waiting by query**: Locks not granted when logs ended (sorted by total wait time)
- **Most frequent waiting queries**: All queries that waited, sorted by lock count

**Fields**:

- **Locks**: Number of lock wait events
- **Avg Wait**: Average time spent waiting for locks
- **Total Wait**: Sum of all lock wait times for this query

### Query Normalization

Queries are normalized to group similar executions. Parameters are replaced with `?` or `$1`, `$2`, etc.

```sql
-- Original queries
SELECT * FROM users WHERE id = 1
SELECT * FROM users WHERE id = 42

-- Normalized as
select * from users where id = ?
```

### SQLID Format

Each query gets a short identifier like `se-a1b2c3` (select), `up-x4y5z6` (update), `in-m7n8o9` (insert), `mv-p1q2r3` (materialized view), or `xx-s4t5u6` (other). Use this ID with `--sql-detail`.

## --sql-overview

Provides an overview of query types and their distribution across different dimensions (database, user, host, application).

```bash
quellog /var/log/postgresql/*.log --sql-overview
```

This mode focuses on query categorization and breakdown rather than performance metrics. It helps answer questions like "which applications run the most SELECT queries?" or "what types of queries does each database receive?"

### Output Format

**Query Category Summary**

```
  Query Category Summary

    DML          : 1,234     (78.5%)
    UTILITY      : 245       (15.6%)
    DDL          : 78        (5.0%)
    TCL          : 14        (0.9%)
```

Queries are grouped into high-level categories:

- **DML** (Data Manipulation Language): SELECT, INSERT, UPDATE, DELETE, MERGE
- **DDL** (Data Definition Language): CREATE, ALTER, DROP, TRUNCATE
- **TCL** (Transaction Control Language): BEGIN, COMMIT, ROLLBACK, SAVEPOINT
- **CTE** (Common Table Expressions): WITH queries
- **CURSOR**: DECLARE, FETCH, CLOSE cursor operations
- **UTILITY**: VACUUM, ANALYZE, EXPLAIN, SET, SHOW, etc.
- **Security**: GRANT, REVOKE
- **Other**: Unclassified queries

**Query Type Distribution**

```
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

Detailed breakdown by specific query type. quellog recognizes 40+ query types including:

- **DML**: SELECT, INSERT, UPDATE, DELETE, MERGE
- **DDL**: CREATE TABLE, ALTER TABLE, DROP TABLE, CREATE INDEX, DROP INDEX, TRUNCATE
- **TCL**: BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE SAVEPOINT
- **CTE**: WITH (Common Table Expressions)
- **CURSOR**: DECLARE, FETCH, CLOSE
- **Utility**: VACUUM, ANALYZE, EXPLAIN, SET, SHOW, COPY, DISCARD, RESET
- **Security**: GRANT, REVOKE

**Dimension Breakdowns**

Four breakdowns show query types per database, user, host, and application:

```
  Queries per Database

    mydb (1,234 queries, 45m 23s)
      SELECT         890      38m 12s
      INSERT         234       5m 45s
      UPDATE         110       1m 26s

    analytics_db (523 queries, 12m 45s)
      SELECT         487      11m 30s
      INSERT          36       1m 15s

  Queries per User

    app_user (1,456 queries, 52m 8s)
      SELECT       1,123      45m 32s
      INSERT         234       5m 12s
      UPDATE          99       1m 24s

    readonly (245 queries, 3m 15s)
      SELECT         245       3m 15s

  Queries per Host

    10.0.1.45 (789 queries, 32m 17s)
      SELECT         567      28m 34s
      INSERT         145       2m 51s
      UPDATE          77         52s

  Queries per Application

    app_server (1,123 queries, 48m 9s)
      SELECT         890      42m 15s
      INSERT         178       4m 32s
      UPDATE          55       1m 22s

    batch_job (234 queries, 8m 14s)
      INSERT          56       3m 45s
      UPDATE          55       2m 18s
      DELETE          43       1m 29s
```

Each dimension shows:
- Total count and cumulative duration
- Top query types with individual counts and durations
- Sorted by total count descending

### Use Cases

**Identify application patterns**

```bash
quellog /var/log/postgresql/*.log --sql-overview
```

See which applications are read-heavy (mostly SELECT) vs write-heavy (INSERT/UPDATE/DELETE).

**Database workload analysis**

```bash
quellog /var/log/postgresql/*.log --dbname production --sql-overview
```

Understand the query mix for a specific database: transactional (DML), analytical (complex SELECT), or maintenance (VACUUM/ANALYZE).

**User activity profiling**

```bash
quellog /var/log/postgresql/*.log --sql-overview
```

Check if users are respecting their role boundaries (e.g., readonly users should only run SELECT queries).

**Combining with filters**

```bash
# Last hour query breakdown
quellog /var/log/postgresql/*.log --last 1h --sql-overview

# Specific database and time range
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --sql-overview

# Exclude monitoring noise
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --sql-overview
```

## --sql-detail

Shows comprehensive analysis for a specific query, including execution patterns, temp files, and lock behavior.

```bash
# Single query
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3

# Multiple queries
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3 --sql-detail up-bG8qBk

# Short form
quellog /var/log/postgresql/*.log -Q se-N2d0E3
```

### Output Format

The output is organized into sections that appear only when relevant data exists:

**SQL DETAILS section** (always present)

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
```

Basic query information with execution count histogram (shown when count > 1).

**TIME section** (when SQL metrics available)

```
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
```

Execution time analysis with two histograms (shown when count > 1):

- **Cumulative time**: Total time spent per time period
- **Query duration distribution**: Number of queries in each duration bucket

**TEMP FILES section** (when tempfile metrics available)

```
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
```

Temporary file analysis with two histograms (shown when count > 1):

- **Temp files size**: Cumulative size of temp files per time period
- **Temp files count**: Number of temp files created per time period

**LOCKS section** (when lock metrics available)

```
LOCKS

  Acquired Locks       : 127
  Acquired Wait Time   : 5m 42s
  Still Waiting Locks  : 3
  Still Waiting Time   : 8.45 s
  Total Wait Time      : 5m 50s
```

Lock wait statistics. Note: "Acquired Locks" is always shown (even if 0) to indicate the query was checked for locks.

**Normalized Query**

```
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
```

Pretty-printed normalized query with automatic indentation and keyword formatting.

**Example Query**

```
Example Query:

SELECT o.id, o.customer_id, o.total_amount, c.name FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.status = 'pending' AND o.created_at >= '2025-01-13 00:00:00' AND o.created_at < '2025-01-14 00:00:00' ORDER BY o.created_at DESC LIMIT 100
```

One actual execution example showing the original query with parameter placeholders.

## Combining with Filters

Use filters to focus SQL analysis:

```bash
# Last hour of logs
quellog /var/log/postgresql/*.log --last 1h --sql-performance

# Production database, specific time window
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --sql-performance

# Specific user
quellog /var/log/postgresql/*.log --dbuser app_user --sql-performance

# Exclude monitoring tools
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --exclude-user powa \
  --sql-performance
```

## Export Formats

All SQL analysis modes (`--sql-performance`, `--sql-overview`, `--sql-detail`) support markdown export:

```bash
# Export performance analysis to markdown
quellog /var/log/postgresql/*.log --sql-performance --md > report.md

# Export query type overview to markdown
quellog /var/log/postgresql/*.log --sql-overview --md > overview.md

# Export specific query details to markdown
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3 --md > query-analysis.md
```

Markdown output includes:

- All histograms in code blocks
- Tables with proper formatting
- SQL queries in syntax-highlighted blocks
- Perfect for documentation and reports

## Next Steps

- [Filter logs](filtering-logs.md) by time and attributes
- [Export formats](json-export.md) for JSON/Markdown output
- [Default report](default-report.md) for comprehensive analysis
