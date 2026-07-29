# quellog

A high-performance PostgreSQL log analyzer. Processes gigabytes of logs in seconds, producing synthetic overviews, SQL performance analysis, and operational insights.

**[Documentation](https://alain-l.github.io/quellog/)** | **[Try the demo](https://alain-l.github.io/quellog/demo.html)** (runs entirely in your browser)

## Quick Start

```bash
quellog /var/log/postgresql/*.log
```

```
quellog v0.12.0 – 155281 entries processed in 0.22 s (52.98 MB)

SUMMARY

  Start date                : 2026-01-01 00:00:01 CET
  End date                  : 2026-01-01 12:30:12 CET
  Duration                  : 12h30m11s
  Total entries             : 155281
  Throughput                : 3.45 entries/s
```

## Features

- **Multi-format** -- stderr, CSV, JSON, syslog + cloud providers (AWS RDS, Cloud SQL, Azure, CNPG)
- **Archives** -- gzip, zstd, tar, zip, 7z decompressed on the fly
- **SQL analysis** -- per-query performance, execution plans (auto_explain), TCL separation
- **Events & errors** -- per-pattern drill-down via stable IDs (`fa-XXXX`, `er-XXXX`), SQLSTATE class grouping, occurrences-over-time chart
- **Locks** -- wait tracking, blocking query identification, deadlock detection
- **Checkpoints** -- WAL distance/estimate, write rates, frequency warnings
- **Connections** -- session durations, concurrent sessions, pre-log/in-log breakdown
- **Filtering** -- by time range, database, user, application, host
- **Export** -- JSON, YAML, Markdown, standalone HTML with click-to-detail modals
- **Follow mode** -- real-time monitoring with periodic refresh

## Installation

### Linux packages (recommended)

```bash
LATEST=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')

# Debian/Ubuntu
wget "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_linux_${ARCH}.deb"
sudo dpkg -i quellog_${LATEST}_linux_${ARCH}.deb

# Red Hat/Fedora
wget "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_linux_${ARCH}.rpm"
sudo dnf install quellog_${LATEST}_linux_${ARCH}.rpm
```

### macOS / other

Download from the [releases page](https://github.com/Alain-L/quellog/releases) or build from source:

```bash
git clone https://github.com/Alain-L/quellog.git && cd quellog
go build -o quellog . && sudo install -m 755 quellog /usr/local/bin/quellog
```

## Usage

```bash
quellog /var/log/postgresql/*.log                        # Full report
quellog /var/log/postgresql/*.log --html -o report.html  # Interactive HTML report
quellog /var/log/postgresql/*.log --html --split 1d      # HTML report, one period per day
quellog /var/log/postgresql/*.log --sql-performance      # SQL analysis
quellog /var/log/postgresql/*.log -E fa-6K1G             # Drill into one event pattern
quellog /var/log/postgresql/*.log --last 1d              # Last 24 hours
quellog /var/log/postgresql/*.log -d mydb -u myuser      # Filter by db/user
quellog /var/log/postgresql/*.log --follow               # Live monitoring
quellog /var/log/postgresql/*.log --json                  # JSON export
```

See the [documentation](https://alain-l.github.io/quellog/) for the complete reference and how-to guides.

## License

PostgreSQL License. See [LICENSE](LICENSE).
