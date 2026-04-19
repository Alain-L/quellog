# Regression corpus

Small, targeted PostgreSQL log fixtures used by `TestRegressionCorpus` and
the per-bug `TestRegressions*` suite.

Each fixture is **mock-style** — written by hand, cross-referenced with
formats observed in `_samples/` and `test/testdata/comprehensive/`. They
exist to:

- **Pin** behaviour fixed by recent commits (so a future revert is caught).
- **Cover** edge cases that the larger fixtures (`comprehensive/`) do not
  exercise specifically.

When adding a fixture: keep it under ~50 lines, document it below with
its purpose and the **expected key values** (so a reviewer can verify
the generated golden without running the parser by hand).

After changing analysis output intentionally, regenerate goldens with:

```
go test ./test/ -run TestRegressionCorpus -update
```

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

Expected:
- `vacuum.aggressive_count = 2`
- `vacuum.tables` contains both relations

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

### `multiline_statement.log` — long multi-line STATEMENT and CTE

A multi-line SQL statement with subqueries and a recursive CTE. Both
have many indented continuation lines that the parser must assemble
into a single STATEMENT per entry.

Expected:
- 2 entries (1 ERROR + 1 LOG with duration), not many continuation
  fragments
- `sql.queries[0].statement` contains the full normalized query
