# SQL Analysis

quellog provides two SQL analysis modes: `--sql-summary` for aggregate query statistics and `--sql-detail` for individual query inspection.

## --sql-summary

Shows detailed statistics for all queries found in logs.

```bash
quellog /var/log/postgresql/*.log --sql-summary
```

### Output Format

```
SQL PERFORMANCE

  Query load distribution | ■ = 169 m

  02:11 - 05:36  4 m
  05:36 - 09:02  ■ 291 m
  09:02 - 12:27  ■■■■■■■■■■■■■■■■■■■■■■■■■■■ 4602 m

  Total query duration      : 11d 4h 33m
  Total queries parsed      : 485
  Total unique query        : 5
  Top 1% slow queries       : 5

  Query max duration        : 1h 12m 50s
  Query min duration        : 2m 00s
  Query median duration     : 42m 05s
  Query 99% max duration    : 1h 08m 57s

  Query duration distribution | ■ = 13 req

  < 1 ms          -
  < 10 ms         -
  < 100 ms        -
  < 1 s           -
  < 10 s          -
  >= 10 s        ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 485 req

Slowest individual queries:
SQLID      Query                                                            Duration
------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customers c on o.customer_id...       15.23 s
se-x4y5z6  with user_segments as ( select user_id, case when total...       14.57 s
se-m7n8o9  with cohort_data as ( select user_id, date_trunc('day'...        12.68 s

Most Frequent Individual Queries:
SQLID      Query                                                            Executed
------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customers c on o.customer_id...         456
se-x4y5z6  select id, email, name from users where status = ?...               234
se-m7n8o9  select count(*) from events where user_id = ?...                    178

Most time consuming queries:
SQLID      Query                                          Executed           Max           Avg         Total
------------------------------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customer...         456       15.23 s        2m 12s     16h 45m 12s
se-x4y5z6  select id, email, name from users w...          234        1.23 s        1m 05s      4h 14m 30s
se-m7n8o9  select count(*) from events where u...          178        4.12 s        2m 14s      6h 37m 52s

Queries generating temp files:
SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
se-p1q2r3  select * from large_table order by created_at desc...                          123       5.67 GB
se-s4t5u6  with recursive categories as ( select id, parent_id from...                      45       2.34 GB
se-v7w8x9  select array_agg(distinct name) from products group by...                        12       1.12 GB

Acquired locks by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-a1b2c3  update inventory set quantity = quantity - ? where...         12           5.23 s           1m 02s
in-d4e5f6  insert into audit_log (user_id, action, timestam...            8           2.15 s          17.20 s
up-g7h8i9  update orders set status = ? where id = ?...                   5           1.87 s           9.35 s

Locks still waiting by query:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
se-j1k2l3  select * from products where category_id = ? for...           3          10.50 s          31.50 s
up-m4n5o6  update users set last_login = now() where id = ?...            2           8.25 s          16.50 s

Most frequent waiting queries:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-a1b2c3  update inventory set quantity = quantity - ? where...         12           5.23 s           1m 02s
in-d4e5f6  insert into audit_log (user_id, action, timestam...            8           2.15 s          17.20 s
up-g7h8i9  update orders set status = ? where id = ?...                   5           1.87 s           9.35 s
se-j1k2l3  select * from products where category_id = ? for...           3          10.50 s          31.50 s
up-m4n5o6  update users set last_login = now() where id = ?...            2           8.25 s          16.50 s
```

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
  Raw Query        : select * from orders o join customers c on o.customer_id = c.id where o.status = $1 ...
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
