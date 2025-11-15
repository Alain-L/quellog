# Markdown Export

Export analysis results as Markdown for documentation and reports.

```bash
# Default report to markdown
quellog /var/log/postgresql/*.log --md > report.md

# SQL summary to markdown
quellog /var/log/postgresql/*.log --sql-summary --md > queries.md

# SQL detail to markdown
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3 --md > query-detail.md

# With filters
quellog /var/log/postgresql/*.log --dbname production --last 1h --md > recent.md
```

## Export Modes

### Default Report (`--md`)

Exports comprehensive analysis of all metrics:

```bash
quellog /var/log/postgresql/*.log --md > full-report.md
```

### SQL Summary (`--sql-summary --md`)

Exports query performance analysis with temp files and locks:

```bash
quellog /var/log/postgresql/*.log --sql-summary --md > sql-analysis.md
```

Includes:
- Query load and duration histograms
- Top queries tables (slowest, most frequent, time consuming)
- TEMP FILES section with query breakdown
- LOCKS section with acquired/waiting queries

### SQL Detail (`--sql-detail <id> --md`)

Exports detailed analysis for specific queries:

```bash
quellog /var/log/postgresql/*.log --sql-detail se-N2d0E3 --md > query-se-N2d0E3.md
```

Includes:
- Execution count histogram
- TIME section with cumulative time and duration distribution
- TEMP FILES section with size and count histograms
- LOCKS section with wait statistics
- Formatted normalized query
- Example query

## Markdown Structure

The markdown export contains all analysis sections in a readable format.

### SUMMARY

```markdown
## SUMMARY

This _quellog_ report summarizes **95,139** log entries collected between 10 Dec 2024, 00:00 (CET) — 11 Dec 2024, 00:00 (CET), spanning 1d of activity.
```

### SQL PERFORMANCE

Query statistics with histograms and tables.

```markdown
## SQL PERFORMANCE

### Query load distribution

\`\`\`
02:11 - 05:36 |  4 m
05:36 - 09:02 | ■ 291 m
09:02 - 12:27 | ■■■■■■■■■■■■■■■■■■■■■■■■■■■ 4602 m
\`\`\`

|  |  |  |  |
|---|---:|---|---:|
| Total query duration | 11d 4h 33m | Total queries parsed | 485 |
| Total unique queries | 5 | Top 1% slow queries | 5 |

### Query Statistics

**Slowest queries (top 10)**

| SQLID | Max | Avg | Count | Query |
|---|---:|---:|---:|---|
| se-N2d0E3 | 1h 12m 50s | 35m 05s | 456 | select node.id as id from... |

**Most frequent queries (top 10)**

**Most time consuming queries (top 10)**
```

### EVENTS

```markdown
## EVENTS

|  |  |
|---|---:|
| ERROR | 28236 |
| LOG | 5943 |
| FATAL | 1215 |
```

### ERROR CLASSES

```markdown
## ERROR CLASSES

| Class | Description | Count |
|---|---|---:|
| 42 | Syntax Error or Access Rule Violation | 4 |
| 23 | Integrity Constraint Violation | 3 |
| 22 | Data Exception | 2 |
```

### TEMP FILES

Temporary file statistics with histogram.

```markdown
## TEMP FILES

### Temp file distribution

\`\`\`
08:09 - 10:26 | ■■■■■■■■■■■ 11 GB
10:26 - 12:44 | ■■■■■■■■■■■■■■■■■■■■■■■■ 24 GB
\`\`\`

- **Temp file messages**: 427
- **Cumulative temp file size**: 102.47 GB
- **Average temp file size**: 245.74 MB
```

### LOCKS

Lock statistics with query tables.

```markdown
## LOCKS

- **Total lock events**: 834
- **Waiting events**: 417
- **Acquired events**: 417
- **Average wait time**: 1507.58 ms
- **Total wait time**: 1257.32 s

### Lock Types

| Lock Type | Count | Percentage |
|---|---:|---:|
| ShareLock | 638 | 76.5% |

### Acquired Locks by Query

| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |
|---|---|---:|---:|---:|
| up-bG8qBk | update alf_node set version = ? , transaction_id = ? ... | 259 | 2879.31 | 745740.59 |
```

### MAINTENANCE

```markdown
## MAINTENANCE

- **Automatic vacuum count**: 668
- **Automatic analyze count**: 353

### Top automatic vacuum operations per table

| Table | Count | % of total | Recovered |
|---|---:|---:|---:|
| alfresco.public.alf_lock | 422 | 63.17% | 0 B |
```

### CHECKPOINTS

```markdown
## CHECKPOINTS

### Checkpoint distribution

\`\`\`
00:00 - 04:00 | ■■■■■■■■■■■■■■■■■■■■■■■■ 48
04:00 - 08:00 | ■■■■■■■■■■■■■■■■■■■■■■■ 47
\`\`\`

- **Checkpoint count**: 258
- **Avg checkpoint write time**: 4m23s

### Checkpoint types

|  |  |  |  |
|------|------:|--:|-----:|
| time | 257 | 99.6% | 10.71/h |
```

### CLIENTS

```markdown
## CLIENTS

- **Unique DBs**: 2
- **Unique Users**: 3
- **Unique Apps**: 0
- **Unique Hosts**: 0

### USERS

- postgres
- app_user

### DATABASES

- postgres
- app_db
```

## Common Uses

**Documentation**: Include in project docs or wiki pages.

**Incident reports**: Generate time-filtered reports for postmortems.

**Daily reports**: Automate with cron or CI/CD to track trends.

## Next Steps

- [JSON Export](json-export.md) for programmatic access
- [Filtering Logs](filtering-logs.md) to focus on specific subsets
- [SQL Analysis](sql-reports.md) for detailed query investigation
