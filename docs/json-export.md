# JSON Export

Export analysis results as JSON for automation and integration.

```bash
# Export to stdout
quellog /var/log/postgresql/*.log --json

# Save to file
quellog /var/log/postgresql/*.log --json > report.json

# With filters
quellog /var/log/postgresql/*.log --dbname production --json > prod.json
```

## Design Philosophy

The JSON export provides **quellog's complete analysis data structures** with all metrics pre-calculated, ready for programmatic consumption. This allows you to:

- Consume analysis results without rendering logic
- Build custom visualizations and dashboards
- Integrate with monitoring and alerting systems
- Archive analysis snapshots for historical comparison
- Perform additional filtering or aggregations on individual events

Each section exports:

- **Aggregated metrics**: all counts, totals, averages, and percentiles already calculated
- **Individual events**: timestamped entries for custom analysis and drill-down
- **Query metadata**: normalized queries with their complete statistics (when applicable)

This unlocks full exploitation of quellog's parsing and analysis results without being constrained by synthetic text report formatting.

## JSON Structure

The JSON output contains all analysis sections. Below is the complete structure with field descriptions.

### summary

General log statistics and severity counts.

```json
{
  "summary": {
    "start_date": "2025-01-01 00:00:01",
    "end_date": "2025-01-01 23:59:59",
    "duration": "23h59m58s",
    "total_logs": 186,
    "throughput": "0.00 entries/s",
    "error_count": 2,
    "fatal_count": 0,
    "panic_count": 0,
    "warning_count": 1,
    "log_count": 183
  }
}
```

**Fields:**
- `start_date`, `end_date`: Time range of analyzed logs
- `duration`: Timespan covered
- `total_logs`: Total number of log entries
- `throughput`: Average entries per second
- `error_count`, `fatal_count`, `panic_count`, `warning_count`, `log_count`: Counts by severity level

### events

Log entries grouped by severity level with percentages.

```json
{
  "events": [
    {
      "type": "LOG",
      "count": 183,
      "percentage": 98.38709677419355
    },
    {
      "type": "ERROR",
      "count": 2,
      "percentage": 1.0752688172043012
    },
    {
      "type": "WARNING",
      "count": 1,
      "percentage": 0.5376344086021506
    }
  ]
}
```

**Fields:**
- `type`: Severity level (LOG, ERROR, FATAL, PANIC, WARNING)
- `count`: Number of entries
- `percentage`: Percentage of total logs

### error_classes

PostgreSQL error classification by SQLSTATE code (when available).

```json
{
  "error_classes": [
    {
      "class_code": "42",
      "description": "Syntax Error or Access Rule Violation",
      "count": 4
    },
    {
      "class_code": "23",
      "description": "Integrity Constraint Violation",
      "count": 3
    }
  ]
}
```

**Fields:**
- `class_code`: Two-character SQLSTATE class code
- `description`: Human-readable error class description
- `count`: Number of errors in this class

**Note:** Error classes require SQLSTATE codes in logs. See [PostgreSQL Setup](postgresql-setup.md) for configuration instructions.

### sql_performance

Query statistics and execution details.

```json
{
  "sql_performance": {
    "total_query_duration": "11d 4h 33m",
    "total_queries_parsed": 485,
    "total_unique_queries": 5,
    "top_1_percent_slow_queries": 5,
    "query_max_duration": "1h 12m 50s",
    "query_min_duration": "2m 00s",
    "query_median_duration": "42m 05s",
    "query_99th_percentile": "1h 08m 57s",
    "executions": [
      {
        "timestamp": "2025-01-01 02:11:47",
        "duration": "3m 21s",
        "query_id": "se-R7UmAf"
      }
    ],
    "queries": [
      {
        "id": "se-R7UmAf",
        "normalized_query": "select * from users where id = ?",
        "raw_query": "SELECT * FROM users WHERE id = 42",
        "count": 485,
        "total_time_ms": 97500.5,
        "avg_time_ms": 201.03,
        "max_time_ms": 4350.2
      }
    ]
  }
}
```

**executions**: List of all query executions
- `timestamp`: When the query executed
- `duration`: How long it took
- `query_id`: Links to the corresponding entry in `queries`

**queries**: Normalized query statistics
- `id`: Unique query identifier (prefix `se-` for SELECT, `up-` for UPDATE, etc.)
- `normalized_query`: Query with parameters replaced by `?`
- `raw_query`: Example of the original query (alphabetically first variant)
- `count`: Number of executions
- `total_time_ms`: Total execution time across all executions
- `avg_time_ms`: Average execution time
- `max_time_ms`: Maximum execution time

### temp_files

Temporary file statistics and events.

```json
{
  "temp_files": {
    "total_messages": 19,
    "total_size": "1.48 GB",
    "avg_size": "79.79 MB",
    "events": [
      {
        "timestamp": "2025-01-01 08:09:34",
        "size": "293.12 MB",
        "query_id": "se-dP4pEd"
      }
    ],
    "queries": [
      {
        "id": "se-dP4pEd",
        "normalized_query": "select * from large_table where created_at > ?",
        "raw_query": "SELECT * FROM large_table WHERE created_at > '2025-01-01'",
        "count": 5,
        "total_size": "612.50 MB"
      }
    ]
  }
}
```

