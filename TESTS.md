# Test Strategy for quellog

This document outlines the plan for comprehensive integration testing using real PostgreSQL logs generated via Docker containers.

## Approach

1. **Generate once, commit as fixtures**: Use Docker to generate realistic logs from controlled scenarios
2. **Deterministic counters**: Every metric has a known expected value
3. **Multi-format**: Same scenario produces stderr, CSV, JSON, and syslog outputs
4. **Static fixtures**: Generated logs are committed to the repo and used as test fixtures

## Fixture Structure

```
test/
├── generate/                       # Docker fixture generation (gitignored)
│   ├── docker-compose.yml          # PostgreSQL + syslog containers
│   ├── postgresql.conf             # Tuned for logging everything
│   ├── scenarios/
│   │   ├── 00_setup.sql            # Users, databases, schema
│   │   ├── 01_connections.sql      # Connection/disconnection patterns
│   │   ├── 02_queries.sql          # DML with varied durations
│   │   ├── 03_ddl.sql              # CREATE/DROP/ALTER
│   │   ├── 04_transactions.sql     # BEGIN/COMMIT/ROLLBACK/SAVEPOINT
│   │   ├── 05_maintenance.sql      # VACUUM/ANALYZE (manual)
│   │   ├── 06_tempfiles.sql        # Queries exceeding work_mem
│   │   ├── 07_locks.sql            # Lock waits and deadlocks
│   │   ├── 08_errors.sql           # Constraint violations, syntax errors
│   │   └── 09_prepared.sql         # PREPARE/EXECUTE/DEALLOCATE
│   └── generate_fixtures.sh        # Orchestration script
├── testdata/
│   ├── stderr.log                  # PostgreSQL stderr format
│   ├── csvlog.csv                  # PostgreSQL CSV format
│   ├── jsonlog.json                # PostgreSQL JSON format (PG15+)
│   ├── syslog.log                  # Syslog ISO timestamp format
│   ├── syslog_bsd.log              # Syslog BSD format (Jan 15 ...)
│   ├── syslog_rfc5424.log          # Syslog RFC5424 format
│   ├── golden.json                 # Golden file for regression tests
│   └── sql_*.log                   # SQL normalization test fixtures
├── summary_test.go                 # Golden file regression + JSON structure
├── comprehensive_test.go           # Format parity tests
├── exhaustive_test.go              # Exhaustive combinatorial tests
├── tempfile_*.go                   # Temp file analysis tests
└── sql_*.go                        # SQL normalization tests
```

### Fixture Files

| File | Format | Description |
|------|--------|-------------|
| `stderr.log` | stderr | PostgreSQL text log with custom prefix |
| `csvlog.csv` | CSV | PostgreSQL CSV log format |
| `jsonlog.json` | JSON | PostgreSQL JSON log format (PG15+) |
| `syslog.log` | syslog | ISO timestamp format via syslog-ng |
| `syslog_bsd.log` | syslog | BSD format (Jan 15 14:30:00) |
| `syslog_rfc5424.log` | syslog | RFC5424 format with structured data |
| `golden.json` | JSON | Expected output for regression tests |

### Generation

```bash
cd test/generate
./generate_fixtures.sh
```

Requirements: Docker, Docker Compose, psql client, ~30 minutes for full generation.

---

## Comprehensive Scenario - Target Counters

### Global

| Metric | Value | Notes |
|--------|-------|-------|
| Duration | 30 min | Enough for autovacuum cycles |
| Total log entries | ~850 | Sum of all events below |

### Connections / Sessions

