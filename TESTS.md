# Test Strategy for quellog

This document outlines the plan for comprehensive integration testing using real PostgreSQL logs generated via Docker containers.

## Current State

- `test/testdata/test_summary.log` and variants (`.csv`, `.json`, `_sys.log`) are manually crafted fixtures
- Limited coverage: not all PostgreSQL log scenarios are tested
- Risk of divergence from real PostgreSQL log formats

## Proposed Architecture

```
test/
├── generate/
│   ├── docker-compose.yml          # Multi-version PostgreSQL
│   ├── configs/
│   │   ├── postgresql.base.conf    # Common config
│   │   ├── postgresql.simple.conf  # Simple protocol (direct queries)
│   │   └── postgresql.extended.conf # Extended protocol (prepared statements)
│   ├── scenarios/
│   │   ├── 00_schema.sql           # Tables, indexes, constraints
│   │   ├── 01_connections.sql      # Multi-users, multi-db
│   │   ├── 02_dml.sql              # SELECT/INSERT/UPDATE/DELETE
│   │   ├── 03_ddl.sql              # CREATE/DROP/ALTER
│   │   ├── 04_transactions.sql     # BEGIN/COMMIT/ROLLBACK/SAVEPOINT
│   │   ├── 05_maintenance.sql      # VACUUM/ANALYZE/REINDEX/CHECKPOINT
│   │   ├── 06_tempfiles.sql        # Queries exceeding work_mem
│   │   ├── 07_locks.sql            # Deadlocks, lock waits
│   │   ├── 08_errors.sql           # Constraint violations, syntax errors
│   │   └── 09_prepared.sql         # PREPARE/EXECUTE/DEALLOCATE
│   ├── generate_activity.sh        # Execute scenarios
│   └── generate_fixtures.sh        # Orchestrate: start → generate → extract → stop
├── testdata/
│   ├── pg14/                       # Per PostgreSQL version
│   │   ├── simple/                 # Simple protocol
│   │   │   ├── stderr.log
│   │   │   ├── csvlog.csv
│   │   │   ├── jsonlog.json
│   │   │   └── syslog.log
│   │   ├── extended/               # Extended protocol (prepared statements)
│   │   │   └── ...
│   │   └── golden.json             # Expected output (one per version)
│   ├── pg15/
│   ├── pg16/
│   ├── pg17/
│   └── current -> pg17             # Symlink to default version
└── integration_test.go             # Tests across all fixtures
```

## PostgreSQL Configuration

### Base Configuration (all versions)

```conf
# Output formats: stderr + CSV + JSON + syslog
log_destination = 'stderr,csvlog,jsonlog,syslog'
logging_collector = on
log_directory = '/var/log/postgresql'
log_filename = 'postgresql'

# Syslog
syslog_facility = 'LOCAL0'
syslog_ident = 'postgres'

# Capture everything
log_min_duration_statement = 0
log_statement = 'none'           # Avoid duplicates with duration logging
log_checkpoints = on
log_connections = on
log_disconnections = on
log_lock_waits = on
log_temp_files = 0
log_autovacuum_min_duration = 0

# Rich prefix for testing detection
log_line_prefix = '%m [%p] %q%u@%d app=%a '

# Force frequent checkpoints for testing
checkpoint_timeout = 30s
max_wal_size = 32MB
```

### Extended Protocol Configuration

```conf
# Log prepared statements
log_statement = 'all'  # To see PREPARE/EXECUTE
```

## Test Scenarios

Target size: ~2000-3000 lines per format (manually verifiable, meaningful metrics)

| Scenario | Lines | What we test |
|----------|-------|--------------|
| 00_schema | ~20 | Basic DDL |
| 01_connections | ~50 | 5 users × 3 dbs × multi-apps, varied sessions |
| 02_dml | ~500 | SELECT/INSERT/UPDATE/DELETE, durations 1ms-500ms |
| 03_ddl | ~30 | CREATE/DROP TABLE/INDEX, ALTER |
| 04_transactions | ~100 | BEGIN/COMMIT/ROLLBACK, nested savepoints |
| 05_maintenance | ~50 | VACUUM, ANALYZE, REINDEX, forced CHECKPOINT |
| 06_tempfiles | ~20 | Large sorts/joins exceeding work_mem=1MB |
| 07_locks | ~30 | Lock waits, provoked deadlocks |
| 08_errors | ~50 | Constraints, syntax, permissions |
| 09_prepared | ~150 | PREPARE/EXECUTE/DEALLOCATE |

## Docker Compose Setup

```yaml
services:
  pg14:
    image: postgres:14-alpine
    environment:
      POSTGRES_PASSWORD: test
    volumes:
      - ./configs:/etc/postgresql/conf.d:ro
      - pg14_logs:/var/log/postgresql
    command: postgres -c config_file=/etc/postgresql/conf.d/postgresql.base.conf
    ports: ["5414:5432"]

  pg15:
    image: postgres:15-alpine
    ports: ["5415:5432"]
    # ... same pattern

  pg16:
    image: postgres:16-alpine
    ports: ["5416:5432"]

  pg17:
    image: postgres:17-alpine
    ports: ["5417:5432"]

  syslog:
    image: balabit/syslog-ng:latest
    volumes:
      - syslog_logs:/var/log
    # Receives syslog from all PostgreSQL instances
```

## GitHub Actions Integration

```yaml
jobs:
  generate-fixtures:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Generate test fixtures
        run: |
          cd test/generate
          ./generate_fixtures.sh
      - uses: actions/upload-artifact@v4
        with:
          name: test-fixtures
          path: test/testdata/

  test:
    needs: generate-fixtures
    strategy:
      matrix:
        go: ['1.21', '1.22', '1.23']
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/download-artifact@v4
        with:
          name: test-fixtures
          path: test/testdata/
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
      - run: go test ./... -v
```

## Open Questions

1. **Syslog**: Use a dedicated syslog-ng container receiving from all PG instances, or configure rsyslog on each PG container?

2. **Extended protocol**: Is `pgbench -M extended` sufficient, or should we also test via a Go/Python client explicitly using prepared statements?

3. **Determinism**: Timestamps will differ on each generation. Compare only counters/metrics in golden files, not timestamps?

4. **PostgreSQL versions**: Currently planning 14, 15, 16, 17 (latest stable versions). Add 13 for broader compatibility testing?

## Implementation Steps

1. [ ] Create `test/generate/` directory structure
2. [ ] Write `docker-compose.yml` with multi-version PostgreSQL
3. [ ] Create PostgreSQL configuration files
4. [ ] Write SQL scenario files
5. [ ] Create `generate_fixtures.sh` orchestration script
6. [ ] Update `integration_test.go` to use generated fixtures
7. [ ] Set up GitHub Actions workflow
8. [ ] Document fixture regeneration process
