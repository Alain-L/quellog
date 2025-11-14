# SQL Analysis

quellog provides two SQL analysis modes: `--sql-summary` for aggregate query statistics and `--sql-detail` for individual query inspection.

## --sql-summary

Shows detailed statistics for all queries found in logs.

```bash
quellog /var/log/postgresql/*.log --sql-summary
```

### Output Sections

**SQL PERFORMANCE**

Summary metrics and histograms:
- Query load distribution (histogram by time)
- Total query duration, query count, unique queries
- Max/min/median/99th percentile durations
- Query duration distribution (< 1ms, < 10ms, < 100ms, < 1s, < 10s, >= 10s)

**Slowest individual queries**

Top queries sorted by maximum single execution time.

Columns: SQLID, Query (truncated), Duration

**Most Frequent Individual Queries**

Top queries sorted by execution count.

Columns: SQLID, Query (truncated), Executed

**Most time consuming queries**

Top queries sorted by total cumulative time.

Columns: SQLID, Query (truncated), Executed, Max, Avg, Total

**Queries generating temp files**

Queries that created temporary files, sorted by total tempfile size.

Columns: SQLID, Query (truncated), Count, Total Size

**Lock-related query tables**

Three tables showing queries involved in lock waits:
- Acquired locks by query
- Locks still waiting by query
- Most frequent waiting queries

Columns: SQLID, Query (truncated), Locks, Avg Wait, Total Wait

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
