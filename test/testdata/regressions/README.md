# Regression corpus

Small, targeted PostgreSQL log fixtures used by `TestRegressionCorpus` and
the per-bug `TestRegressions*` suite.

Each fixture is **mock-style** — written by hand, cross-referenced with
formats observed in `_samples/` and `test/testdata/comprehensive/`. They
exist to:

- **Pin** behaviour fixed by recent commits (so a future revert is caught).
- **Cover** edge cases that the larger fixtures (`comprehensive/`) do not
  exercise specifically.

When adding a fixture: keep it under ~50 lines, drop it in the **right
themed sub-directory** (see Layout below), document it below with its
purpose and the **expected key values** (so a reviewer can verify the
generated golden without running the parser by hand).

After changing analysis output intentionally, regenerate goldens with:

```
go test ./test/ -run TestRegressionCorpus -update
```

---

## Layout

Fixtures live in themed sub-directories. The corpus runner walks
recursively, so adding a new directory works out of the box.

| Sub-dir         | Topic                                                         |
|-----------------|---------------------------------------------------------------|
| `locks/`        | Lock waits, deadlocks, dedup, query association, relations    |
| `sql/`          | SQL normalization, percentiles, TCL, prepared, top queries    |
| `connections/`  | pgBouncer churn, auth failures, multi-DB filtering            |
| `checkpoints/`  | Checkpoint stats, WAL distance, frequency warnings            |
| `vacuum/`       | Normal/aggressive vacuum and analyze                          |
| `parsers/`      | CSV, JSON, syslog (BSD / RFC 5424 / ISO with continuations)   |
| `autoexplain/`  | auto_explain text and JSON plan formats                       |
| `errors/`       | SQLSTATE class variety, mixed severities                      |
| `misc/`         | Multi-line statements, temp file association                  |

Goldens (`<fixture>.<format>.golden`) live next to their fixture.

---

## Fixtures

### `deadlock_basic.log` — single deadlock with full DETAIL+CONTEXT

A `still waiting` event on transaction 100 followed 1.4 s later by a
`deadlock detected` ERROR for the same wait. The DETAIL spans multiple
indented lines (continuation), CONTEXT identifies the relation
(`accounts`).

Expected:
- `locks.total_events = 1` (the still-waiting becomes a deadlock,
  not double-counted)
- `locks.waiting_events = 1`
- `locks.deadlock_events = 1`
- `locks.relation_stats["accounts"] >= 1`

### `deadlock_multiple.log` — three deadlocks without preceding wait

Three deadlock ERRORs at different timestamps, **without** a preceding
`still waiting` LOG. PostgreSQL emits deadlocks this way when detection
is fast enough that no wait was logged. The analyzer counts them as
errors but does not create lock events for them — that path exists only
when a `still waiting` LOG was previously emitted for the same PID.

Expected:
- `top_events` contains "deadlock detected" with `count = 3`
- `events.ERROR.count = 3`
- No `locks` section emitted (no associated lock events)

### `lock_dedup_waiting_acquired.log` — same wait reported twice

A `still waiting` LOG followed by an `acquired` LOG for the **same**
PID/lock combination. Used to be double-counted as 2 lock events.

Expected:
- `locks.total_events = 1` (one wait, not two)
- `locks.acquired_events = 1`
- `locks.waiting_events = 0` (became acquired)

### `lock_with_statement.log` — query association via STATEMENT

Lock waiting + acquired + a separate `duration: ... statement:` line for
the same PID. The STATEMENT continuation line should let the analyzer
associate a `query_id` with the lock event.

Expected:
- `locks.events[0].query_id` non-empty
- `locks.queries` contains the SELECT FOR UPDATE statement
- `sql_performance.queries` also contains it (with duration 3300.5 ms)

### `lock_relation_extraction.log` — relation breakdown from CONTEXT

Three lock events with CONTEXT lines naming the relations (`orders`,
`orders`, `customers`).

Expected:
- `locks.relation_stats["orders"] = 2`
- `locks.relation_stats["customers"] = 1`

### `autoexplain_text.log` — auto_explain text format plan

A `duration: ... plan:` line followed by a multi-line indented Query
Text + Plan (Limit / Sort / HashAggregate / Hash Right Join / Seq Scan).

Expected:
- `sql.queries[0].plan` non-empty (text format)
- Output does not crash on the deeply nested indentation

### `autoexplain_json_negindent.log` — JSON plan with deep nesting

A `duration: ... plan:` line followed by a JSON object plan with
4 levels of nesting (Limit → Sort → Seq Scan). Used to crash with
`strings.Repeat("  ", -1)` when the indent went negative on the closing
braces.

Expected:
- Exit 0, valid JSON output (no panic)
- `sql.queries[0].plan` contains the parsed JSON

