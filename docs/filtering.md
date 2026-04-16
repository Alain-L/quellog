# Filtering

## Time-Based Filtering

### --begin / --end

```bash
quellog /var/log/postgresql/*.log --begin "2025-01-13 14:00:00"
quellog /var/log/postgresql/*.log --end "2025-01-13 15:00:00"

# Combined: 1-hour window
quellog /var/log/postgresql/*.log \
  --begin "2025-01-13 14:00:00" \
  --end "2025-01-13 15:00:00"
```

Format: `YYYY-MM-DD HH:MM:SS`. Use the same timezone as your PostgreSQL logs.

### --last (-L)

Analyze the last N duration from now.

```bash
quellog /var/log/postgresql/*.log --last 1h
quellog /var/log/postgresql/*.log --last 30m
quellog /var/log/postgresql/*.log --last 2h15m
```

Valid units: `h` (hours), `m` (minutes), `s` (seconds). Cannot be combined with `--begin`, `--end`, or `--window`.

## Attribute-Based Filtering

### --dbname (-d)

```bash
quellog /var/log/postgresql/*.log --dbname production
quellog /var/log/postgresql/*.log --dbname app_db --dbname analytics_db
```

### --dbuser (-u)

```bash
quellog /var/log/postgresql/*.log --dbuser app_user
quellog /var/log/postgresql/*.log --dbuser app_user --dbuser batch_processor
```

### --appname (-N)

```bash
quellog /var/log/postgresql/*.log --appname web_server
```

### --exclude-user (-U)

```bash
quellog /var/log/postgresql/*.log --exclude-user health_check --exclude-user powa
```

### Filter Logic

- Multiple values of the **same type** → OR (`--dbname db1 --dbname db2` matches db1 OR db2)
- **Different types** → AND (`--dbname production --dbuser app_user` matches both)

## Output Section Flags

Control which sections are displayed. Without flags, all sections are shown.

| Flag | Section | Details |
|------|---------|---------|
| `--full` | All sections with extended SQL analysis | |
| `--summary` | Summary | |
| `--events` | Events (severity distribution) | |
| `--errors` | Error Classes (SQLSTATE codes) | |
| `--sql-summary` | SQL Summary (default report) | |
| `--sql-performance` | SQL Performance (per-query details) | See [SQL Analysis](sql-reports.md) |
| `--sql-overview` | SQL Overview (query type breakdown) | See [SQL Analysis](sql-reports.md) |
| `--tempfiles` | Temporary Files | |
| `--locks` | Locks | |
| `--maintenance` | Maintenance (vacuum/analyze) | |
| `--checkpoints` | Checkpoints | |
| `--connections` | Connections + session analytics | |
| `--clients` | Clients (all entities, no top-10 limit) | |

Flags can be combined: `quellog logs/ --events --locks --sql-performance`

## --follow

Real-time monitoring with periodic refresh.

```bash
quellog --follow /var/log/postgresql/*.log
quellog --follow --interval 1m --last 1h /var/log/postgresql/*.log
```

See [Continuous Monitoring](howtos/continuous-monitoring.md) for advanced setups.
