# Filtering Logs

quellog provides powerful filtering capabilities to analyze specific subsets of your PostgreSQL logs. This page covers all available filtering options for selecting which log entries to analyze.

## Time-Based Filtering

Filter logs by timestamp to focus on specific time periods.

### --begin: Start Time

Analyze entries occurring after a specific datetime.

```bash
# All entries from January 13, 2025 onwards
quellog /var/log/postgresql/*.log --begin "2025-01-13 00:00:00"

# All entries from 2:30 PM onwards
quellog /var/log/postgresql/*.log --begin "2025-01-13 14:30:00"
```

**Format**: `YYYY-MM-DD HH:MM:SS`

### --end: End Time

Analyze entries occurring before a specific datetime.

```bash
# All entries before January 14, 2025
quellog /var/log/postgresql/*.log --end "2025-01-14 00:00:00"

# All entries before 3:00 PM
quellog /var/log/postgresql/*.log --end "2025-01-13 15:00:00"
```

**Format**: `YYYY-MM-DD HH:MM:SS`

### Combining Begin and End

Analyze a specific time window:

```bash
# 1-hour window on January 13
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00"

# Entire day
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-13 23:59:59"

# Business hours (9 AM to 5 PM)
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 09:00:00" \
  --end "2025-01-13 17:00:00"
```

### --window: Time Window Duration

Specify a duration to automatically calculate begin or end time.

```bash
# Last 30 minutes (if --end is now)
quellog /var/log/postgresql/*.log --window 30m

# Last 2 hours
quellog /var/log/postgresql/*.log --window 2h

# Last 24 hours
quellog /var/log/postgresql/*.log --window 24h
```

**Supported units**:

- `m` - minutes (e.g., `30m`)
- `h` - hours (e.g., `2h`)
- `d` - days (e.g., `7d`)

**Window behavior**:

- If only `--window` is specified: analyzes the most recent entries
- If `--begin` + `--window`: end time = begin + window
- If `--end` + `--window`: begin time = end - window

**Examples**:

```bash
# 2-hour window starting at 2 PM
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --window 2h
# Result: 2025-01-13 14:00:00 to 2025-01-13 16:00:00

# 30-minute window ending at 3 PM
quellog /var/log/postgresql/*.log \
  --end "2025-01-13 15:00:00" \
  --window 30m
# Result: 2025-01-13 14:30:00 to 2025-01-13 15:00:00
```

### Time Filtering Tips

!!! tip "Timezone Handling"
    Use the same timezone as your PostgreSQL logs. If your logs use UTC, use UTC timestamps in filters.

!!! tip "Incident Investigation"
    When investigating an incident at a specific time, use a window before and after:

    ```bash
    # Incident at 14:30, analyze 14:00-15:00
    quellog /var/log/postgresql/*.log \
      --begin "2025-01-13 14:00:00" \
      --end "2025-01-13 15:00:00"
    ```

!!! warning "Large Time Ranges"
    Analyzing months of logs can be slow. Consider using additional filters (database, user) to reduce the dataset.

## Attribute-Based Filtering

Filter by database entities to focus on specific workloads.

### --dbname: Database Name

Analyze logs for specific database(s).

```bash
# Single database
quellog /var/log/postgresql/*.log --dbname production

# Multiple databases
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --dbname analytics_db

# Using shorthand -d
quellog /var/log/postgresql/*.log -d mydb
```

**Use cases**:

- Production vs. staging comparison
- Per-tenant analysis in multi-tenant setups
- Isolating test database activity

### --dbuser: Database User

Analyze logs for specific user(s).

```bash
# Single user
quellog /var/log/postgresql/*.log --dbuser app_user

# Multiple users
quellog /var/log/postgresql/*.log \
  --dbuser app_user \
  --dbuser batch_processor

# Using shorthand -u
quellog /var/log/postgresql/*.log -u readonly
```

**Use cases**:

- Application-specific analysis
- Security auditing
- User activity tracking

### --appname: Application Name

Filter by application name (from `application_name` connection parameter).

```bash
# Single application
quellog /var/log/postgresql/*.log --appname web_server

# Multiple applications
quellog /var/log/postgresql/*.log \
  --appname api_server \
  --appname background_worker

# Using shorthand -N
quellog /var/log/postgresql/*.log -N psql
```

**Use cases**:

- Separate web vs. batch workloads
- Per-service analysis in microservices
- Tool-specific activity (e.g., `pg_dump`, `psql`)

