# JSON Export

quellog can export analysis results as JSON for integration with other tools, automation pipelines, and custom reporting.

## Basic Usage

Add the `--json` (or `-J`) flag to export results as JSON:

```bash
# Export to stdout
quellog /var/log/postgresql/*.log --json

# Save to file
quellog /var/log/postgresql/*.log --json > report.json

# Using shorthand
quellog /var/log/postgresql/*.log -J > report.json
```

## JSON Structure

The exported JSON contains all analysis metrics in a structured format:

```json
{
  "global": {
    "count": 1234,
    "min_timestamp": "2025-01-13T00:00:00Z",
    "max_timestamp": "2025-01-13T23:59:59Z",
    "error_count": 2,
    "fatal_count": 0,
    "panic_count": 0,
    "warning_count": 3,
    "log_count": 1229
  },
  "sql": {
    "total_queries": 456,
    "unique_queries": 127,
    "min_query_duration": 12.0,
    "max_query_duration": 2340.0,
    "sum_query_duration": 526000.0,
    "median_query_duration": 145.0,
    "p99_query_duration": 1870.0,
    "start_timestamp": "2025-01-13T00:00:00Z",
    "end_timestamp": "2025-01-13T23:59:59Z",
    "query_stats": {
      "se-a1b2c3d": {
        "id": "se-a1b2c3d",
        "full_hash": "a1b2c3d4e5f6...",
        "raw_query": "SELECT * FROM users WHERE id = 1",
        "normalized_query": "SELECT * FROM users WHERE id = $1",
        "count": 23,
        "total_time": 45670.0,
        "avg_time": 1985.65,
        "max_time": 3450.0
      },
      ...
    },
    "executions": [
      {
        "timestamp": "2025-01-13T08:15:23Z",
        "duration": 1450.0
      },
      ...
    ]
  },
  "tempfiles": {
    "count": 19,
    "total_size": 1586495488,
    "events": [
      {
        "timestamp": "2025-01-13T00:15:32Z",
        "size": 104857600.0
      },
      ...
    ],
    "query_stats": {
      "se-a1b2c3d": {
        "id": "se-a1b2c3d",
        "raw_query": "SELECT * FROM users ORDER BY created_at",
        "normalized_query": "SELECT * FROM users ORDER BY created_at",
        "count": 8,
        "total_size": 829423616
      },
      ...
    }
  },
  "locks": {
    "total_events": 23,
    "waiting_events": 15,
    "acquired_events": 8,
    "deadlock_events": 0,
    "total_wait_time": 34500.0,
    "lock_type_stats": {
      "AccessShareLock": 12,
      "RowExclusiveLock": 8,
      "ExclusiveLock": 3
    },
    "resource_type_stats": {
      "relation": 18,
      "transaction": 5
    },
    "events": [
      {
        "timestamp": "2025-01-13T08:30:11Z",
        "event_type": "waiting",
        "lock_type": "AccessShareLock",
        "resource_type": "relation",
        "wait_time": 1000.072,
        "process_id": "12345"
      },
      ...
    ]
  },
  "vacuum": {
    "vacuum_count": 12,
    "analyze_count": 8,
    "vacuum_table_counts": {
      "app_db.public.orders": 4,
      "app_db.public.sessions": 3,
      ...
    },
    "analyze_table_counts": {
      "app_db.public.orders": 3,
      ...
    },
    "vacuum_space_recovered": {
      "app_db.public.orders": 2453504,
      ...
    }
  },
  "checkpoints": {
    "complete_count": 19,
    "total_write_time_seconds": 60.8,
    "max_write_time_seconds": 7.8,
    "events": [
      "2025-01-13T00:12:34Z",
      "2025-01-13T00:17:56Z",
      ...
    ],
    "type_counts": {
      "time": 14,
      "wal": 5
    }
  },
  "connections": {
    "connection_received_count": 36,
    "disconnection_count": 23,
    "total_session_time": 89227000000000,
    "connections": [
      "2025-01-13T00:00:12Z",
      "2025-01-13T00:05:23Z",
      ...
    ]
  },
  "unique_entities": {
    "unique_dbs": 3,
    "unique_users": 6,
    "unique_apps": 9,
    "unique_hosts": 12,
    "dbs": ["analytics_db", "app_db", "postgres"],
    "users": ["admin", "analytics", "app_user", ...],
    "apps": ["app_server", "batch_job", ...],
    "hosts": ["192.168.1.100", "192.168.1.101", ...]
  },
  "event_summaries": [
    {"severity": "LOG", "count": 1229},
    {"severity": "WARNING", "count": 3},
    {"severity": "ERROR", "count": 2}
  ],
  "error_classes": [
    {"code": "53100", "class": "disk_full", "count": 1}
  ]
}
```

