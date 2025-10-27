# _quellog_

**_quellog_** is a fast, reliable, and developer-friendly CLI tool for parsing
and analyzing PostgreSQL logs. It generates a synthetic overview, a detailed SQL
performance breakdown, and per-query insights, all with powerful filtering
options to help you quickly extract meaning from your data.

With **_quellog_**, you can analyze query performance, detect anomalies, and
track database maintenance operations effortlessly.

## Features

**_quellog_** is designed for speed and clarity: parse gigabytes of logs in
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
git clone https://github.com/yourusername/quellog.git
cd quellog
go build -o bin/quellog
```

---

## Usage

### Generate a summary report  
```sh
quellog /path/to/logs --summary
```

### Analyze SQL performance  
```sh
quellog /path/to/logs --sql-summary
```

### Show details for a specific SQL query  
```sh
quellog /path/to/logs --sql-detail <query_id>
```

### Filter logs for a specific database and user, within a time range  
```sh
quellog /path/to/logs --dbname mydb --dbuser myuser --begin "2024-01-01 00:00:00" --end "2024-01-01 23:59:59"
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

This project is licensed under the terms of the PostgreSQL License. See
[LICENSE.md](LICENSE.md) for details.
