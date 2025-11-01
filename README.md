# quellog

**quellog** is a fast, reliable, and developer-friendly CLI tool for parsing and
analyzing PostgreSQL logs. It generates a synthetic overview, detailed SQL
performance breakdown, and per-query insights—all with powerful filtering
options to help you quickly extract meaning from your data.

With **quellog**, you can analyze query performance, detect anomalies, and track
database maintenance operations effortlessly.

## Features

**quellog** is designed for speed and clarity: parse gigabytes of logs in
seconds and get instant, actionable insights into your PostgreSQL instance.

Here is what it looks like:

```console
❯ bin/quellog_dev test/testdata/test_summary.log
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
- **Transparent archive support:** Read `*.log.gz`, `*.csv.gz`, `*.json.gz`
  as well as tar bundles (`*.tar`, `*.tar.gz`, `*.tgz`) without manual decompression
- **Time-based filtering:** Analyze logs within specific date ranges or time
  windows
- **Attribute filtering:** Focus on specific databases, users, applications, or
  custom criteria
- **Comprehensive SQL analysis:**
  - Global performance metrics with percentiles and distribution histograms
  - Per-query details with execution statistics
  - Top rankings: slowest queries, most frequent, highest total time consumption
- **Database health monitoring:**
  - Error and warning detection with event classification
  - Vacuum and autovacuum analysis with space recovery metrics
  - Checkpoint tracking and performance impact assessment
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
quellog --help
```

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
log_line_prefix = '%t [%p]: db=%d,user=%u,app=%a,client=%h ' 

# Additional useful settings
log_checkpoints = on                 # Track checkpoint activity
log_autovacuum_min_duration = 0      # Log all autovacuum activity
log_temp_files = 0                   # Log temporary file usage
log_lock_waits = on                  # Log lock waits

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
quellog /path/to/logs --sql-summary
```

### Show details for a specific SQL query  
```sh
quellog /path/to/logs --sql-detail <query_id>
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

For more details, run:
```sh
quellog --help
```

---

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on our code of conduct
and the process for submitting pull requests.

---

## License

This project is licensed under the PostgreSQL License. See [LICENSE](LICENSE)
for details.
