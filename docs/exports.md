# Export Formats

quellog supports four export formats: JSON, YAML, Markdown, and HTML. All formats support section filtering and `--full` for comprehensive output.

```bash
quellog /var/log/postgresql/*.log --json                # JSON to stdout
quellog /var/log/postgresql/*.log --yaml -o report.yaml # YAML to file
quellog /var/log/postgresql/*.log --md -o report.md     # Markdown to file
quellog /var/log/postgresql/*.log --html                # HTML (auto-named)
```

## Multi-format export

Combine flags to produce several files in a single pass — parsing and analysis run only once.

```bash
quellog demo.json --html --md            # quellog-demo.html, quellog-demo.md
quellog demo.json --html --md --json     # quellog-demo.html, quellog-demo.md, quellog-demo.json
quellog logs/*.log --html --json         # quellog.html, quellog.json (multi-input)
```

Default filenames:

- Single input: `quellog-<stem>.<ext>` (stem = basename with the last extension stripped).
- Multiple inputs or stdin: `quellog.<ext>`.

Files are written in the current working directory. `-o`/`--output` is rejected when more than one format flag is set — every format goes to its default name. Multi-format is supported only for the full report, not for `--sql-detail`, `--event-detail`, `--sql-performance`, or `--sql-overview`.

## JSON

Structured output for automation, scripting, and integration with tools like `jq`.

```bash
quellog /var/log/postgresql/*.log --json
quellog /var/log/postgresql/*.log --json-compact  # Minified
quellog /var/log/postgresql/*.log --json --full    # All sections
```

### Section filtering

```bash
quellog logs/ --sql-performance --json  # SQL data only
quellog logs/ --summary --events --json # Multiple sections
```

Section flags control which data is included in all formats, including JSON.

### Using with jq

```bash
# Top 5 slowest queries
quellog logs/ --json | jq '.sql_performance.queries | sort_by(-.max_time_ms) | .[0:5] | .[] | {id, type, max_time_ms}'

# Error count
quellog logs/ --json | jq '.events[] | select(.type == "ERROR") | .count'

# Database list with counts
quellog logs/ --json | jq '.databases | sort_by(-.count) | .[] | "\(.name): \(.count)"'
```

For complete JSON structure reference, see the output of `quellog --json --full`.

## YAML

Same data structure as JSON, serialized as YAML. Ideal for template engines like [gomplate](https://docs.gomplate.ca/).

```bash
quellog /var/log/postgresql/*.log --yaml
quellog /var/log/postgresql/*.log --yaml --full
```

### gomplate integration

```bash
quellog logs/ --yaml --full > data.yaml
gomplate -d report=data.yaml -f template.tmpl -o report.txt
```

Example template:

```
PostgreSQL Log Report
=====================
Period: {{ (ds "report").summary.start_date }} to {{ (ds "report").summary.end_date }}
Total entries: {{ (ds "report").summary.total_logs }}

{{- if (ds "report").checkpoints }}
Checkpoints: {{ (ds "report").checkpoints.total_checkpoints }}
{{- end }}
```

## Markdown

Documentation-ready reports for wikis, Git repos, or ticketing systems.

```bash
quellog /var/log/postgresql/*.log --md
quellog /var/log/postgresql/*.log --md -o report.md
quellog /var/log/postgresql/*.log --md --full
```

### Section filtering

```bash
quellog logs/ --md --sql-summary      # SQL summary only
quellog logs/ --md --maintenance      # Vacuum/analyze only
quellog logs/ --md --checkpoints      # Checkpoint data only
```

## HTML

!!! tip "Try it now"
    **[Open the interactive demo →](https://alain-l.github.io/quellog/demo.html)** — Drop a PostgreSQL log file and explore the report. All processing happens locally in your browser.

Standalone, self-contained HTML reports with interactive charts and client-side filtering.

```bash
quellog /var/log/postgresql/postgres.log --html
# → Creates postgres.html

quellog /var/log/postgresql/*.log --html --full
# → Creates quellog_report.html

quellog /var/log/postgresql/*.log --html -o my_report.html
```

Features:

- **Self-contained**: Single HTML file, no server required
- **Interactive charts**: Zoomable time-series visualizations, double-click to reset zoom, expand to modal, PNG export
- **Click-to-detail modals**: Click any query row (sql-performance, locks, temp files) for a cross-analyzer detail panel; click any event row for the per-pattern panel with full message + occurrences-over-time chart + copy buttons
- **Client-side filtering**: Filter by database, user, application, host, and time range
- **Offline**: Works without internet connection
