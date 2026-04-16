# Log Formats

All formats are auto-detected from file content — no configuration needed.

### stderr/syslog

Plain text format used with `log_destination = 'stderr'` or `log_destination = 'syslog'`.

```
2025-01-13 14:32:18 UTC [12345]: db=mydb,user=postgres LOG:  duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42
```

Supports various `log_line_prefix` configurations. The format is flexible and human-readable.

### CSV

Structured format with `log_destination = 'csvlog'`. Fixed column structure with dedicated fields for timestamp, user, database, PID, client, severity, message, and query text.

```csv
2025-01-13 14:32:18.456 UTC,postgres,mydb,12345,192.168.1.100:54321,6384bef2.3039,1,SELECT,2025-01-13 14:30:00 UTC,2/1234,0,LOG,00000,"duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42",,,,,,"SELECT * FROM users WHERE id = 42",,,"psql"
```

The dedicated `query` field (column 20) ensures complete SQL text capture.

### JSON

JSON format from `log_destination = 'jsonlog'` (PostgreSQL 15+) or cloud providers (GCP Cloud SQL, Azure Database for PostgreSQL, AWS RDS).

```json
{
  "timestamp": "2025-01-13T14:32:18.456Z",
  "user": "postgres",
  "dbname": "mydb",
  "pid": 12345,
  "remote_host": "192.168.1.100",
  "error_severity": "LOG",
  "message": "duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42",
  "application_name": "psql"
}
```

Cloud-specific JSON schemas are automatically detected and parsed.


### Cloud Providers

quellog auto-detects log formats from managed PostgreSQL services:

| Provider | Format | Notes |
|----------|--------|-------|
| AWS RDS / Aurora | stderr | Custom `log_line_prefix` with host, user, db in the timestamp line |
| Google Cloud SQL | JSON | Cloud Logging JSON envelope with `insertId` and `timestamp` fields |
| Azure Database | stderr | Session ID and severity in the timestamp line |
| CloudNative-PG | JSON | Kubernetes JSON wrapper around PostgreSQL CSV records |

No configuration needed — just point quellog at the log files.

## Multiple Inputs

quellog accepts multiple files, glob patterns, and directories:

```bash
# Two days of logs
quellog postgresql-2026-01-14.log postgresql-2026-01-15.log

# Glob pattern
quellog /var/log/postgresql/*.log

# Today's log + previous days compressed
quellog postgresql-2026-01-15.log postgresql-2026-01-14.log.gz postgresql-2026-01-13.log.gz

# Stdin
kubectl logs postgres-pod | quellog -
```

All entries are merged and analyzed together regardless of source format.

## Compression Formats

### gzip (.gz)

```bash
quellog /var/log/postgresql/postgresql.log.gz
```

Uses parallel decompression (pgzip) for faster processing.

### zstd (.zst, .zstd)

```bash
quellog /backups/postgresql.log.zst
```

Faster decompression and better compression ratios than gzip.

### Archives (.tar, .tar.gz, .tgz, .tar.zst, .tzst)

```bash
quellog /backups/postgresql-january.tar.gz
```

Streams archive entries without full extraction. Handles nested compression (e.g., `.tar` containing `.log.gz` files).

### ZIP Archives (.zip)

```bash
quellog /backups/postgresql-logs.zip
```

Extracts and processes all log entries from ZIP archives. Handles nested compressed files (`.gz`, `.zst`) within the archive.

### 7z Archives (.7z)

```bash
quellog /backups/postgresql-logs.7z
```

Extracts and processes log entries from 7z archives (LZMA/LZMA2 compression). Provides excellent compression ratios, especially for large log files. CLI-only — not supported in browser/WASM mode.

