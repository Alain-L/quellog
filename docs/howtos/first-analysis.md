# Your First Analysis

Run quellog against your PostgreSQL log files to get an instant overview
of database activity, errors, and query performance.

## Basic run

Point quellog at one or more log files:

```bash
quellog /var/log/postgresql/*.log
```

quellog auto-detects the log format (stderr, CSV, or JSON) and prints a
report with these sections:

| Section        | What it tells you                                      |
|----------------|--------------------------------------------------------|
| **Summary**    | Time range, entry count, throughput                    |
| **SQL Summary**| Query load histogram, duration stats, percentiles      |
| **Events**     | Errors and fatals grouped by SQL error class           |
| **Temp Files** | Queries spilling to disk (work_mem pressure)           |
| **Locks**      | Lock waits and deadlocks                               |
| **Maintenance**| VACUUM and ANALYZE activity                            |

## Analyze recent logs only

Use `--last` to restrict analysis to a rolling time window:

```bash
quellog /var/log/postgresql/*.log --last 1h
```

This keeps only entries from the last hour relative to the newest
timestamp in the files.

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

