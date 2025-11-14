# Filtering Logs

quellog provides powerful filtering capabilities to analyze specific subsets of your PostgreSQL logs. This page covers all available filtering options for selecting which log entries to analyze.

## Default Behavior

If no filters are specified, quellog analyzes **all log entries** in the provided files:

```bash
# Analyzes everything
quellog /var/log/postgresql/*.log
```

Use the filtering options below to focus on specific subsets.

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

!!! info "Coming Soon"
    The `--window` flag for specifying relative time ranges is planned for a future release. Currently, use `--begin` and `--end` together to specify time ranges.

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

# Exclude monitoring databases
quellog /var/log/postgresql/*.log \
  --dbname production \
  --dbname staging
```

**Use cases**:

- Production vs. staging comparison
- Per-tenant analysis in multi-tenant setups
- Isolating test database activity
- Excluding monitoring tool databases (powa, postgres, template1)

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

# Focus on application users only (exclude monitoring)
quellog /var/log/postgresql/*.log \
  --dbuser myapp \
  --dbuser api_backend
```

**Use cases**:

- Application-specific analysis
- Security auditing
- User activity tracking
- Excluding monitoring tool users (powa, temboard, health_check)

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

# Exclude monitoring tools (powa, temboard)
quellog /var/log/postgresql/*.log \
  --exclude-user powa \
  --exclude-user temboard
```

**Use cases**:

- Filtering out monitoring/health check queries
- Excluding administrative users
- Removing replication connections from analysis
- Excluding monitoring tool activity (powa, temboard_agent)

## Combining Filters

All filters can be combined for precise analysis:

```bash
# Production database, specific user, during business hours
quellog /var/log/postgresql/*.log \
  --dbname production \
  --dbuser app_user \
  --begin "2025-01-13 09:00:00" \
  --end "2025-01-13 17:00:00"

# Exclude monitoring, specific time window
quellog /var/log/postgresql/*.log \
  --exclude-user health_check \
  --begin "2025-01-13 22:00:00" \
  --end "2025-01-14 00:00:00"

# Multiple databases, specific app, yesterday
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --dbname analytics_db \
  --appname web_server \
  --begin "2025-01-12 00:00:00" \
  --end "2025-01-12 23:59:59"
```

### Filter Logic

When combining multiple filters:

- Multiple values of the **same type** use OR logic (e.g., `--dbname db1 --dbname db2` matches db1 OR db2)
- **Different types** use AND logic (e.g., `--dbname production --dbuser app_user` matches production AND app_user)

Example: `--dbname db1 --dbname db2 --dbuser user1` matches entries where database is (db1 OR db2) AND user is user1.

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
  --exclude-user powa \
  --exclude-user temboard \
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

## Next Steps

- [Learn about section filtering](filtering-output.md) to display only specific report sections
- [Understand the default report](default-report.md) to interpret filtered results
- [Analyze SQL performance](sql-reports.md) of filtered queries