**events**: List of temporary file creation events
- `timestamp`: When the tempfile was created
- `size`: Size of the temporary file
- `query_id`: Associated query (if identified)

**queries**: Queries that created temporary files
- `id`: Unique query identifier
- `normalized_query`: Query with parameters replaced by `?`
- `raw_query`: Example of the original query
- `count`: Number of tempfiles created by this query
- `total_size`: Total size across all tempfiles

### locks

Lock wait statistics and events.

```json
{
  "locks": {
    "total_events": 417,
    "waiting_events": 0,
    "acquired_events": 417,
    "avg_wait_time": "2.92 s",
    "total_wait_time": "20m 18s",
    "lock_type_stats": {
      "ShareLock": 417
    },
    "resource_type_stats": {
      "transaction": 417
    },
    "events": [
      {
        "timestamp": "2025-01-01 00:23:56",
        "event_type": "waiting",
        "lock_type": "ShareLock",
        "resource_type": "transaction",
        "wait_time": "1000.09 ms",
        "process_id": "110896",
        "query_id": "up-bG8qBk"
      }
    ],
    "queries": [
      {
        "id": "up-bG8qBk",
        "normalized_query": "update orders set status = ? where id = ?",
        "raw_query": "UPDATE orders SET status = 'shipped' WHERE id = 500",
        "count": 259,
        "avg_wait": "2.88 s",
        "total_wait": "12m 25s"
      }
    ]
  }
}
```

**events**: List of lock wait events
- `timestamp`: When the lock event occurred
- `event_type`: "waiting" or "acquired"
- `lock_type`: PostgreSQL lock type (e.g., ShareLock, ExclusiveLock)
- `resource_type`: What was locked (e.g., relation, transaction)
- `wait_time`: How long the process waited
- `process_id`: PostgreSQL backend PID
- `query_id`: Associated query (if identified)

**queries**: Queries that experienced lock waits
- `id`: Unique query identifier
- `normalized_query`: Query with parameters replaced by `?`
- `raw_query`: Example of the original query
- `count`: Number of lock events for this query
- `avg_wait`: Average wait time
- `total_wait`: Total wait time across all events

### maintenance

Autovacuum and autoanalyze statistics.

```json
{
  "maintenance": {
    "vacuum_count": 668,
    "analyze_count": 353,
    "vacuum_table_counts": {
      "app_db.public.sessions": 422
    },
    "analyze_table_counts": {
      "app_db.public.sessions": 300
    },
    "vacuum_space_recovered": {
      "app_db.public.audit_log": "8.00 KB"
    }
  }
}
```

**Fields:**
- `vacuum_count`: Total autovacuum operations
- `analyze_count`: Total autoanalyze operations
- `vacuum_table_counts`: Vacuum count per table
- `analyze_table_counts`: Analyze count per table
- `vacuum_space_recovered`: Space recovered per table

### checkpoints

Checkpoint statistics and events.

```json
{
  "checkpoints": {
    "total_checkpoints": 13,
    "avg_checkpoint_time": "5.15 s",
    "max_checkpoint_time": "7.00 s",
    "types": {
      "time": {
        "count": 9,
        "percentage": 69.23,
        "rate_per_hour": 0.375,
        "events": [
          "2025-01-01 00:30:05",
          "2025-01-01 01:30:07"
        ]
      },
      "wal": {
        "count": 4,
        "percentage": 30.77,
        "rate_per_hour": 0.167,
        "events": [
          "2025-01-01 01:00:04"
        ]
      }
    },
    "events": [
      "2025-01-01 00:30:05",
      "2025-01-01 01:00:04"
    ]
  }
}
```

**Fields:**
- `total_checkpoints`: Total checkpoint count
- `avg_checkpoint_time`: Average duration
- `max_checkpoint_time`: Maximum duration
- `types`: Breakdown by checkpoint type (time-based vs WAL-based)
- `events`: List of all checkpoint timestamps

### connections

Connection and session statistics with detailed breakdowns.