### --exclude-user: Exclude Users

Exclude specific users from analysis.

```bash
# Exclude monitoring user
quellog /var/log/postgresql/*.log --exclude-user health_check

# Exclude multiple users
quellog /var/log/postgresql/*.log \
  --exclude-user postgres \
  --exclude-user replication

# Using shorthand -U
quellog /var/log/postgresql/*.log -U readonly
```

**Use cases**:

- Filtering out monitoring/health check queries
- Excluding administrative users
- Removing replication connections from analysis

## Combining Filters

All filters can be combined for precise analysis:

```bash
# Production database, specific user, during business hours
quellog /var/log/postgresql/*.log \
  --dbname production \
  --dbuser app_user \
  --begin "2025-01-13 09:00:00" \
  --end "2025-01-13 17:00:00"

# Exclude monitoring, last 2 hours
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --window 2h

# Multiple databases, specific app, yesterday
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --dbname analytics_db \
  --appname web_server \
  --begin "2025-01-12 00:00:00" \
  --end "2025-01-12 23:59:59"
```

### Filter Logic

- **Within each filter type**: OR logic (e.g., `--dbname app_db --dbname analytics_db` matches entries from either database)
- **Across filter types**: AND logic (e.g., `--dbname app_db --dbuser myuser` matches entries that are both `app_db` AND `myuser`)

**Example**:

```bash
quellog /var/log/postgresql/*.log \
  --dbname db1 \
  --dbname db2 \
  --dbuser user1 \
  --dbuser user2 \
  --appname app1
```

This matches entries where:

- (database = db1 OR database = db2) **AND**
- (user = user1 OR user = user2) **AND**
- (application = app1)

## Practical Examples

### Daily Performance Review

Analyze yesterday's logs for production database:

```bash
#!/bin/bash
YESTERDAY=$(date -d "yesterday" +%Y-%m-%d)

quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --exclude-user health_check \
  --json > "reports/daily_$(date -d yesterday +%Y%m%d).json"
```

### Incident Investigation

Investigate slowdown reported at 2:30 PM:

```bash
# Analyze 1-hour window around incident
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --dbname production \
  --sql-summary
```

### User Activity Audit

Review all activity for a specific user over a week:

```bash
quellog /var/log/postgresql/*.log \
  --dbuser suspicious_user \
  --begin "2025-01-06 00:00:00" \
  --end "2025-01-13 23:59:59" \
  --sql-summary
```

### Application Profiling

Compare two applications accessing the same database:

```bash
# API server
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --appname api_server \
  --sql-summary > api_report.txt

# Background worker
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --appname background_worker \
  --sql-summary > worker_report.txt
```

### Peak Hours Analysis

Analyze database activity during peak hours (12 PM - 2 PM) for a week:

```bash
for day in $(seq 0 6); do
  DATE=$(date -d "$day days ago" +%Y-%m-%d)
  quellog /var/log/postgresql/*.log \
    --begin "$DATE 12:00:00" \
    --end "$DATE 14:00:00" \
    --json > "reports/peak_$DATE.json"
done
```

### Excluding Noise

Remove monitoring and read-only users to focus on application queries:

```bash
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --exclude-user monitoring \
  --exclude-user readonly \
  --sql-summary
```

## Filter Verification

To verify which entries match your filters, use section-specific output:

```bash
# Check which databases, users, and apps are in the filtered dataset
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-13 23:59:59" \
  --clients
```

This shows the unique databases, users, and applications in the filtered results.

## Performance Considerations

### Filter Order Impact

quellog applies filters in this order:

1. **Time filters** (--begin, --end, --window)
2. **Attribute filters** (--dbname, --dbuser, --appname, --exclude-user)

Time filters are applied first because they're most selective and fastest to check.

### Large Datasets

For very large log files, pre-filter at the shell level before piping to quellog:

```bash
# Pre-filter with grep, then analyze
grep "db=production" /huge/log/file.log | quellog - --sql-summary
```

!!! tip "stdin Support"
    quellog accepts `-` as an argument to read from stdin, enabling shell-level pre-processing.

### Filter Coverage

No filters specified = analyze entire log file(s):

```bash
# Analyzes everything
quellog /var/log/postgresql/*.log
```

## Next Steps

- [Learn about section filtering](filtering-output.md) to display only specific report sections
- [Understand the default report](default-report.md) to interpret filtered results
- [Analyze SQL performance](sql-reports.md) of filtered queries
