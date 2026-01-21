# Filtering Output

By default, quellog displays a comprehensive report with all analysis sections. You can filter the output to display only specific sections, making it easier to focus on particular aspects of your database activity.

## Comprehensive Report

Use `--full` to display all sections with detailed SQL analysis:

```bash
quellog /var/log/postgresql/*.log --full
```

This includes every analysis section plus extended SQL performance details. Useful for thorough investigations or when generating complete HTML reports with `--html --full`.

## Available Sections

### --summary

Display only the summary section with overall statistics.

```bash
quellog /var/log/postgresql/*.log --summary
```

**Output includes**:

- Date range (start/end timestamps)
- Duration covered
- Total entries processed
- Throughput (entries/second)

**Use when**:

- Quickly checking log coverage
- Verifying time ranges
- Getting entry counts

### --events

Display only the events section showing log severity distribution.

```bash
quellog /var/log/postgresql/*.log --events
```

**Output includes**:

- Count by severity (LOG, WARNING, ERROR, FATAL, PANIC)

**Use when**:

- Investigating errors and warnings
- Health checking
- Tracking error trends

### --errors

Display only the error classes section showing SQLSTATE error distribution.

```bash
quellog /var/log/postgresql/*.log --errors
```

**Output includes**:

- Error classes by SQLSTATE code (42, 23, 22, etc.)
- Description for each error class
- Count per error class

**Use when**:

- Analyzing error patterns
- Identifying specific error types (syntax errors, constraint violations, etc.)
- Troubleshooting application errors

### --sql-performance

Display only the SQL performance section.

```bash
quellog /var/log/postgresql/*.log --sql-performance
```

**Output includes**:

- Query load distribution histogram
- Total query duration
- Query count (total and unique)
- Duration statistics (min, max, median, p99)

**Use when**:

- Quick SQL performance overview
- Checking query load distribution
- Verifying query logging is working

!!! note "Related SQL flags"
    - `--sql-performance`: Detailed per-query statistics with top queries, temp files, and locks
    - `--sql-overview`: Query type breakdown by database/user/host/application
    - Use `--sql-summary` flag to show only the SQL summary section in the default report

### --tempfiles

Display only the temporary files section.

```bash
quellog /var/log/postgresql/*.log --tempfiles
```

**Output includes**:

- Temp file distribution histogram
- Total tempfile count
- Cumulative tempfile size
- Average tempfile size
- Top queries by tempfile size (if queries associated)

**Use when**:

- Investigating `work_mem` issues
- Finding memory-hungry queries
- Tuning sort/hash operations

### --locks

Display only the locks section.

```bash
quellog /var/log/postgresql/*.log --locks
```

**Output includes**:

- Total lock events (waiting, acquired, deadlocks)
- Total wait time
- Lock type distribution
- Resource type distribution
- Top queries by lock wait time (if queries associated)

**Use when**:

- Investigating blocking queries
- Analyzing lock contention
- Deadlock troubleshooting

### --maintenance

Display only the maintenance section.

```bash
quellog /var/log/postgresql/*.log --maintenance
```

**Output includes**:

- Autovacuum count
- Autoanalyze count
- Top tables by vacuum frequency
- Top tables by analyze frequency
- Space recovered by vacuum

**Use when**:

- Monitoring vacuum effectiveness
- Identifying frequently vacuumed tables
- Planning manual VACUUM operations

### --checkpoints

Display only the checkpoints section.

```bash
quellog /var/log/postgresql/*.log --checkpoints
```

**Output includes**:

- Checkpoint distribution histogram
- Total checkpoint count
- Average/max write times
- Checkpoint types (time, WAL, shutdown, etc.)

**Use when**:

- Analyzing checkpoint frequency
- Investigating I/O spikes
- Tuning checkpoint parameters

### --connections

Display detailed connection and session analytics.

```bash
quellog /var/log/postgresql/*.log --connections
```

**Output includes**:

- Connection distribution histogram
- Total connection count and rate per hour
- Disconnection count
- Average and peak concurrent sessions
- **Session duration statistics** (min, max, avg, median, cumulated)
- **Session duration distribution** histogram (6 time buckets)
- **Top 10 sessions by user** (with full statistics)
- **Top 10 sessions by database** (with full statistics)
- **Top 10 sessions by host** (with full statistics)

**Use when**:

- Monitoring connection patterns
- Planning connection pooling
- Investigating connection churn
- Analyzing session duration patterns by user/database/host
- Identifying long-running sessions
- Understanding connection lifecycle

**Example output excerpt**:

```
SESSION DURATION BY USER

  User                       Sessions      Min      Max      Avg   Median  Cumulated
  ---------------------------------------------------------------------------------------
  app_user                         10   31m6s  2h20m16s  1h26m59s  1h26m48s   14h29m46s
  readonly                          5   7m10s   1h3m26s   41m38s    47m30s    3h28m11s
  batch_user                        3  1h21m46s  2h0m30s  1h42m45s   1h46m0s    5h8m16s
```

