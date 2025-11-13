# Supported Log Formats

quellog automatically detects and parses multiple PostgreSQL log formats and compression schemes. This page describes each supported format, how detection works, and examples of each.

## Log Formats

PostgreSQL can output logs in three main formats. quellog supports all of them and automatically detects which format is being used.

### stderr/syslog Format

The traditional PostgreSQL text log format, used when `log_destination = 'stderr'` or `log_destination = 'syslog'`.

#### Detection

quellog identifies stderr/syslog format by:

1. File extension `.log` (hint, not required)
2. Content patterns matching timestamp formats and log levels

#### Example Log Entry

```
2025-01-13 14:32:18 UTC [12345]: [1-1] user=postgres,db=mydb LOG:  duration: 145.234 ms  statement: SELECT * FROM users WHERE email = 'user@example.com'
```

#### Common `log_line_prefix` Formats

quellog supports logs generated with various `log_line_prefix` configurations:

=== "Standard Format"

    ```ini
    log_line_prefix = '%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h '
    ```

    Example output:
    ```
    2025-01-13 14:32:18 UTC [12345]: [1-1] user=postgres,db=mydb,app=psql,client=192.168.1.100 LOG:  connection received
    ```

=== "Minimal Format"

    ```ini
    log_line_prefix = '%t [%p] '
    ```

    Example output:
    ```
    2025-01-13 14:32:18 UTC [12345] LOG:  checkpoint complete
    ```

=== "Detailed Format"

    ```ini
    log_line_prefix = '%m [%p] %q%u@%d '
    ```

    Example output:
    ```
    2025-01-13 14:32:18.456 UTC [12345] postgres@mydb LOG:  duration: 145.234 ms  execute <unnamed>: SELECT 1
    ```

#### Syslog Integration

When using `log_destination = 'syslog'`, PostgreSQL sends logs to the system logger:

```
Jan 13 14:32:18 hostname postgres[12345]: [1-1] LOG:  database system is ready to accept connections
```

quellog parses both direct stderr logs and syslog-formatted entries.

### CSV Format

Structured comma-separated value format, enabled with `log_destination = 'csvlog'`.

#### Detection

quellog identifies CSV format by:

1. File extension `.csv` (hint, not required)
2. Content validation: minimum field count, proper CSV structure, timestamp in first field

#### Example Log Entry

```csv
2025-01-13 14:32:18.456 UTC,postgres,mydb,12345,192.168.1.100,6384bef2.3039,1,,,2025-01-13 14:30:00 UTC,2/1234,0,LOG,00000,"duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42",,,,,,"SELECT * FROM users WHERE id = 42",,,"psql"
```

#### CSV Fields

PostgreSQL's CSV log format includes these fields (in order):

| Field | Description | Example |
|-------|-------------|---------|
| `log_time` | Timestamp with milliseconds | `2025-01-13 14:32:18.456 UTC` |
| `user_name` | Database user | `postgres` |
| `database_name` | Database name | `mydb` |
| `process_id` | Backend process ID | `12345` |
| `connection_from` | Client host/IP | `192.168.1.100` |
| `session_id` | Session identifier | `6384bef2.3039` |
| `session_line_num` | Per-session line number | `1` |
| `command_tag` | Command type | `SELECT` |
| `session_start_time` | Session start timestamp | `2025-01-13 14:30:00 UTC` |
| `virtual_transaction_id` | Virtual transaction ID | `2/1234` |
| `transaction_id` | Transaction ID | `0` |
| `error_severity` | Log level | `LOG`, `ERROR`, `WARNING` |
| `sql_state_code` | SQLSTATE code | `00000` |
| `message` | Log message | `duration: 145.234 ms  statement: ...` |
| `detail` | Detailed message | (optional) |
| `hint` | Hint message | (optional) |
| `internal_query` | Internal query | (optional) |
| `internal_query_pos` | Position in internal query | (optional) |
| `context` | Error context | (optional) |
| `query` | User query | `SELECT * FROM users WHERE id = 42` |
| `query_pos` | Position in query | (optional) |
| `location` | Source code location | (optional) |
| `application_name` | Application name | `psql` |

#### Advantages of CSV Format

