# HTML Export

!!! tip "Try it now"
    **[Open the interactive demo →](../demo.html)** — Drop a PostgreSQL log file and explore the report. All processing happens locally in your browser; your data never leaves your machine.

Generate standalone HTML reports that can be viewed in any browser without a server.

```bash
# Generate HTML report (creates report named after input file)
quellog /var/log/postgresql/postgres.log --html
# → Creates postgres.html

# With filters
quellog /var/log/postgresql/*.log --dbname production --html
# → Creates quellog_report.html
```

## Features

- **Self-contained**: Single HTML file with embedded data and viewer
- **Interactive charts**: Zoomable time-series visualizations
- **Client-side filtering**: Filter by database, user, application, host, and time range
- **No dependencies**: Works offline, no server required

## How It Works

The HTML report embeds zstd-compressed JSON data, decoded client-side via [fzstd](https://github.com/101arrowz/fzstd). The same analysis data available with `--json` is rendered in an interactive interface.

## Comprehensive Reports

Use `--full` to include all sections with detailed SQL analysis:

```bash
quellog /var/log/postgresql/*.log --html --full
```

## See Also

- [JSON Export](json-export.md) - Structured data for automation
- [Markdown Export](markdown-export.md) - Documentation-ready reports
