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

Format: `YYYY-MM-DD HH:MM:SS`, no timezone. The bound is matched against each
entry's **wall clock** — `--begin "2025-01-13 14:00:00"` starts at the moment
the log clock reads 14:00, whatever timezone the log was written in — so just
copy the time as it appears in your logs.

### --last (-L)

Analyze the last N duration from now.

```bash
quellog /var/log/postgresql/*.log --last 1h
quellog /var/log/postgresql/*.log --last 30m
quellog /var/log/postgresql/*.log --last 2h15m
quellog /var/log/postgresql/*.log --last 1d
quellog /var/log/postgresql/*.log --last 5y
```

Valid units: `s` (seconds), `m` (minutes), `h` (hours), `d` (days),
`w` (weeks), `y` (years). Anchored to the current system time (now), so
`--last` is meant for recent/live logs — on older archives it may filter
out everything; use `--begin`/`--end` there instead. Cannot be combined
with `--begin`, `--end`, or `--window`.

### --window (-W)

```bash
quellog /var/log/postgresql/*.log --begin "2026-01-01 09:00:00" --window 30m
quellog /var/log/postgresql/*.log --end "2026-01-01 10:00:00" --window 1h
```

Derives the missing bound from `--begin` (+ window) or `--end` (− window),
using the same unit suffixes as `--last`. `--begin`, `--end`, and
`--window` cannot all three be used together.

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
- **`--exclude-user` takes precedence** — a user excluded by `-U` is dropped even if also named in `--dbuser`

## Output Section Flags

Control which sections are displayed. Without flags, all sections are shown.

| Flag | Section | Details |
|------|---------|---------|
| `--full` | All sections with extended SQL analysis | |
| `--summary` | Summary | |
| `--events` | Events (severity distribution) | |
| `--errors` | Events, filtered to error severities (ERROR/FATAL/PANIC) | |
| `--sql-summary` | SQL Summary (default report) | |
| `--sql-performance` | SQL Performance (per-query details) | See [SQL Analysis](sql-reports.md) |
| `--sql-overview` | SQL Overview (query type breakdown) | See [SQL Analysis](sql-reports.md) |
| `--tempfiles` | Temporary Files | |
| `--locks` | Locks | |
| `--maintenance` | Autovacuum + autoanalyze (elapsed, dead-not-removable, buffer/WAL) | |
| `--checkpoints` | Checkpoints | |
| `--connections` | Connections + session analytics | |
| `--clients` | Clients (all entities, no top-10 limit) | |
| `--server` | Server lifecycle (starts/shutdowns, replication sub-zone) | |

Flags can be combined: `quellog logs/ --events --locks --sql-performance`

## --follow

Real-time monitoring with periodic refresh.

```bash
quellog --follow /var/log/postgresql/*.log
quellog --follow --interval 1m --last 1h /var/log/postgresql/*.log
```

See [Continuous Monitoring](howtos/continuous-monitoring.md) for advanced setups.
