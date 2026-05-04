# Incident Triage

A timed workflow for investigating a PostgreSQL incident using log
analysis. Start broad, then narrow down.

## Step 1: Get the overview (2 minutes)

Start with the summary and events for the incident window:

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --summary --events
```

Check the **Events** section: FATAL errors (class 28, 3D) point to
auth/connection issues; ERROR entries (class 23, 40, 42) indicate
application problems like deadlocks or constraint violations. Each
pattern carries a short id (`fa-XXXX`, `er-XXXX`, etc.) you can
re-use with `--event-detail` in Step 3.

Use `--begin` with `--window` if you know the approximate time but not
the exact end:

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" --window 30m
```

## Step 2: Check queries and locks (3 minutes)

Once you have the time window, look at SQL performance and locks:

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-performance --locks --tempfiles
```

Look for query duration spikes in the histogram, lock wait times in
seconds, temp file surges, or a single query dominating the "Most
time-consuming" list. Note the SQLIDs of suspicious queries.

## Step 3: Deep dive (5 minutes)

Drill into specific queries identified in Step 2:

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-detail se-a1b2c3
```

This shows the normalized query, execution stats, and temp file usage.
You can pass multiple `--sql-detail` flags in one invocation.

For an event pattern, use `--event-detail` (or `-E`) with the id from
Step 1's output:

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --event-detail fa-6K1G
```

Shows the full raw message, occurrences-over-time chart, first/last
seen and frequency. See [SQL Reports → --event-detail](../sql-reports.md#-event-detail).

## Step 4: Generate a shareable report

```bash
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --html -o incident-2025-01-13.html
```

## Filtering tips

Narrow results further with database, user, or application filters:

```bash
# Only entries from the production database
quellog /var/log/postgresql/*.log -d production \
  --begin "2025-01-13 14:00:00" --window 1h

# Exclude a noisy monitoring user
quellog /var/log/postgresql/*.log -U monitoring_user \
  --begin "2025-01-13 14:00:00" --window 1h
```
