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

- **Multi-format support:** Automatically detects and parses PostgreSQL logs in
  stderr, CSV, or JSON format
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

Clone the repository and build the binary:
```sh
git clone https://github.com/Alain-L/quellog.git
cd quellog
go build -o bin/quellog
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