## Field Reference

### global

General log statistics.

| Field | Type | Description |
|-------|------|-------------|
| `count` | int | Total log entries processed |
| `min_timestamp` | string (ISO 8601) | Earliest log entry timestamp |
| `max_timestamp` | string (ISO 8601) | Latest log entry timestamp |
| `error_count` | int | Number of ERROR-level entries |
| `fatal_count` | int | Number of FATAL-level entries |
| `panic_count` | int | Number of PANIC-level entries |
| `warning_count` | int | Number of WARNING-level entries |
| `log_count` | int | Number of LOG-level entries |

### sql

SQL query statistics.

| Field | Type | Description |
|-------|------|-------------|
| `total_queries` | int | Total number of queries executed |
| `unique_queries` | int | Number of distinct normalized queries |
| `min_query_duration` | float | Fastest query duration (ms) |
| `max_query_duration` | float | Slowest query duration (ms) |
| `sum_query_duration` | float | Total execution time (ms) |
| `median_query_duration` | float | 50th percentile duration (ms) |
| `p99_query_duration` | float | 99th percentile duration (ms) |
| `query_stats` | object | Per-query statistics (keyed by full hash) |
| `executions` | array | All query executions with timestamps |

### tempfiles

Temporary file statistics.

| Field | Type | Description |
|-------|------|-------------|
| `count` | int | Number of tempfile creation events |
| `total_size` | int | Cumulative tempfile size (bytes) |
| `events` | array | Tempfile events with timestamps and sizes |
| `query_stats` | object | Per-query tempfile statistics |

### locks

Lock contention statistics.

| Field | Type | Description |
|-------|------|-------------|
| `total_events` | int | Total lock-related events |
| `waiting_events` | int | "still waiting" events |
| `acquired_events` | int | "acquired" events |
| `deadlock_events` | int | Deadlock detection events |
| `total_wait_time` | float | Cumulative lock wait time (ms) |
| `lock_type_stats` | object | Count by lock type |
| `resource_type_stats` | object | Count by resource type |
| `events` | array | Individual lock events |

### vacuum, checkpoints, connections, unique_entities, event_summaries, error_classes

See JSON structure above for field details.

## Processing JSON with jq

