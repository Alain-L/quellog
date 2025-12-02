# quellog

**quellog** is a fast, reliable, and developer-friendly CLI tool for parsing and
analyzing PostgreSQL logs. It generates a synthetic overview, detailed SQL
performance breakdown, and per-query insights—all with powerful filtering
options to help you quickly extract meaning from your data.

With **quellog**, you can analyze query performance, detect anomalies, and track
database maintenance operations effortlessly.

**[Read the full documentation](https://alain-l.github.io/quellog/)**

## Features

**quellog** is designed for speed and clarity: parse gigabytes of logs in
seconds and get instant, actionable insights into your PostgreSQL instance.

Here is what it looks like:

```console
❯ quellog test_summary.log
quellog – 180 entries processed in 0.00 s (29.2kB)

SUMMARY

  Start date                : 2025-01-01 00:00:00 CET
  End date                  : 2025-01-01 23:59:35 CET
  Duration                  : 23h59m35s
  Total entries             : 180
  Throughput                : 0.00 entries/s

SQL PERFORMANCE

  Query load distribution | ■ = 1 s

  00:00 - 00:59  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 29 s
  00:59 - 01:58  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 35 s
  01:58 - 02:57  ■■■■■■■■■■■■■■■■■■■■■■■■■■■ 27 s
  02:57 - 03:57  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 28 s
  03:57 - 04:56  ■■■■■■■■■■■■■■■■■■■■■ 21 s
  04:56 - 05:55  ■■■■■■■■■■■■■■■■■■■■■■■■ 24 s

  Total query duration      : 2m 40s              
  Total queries parsed      : 25                  
  Total unique query        : 25                  
  Top 1% slow queries       : 1                   

  Query max duration        : 15.23 s             
  Query min duration        : 45 ms               
  Query median duration     : 7.89 s              
  Query 99% max duration    : 15.23 s             


EVENTS

  LOG     : 172
  ERROR   : 2
  WARNING : 1

TEMP FILES

  Temp file distribution | ■ = 10 MB

  00:15 - 01:11  ■■■■■■■■■■■■■■■■■■■■■■■■ 249 MB
  01:11 - 02:08  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 389 MB
  02:08 - 03:05  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■ 300 MB
  03:05 - 04:01  ■■■■■■■■■■■■■■■■■■■■■ 214 MB
  04:01 - 04:58  ■■■■■■■■■■■■■■■■■ 170 MB
  04:58 - 05:55  ■■■■■■■■■■■■■■■■■■■ 194 MB

  Temp file messages        : 19
  Cumulative temp file size : 1.48 GB
  Average temp file size    : 79.79 MB

MAINTENANCE

  Automatic vacuum count    : 2
  Automatic analyze count   : 2
  Top automatic vacuum operations per table:
    app_db.public.orders        1  50.00%
    app_db.public.sessions      1  50.00%       1.00 MB removed
  Top automatic analyze operations per table:
    app_db.public.orders        1  50.00%
    app_db.public.products      1  50.00%

CHECKPOINTS

  Checkpoints | ■ = 1 

  00:00 - 04:00  ■■■■■■■ 7 
  04:00 - 08:00  ■■■■■ 5 
  08:00 - 12:00   -
  12:00 - 16:00   -
  16:00 - 20:00   -
  20:00 - 00:00  ■ 1 

  Checkpoint count          : 13
  Avg checkpoint write time : 5s
  Max checkpoint write time : 7s
  Checkpoint types:
    time          9   69.2%  (0.38/h)
    wal           4   30.8%  (0.17/h)

CONNECTIONS & SESSIONS

  Connection distribution | ■ = 1 

  00:00 - 00:58  ■■■■■■■■■■■■■■■ 15 
  00:58 - 01:56  ■■■■■ 5 
  01:56 - 02:55  ■■■■ 4 
  02:55 - 03:53  ■■■■ 4 
  03:53 - 04:51  ■■■■ 4 
  04:51 - 05:50  ■■■■ 4 

  Connection count          : 36
  Avg connections per hour  : 1.50
  Disconnection count       : 23
  Avg session time          : 1h14m7s

CLIENTS

  Unique DBs                : 3
  Unique Users              : 6
  Unique Apps               : 9

USERS

    admin
    analytics
    app_user
    backup_user
    batch_user
    readonly

APPS

    app_server
    batch_job
    metabase
    pg_dump
    pgadmin
    psql
    python_script
    reporting_tool
    tableau

DATABASES

    analytics_db
    app_db
    postgres
```

Here are the main features:

- **Multi-format support:** Automatically detects and parses PostgreSQL logs in
  stderr, CSV, or JSON format
- **Cloud provider support:** Works out-of-the-box with AWS RDS, Azure Database
  for PostgreSQL, and GCP Cloud SQL logs
- **Automatic log_line_prefix detection:** Heuristically detects custom
  `log_line_prefix` configurations for accurate metadata extraction
- **Transparent compression and archive support:** Read gzip (`.gz`), zstd (`.zst`, `.zstd`),
  and tar archives (`.tar`, `.tar.gz`, `.tar.zst`, `.tzst`) without manual decompression
- **Time-based filtering:** Analyze logs within specific date ranges or time
  windows
- **Attribute filtering:** Focus on specific databases, users, applications, or
  custom criteria
- **Comprehensive SQL analysis:**
  - Global performance metrics with percentiles and distribution histograms
  - Per-query details with execution statistics
  - Top rankings: slowest queries, most frequent, highest total time consumption
  - Query type overview: breakdown by database, user, host, and application
- **Database health monitoring:**
  - Error and warning detection with event classification
  - Vacuum and autovacuum analysis with space recovery metrics
  - Checkpoint tracking and performance impact assessment
- **Lock analysis:**
  - Lock wait tracking with wait time statistics
  - Query-to-lock association for identifying blocking queries
  - Lock type and resource type distribution analysis
  - Most frequent waiting queries and acquired locks by query
- **Temporary file analysis:**
  - SQL query association with 99.79% coverage across all log formats
  - Multi-pattern recognition for comprehensive tempfile tracking
  - Top queries by temporary file size with cumulative statistics
- **Connection insights:** Session duration, client distribution, and connection
  patterns
- **Flexible output formats:** Human-readable reports, JSON export, or Markdown
  documentation
- **High performance:** Streaming parser with concurrent processing for large
  log files

---

## Installation

Binaries are available for Linux, macOS and Windows in the Releases page:

> https://github.com/Alain-L/quellog/releases

Download the archive for your platform, extract it and move the binary to your
PATH, e.g.:

```sh
tar -xzf quellog_*_linux_amd64.tar.gz
sudo mv quellog /usr/local/bin/
```

Check it works:

```sh
quellog --version
quellog --help
```

### Package installation (Linux)

**Debian/Ubuntu (.deb):**
```sh
wget https://github.com/Alain-L/quellog/releases/download/v0.4.0/quellog_0.4.0_linux_amd64.deb
sudo dpkg -i quellog_0.4.0_linux_amd64.deb
```

**RedHat/Fedora/CentOS (.rpm):**
```sh
wget https://github.com/Alain-L/quellog/releases/download/v0.4.0/quellog_0.4.0_linux_amd64.rpm
sudo rpm -i quellog_0.4.0_linux_amd64.rpm
```

### Build from source

To build from source :

```sh
git clone https://github.com/Alain-L/quellog.git
cd quellog
go build -o bin/quellog .
```

---

## PostgreSQL Configuration

To get the most out of **quellog**, configure your PostgreSQL instance to log the information you need:

### Recommended settings for comprehensive analysis

```ini
# What to log
log_connections = on                 # Track connections
log_disconnections = on              # Track disconnections
log_min_duration_statement = 0       # Log all queries 
log_line_prefix = '%t [%p] %e: db=%d,user=%u,app=%a,client=%h '


# Additional useful settings
log_checkpoints = on                 # Track checkpoint activity
log_autovacuum_min_duration = 0      # Log all autovacuum activity
log_temp_files = 0                   # Log temporary file usage
log_lock_waits = on                  # Log lock waits (required for lock analysis)

```

Consider setting `log_min_duration_statement` to 0 only temporarily if you're 
conducting a performance investigation, as this may generate very large logs 
and increase disk usage or I/O pressure.

After updating `postgresql.conf`, reload the configuration:

```sql
SELECT pg_reload_conf();
```

---

## Usage

### Generate a complete report  
```sh
quellog /path/to/logs
```

### Generate specific section reports  
```sh
quellog /path/to/logs --summary
quellog /path/to/logs --checkpoints --connections
```

### Analyze SQL performance
```sh
quellog /path/to/logs --sql-performance
```

### Query type overview
```sh
quellog /path/to/logs --sql-overview
```

This displays query breakdowns by type, category, and dimensions (database, user, host, application):

```
SQL OVERVIEW

  Query Category Summary

    DML          : 1,234     (78.5%)
    UTILITY      : 245       (15.6%)
    DDL          : 78        (5.0%)
    TCL          : 14        (0.9%)

  Query Type Distribution

    SELECT       : 890       (56.6%)
    INSERT       : 234       (14.9%)
    UPDATE       : 110       (7.0%)
    DELETE       : 45        (2.9%)
    BEGIN        : 78        (5.0%)
    COMMIT       : 65        (4.1%)
    ...

  Queries per Database

    mydb (1,234 queries, 45m 23s)
      SELECT         890      38m 12s
      INSERT         234       5m 45s
      UPDATE         110       1m 26s

    analytics_db (523 queries, 12m 45s)
      SELECT         487      11m 30s
      INSERT          36       1m 15s

  Queries per User

    app_user (1,456 queries, 52m 8s)
      SELECT       1,123      45m 32s
      INSERT         234       5m 12s
      UPDATE         99        1m 24s
```

### Show details for a specific SQL query
```sh
quellog /path/to/logs --sql-detail <query_id>
```

### Analyze lock contention
```sh
quellog /path/to/logs --locks
```

### Filter by database, user, and time range  
```sh
quellog /path/to/logs --dbname mydb --dbuser myuser --appname myapp \
  --begin "2025-01-01 00:00:00" --end "2025-01-01 23:59:59"
```

### Use multiple filters  
```sh
quellog /path/to/logs --dbname mydb1 --dbname mydb2 
```

### Export to JSON or Markdown
```sh
quellog /path/to/logs --json
quellog /path/to/logs --md
```

---

## Command Reference

```
Usage:
  quellog [files or dirs] [flags]

Time Filters:
  -b, --begin string      Filter entries after this datetime (YYYY-MM-DD HH:MM:SS)
  -e, --end string        Filter entries before this datetime (YYYY-MM-DD HH:MM:SS)
  -W, --window string     Time window duration (e.g., 30m, 2h). Adjusts --begin or --end
  -L, --last string       Analyze last N duration from now (e.g., 1h, 30m, 24h)

Attribute Filters:
  -d, --dbname strings    Filter by database name(s)
  -u, --dbuser strings    Filter by database user(s)
  -N, --appname strings   Filter by application name(s)
  -U, --exclude-user      Exclude entries from specified user(s)

SQL Analysis:
      --sql-performance   Display detailed SQL performance analysis with metrics and percentiles
      --sql-overview      Display query type overview with breakdown by dimension
  -Q, --sql-detail        Show details for specific SQL ID(s)

Section Selection:
      --summary           Print only the summary section
      --checkpoints       Print only the checkpoints section
      --connections       Print only the connections section
      --clients           Print only the clients section
      --events            Print only the events section
      --errors            Print only the error classes section
      --sql-summary       Print only the SQL summary section
      --tempfiles         Print only the temporary files section
      --locks             Print only the locks section
      --maintenance       Print only the maintenance section

Output:
  -J, --json              Export results in JSON format
      --md                Export results in Markdown format

Other:
  -h, --help              Show help
  -v, --version           Show version
```

---

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on our code of conduct
and the process for submitting pull requests.

---

## License

This project is licensed under the PostgreSQL License. See [LICENSE](LICENSE)
for details.
