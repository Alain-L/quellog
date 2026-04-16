# Troubleshooting Slow Queries

A step-by-step workflow to find and diagnose slow SQL queries in
PostgreSQL logs.

## Step 1: Find the worst queries

Run `--sql-performance` to get the full SQL performance breakdown:

```bash
quellog /var/log/postgresql/*.log --sql-performance
```

This displays three ranked tables:

- **Slowest individual queries** -- highest single-execution duration
- **Most frequent queries** -- highest execution count
- **Most time-consuming queries** -- highest cumulative duration

Focus on the "Most time-consuming" table first: a query running 275
times at 274 ms average costs more than a single 11 s outlier.

Each query has a **SQLID** (e.g. `up-UXcfCG`). Note the IDs you want
to investigate.

## Step 2: Drill into a specific query

Use `--sql-detail` with the SQLID to get the full picture:

```bash
quellog /var/log/postgresql/*.log --sql-detail up-UXcfCG
```

The detail view shows:

- **Execution count and duration stats** (min / median / max)
- **Temp file usage** for this query (indicates `work_mem` pressure)
- **Normalized query** (parameters replaced with `?`)
- **Example query** (one real execution with actual values)

If the detail shows temp file activity, the query is sorting or hashing
more data than `work_mem` allows.

## Step 3: Check temp files

Queries spilling to disk are a common cause of slowness:

```bash
quellog /var/log/postgresql/*.log --tempfiles
```

This shows a histogram of temp file activity over time and lists which
queries generate the most temp files. Large temp file sizes suggest
increasing `work_mem` or rewriting the query to reduce the sort/hash
set.

## Step 4: Check lock contention

Slow queries can also be blocked by locks:

```bash
quellog /var/log/postgresql/*.log --locks
```

Look for high wait times and recurring lock patterns. The "Waiting
queries" table shows which SQLIDs are blocked and for how long.

## Putting it together

Combine sections in a single pass for a complete view:

```bash
quellog /var/log/postgresql/*.log --sql-performance --tempfiles --locks
```

This gives you query rankings, disk spill data, and lock contention in
one report -- usually enough to identify the root cause.
