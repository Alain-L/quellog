# YAML Export

Export analysis results in YAML format, ideal for template engines like [gomplate](https://docs.gomplate.ca/).

```bash
# YAML to stdout
quellog /var/log/postgresql/postgres.log --yaml

# Save to file
quellog /var/log/postgresql/postgres.log --yaml --output report.yaml

# Comprehensive report with all sections
quellog /var/log/postgresql/postgres.log --yaml --full
```

## Data Structure

The YAML output uses the same data structure as the [JSON export](json-export.md), with identical keys and values. Only the serialization format differs.

```yaml
summary:
  start_date: "2025-01-15 00:00:01"
  end_date: "2025-01-15 23:59:58"
  duration: 23h59m57s
  total_logs: 35394
  throughput: 1.47 entries/s
checkpoints:
  total_checkpoints: 48
  types:
    time:
      count: 42
    wal:
      count: 6
```

## gomplate Integration

Use the YAML output as a datasource for gomplate templates:

```bash
# Generate YAML data
quellog /var/log/postgresql/*.log --yaml --full > data.yaml

# Use with gomplate
gomplate -d report=data.yaml -f template.tmpl -o report.txt
```

Example gomplate template:

```
PostgreSQL Log Report
=====================
Period: {{ (ds "report").summary.start_date }} to {{ (ds "report").summary.end_date }}
Total entries: {{ (ds "report").summary.total_logs }}
Throughput: {{ (ds "report").summary.throughput }}

{{- if (ds "report").checkpoints }}
Checkpoints: {{ (ds "report").checkpoints.total_checkpoints }}
{{- end }}
```

## Section Filtering

Use section flags to export only specific data:

```bash
quellog logs/ --yaml --sql-summary    # SQL performance only
quellog logs/ --yaml --maintenance    # Vacuum/analyze only
quellog logs/ --yaml --checkpoints   # Checkpoint data only
```

## See Also

- [JSON Export](json-export.md) - Same structure in JSON format
- [Markdown Export](markdown-export.md) - Documentation-ready reports
- [HTML Export](html-export.md) - Interactive visual reports