- **Structured**: Easy to parse programmatically
- **Complete**: Includes dedicated `query` field for full SQL text
- **Unambiguous**: No need to parse free-form text

quellog achieves 99.79% query-to-tempfile association accuracy with CSV format due to the dedicated `query` field.

### JSON Format

Modern structured JSON output, used in cloud environments and with `log_destination = 'jsonlog'` (PostgreSQL 15+).

#### Detection

quellog identifies JSON format by:

1. File extension `.json` (hint, not required)
2. Valid JSON structure with recognized timestamp fields

#### Example Log Entry (Standard)

```json
{
  "timestamp": "2025-01-13T14:32:18.456Z",
  "user": "postgres",
  "dbname": "mydb",
  "pid": 12345,
  "remote_host": "192.168.1.100",
  "session_id": "6384bef2.3039",
  "line_num": 1,
  "ps": "idle",
  "session_start": "2025-01-13T14:30:00.000Z",
  "vxid": "2/1234",
  "txid": 0,
  "error_severity": "LOG",
  "message": "duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42",
  "application_name": "psql",
  "backend_type": "client backend"
}
```

#### Cloud Provider JSON Formats

quellog also supports cloud-specific JSON formats:

=== "Google Cloud SQL"

    ```json
    {
      "insertId": "abc123xyz",
      "timestamp": "2025-01-13T14:32:18.456789Z",
      "severity": "INFO",
      "jsonPayload": {
        "user": "postgres",
        "database": "mydb",
        "remoteHost": "192.168.1.100",
        "pid": 12345,
        "message": "duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42"
      }
    }
    ```

=== "Azure Database for PostgreSQL"

    ```json
    {
      "time": "2025-01-13T14:32:18.456Z",
      "resourceId": "/SUBSCRIPTIONS/.../POSTGRESQL/SERVERS/myserver",
      "category": "PostgreSQLLogs",
      "operationName": "LogEvent",
      "properties": {
        "prefix": "[12345]",
        "message": "duration: 145.234 ms  statement: SELECT * FROM users WHERE id = 42",
        "detail": "",
        "errorLevel": "LOG",
        "domain": "postgres-12",
        "schemaName": "",
        "tableName": "",
        "columnName": "",
        "datatypeName": ""
      }
    }
    ```

quellog automatically handles these cloud-specific schemas.

## Compression Formats

quellog can process compressed log files directly, without manual decompression. This saves time and disk space.

### gzip (.gz)

Standard gzip compression, commonly used for log rotation.

#### Detection

- File extension: `.gz`
- Content: gzip magic bytes

#### Processing

- Uses parallel decompression (pgzip) for faster processing
- Configurable thread count (automatically tuned based on CPU cores)
- Typical speedup: 3-5x faster than single-threaded decompression

#### Example

```bash
# Process gzipped log directly
quellog /var/log/postgresql/postgresql-2025-01-12.log.gz

# Process multiple compressed logs
quellog /var/log/postgresql/*.log.gz
```

### zstd (.zst, .zstd)

Zstandard compression, offering better compression ratios and faster decompression than gzip.

#### Detection

- File extensions: `.zst`, `.zstd`
- Content: zstd magic bytes

#### Processing

- Streaming decompression with klauspost/zstd
- Better compression ratios mean smaller backup sizes
- Faster decompression than gzip

#### Example

```bash
# Process zstd-compressed log
quellog /backups/postgresql-2025-01.log.zst

# Both extensions work
quellog archive.zstd
```

### Archives (.tar, .tar.gz, .tar.zst, .tgz, .tzst)

Tar archives, optionally compressed with gzip or zstd.

#### Detection

- File extensions: `.tar`, `.tar.gz`, `.tgz`, `.tar.zst`, `.tar.zstd`, `.tzst`
- Content: tar magic bytes

#### Processing

- Streams archive entries without full extraction
- Recursively handles compressed entries within archives
- Skips non-log files automatically
- Processes multiple logs in parallel from a single archive

#### Supported Archive Structures

quellog handles various archive structures:

=== "Plain Tar"

    ```
    logs.tar
    ├── postgresql-2025-01-01.log
    ├── postgresql-2025-01-02.log
    └── postgresql-2025-01-03.log
    ```

