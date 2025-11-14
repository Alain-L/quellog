# SQL Analysis Reports

quellog provides two specialized modes for in-depth SQL performance analysis: `--sql-summary` for per-query statistics and `--sql-detail` for drilling into specific queries.

## --sql-summary: Per-Query Statistics

The `--sql-summary` flag provides detailed metrics for every unique query, sorted by various performance dimensions.

### Usage

```bash
quellog /var/log/postgresql/*.log --sql-summary
```

### Output Format

```
SQL PERFORMANCE

  Query load distribution | ■ = 1 s

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

Slowest individual queries:
SQLID      Query                                                            Duration
------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customers c on o.customer_id...       15.23 s
se-x4y5z6  with user_segments as ( select user_id, case when total...       14.57 s
se-m7n8o9  with cohort_data as ( select user_id, date_trunc('day',...       12.68 s

Most time consuming queries:
SQLID      Query                                          Executed           Max           Avg         Total
------------------------------------------------------------------------------------------------------------
se-a1b2c3  select * from orders o join customer...              23       15.23 s        1.99 s       45.67 s
se-x4y5z6  select id, email, name from users w...              156        1.23 s      206 ms        32.14 s
se-m7n8o9  select count(*) from events where u...                8        4.12 s        2.74 s       21.89 s
```

### Sections Explained

#### Query load distribution

Histogram showing total query execution time per time bucket across the log period. Helps identify peak load periods.

#### Query duration distribution

Histogram showing distribution of queries by execution time ranges:
- < 1 ms
- < 10 ms
- < 100 ms
- < 1 s
- < 10 s
- >= 10 s

#### Slowest individual queries

Top 10 queries sorted by their longest single execution (max duration).

**Columns**: SQLID, Query text (truncated), Duration (max)

**Use for**:

- Finding outlier slow queries
- Detecting queries that sometimes perform poorly
- Investigating performance regressions or lock contention

#### Most time consuming queries

Top 10 queries sorted by cumulative execution time (total duration).

**Columns**: SQLID, Query text (truncated), Executed (count), Max, Avg, Total

**Use for**:

- Finding queries that consume the most database time overall
- Identifying optimization candidates with high total impact
- Understanding which queries dominate your workload

### Query Information

Each query entry shows:

- **SQLID**: Short identifier (e.g., `se-a1b2c3`) for use with `--sql-detail`
- **Query**: Normalized SQL text (truncated), with parameters replaced by `?` or `$1`, `$2`, etc.
- **Metrics** (varies by table):
    - **Duration**: Maximum execution time (in "Slowest individual queries")
    - **Executed**: Number of times executed
    - **Max**: Slowest single execution
    - **Avg**: Average execution time
    - **Total**: Sum of all execution times

### Query Normalization

quellog normalizes queries to group similar executions:

**Original queries**:
```sql
SELECT * FROM users WHERE id = 1
SELECT * FROM users WHERE id = 42
SELECT * FROM users WHERE id = 999
```

**Normalized**:
```sql
SELECT * FROM users WHERE id = $1
```

This allows aggregating statistics across parameter variations.

### Combining with Filters

Filter by time, database, user, etc. to focus analysis:

```bash
# Production database, last 24 hours
quellog /var/log/postgresql/*.log \
  --dbname production \
  --window 24h \
  --sql-summary

# Specific user, yesterday
quellog /var/log/postgresql/*.log \
  --dbuser app_user \
  --begin "2025-01-12 00:00:00" \
  --end "2025-01-12 23:59:59" \
  --sql-summary

# Exclude monitoring, peak hours
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --begin "2025-01-13 12:00:00" \
  --end "2025-01-13 14:00:00" \
  --sql-summary
```

## --sql-detail: Query Deep Dive

The `--sql-detail` flag shows detailed information for specific queries, including all executions and timing distributions.

### Usage

```bash
# Single query
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3d

# Multiple queries
quellog /var/log/postgresql/*.log \
  --sql-detail se-a1b2c3d \
  --sql-detail se-x4y5z6w

# Using shorthand -Q
quellog /var/log/postgresql/*.log -Q se-a1b2c3d
```

### Finding Query IDs

Query IDs are shown in:

1. `--sql-summary` output
2. Default report (top queries in SQL performance section)
3. Tempfiles section (queries associated with tempfiles)
4. Locks section (queries associated with lock waits)

### Output Format

```
SQL DETAIL: se-a1b2c3d

QUERY

  SELECT * FROM orders o
  JOIN customers c ON o.customer_id = c.id
  WHERE o.created_at > NOW() - INTERVAL '7 days'
  ORDER BY o.created_at DESC

STATISTICS

  Execution count           : 23
  Total time                : 45.67 s
  Average time              : 1.99 s
  Median time               : 1.87 s
  Min time                  : 1.23 s
  Max time                  : 3.45 s
  99th percentile time      : 3.12 s

EXECUTION TIMELINE

  2025-01-13 08:15:23  1.45 s
  2025-01-13 08:18:12  1.89 s
  2025-01-13 08:22:45  2.12 s
  2025-01-13 08:25:33  1.67 s
  2025-01-13 08:30:11  3.45 s  ← slowest
  2025-01-13 08:35:22  1.92 s
  ...

ASSOCIATED EVENTS

  Temporary files:
    - 2025-01-13 08:30:11: 123 MB
    - 2025-01-13 09:15:42: 89 MB

  Lock waits:
    - 2025-01-13 08:25:33: 2.3s wait (RowExclusiveLock on relation)
```

### Sections Explained

#### QUERY

The normalized query text with parameters replaced by `$1`, `$2`, etc.

#### STATISTICS

Key metrics:

