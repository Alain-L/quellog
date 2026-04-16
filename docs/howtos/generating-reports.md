# Generating Reports

## Daily Report Script

```bash
#!/bin/bash
# daily_report.sh - Generate focused daily reports

YESTERDAY=$(date -d "yesterday" +%Y-%m-%d)
LOG_DIR="/var/log/postgresql"

# Summary report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --summary --events > "reports/summary_$YESTERDAY.txt"

# Performance report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --sql-performance --tempfiles --locks > "reports/performance_$YESTERDAY.txt"

# Maintenance report
quellog $LOG_DIR/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --maintenance --checkpoints > "reports/maintenance_$YESTERDAY.txt"
```

## HTML Report

```bash
# Full interactive report
quellog /var/log/postgresql/*.log --html --full

# Filtered to a specific database
quellog /var/log/postgresql/*.log --dbname production --html
```

## Markdown for Tickets

```bash
# Export to markdown for Jira/GitLab/GitHub
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --sql-performance --md > incident_report.md
```

!!! note "Work in progress"
    This how-to will be expanded with gomplate templates and automated reporting setups.