| Metric | Value | Detail |
|--------|-------|--------|
| Connections | 25 | `connection received` |
| Disconnections | 23 | 2 sessions stay open |
| **Users** | **5** | |
| - app_user | 12 | Main application user |
| - admin | 5 | Administrative tasks |
| - readonly | 4 | Read-only queries |
| - batch | 3 | Batch jobs |
| - analyst | 1 | Analytics queries |
| **Databases** | **3** | |
| - app_db | 15 | Main application database |
| - analytics | 7 | Analytics database |
| - postgres | 3 | System database |
| **Applications** | **6** | |
| - webapp | 8 | Web application |
| - psql | 6 | Interactive sessions |
| - pgadmin | 4 | Admin UI |
| - batch_job | 3 | Scheduled jobs |
| - metabase | 2 | BI tool |
| - pg_dump | 2 | Backups |
| **Hosts** | **4** | |
| - 192.168.1.x | 10 | Internal network |
| - 10.0.0.x | 8 | Docker network |
| - 172.16.x.x | 5 | VPN clients |
| - localhost | 2 | Local connections |

### SQL Queries

| Metric | Value | Detail |
|--------|-------|--------|
| **Total queries** | **300** | With duration logged |
| **Unique queries** | **45** | Distinct normalized patterns |

#### By Type

| Type | Count | Notes |
|------|-------|-------|
| SELECT | 150 | Simple, JOIN, subquery, CTE |
| INSERT | 50 | Single row, multi-row, RETURNING |
| UPDATE | 40 | Simple, complex WHERE |
| DELETE | 15 | With and without WHERE |
| BEGIN | 15 | |
| COMMIT | 12 | |
| ROLLBACK | 3 | |
| SAVEPOINT | 5 | Nested transactions |
| CREATE | 8 | TABLE(3), INDEX(3), VIEW(1), FUNCTION(1) |
| DROP | 4 | TABLE(2), INDEX(2) |
| ALTER | 3 | ADD COLUMN, DROP COLUMN, RENAME |
| TRUNCATE | 2 | |
| VACUUM | 3 | Manual |
| ANALYZE | 3 | Manual |
| EXPLAIN | 5 | EXPLAIN ANALYZE |
| PREPARE | 6 | |
| EXECUTE | 12 | ~2 per PREPARE |
| DEALLOCATE | 4 | |
| COPY | 2 | TO and FROM |

#### By Category

| Category | Count | Types included |
|----------|-------|----------------|
| DML | 255 | SELECT, INSERT, UPDATE, DELETE |
| DDL | 15 | CREATE, DROP, ALTER, TRUNCATE |
| TCL | 35 | BEGIN, COMMIT, ROLLBACK, SAVEPOINT |
| UTILITY | 18 | VACUUM, ANALYZE, EXPLAIN, COPY |
| PREPARED | 22 | PREPARE, EXECUTE, DEALLOCATE |

#### Duration Distribution

| Bucket | Count | Notes |
|--------|-------|-------|
| < 1 ms | 80 | Trivial queries |
| < 10 ms | 150 | Normal queries |
| < 100 ms | 50 | Moderate queries |
| < 1 s | 18 | Slow queries |
| >= 1 s | 2 | Very slow (intentional) |

| Stat | Value |
|------|-------|
| Min | 0 ms |
| Max | 2500 ms |
| Median | ~5 ms |
| P99 | ~500 ms |

### Maintenance

| Metric | Value | Notes |
|--------|-------|-------|
| **Autovacuum** | **4** | Triggered by activity |
| **Auto-analyze** | **3** | |
| **Manual VACUUM** | **3** | Explicit commands |
| **Manual ANALYZE** | **3** | Explicit commands |
| **Checkpoints** | **5** | |
| - time-based | 3 | checkpoint_timeout triggered |
| - wal-based | 2 | max_wal_size triggered |

### Temp Files

| Metric | Value | Notes |
|--------|-------|-------|
| Count | 4 | Events logged |
| Total size | ~100 MB | |
| Distinct queries | 3 | One query executed twice |

### Locks

| Metric | Value | Notes |
|--------|-------|-------|
| Lock waits | 8 | `still waiting for` |
| Locks acquired | 6 | `acquired after waiting` |
| Deadlocks | 2 | `deadlock detected` |

