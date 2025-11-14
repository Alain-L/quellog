# Markdown Export

quellog can export analysis results as Markdown for documentation, reports, and collaborative analysis.

## Basic Usage

Add the `--md` flag to export results as Markdown:

```bash
# Export to stdout
quellog /var/log/postgresql/*.log --md

# Save to file
quellog /var/log/postgresql/*.log --md > report.md

# Combine with filters
quellog /var/log/postgresql/*.log \
  --dbname production \
  --begin "2025-01-13 00:00:00" \
  --end "2025-01-14 00:00:00" \
  --md > daily_report.md
```

## Output Format

The Markdown export includes all analysis sections in a structured, readable format:

```markdown
# PostgreSQL Log Analysis Report

**Analysis date**: 2025-01-13
**Log period**: 2025-01-13 00:00:00 to 2025-01-13 23:59:59
**Duration**: 23h59m59s

## Summary

- **Total entries**: 1,234
- **Processing time**: 0.15 s
- **Throughput**: 8,227 entries/s

## SQL Performance

### Overview

- **Total query duration**: 8m 46s
- **Total queries**: 456
- **Unique queries**: 127
- **Query median duration**: 145 ms
- **Query 99% duration**: 1.87 s

### Top Queries by Total Time

#### 1. se-a1b2c3d

**Metrics**:
- Total time: 45.67 s
- Executions: 23
- Average: 1.99 s
- Max: 3.45 s

**Query**:
```sql
SELECT * FROM orders o
JOIN customers c ON o.customer_id = c.id
WHERE o.created_at > NOW() - INTERVAL '7 days'
ORDER BY o.created_at DESC
```

#### 2. se-x4y5z6w

**Metrics**:
- Total time: 32.14 s
- Executions: 156
- Average: 206 ms
- Max: 1.23 s

**Query**:
```sql
SELECT id, email, name FROM users WHERE id = $1
```

... (continues with all sections)
```

## Sections Included

Markdown export includes all report sections:

1. Summary
2. SQL Performance (with top queries)
3. Events (severity distribution)
4. Temporary Files (with top queries)
5. Locks (with statistics)
6. Maintenance (vacuum/analyze)
7. Checkpoints
8. Connections & Sessions
9. Clients (unique entities)

## Use Cases

### 1. Documentation

Include PostgreSQL analysis in project documentation:

```bash
# Generate report for documentation
quellog /var/log/postgresql/*.log --md > docs/database-performance.md
```

Add to your docs:

```markdown
# Database Performance Analysis

{{include(database-performance.md)}}
```

### 2. Incident Reports

Create detailed incident reports:

```bash
# Incident at 2:30 PM on Jan 13
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00" \
  --md > incident_2025-01-13.md
```

Edit the file to add context:

```markdown
# Incident Report: Slowdown on 2025-01-13

## Background

At 2:30 PM, users reported slow page loads...

## Database Analysis

{{auto-generated content from quellog}}

## Root Cause

Based on the analysis above, the root cause was...

## Resolution

We applied the following fixes:
1. Added index on `orders.created_at`
2. Increased `work_mem` to 64MB
...
```

### 3. Daily Reports

Generate daily markdown reports:

```bash
#!/bin/bash
# daily_report.sh

YESTERDAY=$(date -d "yesterday" +%Y-%m-%d)

# Generate report
quellog /var/log/postgresql/*.log \
  --begin "$YESTERDAY 00:00:00" \
  --end "$YESTERDAY 23:59:59" \
  --md > "reports/$YESTERDAY.md"

# Add header
sed -i "1i # PostgreSQL Daily Report - $YESTERDAY\n" "reports/$YESTERDAY.md"
```

### 4. Team Collaboration

Share reports via:

- **Git**: Commit reports to repository
- **Wiki**: Post to internal wiki
- **Slack/Teams**: Paste as formatted message
- **Email**: Send as HTML (convert with pandoc)

### 5. Trending Analysis

Track performance trends over time:

```bash
# Generate weekly reports
for day in $(seq 0 6); do
  DATE=$(date -d "$day days ago" +%Y-%m-%d)
  quellog /var/log/postgresql/*.log \
    --begin "$DATE 00:00:00" \
    --end "$DATE 23:59:59" \
    --md > "weekly_reports/$DATE.md"
done

# Compare with diff
diff weekly_reports/2025-01-12.md weekly_reports/2025-01-13.md
```

## Converting Markdown

### To HTML

