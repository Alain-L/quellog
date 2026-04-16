# Troubleshooting Slow Queries

## Step 1: Identify slow queries

```bash
quellog /var/log/postgresql/*.log --sql-performance
```

Look at the "Slowest individual queries" and "Most time consuming queries" tables.

## Step 2: Check memory pressure

```bash
# Show tempfiles and related SQL performance
quellog /var/log/postgresql/*.log --tempfiles --sql-performance
```

Queries generating large temp files are exceeding `work_mem` and spilling to disk.

## Step 3: Check lock contention

```bash
# Locks + SQL performance + events
quellog /var/log/postgresql/*.log --locks --sql-performance --events
```

Lock contention alongside query performance and errors.

## Step 4: Drill down into a specific query

```bash
quellog /var/log/postgresql/*.log --sql-detail se-a1b2c3
```

Shows execution patterns, temp files, locks, and the full query text.

!!! note "Work in progress"
    This how-to will be expanded with interpretation guidance and tuning recommendations.