[jq](https://stedolan.github.io/jq/) is a powerful command-line JSON processor.

### Extract Specific Fields

```bash
# Get total query count
quellog /var/log/postgresql/*.log --json | jq '.sql.total_queries'

# Get unique databases
quellog /var/log/postgresql/*.log --json | jq '.unique_entities.dbs'

# Get checkpoint count
quellog /var/log/postgresql/*.log --json | jq '.checkpoints.complete_count'
```

### Top Queries

```bash
# Top 5 queries by total time
quellog /var/log/postgresql/*.log --json | \
  jq '.sql.query_stats | to_entries | sort_by(-.value.total_time) | .[0:5] | .[] | {id: .value.id, total_time: .value.total_time, query: .value.normalized_query}'

# Top 5 queries by execution count
quellog /var/log/postgresql/*.log --json | \
  jq '.sql.query_stats | to_entries | sort_by(-.value.count) | .[0:5] | .[] | {id: .value.id, count: .value.count, query: .value.normalized_query}'

# Queries with avg time > 1 second
quellog /var/log/postgresql/*.log --json | \
  jq '.sql.query_stats | to_entries | map(select(.value.avg_time > 1000)) | .[] | {id: .value.id, avg_time: .value.avg_time, query: .value.normalized_query}'
```

### Filtering Events

```bash
# Errors only
quellog /var/log/postgresql/*.log --json | jq '.event_summaries | map(select(.severity == "ERROR"))'

# Tempfiles > 100 MB
quellog /var/log/postgresql/*.log --json | jq '.tempfiles.events | map(select(.size > 104857600))'

# Lock waits > 5 seconds
quellog /var/log/postgresql/*.log --json | jq '.locks.events | map(select(.wait_time > 5000))'
```

### Custom Reports

```bash
# Generate CSV of top 10 slowest queries
quellog /var/log/postgresql/*.log --json | \
  jq -r '.sql.query_stats | to_entries | sort_by(-.value.max_time) | .[0:10] | .[] | [.value.id, .value.max_time, .value.count, .value.normalized_query] | @csv' > top_slow_queries.csv

# Extract summary statistics
quellog /var/log/postgresql/*.log --json | \
  jq '{
    total_entries: .global.count,
    total_queries: .sql.total_queries,
    unique_queries: .sql.unique_queries,
    errors: .global.error_count,
    checkpoints: .checkpoints.complete_count,
    tempfile_size_mb: (.tempfiles.total_size / 1048576)
  }'
```

## Integration Examples

### Prometheus Exporter

```bash
#!/bin/bash
# Export metrics to Prometheus format

METRICS=$(quellog /var/log/postgresql/*.log --json)

echo "# HELP pg_log_entries_total Total log entries processed"
echo "# TYPE pg_log_entries_total counter"
echo "pg_log_entries_total $(echo $METRICS | jq '.global.count')"

echo "# HELP pg_query_total Total queries executed"
echo "# TYPE pg_query_total counter"
echo "pg_query_total $(echo $METRICS | jq '.sql.total_queries')"

echo "# HELP pg_error_total Total errors"
echo "# TYPE pg_error_total counter"
echo "pg_error_total $(echo $METRICS | jq '.global.error_count')"
```

### Grafana Dashboard

```bash
# Export to InfluxDB line protocol
quellog /var/log/postgresql/*.log --json | \
  jq -r '. as $data | "postgresql,host=myserver queries=\($data.sql.total_queries),errors=\($data.global.error_count),tempfiles=\($data.tempfiles.count)"'
```

### Slack Alert

```bash
#!/bin/bash
# Alert if errors exceed threshold

ERRORS=$(quellog /var/log/postgresql/*.log --window 1h --json | jq '.global.error_count')

if [ $ERRORS -gt 10 ]; then
  curl -X POST -H 'Content-type: application/json' \
    --data "{\"text\":\"PostgreSQL: $ERRORS errors in last hour\"}" \
    $SLACK_WEBHOOK_URL
fi
```

### Daily Report Automation

```bash
#!/bin/bash
# Generate daily JSON reports

DATE=$(date -d "yesterday" +%Y-%m-%d)
LOG_DIR="/var/log/postgresql"
REPORT_DIR="/reports"

# Full analysis
quellog $LOG_DIR/*.log \
  --begin "$DATE 00:00:00" \
  --end "$DATE 23:59:59" \
  --json > "$REPORT_DIR/daily_$DATE.json"

# Extract key metrics
jq '{
  date: "'$DATE'",
  total_queries: .sql.total_queries,
  errors: .global.error_count,
  slow_queries: [.sql.query_stats | to_entries | sort_by(-.value.avg_time) | .[0:5] | .[] | .value.id],
  tempfile_size_gb: (.tempfiles.total_size / 1073741824 | round)
}' "$REPORT_DIR/daily_$DATE.json" > "$REPORT_DIR/summary_$DATE.json"

# Upload to S3
aws s3 cp "$REPORT_DIR/daily_$DATE.json" s3://my-bucket/postgresql-reports/
```

## Combining with Filters

Section flags don't affect JSON output - full analysis is always included:

```bash
# Section flags have no effect on JSON content
quellog /var/log/postgresql/*.log --sql-performance --json > full_report.json

# Use jq to filter JSON instead
quellog /var/log/postgresql/*.log --json | jq '{sql: .sql}' > sql_only.json
```

However, log filters do affect results:

```bash
# Only production database
quellog /var/log/postgresql/*.log --dbname production --json > prod_report.json

# Last 24 hours
quellog /var/log/postgresql/*.log --window 24h --json > last_24h.json
```

## JSON Schema

!!! info "Coming Soon"
    A formal JSON schema will be provided in a future release for validation and code generation.

## Next Steps

- [Markdown Export](markdown-export.md) for documentation-friendly output
- [SQL Analysis](sql-reports.md) for detailed query investigation
- [Filtering](filtering-logs.md) to focus JSON export on specific subsets
