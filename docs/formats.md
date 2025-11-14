# Supported Log Formats

quellog automatically detects and parses PostgreSQL log formats and compression schemes.

## Log Formats

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

## Format Detection

Format detection is automatic:

1. File extension provides a hint (`.log`, `.csv`, `.json`, `.gz`, `.zst`, `.tar`)
2. Content is sampled (first 32 KB) to verify format
3. Compression is detected by magic bytes (gzip: `1f 8b`, zstd: `28 b5 2f fd`, tar: `ustar`)
4. Log format is identified by structure (JSON object, CSV fields, or stderr patterns)

If extension and content disagree, content wins. Binary files are automatically rejected.

## Next Steps

- [Configure PostgreSQL](postgresql-setup.md) logging settings
- [Learn about filtering](filtering-logs.md) to analyze specific log subsets
- [Run your first analysis](quick-start.md) on your logs
