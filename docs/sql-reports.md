# SQL Analysis

quellog provides two SQL analysis modes: `--sql-summary` for aggregate query statistics and `--sql-detail` for individual query inspection.

## --sql-summary

Shows detailed statistics for all queries found in logs.

```bash
quellog /var/log/postgresql/*.log --sql-summary
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
se-a1b2c3  select * from orders o join customer...              23        2.34 s      987 ms        22.71 s
se-x4y5z6  select id, name, email from users wh...             456       456 ms       45 ms        20.52 s
se-m7n8o9  select count(*) from events where us...             178        1.98 s      112 ms        19.94 s
```

Top queries by total cumulative time. **Executed**: number of times run. **Max**: slowest execution. **Avg**: average duration. **Total**: sum of all executions.

**Queries generating temp files**

```
Queries generating temp files:
SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
se-y1z2a3  select * from large_table order by created_at desc...                           12       1.23 GB
se-b4c5d6  with recursive categories as ( select id, parent_id from...                       8     456.78 MB
se-e7f8g9  select array_agg(distinct name) from products group by...                         5     234.56 MB
```

Queries that created temporary files, sorted by total tempfile size. **Count**: number of tempfile creations. **Total Size**: cumulative size of all tempfiles created by this query.

**Lock-related query tables**

```
Acquired locks by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-h1i2j3  update inventory set quantity = quantity - ? where...         12          523 ms           6.28 s
in-k4l5m6  insert into audit_log (user_id, action, timestam...             8          215 ms           1.72 s
up-n7o8p9  update orders set status = ? where id = ?...                    5          187 ms         935 ms

Locks still waiting by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
se-q1r2s3  select * from products where category_id = ? for...            3           1.05 s           3.15 s
up-t4u5v6  update users set last_login = now() where id = ?...             2          825 ms           1.65 s

Most frequent waiting queries:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-h1i2j3  update inventory set quantity = quantity - ? where...         12          523 ms           6.28 s
in-k4l5m6  insert into audit_log (user_id, action, timestam...             8          215 ms           1.72 s
up-n7o8p9  update orders set status = ? where id = ?...                    5          187 ms         935 ms
se-q1r2s3  select * from products where category_id = ? for...            3           1.05 s           3.15 s
up-t4u5v6  update users set last_login = now() where id = ?...             2          825 ms           1.65 s
```

Three tables showing queries involved in lock waits. **Locks**: number of lock wait events. **Avg Wait**: average time spent waiting. **Total Wait**: sum of all wait times. "Acquired locks" = locks eventually granted. "Still waiting" = locks not granted when logs ended. "Most frequent" = all queries that waited, sorted by lock count.

### Query Normalization

Queries are normalized to group similar executions. Parameters are replaced with `?` or `$1`, `$2`, etc.

```sql
-- Original queries
SELECT * FROM users WHERE id = 1
SELECT * FROM users WHERE id = 42

-- Normalized as
SELECT * FROM users WHERE id = ?
```

### SQLID Format

Each query gets a short identifier like `se-a1b2c3` (select), `up-x4y5z6` (update), `in-m7n8o9` (insert), `mv-p1q2r3` (materialized view), or `xx-s4t5u6` (other). Use this ID with `--sql-detail`.

## --sql-detail

Shows detailed information for a specific query.

```bash
# Single query
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3

# Multiple queries
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3 --sql-detail se-x4y5z6
```

### Output Format

```
Details for SQLID: se-a1b2c3
SQL Query Details:
  SQLID            : se-a1b2c3
  Query Type       : select
  Raw Query        : SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id WHERE o.status = $1 ...
  Normalized Query : select * from orders o join customers c on o.customer_id = c.id where o.status = ? ...
  Executed         : 234
  Total Time       : 2h 15m 30s
  Median Time      : 1m 23s
  Max Time         : 5m 12s
```

**Fields**:

- **SQLID**: Query identifier
- **Query Type**: select, insert, update, delete, etc.
- **Raw Query**: Original query with parameter placeholders ($1, $2, etc.)
- **Normalized Query**: Query with all parameters replaced by `?`
- **Executed**: Number of executions
- **Total Time**: Cumulative execution time
- **Median Time**: Typical execution time (50th percentile)
- **Max Time**: Slowest execution

## Combining with Filters

Use filters to focus SQL analysis:

```bash
# Production database, specific time window
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --sql-summary

# Specific user
quellog /var/log/postgresql/*.log --dbuser app_user --sql-summary

# Exclude monitoring tools
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --exclude-user powa \
  --sql-summary
```

## Next Steps

- [Filter logs](filtering-logs.md) by time and attributes
- [Export formats](json-export.md) for JSON/Markdown output
- [Default report](default-report.md) for comprehensive analysis