### `vacuum_aggressive.log` — automatic aggressive vacuum

Two `automatic aggressive vacuum` operations on `large_events` and
`audit_logs` with full stats (pages, tuples, buffer, rates, system).

**Current analyzer limitation**: `analysis/vacuum.go` matches
`"automatic vacuum"` and `"automatic analyze"` literally, so
`"automatic aggressive vacuum"` is not counted. This fixture exists to:

1. Verify the parser handles the multi-line block without crashing
2. Catch a future change where the analyzer learns to recognize
   aggressive mode (the golden will then need regenerating)

Expected (today):
- 2 entries parsed, no crash
- `maintenance` section absent or empty (analyzer gap)
- `top_events` contains the aggressive vacuum signature

If the analyzer is enhanced to count aggressive vacuums, expected:
- `maintenance.vacuum_count >= 2` and tables include both relations

### `vacuum_normal.log` — automatic vacuum + analyze

Two normal vacuums + one analyze. Distinct from aggressive mode.

Expected:
- `vacuum.aggressive_count = 0`
- `vacuum.normal_count >= 2`
- `vacuum.analyze_count = 1`

### `csv_empty_fields.csv` — CSV log with optional fields empty

Four CSV records with various combinations of empty fields (no client,
no application_name, no detail, etc.).

Expected:
- 4 entries parsed without errors
- `events.error_count = 1` (the duplicate-key ERROR record)

### `json_rds_multiline.json` — RDS JSON with multiline message

Five JSONL records with one containing a multiline `message` (an
embedded JSON plan with `\n\t` escapes).

Expected:
- 5 entries parsed
- The multiline plan is preserved (not split into 5 entries)
- Includes a FATAL with state_code `57P03`

### `syslog_continuation.log` — syslog format with #011 tab markers

PostgreSQL syslog format with continuation lines using `#011` (the syslog
escaped tab marker for indented continuation lines). Mixes a duration
log, a constraint ERROR with DETAIL+STATEMENT, and a multi-line plan.

Expected:
- Multi-line continuations correctly assembled (no duplicate entries)
- Each PostgreSQL log entry counted once (3 entries, not 9 lines)

### `connections_pgbouncer.log` — high churn pgBouncer pattern

Four short-lived connections from 10.0.1.10 through pgBouncer
(`application_name=pgbouncer`) plus one longer admin session.

Expected:
- `connections.session_count = 5`
- `connections.unique_clients = 2` (10.0.1.10 and 10.0.1.11)
- 4 sessions under 1 second

### `checkpoints_warning.log` — checkpoints occurring too frequently

One normal time-triggered checkpoint, then two xlog-triggered with the
"checkpoints are occurring too frequently" warning + HINT.

Expected:
- `checkpoints.warning_count = 2`
- `checkpoints.complete_count = 2` (the third is still in progress)

### `error_classes_variety.log` — assorted errors

Six errors covering syntax, permission_denied, FK violation, lock
timeout, no-transaction warning, and authentication FATAL.

Expected:
- `events.error_count >= 4`
- `events.fatal_count >= 1`
- `errors` section has at least 4 distinct SQLSTATE classes

### `sql_normalization.log` — query ID stability under literal variation

10 statements: 5 INSERTs with different values, 3 SELECTs with different
ids, 2 UPDATEs with different emails. The SQL normalizer must collapse
each shape into one Query ID.

Expected:
- `total_queries_parsed = 10`
- `total_unique_queries = 3`
- The INSERT entry has `count = 5`, the SELECT `count = 3`, the UPDATE `count = 2`

### `sql_percentiles.log` — calibrated input for P²

51 entries of the SAME query with durations 10, 20, ..., 510 ms (the
true median is the 26th value = 260 ms).

Expected:
- `total_queries_parsed = 51`, `total_unique_queries = 1`
- `query_min_duration = "10 ms"`, `query_max_duration = "510 ms"`
- `query_median_duration` ≈ 260 ms (P² is approximate; tolerate ±20%)

### `sql_tcl_separation.log` — TCL not collapsed with DML

10 statements: 3 BEGIN + 2 SELECT + UPDATE + INSERT + 2 COMMIT + ROLLBACK.

Expected:
- `total_queries_parsed = 10`, `total_unique_queries = 7`
- `begin` appears with `count = 3`, `commit` with `count = 2`, `rollback`
  with `count = 1` — TCL must not be merged with DML normalization.

### `sql_prepared_statements.log` — only EXECUTE counts

1 parse + 3 binds + 3 executes for the same prepared statement.

Expected:
- `total_queries_parsed = 3` (only EXECUTE; parse and bind are skipped)
- `total_unique_queries = 1`