!!! tip "Detailed Analytics"
    The `--connections` flag provides much more detail than the connections section in the default report, including session breakdowns by entity and duration distributions.

### --clients

Display only the clients section showing **all** unique entities with counts and percentages.

```bash
quellog /var/log/postgresql/*.log --clients
```

**Output includes**:

- Unique database count and complete list with activity counts
- Unique user count and complete list with activity counts
- Unique application count and complete list with activity counts
- Unique host count and complete list with activity counts (if available)

**Difference from default report**:

- Default report shows **TOP 10** most active entities per category
- `--clients` flag shows **ALL** entities (no limit)

**Example output** (complete --clients output showing ALL entities):

```
CLIENTS

  Unique DBs                : 3
  Unique Users              : 15
  Unique Apps               : 12
  Unique Hosts              : 37

USERS

  app_user                   1250   42.5%
  readonly                    856   29.1%
  batch_user                  423   14.4%
  admin                       198    6.7%
  analytics                   145    4.9%
  backup_user                  52    1.8%
  postgres                     16    0.5%
  monitoring                   12    0.4%
  replication                   8    0.3%
  test_user                     5    0.2%
  dev_user_1                    3    0.1%
  dev_user_2                    2    0.1%
  dev_user_3                    1    0.0%
  dev_user_4                    1    0.0%
  qa_user                       1    0.0%

APPS

  app_server                 1342   45.6%
  psql                        687   23.4%
  metabase                    456   15.5%
  pgadmin                     234    8.0%
  batch_job                   145    4.9%
  pg_dump                      52    1.8%
  pg_restore                   12    0.4%
  python_script                 8    0.3%
  monitoring_tool               5    0.2%
  tableau                       3    0.1%
  dbeaver                       2    0.1%
  datagrip                      1    0.0%

DATABASES

  app_db                     2456   83.5%
  postgres                    342   11.6%
  analytics_db                142    4.8%

HOSTS

  192.168.1.100               876   29.8%
  10.0.1.50                   654   22.2%
  172.16.0.10                 543   18.5%
  10.0.1.51                   432   14.7%
  172.16.0.12                 234    8.0%
  192.168.1.101               123    4.2%
  10.0.1.52                    56    1.9%
  172.16.0.15                  22    0.7%
  10.0.1.53                    18    0.6%
  172.16.0.20                  12    0.4%
  [... 27 more hosts listed with counts ...]

USER × DATABASE

  app_user                  × app_db                      1856   63.1%
  readonly                  × app_db                       543   18.5%
  app_user                  × analytics_db                 123    4.2%
  batch_user                × app_db                        98    3.3%
  readonly                  × analytics_db                  87    3.0%
  admin                     × postgres                      65    2.2%
  batch_user                × analytics_db                  45    1.5%
  analytics                 × analytics_db                  34    1.2%
  admin                     × app_db                        23    0.8%
  backup_user               × postgres                      12    0.4%
  monitoring                × postgres                       8    0.3%
  replication               × app_db                         5    0.2%
  postgres                  × postgres                       4    0.1%
  test_user                 × app_db                         3    0.1%
  [... all 18 combinations listed ...]

USER × HOST

  app_user                  × 192.168.1.100                 654   22.2%
  readonly                  × 10.0.1.50                     432   14.7%
  app_user                  × 172.16.0.10                   345   11.7%
  batch_user                × 10.0.1.51                     234    8.0%
  app_user                  × 10.0.1.50                     187    6.4%
  readonly                  × 192.168.1.100                 156    5.3%
  admin                     × 172.16.0.12                   123    4.2%
  analytics                 × 10.0.1.52                      98    3.3%
  batch_user                × 192.168.1.101                  76    2.6%
  readonly                  × 172.16.0.10                    65    2.2%
  [... all 52 combinations listed ...]
```

**Use when**:

- Auditing database access (need complete list)
- Understanding client diversity
- Security reviews
- Generating compliance reports

!!! tip "Complete Entity Lists"
    The `--clients` flag displays **all** entities without the 10-item limit applied in the default report. This is useful for comprehensive audits and compliance reporting.

## Combining Sections

You can combine multiple section flags to display several sections:

```bash
# SQL performance and tempfiles
quellog /var/log/postgresql/*.log --sql-performance --tempfiles

# Errors and locks
quellog /var/log/postgresql/*.log --events --locks

# Maintenance and checkpoints
quellog /var/log/postgresql/*.log --maintenance --checkpoints

# Summary, connections, and clients
quellog /var/log/postgresql/*.log --summary --connections --clients
```

## Default Behavior (No Section Flags)

If no section flags are specified, quellog displays **all sections**:

```bash
# Shows everything
quellog /var/log/postgresql/*.log
```

Output order:

1. Summary
2. SQL Performance
3. Events
4. Error Classes (if any)
5. Temporary Files (if any)
6. Locks (if any)
7. Maintenance
8. Checkpoints
9. Connections
10. Clients

