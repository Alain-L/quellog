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

## JSON Structure

The JSON output contains all analysis sections:

### summary

General log statistics.

```json
{
  "summary": {
    "start_date": "2024-12-10 00:00:01",
    "end_date": "2024-12-11 00:00:01",
    "duration": "24h0m0s",
    "total_logs": 95139,
    "throughput": "1.10 entries/s"
  }
}
```

### events

Log entries by severity level.

```json
{
  "events": {
    "LOG": 5943,
    "ERROR": 28236,
    "FATAL": 1215
  }
}
```

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
        "timestamp": "2024-12-10 02:11:47",
        "duration": "3m 21s"
      }
    ]
  }
}
```

**executions**: list of all query executions with timestamp and duration.

### temp_files

Temporary file statistics and events.

```json
{
  "temp_files": {
    "total_messages": 668,
    "total_size": "107.62 GB",
    "avg_size": "165.02 MB",
    "events": [
      {
        "timestamp": "2024-12-10 08:09:34",
        "size": "293.12 MB"
      }
    ]
  }
}
```

**events**: list of tempfile creation events with timestamp and size.

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
        "timestamp": "2024-12-10 00:23:56",
        "event_type": "waiting",
        "lock_type": "ShareLock",
        "resource_type": "transaction",
        "wait_time": "1000.09 ms",
        "process_id": "110896"
      }
    ],
    "top_queries": [
      {
        "sql_id": "up-bG8qBk",
        "count": 259,
        "avg_wait": "2.88 s",
        "total_wait": "12m 25s"
      }
    ]
  }
}
```

**events**: list of lock wait events with details. **top_queries**: queries sorted by lock wait frequency.

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

### checkpoints

Checkpoint statistics.

```json
{
  "checkpoints": {
    "total_checkpoints": 282,
    "avg_checkpoint_time": "29s",
    "max_checkpoint_time": "2m31s",
    "types": {
      "wal": 171,
      "time": 110,
      "immediate force wait": 1
    },
    "events": [
      "2024-12-10 00:03:25",
      "2024-12-10 00:08:34"
    ]
  }
}
```

**events**: list of checkpoint timestamps.

### clients, users, databases

Unique database entities.

```json
{
  "clients": {
    "unique_dbs": 9,
    "unique_users": 48,
    "unique_apps": 15,
    "unique_hosts": 45
  },
  "users": [
    "postgres",
    "app_user"
  ],
  "databases": [
    "postgres",
    "app_db"
  ]
}
```

## Using jq

Extract specific fields:

```bash
# Total queries
quellog /var/log/postgresql/*.log --json | jq '.sql_performance.total_queries_parsed'

# Checkpoint count
quellog /var/log/postgresql/*.log --json | jq '.checkpoints.total_checkpoints'

# Unique databases
quellog /var/log/postgresql/*.log --json | jq '.databases'

# Error count
quellog /var/log/postgresql/*.log --json | jq '.events.ERROR'

# Tempfile total size
quellog /var/log/postgresql/*.log --json | jq '.temp_files.total_size'
```

## Next Steps

- [Markdown Export](markdown-export.md) for documentation format
- [Filtering Logs](filtering-logs.md) to focus exports on specific subsets
