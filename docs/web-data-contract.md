# Web report data contract (Go → JS)

The standalone HTML report embeds one JSON payload that every JS renderer reads.
This page documents that contract as observed in a real payload (generated from
`test/testdata/comprehensive/stderr.log --html`, then base64-decoded and
zstd-decompressed from the embedded blob).

## Envelope

- Producer: `output.ExportHTML` → `buildJSONData(metrics, ["all"], full=true)` in
  `output/json.go`, plus a `meta` key added in `output/html.go`.
- Transport: JSON → zstd (best compression) → base64 → inlined as
  `const COMPRESSED_DATA = "..."` in the report. `web/js/compression.js`
  reverses this in the browser (fzstd), then `window.renderReport(data)` runs.
- The contract linter `web/tests/contracts/check-data-keys.mjs` re-extracts this
  payload on every run and diffs it against the key paths the renderers read.

Conventions:

- Timestamps appear in three shapes: display strings `"YYYY-MM-DD HH:MM:SS"`
  (most `events` arrays), ISO strings `"YYYY-MM-DDTHH:MM:SS"` (`session_events`,
  `sql_performance.executions`), and Unix milliseconds (`top_events[].timestamps`).
- Human-formatted values (durations `"1m 32s"`, sizes `"683.59 KB"`, rates) are
  pre-rendered strings; raw numbers keep a `_ms`, `_kb`, `_seconds` or `count` name.
- A section key is **absent** when the section has no data (see "Gated keys").

## Top-level keys

### meta — always present
Read by: `app.js`, `js/period-nav.js`, `js/sections/events.js`.
`entries` (parsed log entries), `filename`, `filesize` (bytes), `format`
(`stderr`/`csv`/`json`/`syslog`), `parse_time_ms`, `sections` (CLI section list,
`["all"]` for a default report).

### summary — always present
Read by: `app.js`, `js/filters.js`, `js/sections/summary.js`, `js/sections/connections.js`;
rewritten by `js/report-filter.js` when a time filter is applied.
`start_date`/`end_date` (display strings), `duration`, `total_logs`,
`throughput` (`"N entries/s"`), and severity counters `error_count`,
`fatal_count`, `panic_count`, `warning_count`, `log_count`.

### events — severity distribution
Read by: `app.js`, `js/charts.js`, `js/sections/events.js`.
Array of `{type, count, percentage}` — one entry per severity (LOG, ERROR,
FATAL, WARNING, ...). `percentage` is a raw float (0–100).

### top_events — most frequent messages
Read by: `js/sections/events.js`, `js/sections/modals.js`.
Array of `{id, message, count, severity, example, sql_state_class, timestamps,
triggering_queries?}`. `id` is the stable short handle (e.g. `"fa-6K1G"`) used
as modal click-target and `--event-detail` selector. `timestamps` is Unix
milliseconds per occurrence (drives the modal sparkline). `sql_state_class` is
the two-char SQLSTATE class (omitted when unknown). `triggering_queries`
(omitted when empty) links events to query ids for the modal cross-reference.

### connections
Read by: `js/sections/connections.js`; re-aggregated by `js/report-filter.js`.
- Totals: `connection_count`, `disconnection_count`, `avg_connections_per_hour`,
  `avg_session_time`.
- `session_stats`: `{count, min_duration, max_duration, avg_duration,
  median_duration, cumulated_duration}` (formatted strings).
- `session_distribution`: map of duration bucket label (`"< 1s"`, `"1s - 1min"`,
  ...) → session count.
- `sessions_by_user` / `sessions_by_database` / `sessions_by_host`: map of
  name → session-stats object (same shape as `session_stats`).
- `peak_concurrent_sessions` + `peak_concurrent_timestamp` (omitted when 0).
- `client_io_failures` (optional): `{total, receiving_from_client,
  sending_to_client}` — only when client I/O failures were seen.
- `connections`: array of display-string timestamps, one per connection
  received (feeds the connections/hour chart and time re-filtering).
- `session_events`: array of `{s, e}` ISO start/end pairs, one per completed
  session (feeds the concurrent-sessions chart).

### clients / users / apps / databases / hosts
Read by: `js/sections/connections.js`, `js/filters.js` (dropdown population).
`clients` is `{unique_databases, unique_users, unique_apps, unique_hosts}`.
The other four are arrays of `{name, count}` sorted by count (connection
attribution per user/app/database/host).

### sql_overview — query mix (full mode only)
Read by: `app.js`, `js/sections/sql.js`.
`total_queries`; `categories` `[{category, count, percentage, total_time}]`
(DML/DDL/TCL/...); `types` `[{type, category, count, percentage, total_time,
avg_time, max_time}]` (SELECT, INSERT, ...); `by_database` / `by_user` /
`by_host` / `by_app`: `[{name, count, total_time, query_types: [{type, count,
total_time}]}]`.

### sql_performance — durations (enriched in full mode)
Read by: `js/sections/sql.js`, `js/sections/modals.js`; re-aggregated by
`js/report-filter.js` from `executions`.
- Totals: `total_query_duration`, `total_queries_parsed`,
  `total_unique_queries`, `top_1_percent_slow_queries`, `query_max_duration`,
  `query_min_duration`, `query_median_duration`, `query_99th_percentile`.
- `duration_distribution`: `[{bucket, count}]` with labels like `"< 1 ms"`.
- `slowest_queries` / `most_frequent_queries` / `most_time_consuming`:
  `[{id, normalized_query, count, total_time, avg_time, max_time}]` — present in
  the payload but NOT read by the report (it ranks from `queries` / `executions`
  itself); retained for `--json` CLI consumers.