## Combining with Other Filters

Section filtering works seamlessly with log filtering:

```bash
# Production database, specific time range, only SQL performance
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --sql-performance

# Specific user, yesterday, only locks and tempfiles
quellog /var/log/postgresql/*.log \
  --dbuser app_user \
  --begin "2025-01-12 00:00:00" \
  --end "2025-01-12 23:59:59" \
  --locks \
  --tempfiles

# Exclude monitoring, specific hour, summary only
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --summary
```

## Practical Examples

### Quick Error Check

```bash
# Just show errors and warnings
quellog /var/log/postgresql/*.log --events
```

Output:

```
EVENTS

  LOG     : 1,234
  WARNING : 5
  ERROR   : 2
```

### Memory Pressure Investigation

```bash
# Show tempfiles and related SQL performance
quellog /var/log/postgresql/*.log --tempfiles --sql-performance
```

This combination helps identify which queries are exceeding `work_mem` and their performance impact.

### Lock Contention Analysis

```bash
# Locks + SQL performance + events
quellog /var/log/postgresql/*.log --locks --sql-performance --events
```

Shows lock contention alongside query performance and any errors that occurred.

### Daily Report Script

```bash
#!/bin/bash
# daily_report.sh - Generate focused daily reports

YESTERDAY=$(date -d "yesterday" +%Y-%m-%d)
LOG_DIR="/var/log/postgresql"

# Summary report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --summary --events > "reports/summary_$YESTERDAY.txt"

# Performance report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --sql-performance --tempfiles --locks > "reports/performance_$YESTERDAY.txt"

# Maintenance report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --maintenance --checkpoints > "reports/maintenance_$YESTERDAY.txt"
```

### Connection Monitoring

```bash
# Monitor connection patterns for specific hour
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --connections \
  --clients
```

Useful for:

- Detecting connection leaks
- Monitoring connection pool effectiveness
- Tracking client access patterns

### Incident Triage

When investigating an incident, start with a high-level overview then drill down:

```bash
# Step 1: High-level overview
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --summary --events

# Step 2: If errors found, check SQL and locks
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-performance --locks --tempfiles

# Step 3: Deep dive into specific queries
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-detail se-a1b2c3
```

## Section Availability

Some sections may be empty or omitted if no relevant data exists:

| Section | Requires |
|---------|----------|
| Summary | (always available) |
| SQL Performance | `log_min_duration_statement >= 0` |
| Events | (always available) |
| Error Classes | Errors with SQLSTATE codes occurred |
| Temporary Files | `log_temp_files >= 0` and tempfiles created |
| Locks | `log_lock_waits = on` and locks occurred |
| Maintenance | `log_autovacuum_min_duration >= 0` and autovacuum ran |
| Checkpoints | `log_checkpoints = on` and checkpoints occurred |
| Connections | `log_connections = on` and connections occurred |
| Clients | Depends on `log_line_prefix` including user/db/app info |

!!! tip "Empty Sections"
    If a requested section is empty (e.g., no locks occurred), quellog will still display the section header with "No data" or similar message.

## Output Redirection

Redirect specific sections to files for later analysis:

```bash
# Save summary to file
quellog /var/log/postgresql/*.log --summary > summary.txt

# Save performance metrics to file
quellog /var/log/postgresql/*.log --sql-performance --tempfiles > perf.txt

# Append to daily log
quellog /var/log/postgresql/*.log --events >> errors.log
```

## Combining with Export Formats

Section filtering works with JSON and Markdown export:

```bash
# Export only SQL performance as JSON
quellog /var/log/postgresql/*.log --sql-performance --json > perf.json

# Export maintenance section as Markdown
quellog /var/log/postgresql/*.log --maintenance --md > maintenance.md

# Multiple sections in JSON
quellog /var/log/postgresql/*.log \
  --summary --events --connections --json > overview.json
```

!!! note "JSON Export Completeness"
    When using `--json`, the JSON output contains all analyzed data, regardless of section flags. Section flags only affect the text output format. Use `jq` to filter JSON if needed.

## Continuous Monitoring (--follow)

Monitor log files in real-time with periodic refresh. By default, analyzes the last 24 hours of logs and outputs to the terminal:

```bash
# Refresh every 30 seconds (default), last 24 hours, output to terminal
quellog --follow /var/log/postgresql/*.log

# Custom interval
quellog --follow --interval 1m /var/log/postgresql/*.log

# Change the time window with --last
quellog --follow --last 1h /var/log/postgresql/*.log

# Write to file instead of terminal (for external tools like Grafana)
quellog --follow --json --output /tmp/quellog.json /var/log/postgresql/*.log
```

Press `Ctrl+C` to stop.

## Next Steps

- [Understand the default report](default-report.md) to interpret each section
- [Deep dive into SQL analysis](sql-reports.md) with `--sql-performance`, `--sql-overview`, and `--sql-detail`
- [Export results](json-export.md) for further processing