- **Execution count**: How many times the query ran
- **Total time**: Sum of all executions
- **Average time**: Mean execution time
- **Median time**: 50th percentile (typical execution)
- **Min/Max time**: Fastest and slowest executions
- **99th percentile**: Upper bound for "normal" performance

**Interpreting**:

- **High median vs. low min**: Query performance is degrading over time
- **High max vs. median**: Occasional outliers (plan changes, cache misses)
- **Consistent times**: Predictable performance

#### EXECUTION TIMELINE

Chronological list of all executions with timestamps and durations.

**Use for**:

- Correlating slow executions with external events
- Identifying time-of-day performance patterns
- Finding performance regressions over time

#### ASSOCIATED EVENTS

Links the query to related events:

- **Temporary files**: When this query created tempfiles and their sizes
- **Lock waits**: When this query waited for locks and how long

**Use for**:

- Understanding why specific executions were slow
- Correlating tempfile creation with slow queries
- Identifying blocking scenarios

### Interpretation Examples

#### Example 1: Slow Query with Tempfiles

```
QUERY: se-abc123
  SELECT * FROM large_table ORDER BY created_at

STATISTICS
  Avg: 3.45s  Max: 5.12s

ASSOCIATED EVENTS
  Temporary files:
    - 456 MB
    - 389 MB
```

**Interpretation**: Query exceeds `work_mem` during sorting, creating large tempfiles. Solution: Add index on `created_at` or increase `work_mem`.

#### Example 2: Lock Contention

```
QUERY: se-xyz789
  UPDATE inventory SET quantity = quantity - $1 WHERE product_id = $2

STATISTICS
  Avg: 2.1s  Max: 8.3s

ASSOCIATED EVENTS
  Lock waits:
    - 5.2s wait (RowExclusiveLock)
    - 3.1s wait (RowExclusiveLock)
```

**Interpretation**: Query frequently waits for locks, likely concurrent updates on same rows. Solution: Reduce transaction duration, use `SELECT FOR UPDATE SKIP LOCKED`, or partition hot rows.

#### Example 3: Performance Degradation

```
EXECUTION TIMELINE
  2025-01-13 08:00:00  1.2s
  2025-01-13 09:00:00  1.3s
  2025-01-13 10:00:00  1.5s
  2025-01-13 11:00:00  2.1s
  2025-01-13 12:00:00  3.4s
  2025-01-13 13:00:00  5.2s
```

**Interpretation**: Query slows down over time. Possible causes: table growth without index, statistics out of date, cache pollution. Solution: ANALYZE table, check index usage, consider partitioning.

## Workflow: From Summary to Detail

Typical analysis workflow:

### Step 1: Get Overview

```bash
quellog /var/log/postgresql/*.log --sql-summary
```

Identify top queries by total time, average time, or count.

### Step 2: Drill into Specific Queries

```bash
# Investigate the slowest query
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3d

# Investigate the most frequent query
quellog /var/log/postgresql/*.log --sql-detail se-x4y5z6w
```

### Step 3: Correlate with Other Metrics

```bash
# Check if slow query creates tempfiles
quellog /var/log/postgresql/*.log --tempfiles

# Check if slow query has lock contention
quellog /var/log/postgresql/*.log --locks
```

### Step 4: Filter by Time to Isolate Issues

```bash
# When did this query become slow?
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 12:00:00" \
  --end "2025-01-13 13:00:00" \
  --sql-detail se-a1b2c3d
```

## Practical Examples

### Find and Optimize Slowest Query

```bash
# Step 1: Find slowest
quellog /var/log/postgresql/*.log --sql-summary | head -30

# Step 2: Get details
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3d

# Step 3: Explain the query in PostgreSQL
psql -c "EXPLAIN ANALYZE <query>"
```

### Identify Caching Candidates

```bash
# Find most frequent queries
quellog /var/log/postgresql/*.log --sql-summary | grep "TOP QUERIES BY EXECUTION COUNT" -A 20

# Check if they're fast (good cache candidates)
quellog /var/log/postgresql/*.log --sql-detail se-<top-query-id>
```

### Detect Lock-Heavy Queries

```bash
# Get queries with lock waits
quellog /var/log/postgresql/*.log --locks

# Drill into specific query
quellog /var/log/postgresql/*.log --sql-detail se-<query-with-locks>
```

### Track Query Performance Over Time

```bash
# Compare yesterday vs. today
quellog /var/log/postgresql/postgresql-2025-01-12.log --sql-detail se-a1b2c3d > yesterday.txt
quellog /var/log/postgresql/postgresql-2025-01-13.log --sql-detail se-a1b2c3d > today.txt
diff yesterday.txt today.txt
```

## Exporting SQL Analysis

### JSON Export

```bash
# Export summary as JSON
quellog /var/log/postgresql/*.log --sql-summary --json > sql_summary.json

# Extract specific query from JSON
quellog /var/log/postgresql/*.log --json | jq '.sql.query_stats["se-a1b2c3d"]'
```

### Markdown Export

```bash
# Generate markdown report
quellog /var/log/postgresql/*.log --sql-summary --md > sql_report.md
```

## Limitations

### Queries Without Duration

If a query appears in logs (e.g., in `STATEMENT:` lines) but doesn't have duration information, it won't appear in SQL summary:

```sql
-- This won't show up (no duration logged)
SELECT 1;
```

To include these, ensure `log_min_duration_statement >= 0` in PostgreSQL config.

### Query Normalization Edge Cases

Complex queries with string literals may not normalize perfectly:

```sql
-- Original
SELECT * FROM users WHERE name = 'John''s'

-- May normalize to
SELECT * FROM users WHERE name = $1
```

This is usually acceptable for analysis purposes.

## Next Steps

- [Export formats](json-export.md) for automated processing
- [Filtering logs](filtering-logs.md) to focus on specific subsets
- [Default report](default-report.md) for comprehensive analysis