### Events / Errors

| Level | Count | Detail |
|-------|-------|--------|
| LOG | ~700 | Normal operations |
| ERROR | 15 | Constraints(5), syntax(3), permission(3), other(4) |
| WARNING | 8 | Non-fatal issues |
| FATAL | 2 | Auth failure(1), connection limit(1) |
| PANIC | 0 | None (we want a healthy DB) |

---

## PostgreSQL Configuration

Tuned to generate the expected maintenance events within 30 minutes:

```conf
# =============================================================================
# LOGGING - Capture everything
# =============================================================================
log_destination = 'stderr,csvlog'
logging_collector = on
log_directory = '/var/log/postgresql'
log_filename = 'postgresql'
log_file_mode = 0644

# What to log
log_min_duration_statement = 0      # All queries with duration
log_statement = 'none'              # Avoid duplicates
log_checkpoints = on
log_connections = on
log_disconnections = on
log_lock_waits = on
deadlock_timeout = 1s
log_temp_files = 0                  # All temp files

# Prefix for stderr (CSV/JSON have fixed formats)
# Recommended format from docs/postgresql-setup.md
log_line_prefix = '%t [%p] %e: db=%d,user=%u,app=%a,client=%h '

# =============================================================================
# AUTOVACUUM - Aggressive settings for 30-min test window
# =============================================================================
autovacuum = on
log_autovacuum_min_duration = 0     # Log all autovacuum
autovacuum_naptime = 1min           # Check every minute (default 1min)
autovacuum_vacuum_threshold = 20    # Trigger after 20 dead tuples (default 50)
autovacuum_vacuum_scale_factor = 0.01  # 1% of table (default 20%)
autovacuum_analyze_threshold = 10   # Trigger after 10 changes (default 50)
autovacuum_analyze_scale_factor = 0.01 # 1% of table (default 10%)

# =============================================================================
# CHECKPOINTS - Frequent for testing
# =============================================================================
checkpoint_timeout = 5min           # Time-based every 5 min (default 5min)
max_wal_size = 64MB                 # WAL-based with moderate activity
checkpoint_completion_target = 0.5  # Faster checkpoints
checkpoint_warning = 30s

# =============================================================================
# TEMP FILES - Low threshold to trigger easily
# =============================================================================
work_mem = 1MB                      # Low value to trigger temp files
temp_file_limit = -1                # No limit

# =============================================================================
# CONNECTIONS - Allow enough for test scenarios
# =============================================================================
max_connections = 50
```

---

## Docker Compose Setup

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: testpass
      POSTGRES_DB: postgres
    volumes:
      - ./postgresql.conf:/etc/postgresql/postgresql.conf:ro
      - ./scenarios:/docker-entrypoint-initdb.d:ro
      - pg_logs:/var/log/postgresql
    command: postgres -c config_file=/etc/postgresql/postgresql.conf
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  syslog:
    image: balabit/syslog-ng:latest
    volumes:
      - syslog_logs:/var/log
    ports:
      - "514:514/udp"

volumes:
  pg_logs:
  syslog_logs:
