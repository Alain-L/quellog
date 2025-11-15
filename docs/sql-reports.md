# SQL Analysis

quellog provides two SQL analysis modes: `--sql-summary` for aggregate query statistics and `--sql-detail` for individual query inspection.

## --sql-summary

Shows detailed statistics for all queries found in logs, including SQL performance, temp files, and locks.

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

**TEMP FILES section**

```
TEMP FILES

SQLID      Query                                                                         Count    Total Size
------------------------------------------------------------------------------------------------------------
se-N2d0E3  select node.id as id from alf_node node where node.type_qname_id <> ?...       364     100.47 GB
se-y1z2a3  select * from large_table order by created_at desc...                           12       1.23 GB
se-b4c5d6  with recursive categories as ( select id, parent_id from...                      8     456.78 MB
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
se-q1r2s3  select * from products where category_id = ? for...         3           1.05 s           3.15 s
up-t4u5v6  update users set last_login = now() where id = ?...         2          825 ms           1.65 s

Most frequent waiting queries:
SQLID      Query                                                     Locks         Avg Wait       Total Wait
------------------------------------------------------------------------------------------------------------
up-bG8qBk  update alf_node set version = ? , transaction_id...        259           2.88 s          12m 25s
in-79Lxjd  insert into alf_content_url (id, content_url, co...        130           2.97 s           6m 26s
up-Yd6ZIK  update act_ru_task set rev_ = ?, name_ = ?, pare...         19           5.68 s           1m 47s
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
SELECT * FROM users WHERE id = ?
```

### SQLID Format

Each query gets a short identifier like `se-a1b2c3` (select), `up-x4y5z6` (update), `in-m7n8o9` (insert), `mv-p1q2r3` (materialized view), or `xx-s4t5u6` (other). Use this ID with `--sql-detail`.

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

  Query count | ■ = 5

  07:50 - 10:19  ■■■■■■■■■■ 54
  10:19 - 12:48  ■■■■■■■■■■■■■■■■■■ 94
  12:48 - 15:16  ■■■■■■■■■■■■■■■ 79
  15:16 - 17:45  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 170
  17:45 - 20:14  ■■■■■■■■■■■ 56
  20:14 - 22:42  3

  Id                   : se-N2d0E3
  Query Type           : select
  Count                : 456
```

Basic query information with execution count histogram (shown when count > 1).

**TIME section** (when SQL metrics available)

```
TIME

  Cumulative time | ■ = 183 m

  07:50 - 10:19  ■■■■■■■■■ 1772 m
  10:19 - 12:48  ■■■■■■■■■■■■■■■■ 3059 m
  12:48 - 15:16  ■■■■■■■■■■■■ 2227 m
  15:16 - 17:45  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 7314 m
  17:45 - 20:14  ■■■■■■■■ 1562 m
  20:14 - 22:42  71 m

  Query duration distribution | ■ = 12 queries

  < 1 ms          -
  < 10 ms         -
  < 100 ms        -
  < 1 s           -
  < 10 s          -
  >= 10 s        ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 456 queries

  Total Duration       : 11d 2h 42m
  Min Duration         : 2m 00s
  Median Duration      : 35m 05s
  Max Duration         : 1h 12m 50s
```

Execution time analysis with two histograms (shown when count > 1):
- **Cumulative time**: Total time spent per time period
- **Query duration distribution**: Number of queries in each duration bucket

**TEMP FILES section** (when tempfile metrics available)

```
TEMP FILES

  Temp files size | ■ = 1 GB

  08:09 - 10:27  ■■■■■■■■■■■ 11 GB
  10:27 - 12:44  ■■■■■■■■■■■■■■■■■■■■■■■ 23 GB
  12:44 - 15:01  ■■■■■■■■■■■■■ 13 GB
  15:01 - 17:19  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 31 GB
  17:19 - 19:36  ■■■■■■■■■■■■■■■■■■■■■■■■ 24 GB
  19:36 - 21:53  ■ 1 GB

  Temp files count | ■ = 3

  08:09 - 10:27  ■■■■■■■■■■■■ 38
  10:27 - 12:44  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 91
  12:44 - 15:01  ■■■■■■■■■■■■■■ 44
  15:01 - 17:19  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 107
  17:19 - 19:36  ■■■■■■■■■■■■■■■■■■■■■■■■■■■ 81
  19:36 - 21:53  ■ 3

  Temp Files count     : 364
  Temp File min size   : 77.08 MB
  Temp File max size   : 295.61 MB
  Temp File avg size   : 282.63 MB
  Temp Files size      : 100.47 GB
```

Temporary file analysis with two histograms (shown when count > 1):
- **Temp files size**: Cumulative size of temp files per time period
- **Temp files count**: Number of temp files created per time period

**LOCKS section** (when lock metrics available)

```
LOCKS

  Acquired Locks       : 259
  Acquired Wait Time   : 12m 25s
  Still Waiting Locks  : 0
  Still Waiting Time   : 0 ms
  Total Wait Time      : 12m 25s
```

Lock wait statistics. Note: "Acquired Locks" is always shown (even if 0) to indicate the query was checked for locks.

**Normalized Query**

```
Normalized Query:

 select node.id as id
 from alf_node node
 where node.type_qname_id <> ?
 and node.store_id = ?
 and ( node.id in (
     select prop.node_id
     from alf_node_properties prop
     where (? = prop.qname_id)
     and prop.string_value = ? )
   and node.id in (
     select prop.node_id
     from alf_node_properties prop
     where (? = prop.qname_id)
     and prop.string_value = ? )
   and node.id in (
     select prop.node_id
     from alf_node_properties prop
     where (? = prop.qname_id)
     and prop.string_value = ? )
   and ( node.type_qname_id in ( ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? ) ) )
```

Pretty-printed normalized query with automatic indentation and keyword formatting.

**Example Query**

```
Example Query:

select  node.id             as id from alf_node node  where node.type_qname_id <> $1 AND node.store_id = $2     AND   (        node.id IN (     select PROP.node_id from alf_node_properties PROP where ($3 = PROP.qname_id) AND   PROP.string_value = $4  )     AND        node.id IN (     select PROP.node_id from alf_node_properties PROP where ($5 = PROP.qname_id) AND   PROP.string_value = $6  )     AND        node.id IN (     select PROP.node_id from alf_node_properties PROP where ($7 = PROP.qname_id) AND   PROP.string_value = $8  )     AND       (     node.type_qname_id IN  (  $9 , $10 , $11 , $12 , $13 , $14 , $15 , $16 , $17 , $18 , $19 , $20 , $21 , $22 , $23 , $24 , $25 , $26 , $27 , $28 , $29 )  )     )
```

One actual execution example showing the original query with parameter placeholders.

### Real-World Example

Query `se-N2d0E3` from production logs shows a complex query pattern:
- **456 executions** over ~15 hours
- **11 days total runtime** (avg 35 minutes per execution)
- **100 GB temp files** generated (avg 282 MB per file)
- Peak activity: 170 executions between 15:16-17:45

This data reveals:
- The query needs optimization (very long execution times)
- Significant temp file usage indicates work_mem may be too low
- Execution pattern follows business hours (peak afternoon activity)

## Combining with Filters

Use filters to focus SQL analysis:

```bash
# Last hour of logs
quellog /var/log/postgresql/*.log --last 1h --sql-summary

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

## Export Formats

Both `--sql-summary` and `--sql-detail` support markdown export:

```bash
# Export summary to markdown
quellog /var/log/postgresql/*.log --sql-summary --md > report.md

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