Using [pandoc](https://pandoc.org/):

```bash
# Basic HTML
pandoc report.md -o report.html

# With CSS styling
pandoc report.md -o report.html --css style.css

# Standalone HTML with ToC
pandoc report.md -s --toc -o report.html
```

### To PDF

```bash
# Requires LaTeX (via pandoc)
pandoc report.md -o report.pdf

# With custom template
pandoc report.md --template=custom.tex -o report.pdf
```

### To DOCX

```bash
# Microsoft Word format
pandoc report.md -o report.docx
```

## Combining with Section Filters

Section filters don't affect Markdown output (full report is always exported):

```bash
# Section flags are ignored for Markdown
quellog /var/log/postgresql/*.log --sql-performance --md > full_report.md
```

To export only specific sections, use text output and redirect:

```bash
# Export only SQL performance as text
quellog /var/log/postgresql/*.log --sql-performance > sql_only.txt
```

## Combining with Log Filters

Filter which logs are analyzed, then export to Markdown:

```bash
# Production database only
quellog /var/log/postgresql/*.log \
  --dbname production \
  --md > prod_report.md

# Specific user, yesterday
quellog /var/log/postgresql/*.log \
  --dbuser app_user \
  --begin "2025-01-12 00:00:00" \
  --end "2025-01-12 23:59:59" \
  --md > user_activity_2025-01-12.md

# Peak hours, exclude monitoring
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 12:00:00" \
  --end "2025-01-13 14:00:00" \
  --exclude-user health_check \
  --md > peak_hours_report.md
```

## Customizing Output

### Adding Context

Edit generated Markdown to add context:

```markdown
# PostgreSQL Analysis - Q1 2025

## Executive Summary

This report analyzes PostgreSQL performance for Q1 2025...

## Technical Analysis

<!-- Auto-generated from quellog -->
{{quellog output}}
<!-- End auto-generated -->

## Recommendations

Based on the analysis:
1. **Slow Queries**: Optimize queries se-a1b2c3d and se-x4y5z6w
2. **Tempfiles**: Increase work_mem to reduce disk I/O
3. **Locks**: Review transaction isolation levels
```

### Combining Multiple Analyses

```bash
# Generate reports for multiple databases
quellog /var/log/postgresql/*.log --dbname db1 --md > db1.md
quellog /var/log/postgresql/*.log --dbname db2 --md > db2.md

# Combine into one document
cat > combined.md <<EOF
# Multi-Database Analysis

## Database 1

$(cat db1.md)

## Database 2

$(cat db2.md)
EOF
```

## Version Control

Track reports in Git to see changes over time:

```bash
# Initialize reports directory
mkdir reports
cd reports
git init

# Generate daily reports
./daily_report.sh

# Commit
git add *.md
git commit -m "Daily report for $(date +%Y-%m-%d)"

# View changes
git diff HEAD~1 HEAD
```

Example diff:

```diff
@@ -10,7 +10,7 @@
 ### Overview

-- **Total query duration**: 8m 46s
+- **Total query duration**: 12m 31s
-- **Total queries**: 456
+- **Total queries**: 687
```

## Automation Examples

### GitHub Actions

```yaml
# .github/workflows/daily-report.yml
name: Daily PostgreSQL Report

on:
  schedule:
    - cron: '0 6 * * *'  # 6 AM UTC daily

jobs:
  generate-report:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Download logs
        run: |
          scp postgres@db-server:/var/log/postgresql/*.log logs/

      - name: Install quellog
        run: |
          wget https://github.com/Alain-L/quellog/releases/latest/download/quellog_Linux_x86_64.tar.gz
          tar -xzf quellog_Linux_x86_64.tar.gz
          sudo mv quellog /usr/local/bin/

      - name: Generate report
        run: |
          DATE=$(date -d "yesterday" +%Y-%m-%d)
          quellog logs/*.log \
            --begin "$DATE 00:00:00" \
            --end "$DATE 23:59:59" \
            --md > reports/$DATE.md

      - name: Commit report
        run: |
          git config user.name "GitHub Actions"
          git config user.email "actions@github.com"
          git add reports/
          git commit -m "Daily report for $(date -d yesterday +%Y-%m-%d)"
          git push
```

### Cron Job

```bash
# /etc/cron.d/postgresql-report
0 6 * * * postgres /opt/scripts/generate_report.sh
```

```bash
#!/bin/bash
# /opt/scripts/generate_report.sh

DATE=$(date -d "yesterday" +%Y-%m-%d)
LOG_DIR="/var/log/postgresql"
REPORT_DIR="/var/www/reports"

# Generate Markdown
quellog $LOG_DIR/*.log \
  --begin "$DATE 00:00:00" \
  --end "$DATE 23:59:59" \
  --md > "$REPORT_DIR/$DATE.md"

# Convert to HTML
pandoc "$REPORT_DIR/$DATE.md" -s --toc -o "$REPORT_DIR/$DATE.html"

# Cleanup old reports (keep 30 days)
find $REPORT_DIR -name "*.md" -mtime +30 -delete
find $REPORT_DIR -name "*.html" -mtime +30 -delete
```

## Markdown Compatibility

quellog's Markdown output follows GitHub Flavored Markdown (GFM) and is compatible with:

- **GitHub/GitLab**: Renders correctly in repository browsers
- **Confluence**: Import via Markdown macro
- **Notion**: Import as Markdown file
- **Obsidian**: Open directly as note
- **VS Code**: Preview with Markdown extension
- **Slack**: Paste as formatted message
- **Discord**: Paste as formatted message

## Comparison with JSON

| Feature | Markdown | JSON |
|---------|----------|------|
| **Human-readable** | ✅ Yes | ❌ No |
| **Machine-parseable** | ⚠️ Limited | ✅ Yes |
| **Version control friendly** | ✅ Yes | ⚠️ Verbose diffs |
| **Documentation** | ✅ Perfect | ❌ Not suitable |
| **Automation** | ⚠️ Limited | ✅ Perfect |
| **Collaboration** | ✅ Great | ❌ Poor |

**Use Markdown when**:

- Creating documentation
- Sharing with non-technical stakeholders
- Tracking changes over time in Git
- Generating reports for human consumption

**Use JSON when**:

- Integrating with monitoring tools
- Building custom dashboards
- Automating alerts
- Processing with scripts

## Next Steps

- [JSON Export](json-export.md) for programmatic access
- [SQL Analysis](sql-reports.md) for detailed query investigation
- [Filtering](filtering-logs.md) to focus reports on specific subsets