```

---

## Expected Output File (expected.json)

```json
{
  "_meta": {
    "generator": "Docker PostgreSQL 17",
    "scenario": "comprehensive",
    "duration_minutes": 30
  },
  "summary": {
    "total_entries": 850
  },
  "connections": {
    "connections": 25,
    "disconnections": 23,
    "unique_users": 5,
    "unique_databases": 3,
    "unique_apps": 6,
    "unique_hosts": 4,
    "users": {
      "app_user": 12,
      "admin": 5,
      "readonly": 4,
      "batch": 3,
      "analyst": 1
    },
    "databases": {
      "app_db": 15,
      "analytics": 7,
      "postgres": 3
    }
  },
  "sql": {
    "total_queries": 300,
    "unique_queries": 45,
    "by_type": {
      "SELECT": 150,
      "INSERT": 50,
      "UPDATE": 40,
      "DELETE": 15,
      "BEGIN": 15,
      "COMMIT": 12,
      "ROLLBACK": 3,
      "SAVEPOINT": 5,
      "CREATE": 8,
      "DROP": 4,
      "ALTER": 3,
      "TRUNCATE": 2,
      "VACUUM": 3,
      "ANALYZE": 3,
      "EXPLAIN": 5,
      "PREPARE": 6,
      "EXECUTE": 12,
      "DEALLOCATE": 4,
      "COPY": 2
    },
    "by_category": {
      "DML": 255,
      "DDL": 15,
      "TCL": 35,
      "UTILITY": 18,
      "PREPARED": 22
    },
    "duration_distribution": {
      "lt_1ms": 80,
      "lt_10ms": 150,
      "lt_100ms": 50,
      "lt_1s": 18,
      "gte_1s": 2
    }
  },
  "maintenance": {
    "autovacuum": 4,
    "autoanalyze": 3,
    "manual_vacuum": 3,
    "manual_analyze": 3,
    "checkpoints": 5,
    "checkpoints_time": 3,
    "checkpoints_wal": 2
  },
  "temp_files": {
    "count": 4,
    "total_size_mb": 100,
    "distinct_queries": 3
  },
  "locks": {
    "lock_waits": 8,
    "locks_acquired": 6,
    "deadlocks": 2
  },
  "events": {
    "LOG": 700,
    "ERROR": 15,
    "WARNING": 8,
    "FATAL": 2,
    "PANIC": 0
  },
  "errors": {
    "constraint_violation": 5,
    "syntax_error": 3,
    "permission_denied": 3,
    "other": 4
  }
}
```

---

## Test Suite

### Test Files

| File | Description |
|------|-------------|
| `summary_test.go` | Golden file regression test (JSON output vs `golden.json`) |
| `flags_test.go` | CLI flag combinations, output formats, section filters |
| `comprehensive_test.go` | Format parity tests across all input formats |
| `exhaustive_test.go` | Exhaustive combinatorial testing (216 combinations) |

### comprehensive_test.go

| Test | Description |
|------|-------------|
| **TestComprehensiveFormatParity** | All formats (stderr, CSV, JSON, syslog_bsd) produce identical metrics |
| **TestSyslogAllFormats** | All 3 syslog variants (BSD, ISO, RFC5424) produce identical metrics |
| **TestComprehensiveCompression** | Compressed files (gzip, zstd) produce same results as uncompressed |

### exhaustive_test.go

Tests **all combinations** of:
- **6 input formats**: stderr, CSV, JSON, syslog_bsd, syslog_iso, syslog_rfc5424
- **12 sections**: default, summary, checkpoints, events, errors, connections, clients, maintenance, locks, tempfiles, sql_summary, sql_performance
- **3 output formats**: text, JSON, markdown

**Total: 36 sub-tests × 6 input formats = 216 executions**

For each `(section, output)` combination, verifies that all 6 input formats produce equivalent results.

### flags_test.go

| Test | Description |
|------|-------------|
| **TestFlagCompatibility** | Valid/invalid flag combinations, edge cases |
| **TestOutputFormats** | Text, JSON, Markdown output validation |
| **TestSectionFilters** | Section flags filter output correctly |

---

## Implementation Status

- [x] Create `test/generate/` directory structure
- [x] Write `docker-compose.yml`
- [x] Write `postgresql.conf`
- [x] Write SQL scenario files (00-09)
- [x] Write `generate_fixtures.sh` orchestration script
- [x] Generate fixtures and verify counters manually
- [x] Commit fixtures to `test/testdata/`
- [x] Write test suite (comprehensive, exhaustive, summary tests)
- [ ] Set up GitHub Actions workflow (uses committed fixtures)
- [x] Document fixture regeneration process
