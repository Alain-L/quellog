# Filtering Output

By default, quellog displays a comprehensive report with all analysis sections. You can filter the output to display only specific sections, making it easier to focus on particular aspects of your database activity.

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

!!! note "Difference from --sql-summary"
    `--sql-performance` shows the overview histogram and aggregate stats from the default report, while `--sql-summary` provides detailed per-query statistics.

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

Display only the connections section.

```bash
quellog /var/log/postgresql/*.log --connections
```

**Output includes**:

- Connection distribution histogram
- Total connection count
- Average connections per hour
- Disconnection count
- Average session duration

**Use when**:

- Monitoring connection patterns
- Planning connection pooling
- Investigating connection churn

### --clients

Display only the clients section showing unique entities.

```bash
quellog /var/log/postgresql/*.log --clients
```

**Output includes**:

- Unique database count and list
- Unique user count and list
- Unique application count and list
- Unique host count and list (if available)

**Use when**:

- Auditing database access
- Understanding client diversity
- Security reviews

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
  --sql-summary
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

## Next Steps

- [Understand the default report](default-report.md) to interpret each section
- [Deep dive into SQL analysis](sql-reports.md) with --sql-summary
- [Export results](json-export.md) for further processing
