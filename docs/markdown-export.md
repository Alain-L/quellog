# Markdown Export

Export analysis results as Markdown for documentation and reports.

```bash
# Default report to markdown
quellog /var/log/postgresql/*.log --md > report.md

# SQL performance to markdown
quellog /var/log/postgresql/*.log --sql-performance --md > queries.md

# SQL overview to markdown
quellog /var/log/postgresql/*.log --sql-overview --md > overview.md

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

### SQL Performance (`--sql-performance --md`)

Exports detailed query performance analysis with temp files and locks:

```bash
quellog /var/log/postgresql/*.log --sql-performance --md > sql-analysis.md
```

Includes:
- Query load and duration histograms
- Top queries tables (slowest, most frequent, time consuming)
- TEMP FILES section with query breakdown
- LOCKS section with acquired/waiting queries

### SQL Overview (`--sql-overview --md`)

Exports query type breakdown by dimension:

```bash
quellog /var/log/postgresql/*.log --sql-overview --md > sql-overview.md
```

Includes:
- Query Category Summary (DML, DDL, TCL, etc.)
- Query Type Distribution (SELECT, INSERT, UPDATE, etc.)
- Breakdown by Database, User, Host, and Application

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

### CONNECTIONS & SESSIONS

Connection patterns and session duration analytics.

```markdown
## CONNECTIONS & SESSIONS

### Connection distribution

\`\`\`
00:00 - 00:58 | ■■■■■■■■■■■■■■■ 15
00:58 - 01:56 | ■■■■■ 5
01:56 - 02:55 | ■■■■ 4
\`\`\`

- **Connection count**: 36
- **Avg connections per hour**: 1.50
- **Disconnection count**: 23
- **Avg session time**: 1h14m7s
- **Avg concurrent sessions**: 13.45
- **Peak concurrent sessions**: 36 (at 05:50:00)

### Session Duration Statistics

- **Count**: 23
- **Min**: 7m10s
- **Max**: 2h20m16s
- **Avg**: 1h14m7s
- **Median**: 1h17m45s
- **Cumulated**: 28h24m31s

### Session duration distribution

\`\`\`
< 1s         | -
1s - 1min    | -
1min - 30min | ■ 1
30min - 2h   | ■■■■■■■■■■■■■■■■■■■ 19
2h - 5h      | ■■■ 3
> 5h         | -
\`\`\`

### Session Duration by User

| User | Sessions | Min | Max | Avg | Median | Cumulated |
|---|---:|---|---|---|---|---|
| app_user | 10 | 31m6s | 2h20m16s | 1h26m59s | 1h26m48s | 14h29m46s |
| readonly | 5 | 7m10s | 1h3m26s | 41m38s | 47m30s | 3h28m11s |
| batch_user | 3 | 1h21m46s | 2h0m30s | 1h42m45s | 1h46m0s | 5h8m16s |

### Session Duration by Database

| Database | Sessions | Min | Max | Avg | Median | Cumulated |
|---|---:|---|---|---|---|---|
| app_db | 16 | 7m10s | 2h20m16s | 1h19m42s | 1h22m18s | 21h15m18s |
| postgres | 4 | 42m46s | 1h44m45s | 1h0m8s | 46m31s | 4h0m32s |

### Session Duration by Host

| Host | Sessions | Min | Max | Avg | Median | Cumulated |
|---|---:|---|---|---|---|---|
| 192.168.1.100 | 3 | 31m6s | 1h13m10s | 50m40s | 45m30s | 2h32m1s |
| 10.0.1.50 | 2 | 1h17m45s | 1h52m46s | 1h35m16s | 1h35m16s | 3h10m31s |
```

### CLIENTS

Unique database entities with activity counts and cross-tabulations.

```markdown
## CLIENTS

- **Unique DBs**: 3
- **Unique Users**: 7
- **Unique Apps**: 9
- **Unique Hosts**: 37

### USERS

| User | Count | % |
|---|---:|---:|
| app_user | 1250 | 42.5% |
| readonly | 856 | 29.1% |
| batch_user | 423 | 14.4% |
| admin | 198 | 6.7% |
| analytics | 145 | 4.9% |
| backup_user | 52 | 1.8% |
| postgres | 16 | 0.5% |

### APPS

| App | Count | % |
|---|---:|---:|
| app_server | 1342 | 45.6% |
| psql | 687 | 23.4% |
| metabase | 456 | 15.5% |

### DATABASES

| Database | Count | % |
|---|---:|---:|
| app_db | 2456 | 83.5% |
| postgres | 342 | 11.6% |
| analytics_db | 142 | 4.8% |

### HOSTS

| Host | Count | % |
|---|---:|---:|
| 192.168.1.100 | 876 | 29.8% |
| 10.0.1.50 | 654 | 22.2% |
| 172.16.0.10 | 543 | 18.5% |

### USER × DATABASE

| User | Database | Count | % |
|---|---|---:|---:|
| app_user | app_db | 1856 | 63.1% |
| readonly | app_db | 543 | 18.5% |
| app_user | analytics_db | 123 | 4.2% |
| batch_user | app_db | 98 | 3.3% |

### USER × HOST

| User | Host | Count | % |
|---|---|---:|---:|
| app_user | 192.168.1.100 | 654 | 22.2% |
| readonly | 10.0.1.50 | 432 | 14.7% |
| app_user | 172.16.0.10 | 345 | 11.7% |
| batch_user | 10.0.1.51 | 234 | 8.0% |
```

## Common Uses

**Documentation**: Include in project docs or wiki pages.

**Incident reports**: Generate time-filtered reports for postmortems.

**Daily reports**: Automate with cron or CI/CD to track trends.

## Next Steps

- [JSON Export](json-export.md) for programmatic access
- [Filtering Logs](filtering-logs.md) to focus on specific subsets
- [SQL Analysis](sql-reports.md) for detailed query investigation