=== "Compressed Tar"

    ```
    logs.tar.gz
    ├── postgresql-2025-01-01.log
    ├── postgresql-2025-01-02.log
    └── postgresql-2025-01-03.log
    ```

=== "Nested Compression"

    ```
    archive.tar
    ├── postgresql-2025-01-01.log.gz
    ├── postgresql-2025-01-02.log.zst
    └── postgresql-2025-01-03.csv.gz
    ```

quellog detects and decompresses nested compressed files automatically.

#### Example

```bash
# Process tar archive
quellog /backups/postgresql-january.tar

# Process compressed tar
quellog /backups/postgresql-january.tar.gz

# Process zstd-compressed tar
quellog /backups/postgresql-january.tar.zst
```

## Format Detection Process

quellog uses a multi-stage detection algorithm to identify file formats:

1. **Extension-based hint**
    - Check file extension (`.log`, `.csv`, `.json`, `.gz`, `.zst`, `.tar`, etc.)
    - Not mandatory, but speeds up detection

2. **Binary check**
    - Read first 32 KB of file
    - Reject files with null bytes or excessive non-printable characters
    - Prevents misclassification of binary files

3. **Compression detection**
    - Check for gzip magic bytes (`1f 8b`)
    - Check for zstd magic bytes (`28 b5 2f fd`)
    - Check for tar magic bytes (`ustar`)

4. **Content-based detection** (for plain text)
    - **JSON**: Valid JSON structure with timestamp fields
    - **CSV**: Comma count, CSV parsing, timestamp format in first field
    - **stderr/syslog**: Regex patterns matching PostgreSQL log formats

5. **Fallback strategy**
    - If extension and content disagree, content wins
    - If detection fails, file is skipped with error message

### Detection Examples

```bash
# Auto-detects stderr format (extension + content)
quellog postgresql.log

# Auto-detects CSV format (extension + content)
quellog postgresql.csv

# Auto-detects JSON format (extension + content)
quellog postgresql.json

# Auto-detects gzip, then CSV inside
quellog postgresql.csv.gz

# Auto-detects despite wrong extension (content wins)
quellog file.txt  # Actually a valid PostgreSQL log

# Auto-detects tar, processes all entries
quellog archive.tar.gz
```

## Format Limitations

### stderr/syslog

- **Multi-line messages**: Continuation lines (DETAIL, HINT, STATEMENT, CONTEXT) must appear immediately after the main message
- **Timestamp parsing**: Requires recognizable timestamp format

### CSV

- **Field count**: Must have at least 12 fields (PostgreSQL standard)
- **Timestamp format**: First field must be valid PostgreSQL timestamp

### JSON

- **Timestamp field**: Must contain recognized timestamp field (`timestamp`, `time`, `@timestamp`, `insertId`, etc.)
- **Structure**: Must be valid JSON (either single object, array, or JSONL)

## Performance Characteristics

| Format | Detection Speed | Parsing Speed | Memory Usage |
|--------|----------------|---------------|--------------|
| stderr/syslog | Fast | Very Fast (mmap) | Low |
| CSV | Fast | Fast | Low |
| JSON | Fast | Medium | Medium |
| gzip | Medium | Fast (parallel) | Medium |
| zstd | Medium | Very Fast | Medium |
| tar | Medium | Fast (streaming) | Low |

## Best Practices

1. **Use CSV format** for maximum query association accuracy (99.79%)
2. **Use zstd compression** for backups (better ratio and speed than gzip)
3. **Keep consistent naming** to leverage extension hints
4. **Avoid binary files** in log directories (will be skipped but waste detection time)

## Unsupported Formats

quellog does **not** support:

- **syslog-ng or rsyslog templates** other than standard PostgreSQL output
- **Custom log formats** created with non-standard `log_line_prefix`
- **Binary formats** (e.g., raw database files, pg_dump output)
- **Proprietary formats** from third-party tools

For unsupported formats, consider converting to CSV or stderr format using PostgreSQL's logging configuration.

## Next Steps

- [Configure PostgreSQL](postgresql-setup.md) to use your preferred log format
- [Learn about filtering](filtering-logs.md) to analyze specific log subsets
- [Understand the default report](default-report.md) output format
