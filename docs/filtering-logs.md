# Filtering Logs

quellog provides filtering to analyze specific subsets of PostgreSQL logs.

## Default Behavior

Without filters, quellog analyzes all log entries:

```bash
quellog /var/log/postgresql/*.log
```

## Time-Based Filtering

### --begin

Analyze entries after a specific datetime.

```bash
quellog /var/log/postgresql/*.log --begin "2025-01-13 14:30:00"
```

Format: `YYYY-MM-DD HH:MM:SS`

### --end

Analyze entries before a specific datetime.

```bash
quellog /var/log/postgresql/*.log --end "2025-01-13 15:00:00"
```

Format: `YYYY-MM-DD HH:MM:SS`

### Time Window

Combine `--begin` and `--end` for a specific time range:

```bash
# 1-hour window
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00"
```

Use the same timezone as your PostgreSQL logs.

## Attribute-Based Filtering

### --dbname (-d)

Filter by database name. Can be specified multiple times.

```bash
# Single database
quellog /var/log/postgresql/*.log --dbname production

# Multiple databases
quellog /var/log/postgresql/*.log --dbname app_db --dbname analytics_db
```

### --dbuser (-u)

Filter by database user. Can be specified multiple times.

```bash
# Single user
quellog /var/log/postgresql/*.log --dbuser app_user

# Multiple users
quellog /var/log/postgresql/*.log --dbuser app_user --dbuser batch_processor
```

### --appname (-N)

Filter by application name. Can be specified multiple times.

```bash
# Single application
quellog /var/log/postgresql/*.log --appname web_server

# Multiple applications
quellog /var/log/postgresql/*.log --appname api_server --appname background_worker
```

### --exclude-user (-U)

Exclude specific users from analysis. Can be specified multiple times.

```bash
# Exclude monitoring users
quellog /var/log/postgresql/*.log --exclude-user health_check --exclude-user powa
```

## Combining Filters

All filters can be combined:

```bash
# Production database, specific user, during business hours
quellog /var/log/postgresql/*.log \
  --dbname production \
  --dbuser app_user \
  --begin "2025-01-13 09:00:00" \
  --end "2025-01-13 17:00:00"

# Multiple databases, exclude monitoring, specific time window
quellog /var/log/postgresql/*.log \
  --dbname app_db \
  --dbname analytics_db \
  --exclude-user powa \
  --exclude-user temboard \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-13 23:59:59"
```

### Filter Logic

- Multiple values of the **same type** use OR logic
  - `--dbname db1 --dbname db2` matches db1 OR db2
- **Different types** use AND logic
  - `--dbname production --dbuser app_user` matches production AND app_user

Example: `--dbname db1 --dbname db2 --dbuser user1` matches entries where database is (db1 OR db2) AND user is user1.

## Next Steps

- [Filter report sections](filtering-output.md) to display only specific sections
- [Understand the default report](default-report.md) output
- [Analyze SQL performance](sql-reports.md) with filters
