# Your First Analysis

Run quellog against your PostgreSQL log files to get an instant overview
of database activity, errors, and query performance.

## Basic run

Point quellog at one or more log files:

```bash
quellog /var/log/postgresql/*.log
```

quellog auto-detects the log format (stderr, CSV, or JSON) and prints a
report with sections such as:

| Section                    | What it tells you                                      |
|----------------------------|--------------------------------------------------------|
| **Summary**                | Time range, entry count, throughput                    |
| **SQL Summary**            | Query load histogram, duration stats, percentiles      |
| **Events**                 | Errors and fatals grouped by SQL error class           |
| **Temp Files**             | Queries spilling to disk (work_mem pressure)           |
| **Locks**                  | Lock waits and deadlocks                               |
| **Autovacuum / Autoanalyze** | Maintenance activity (two sibling sections)          |
| **Checkpoints**            | Checkpoint frequency and write times                   |
| **Connections & Sessions** | Connection/session counts and concurrency              |
| **Clients**                | Top users, apps, databases, hosts                      |
| **Server**                 | Starts, shutdowns, replication                         |

## Analyze recent logs only

Use `--last` to restrict analysis to a rolling time window:

```bash
quellog /var/log/postgresql/*.log --last 1h
```

This keeps only entries from the last hour relative to the current system
time (now) — so `--last` is meant for recent/live logs. On an older archive
it may filter out everything; use `--begin`/`--end` to target a past window.

## Generate an HTML report

Add `--html` and `-o` to produce a standalone, interactive report you
can open in any browser:

```bash
quellog /var/log/postgresql/*.log --html -o report.html
```

The HTML report includes interactive charts and filterable tables.

## Show everything

The default output already covers the main sections. To include the
full SQL performance breakdown and all available detail, add `--full`:

```bash
quellog /var/log/postgresql/*.log --full
```

## Filter by database or user

Narrow the analysis to a specific database, user, or application:

```bash
quellog /var/log/postgresql/*.log -d mydb -u myuser
```