**Documents current behaviour**: if quellog ever changes to count parse
or bind too, the matching test goes red and we re-evaluate (use case:
might want to surface bind durations separately).

### `sql_duration_variants.log` — statement vs execute (parse/bind skipped)

3 plain `duration ... statement:` + 1 each of parse/bind/execute for a
prepared statement.

Expected:
- `total_queries_parsed = 4` (3 statement + 1 execute, parse/bind dropped)

### `sql_top_queries.log` — slowest list ordered by max_time

3 distinct queries: pg_sleep(?) ×1 (1s), big_table SELECT ×5 (100ms),
now() ×10 (1ms).

Expected (using `--sql-performance --json` view):
- `slowest_queries[0]` is pg_sleep (slowest single call)
- `slowest_queries[1]` is the big_table SELECT
- `slowest_queries[2]` is now() (most frequent but fastest)

### `tempfile_with_query.log` — temp file association with shared filesets

1 single temp file for query A + 3 temp files (sharedfileset/*) for
query B. The analyzer must associate each event to its STATEMENT.

Expected:
- `temp_files.total_messages = 4`
- `temp_files.events[0..3].query_id` all non-empty
- The 3 sharedfileset events share the same `query_id` (collapsed by
  the SQL normalizer to one ID)

### `connections_auth_failures.log` — 4 auth failures + 2 sessions

md5, pg_hba, peer, LDAP failures + one IPv6 session + one local session.

Expected:
- `events.FATAL.count = 4` (all 4 auth failures)
- `connections.connection_count = 2`, `disconnection_count = 2`
  (failed authentications do NOT count as connections)
- IPv6 client `fe80::1234:5678` appears in `connections.sessions_by_*`

### `errors_sqlstate_classes.log` — 8 PostgreSQL SQLSTATE classes

12 errors covering classes 08, 22, 23, 25, 28, 42, 53, 57.

Expected (current behaviour):
- `events.ERROR.count = 10`, `events.FATAL.count = 2`
- `--errors` (text) prints the structured class breakdown:
  `08 - Connection Exception`, `22 - Data Exception`, etc.

**Known gaps documented (find while writing this fixture):**
- `--errors --json` returns `{}` (the errors section is missing from
  the JSON export, while the text rendering works).
- `summary.error_count` and `summary.fatal_count` stay at 0 even when
  events show ERROR=10/FATAL=2. The `summary` block does not aggregate
  from the events analyzer. Both are tracked in the roadmap.

### `checkpoints_full.log` — full checkpoint metrics

3 normal time-triggered checkpoints + 1 xlog with the "too frequently"
warning. Stats include WAL distance, buffer counts, write rates.

Expected:
- `checkpoints.total_checkpoints = 4`
- `checkpoints.warning_count = 1`
- `types.time.count = 3`, `types.xlog.count = 1`
- `total_buffers_written = 46668` (1234+6047+1318+38069)
- `wal_distances` has 4 entries with `distance_kb` and `estimate_kb`

### `syslog_bsd.log` — BSD syslog format

Standard PostgreSQL output via `log_destination=syslog` in BSD/RFC3164
form: `Apr 20 08:00:00 dbhost01 postgres[30001]: [1-1] [...]`. Includes
a session, an ERROR with STATEMENT continuation, and a checkpoint pair.

Expected:
- `summary.total_logs = 7` (8 raw lines, ERROR+STATEMENT folds into 1)
- `events.LOG = 6`, `events.ERROR = 1`
- `checkpoints` section present (BSD timestamps must parse)

Historical note: MD output was non-deterministic on this fixture
because the histogram sort compared bucket start times at minute
resolution, and 30-second buckets starting in the same minute tied
(`sort.Slice` is not stable). Fixed by falling back to the full label
string on tie; this fixture now runs the MD subtest like any other.

### `syslog_rfc5424.log` — RFC 5424 syslog format

PostgreSQL via `syslog`+`rsyslog` with RFC 5424 framing:
`<134>1 2026-04-20T09:00:00.100+00:00 host postgres 31001 - - [...]`.
Mix of LOG and ERROR with priority `<134>` (info) and `<131>` (err).

Expected:
- `summary.total_logs = 5` (6 raw lines, ERROR+STATEMENT folds)
- `events.ERROR = 1`
- `top_events.message` does NOT contain `<134>` or any priority prefix
  (the RFC5424 prefix must be stripped before normalization)

### `multiline_statement.log` — long multi-line STATEMENT and CTE

A multi-line SQL statement with subqueries and a recursive CTE. Both
have many indented continuation lines that the parser must assemble
into a single STATEMENT per entry.

Expected:
- 2 entries (1 ERROR + 1 LOG with duration), not many continuation
  fragments
- `sql.queries[0].statement` contains the full normalized query
