# quellog

A high-performance PostgreSQL log analyzer. Processes gigabytes of logs in seconds, producing synthetic overviews, SQL performance analysis, and operational insights.

!!! tip "Try it in your browser"
    **[Open the interactive demo →](https://alain-l.github.io/quellog/demo.html)** — Drop a PostgreSQL log file and explore the report. All processing happens locally; your data never leaves your machine.

## Key Features

- **Fast** — high throughput, streaming architecture, bounded memory
- **Multi-format** — stderr, CSV, JSON, syslog + cloud providers (RDS, Cloud SQL, Azure, CNPG)
- **Archives** — gzip, zstd, tar, zip, 7z — decompressed on the fly
- **Filtering** — by time range, database, user, application, host
- **Interactive HTML** — standalone reports with charts, filtering, query drill-down
- **Export** — JSON, YAML, Markdown for automation and integration

## Getting Started

1. [Install quellog](installation.md)
2. [Configure PostgreSQL logging](postgresql-setup.md)
3. Run `quellog /var/log/postgresql/*.log`