- `queries` (full mode): per-query detail `[{id, normalized_query, raw_query,
  type, count, total_time_ms, avg_time_ms, max_time_ms, top_databases,
  top_users, top_apps, top_hosts}]` — the `top_*` lists are `[{name, count}]`.
  Backs the query-detail modal.
- `executions` (full mode): flat `[{timestamp (ISO), duration_ms, query_id}]`,
  one per logged duration — the ground truth for time-filter re-aggregation
  and the query scatter chart.

### checkpoints
Read by: `js/sections/checkpoints.js`; re-aggregated by `js/report-filter.js`.
- Totals: `total_checkpoints`, `avg_checkpoint_time`, `max_checkpoint_time`,
  `total_buffers_written`, `avg_wal_distance`, `max_wal_distance`, `wal_rate`,
  `flush_rate`.
- `events`: display-string timestamps of completed checkpoints.
- `types`: map of trigger reason (`"time"`, `"wal"`, `"immediate force wait"`,
  ...) → `{count, percentage, rate_per_hour, events[]}`.
- `wal_distances`: `[{timestamp, distance_kb, estimate_kb}]` (WAL chart).
- "Checkpoints occurring too frequently" warnings: `warning_count`,
  `warning_min_interval_seconds`, `warning_max_interval_seconds`,
  `warning_events` (timestamps).

### server — lifecycle timeline
Read by: `js/sections/checkpoints.js` (rendered inside the checkpoints card).
`starts`, `start_times[]`, `reloads`, `shutdowns_fast`, `shutdowns_immediate`,
`shutdowns_smart`, `shutdown_completed`, and `timeline`
`[{timestamp, kind, detail}]` with `kind` in start/shutdown/reload/....

### temp_files
Read by: `js/sections/tempfiles.js`, `js/sections/modals.js`; re-aggregated by
`js/report-filter.js`.
`total_messages`, `total_size`, `avg_size`, `max_size` (formatted strings);
`events` `[{timestamp, size, query_id}]`; `queries` `[{id, normalized_query,
raw_query, count, total_size, min_size, max_size, avg_size}]`.

### locks
Read by: `js/sections/locks.js`, `js/sections/modals.js`.
Totals `total_events`, `waiting_events`, `acquired_events`, `deadlock_events`,
`total_wait_time`, `avg_wait_time`; breakdown maps `lock_type_stats`,
`resource_type_stats`, `relation_stats` (name → count); `events`
`[{timestamp, event_type, lock_type, resource_type, wait_time, process_id,
query_id, blocking_pid, blocking_query_id, blocking_query, relation}]`;
`queries` `[{id, normalized_query, raw_query, acquired_count,
acquired_wait_time, still_waiting_count, still_waiting_time, total_wait_time}]`
(backs the query-detail modal's Lock Waits block).

### maintenance — vacuum & analyze
Read by: `js/sections/maintenance.js`.
- Counters: `vacuum_count`, `aggressive_vacuum_count`, `analyze_count`,
  `total_vacuum_elapsed_seconds`, `total_analyze_elapsed_seconds`,
  `total_tuples_removed`, `total_tuples_not_yet_removable`,
  `total_buffer_hits`, `total_buffer_misses`, `total_buffer_dirtied`.
- `vacuum_table_counts` / `analyze_table_counts`: map of
  `db.schema.table` → run count.
- `vacuum_space_recovered`: map of table → formatted size.
- `top_vacuum_tables` / `xmin_blocked_tables`: `[{table, vacuum_count,
  total_elapsed_seconds, max_elapsed_seconds, tuples_removed,
  tuples_not_yet_removable, buffer_hits, buffer_misses, buffer_dirtied}]`.
- `top_analyze_tables_by_elapsed`: same minus the tuple/buffer fields.
- `slowest_vacuum`: `{table, timestamp, elapsed_seconds,
  tuples_not_yet_removable}`.

### replication — gated, absent from this fixture
Read by: `js/sections/checkpoints.js` (sub-zone of the server area).
Emitted only when the log contains replication markers
(`m.Replication.HasAny`, see `output/json.go`). Shape (`ReplicationJSON`):
`total_events`, `markers` (map marker → count), plus omitempty fields
`stream_reconnects`, `recovery_pauses`, `recovery_resumes`,
`conflicts_with_recovery`, `invalidated_slots`, `replication_terminations`,
`last_termination`, `peak_hour_label`, `peak_hour_count`, `hour_counts`,
`conflict_queries` `[{id, normalized_query, raw_query, count}]`, and `events`
`[{timestamp, marker, severity?}]`.

## Gated keys

Every section except `summary` and `meta` is conditional — `buildJSONData`
skips a key when its section has no data, so renderers must null-check:

| Key | Emitted only when |
|---|---|
| `events`, `top_events` | at least one event summary / top event |
| `temp_files` | temp-file count > 0 |
| `locks` | lock events > 0 |
| `maintenance` | vacuum or analyze count > 0 |
| `replication` | replication markers present (absent from the reference fixture) |
| `checkpoints` | completed checkpoints or frequency warnings > 0 |
| `connections` | connection/disconnection events present |
| `clients` | at least one unique database, user, app or host |
| `users`, `apps`, `databases`, `hosts` | each list emitted separately, skipped when its dimension is empty or all-`UNKNOWN` |
| `server` | server lifecycle events present |
| `sql_overview`, enriched `sql_performance` (`queries`, `executions`) | full mode — always true for `--html` |
| `sql_performance` at all | at least one parsed query |

`js/report-filter.js` keeps a deep copy of the original payload and, on time
filtering, re-aggregates `sql_performance` (from `executions`), `temp_files`,
`checkpoints` and `connections` from their event arrays, and rewrites
`summary.start_date/end_date/duration`. Fields without a backing event array
(e.g. `locks`, `maintenance`) are left untouched by the time filter.