```json
{
  "connections": {
    "connection_count": 36,
    "disconnection_count": 23,
    "avg_connections_per_hour": "1.50",
    "avg_session_time": "1h14m6.55s",
    "session_stats": {
      "count": 23,
      "min_duration": "7m10.234s",
      "max_duration": "2h20m15.89s",
      "avg_duration": "1h14m6.55s",
      "median_duration": "1h17m45.123s",
      "cumulated_duration": "28h24m30.67s"
    },
    "session_distribution": {
      "< 1s": 0,
      "1s - 1min": 0,
      "1min - 30min": 1,
      "30min - 2h": 19,
      "2h - 5h": 3,
      "> 5h": 0
    },
    "sessions_by_user": {
      "app_user": {
        "count": 10,
        "min_duration": "31m5.567s",
        "max_duration": "2h20m15.89s",
        "avg_duration": "1h26m58.587s",
        "median_duration": "1h26m48.123s",
        "cumulated_duration": "14h29m45.872s"
      },
      "readonly": {
        "count": 5,
        "min_duration": "7m10.234s",
        "max_duration": "1h3m25.678s",
        "avg_duration": "41m38.256s",
        "median_duration": "47m30.123s",
        "cumulated_duration": "3h28m11.281s"
      }
    },
    "sessions_by_database": {
      "app_db": {
        "count": 16,
        "min_duration": "7m10.234s",
        "max_duration": "2h20m15.89s",
        "avg_duration": "1h19m42.351s",
        "median_duration": "1h22m18.178s",
        "cumulated_duration": "21h15m17.609s"
      }
    },
    "sessions_by_host": {
      "192.168.1.100": {
        "count": 3,
        "min_duration": "31m5.567s",
        "max_duration": "1h13m10.456s",
        "avg_duration": "50m40.342s",
        "median_duration": "45m30.123s",
        "cumulated_duration": "2h32m1.027s"
      }
    },
    "peak_concurrent_sessions": 36,
    "peak_concurrent_timestamp": "2025-01-01 05:50:00",
    "connections": [
      "2025-01-01 00:00:15",
      "2025-01-01 00:00:20"
    ]
  }
}
```

**Fields:**
- `connection_count`: Total connections
- `disconnection_count`: Total disconnections
- `avg_connections_per_hour`: Connection rate
- `avg_session_time`: Average session duration
- `session_stats`: Global session duration statistics
  - `count`: Number of sessions with duration data
  - `min_duration`: Shortest session
  - `max_duration`: Longest session
  - `avg_duration`: Mean session duration
  - `median_duration`: 50th percentile (robust to outliers)
  - `cumulated_duration`: Total time in sessions
- `session_distribution`: Histogram of session durations by time buckets
- `sessions_by_user`: Per-user session statistics (same fields as `session_stats`)
- `sessions_by_database`: Per-database session statistics
- `sessions_by_host`: Per-host session statistics
- `peak_concurrent_sessions`: Maximum simultaneous sessions
- `peak_concurrent_timestamp`: When peak occurred
- `connections`: List of connection timestamps

### clients, users, databases, apps, hosts

Unique database entities.

```json
{
  "clients": {
    "unique_dbs": 3,
    "unique_users": 7,
    "unique_apps": 9,
    "unique_hosts": 37
  },
  "users": [
    "postgres",
    "app_user",
    "readonly"
  ],
  "databases": [
    "postgres",
    "app_db",
    "analytics_db"
  ],
  "apps": [
    "psql",
    "pgadmin",
    "metabase"
  ],
  "hosts": [
    "10.0.1.50",
    "172.16.0.10"
  ]
}
```

## Using jq

Extract specific fields from JSON output:

```bash
# Total queries
quellog /var/log/postgresql/*.log --json | jq '.sql_performance.total_queries_parsed'

# Checkpoint count
quellog /var/log/postgresql/*.log --json | jq '.checkpoints.total_checkpoints'

# Unique databases
quellog /var/log/postgresql/*.log --json | jq '.databases'

# Error count
quellog /var/log/postgresql/*.log --json | jq '.summary.error_count'

# Tempfile total size
quellog /var/log/postgresql/*.log --json | jq '.temp_files.total_size'

# Error classes
quellog /var/log/postgresql/*.log --json | jq '.error_classes'

# All queries with their IDs
quellog /var/log/postgresql/*.log --json | jq '.sql_performance.queries[] | {id, normalized_query, count}'

# Tempfile events for a specific query
quellog /var/log/postgresql/*.log --json | jq '.temp_files.events[] | select(.query_id == "se-abc123")'

# Lock waits grouped by query
quellog /var/log/postgresql/*.log --json | jq '.locks.queries | sort_by(-.count)'
```

## Reconstructing Analysis

Because JSON exports complete raw data, you can rebuild the entire analysis:

```python
import json

with open('report.json') as f:
    data = json.load(f)

# Reconstruct query statistics from executions
for execution in data['sql_performance']['executions']:
    query_id = execution['query_id']
    # Find corresponding query in queries array
    query = next(q for q in data['sql_performance']['queries'] if q['id'] == query_id)
    print(f"{execution['timestamp']}: {query['normalized_query']} ({execution['duration']})")

# Analyze tempfile patterns
for event in data['temp_files']['events']:
    if event.get('query_id'):
        query = next(q for q in data['temp_files']['queries'] if q['id'] == event['query_id'])
        print(f"{event['timestamp']}: {event['size']} for {query['normalized_query']}")
```

## Next Steps

- [Markdown Export](markdown-export.md) for documentation format
- [PostgreSQL Configuration](postgresql-setup.md) for optimal logging setup
- [Filtering Logs](filtering-logs.md) to focus exports on specific subsets
