# Web report data contract (Go → JS)

The standalone HTML report embeds one JSON payload that every JS renderer reads.
This page documents that contract as observed in a real payload (generated from
`test/testdata/comprehensive/stderr.log --html`, then base64-decoded and
zstd-decompressed from the embedded blob).

## Envelope

- Producer: `output.ExportHTMLWithPgBackRest` → `buildJSONData(metrics, ["all"],
  full=true)` in `output/json.go`, plus a `meta` key added in `output/html.go`,
  plus a `pgbackrest` key (see below) when the run analyzed any pgBackRest
  content.
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
Except in the CLI JSON/YAML of a pgBackRest-only or Patroni-only run (see
`pgbackrest` and `patroni`).
Read by: `app.js`, `js/period-nav.js`, `js/sections/events.js`.
`entries` (parsed log entries), `filename`, `filesize` (bytes), `format`
(`stderr`/`csv`/`json`/`syslog`), `parse_time_ms`, `sections` (CLI section list,
`["all"]` for a default report).

### summary — always present
Except in the CLI JSON/YAML and browser payloads of a pgBackRest-only run
(see `pgbackrest`), and in the CLI JSON/YAML of a Patroni-only run, which
holds `patroni` alone.
Read by: `app.js`, `js/filters.js`, `js/sections/summary.js`, `js/sections/connections.js`;
rewritten by `js/report-filter.js` when a time filter is applied.
`start_date`/`end_date` (display strings), `duration`, `total_logs`,
`throughput` (`"N entries/s"`), severity counters `error_count`,
`fatal_count`, `panic_count`, `warning_count`, `log_count`, and
`utc_offset_minutes` (always present, int minutes east of UTC, 0 for UTC
logs) — the time filter uses it to convert slider bounds to the epoch basis
of `top_events[].timestamps`.

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
(omitted when empty) is an array of `{id, count, normalized_query}` linking the
event to the query ids for the modal cross-reference.

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
- `session_events`: array of `{s, e, d, u, db, h, orphan?}`, one per session
  (feeds the concurrent-sessions chart). `s`/`e` = ISO start/end; `d` =
  duration in ms; `u`/`db`/`h` = interned user/database/host **indices**
  (0 = unknown) resolved via the lookup tables below; `orphan: true` marks a
  session flushed at log end with no matching disconnect.
- `session_users` / `session_databases` / `session_hosts` (omitted when empty):
  `[]string` reverse-lookup tables for the `u`/`db`/`h` indices in
  `session_events` (index 0 is always `""`/unknown).

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
`events` `[{timestamp, size, size_bytes, query_id?}]` (`size` is the formatted
string, `size_bytes` the exact integer for lossless re-summing, `query_id`
omitted when absent); `queries` `[{id, normalized_query,
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
- Counters: `vacuum_count`, `aggressive_vacuum_count`, `anti_wraparound_vacuum_count` (omitted when 0), `analyze_count`,
  `total_vacuum_elapsed_seconds`, `total_analyze_elapsed_seconds`,
  `total_tuples_removed`, `total_tuples_not_yet_removable`,
  `total_buffer_hits`, `total_buffer_misses` (omitted when 0), `total_buffer_dirtied`.
- `vacuum_table_counts` / `analyze_table_counts`: map of
  `db.schema.table` → run count.
- `vacuum_space_recovered`: map of table → formatted size.
- `top_vacuum_tables` / `xmin_blocked_tables`: `[{table, vacuum_count,
  total_elapsed_seconds, max_elapsed_seconds, tuples_removed,
  tuples_not_yet_removable, buffer_hits, buffer_misses (omitted when 0), buffer_dirtied}]`.
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

### patroni — gated, absent from this fixture
Read by: `app.js` (a report without PostgreSQL logs renders only this section)
and `js/sections/patroni.js`. Emitted only when Patroni logs were part of the
run, in the full report (see `output/patroni_json.go`). Shape (`PatroniJSON`):
`start_date`, `end_date`, `duration`, `total_logs`, `error_count`,
`warning_count`, `leader_changes`, `switchovers`, `failovers`, `lock_retaken`,
`leaderless_time`, `members` `[{name, role, first_seen, last_seen,
leader_time, total_logs}]`, plus omitempty `members_without_log` (names),
`leader`, `leader_since`,
`dcs_incidents` `[{start, end, duration, members}]`, `leadership`
`[{member, start, end, duration}]`, `changes` `[{timestamp, kind, from, to,
leaderless, postgres?}]` (`postgres` uses the `server.timeline` item shape),
`event_counts`, `events` `[{timestamp, member, kind, detail?}]` and
`messages` `[{id, level, message, count, first_date?, last_date?}]`, `id`
being the `--event-detail` handle (`er-`/`wa-`, `fa-` for CRITICAL).

### pgbackrest
Read by: `js/sections/pgbackrest.js` (`buildPgBackRestSection`), one report
section per stanza. Emitted by `output.ExportJSONWithPgBackRest` /
`output.ExportYAMLWithPgBackRest` / `output.ExportHTMLWithPgBackRest`
(CLI `--json`/`--yaml`/`--html`) and by the browser build, whenever the input
carried pgBackRest lines, in a pgBackRest log file or a PostgreSQL log, and
no attribute filter or section option left the report out — see
`docs/pgbackrest.md`.

A list, one entry per stanza (`pgBackRestJSON` in
`output/pgbackrest_json.go`), snake_case keys. Dates are display strings
(`"YYYY-MM-DD HH:MM:SS"`) and every date key is omitted when unknown. `?`
marks a key omitted when empty, false or zero; the others are always
present.

- Stanza: `stanza` (`""` for the lines that named no stanza, which the
  reports name "(from PostgreSQL log)"), `files?`, `lines_read` (physical lines of the stanza's
  pgBackRest entries, banners and the blank lines between processes
  included), `versions?`, `start_date?`,
  `end_date?`, `has_timestamps`, `saw_begin_end`, `backups_logged` (the
  begin or end line of a command other than archive-push/archive-get was
  read, which only pgBackRest's own logs hold; when false and `backups` is
  empty, backups are not logged rather than none), `pg_retry_warnings`,
  `pg_restore_failures`, `missing_info?` (information the log does not
  carry: a log level or `log-timestamp=n` that hid it), `missing_wal?`
  `[{segment, path?, date?, pushed_date?}]` (a WAL segment or `.history` file archive-get could
  not find for `path`, and never found for it later, whose push was logged
  at `pushed_date`: once per segment and path, in time order),
  `restore_dates?` `[{path, dates}]` (each segment archive-get found, per
  data directory, in order of first find), `pg_retry_dates?` (the date of
  each of `pg_retry_warnings`), `push_dates?`
  (the date of each segment `archive_push.pushed` and `pushed_async`
  count, in time order, an async batch's segments at its end: as many
  dates as the two counters sum).
- The short labels (`label`, `error_label`, `cause`, `detail` below) are
  worded in Go as the text report words them, so that both reports say a
  fact the same way.
- `window?` `{begin?, end?, cut_before, cut_after}`: present under
  `--begin`/`--end`, the report then covering that window only;
  `cut_before`/`cut_after` say pgBackRest lines before/after it were left
  out, so an absent backup means none in the window.
- `backups?` `[{label?, type?, changed_to_full?, requested_type?, repo?,
  start_date?, end_date?, last_line_date?, duration_ms?, size?, size_bytes?,
  file_total?, succeeded, crashed?, waiting_for?, signal?, in_progress?,
  error_code?, error_line?, error_detail?, exec_id?}]`, in start order.
  Only runs whose `command begin` is in the log (in the window) are
  listed. `duration_ms` is pgBackRest's own `(Nms)` on success, the time
  between the begin and end lines on failure. `requested_type` is the type
  asked for when pgBackRest changed it to full. `size` is pgBackRest's own
  string (`"29.4MB"`), `size_bytes` the same size in bytes (1024-based
  units), which the HTML report formats; `file_total` the file count. The
  three are absent when pgBackRest did not log them (a failed or killed
  run): unknown, not zero. `crashed: true` means the
  run never wrote its `command end` (killed or crashed): `succeeded` is
  then false, `end_date` and `duration_ms` are absent (unknown) and
  `last_line_date` is when it wrote its last line. `signal` (e.g.
  `"SIGTERM"`) names the signal pgBackRest logged for that run.
  `waiting_for` names the checkpoint its last line said it waited for
  (`"next regular checkpoint"`, `"requested immediate checkpoint"`).
  `in_progress: true` means the run had not ended by the `--end` bound
  while later lines were left out: its outcome is past the window,
  `succeeded` is false, `end_date` is absent and `last_line_date` is its
  last line in the window.
  `error_line` is a failed run's last error line, `error_detail` the
  lines pgBackRest wrote under it (the cause, such as `FileMissingError:
  ...`), `HINT:` lines left out; `error_label` the failure's short cause
  ("lock held by another command"), on a failed run only.
- `last_full?`/`last_diff?`/`last_incr?` `{label, repo?, end_date,
  age_seconds}`: the latest successful backup of that type not removed by
  this log's own expire runs; `age_seconds` is measured to the last
  timestamp of the whole input (every stanza and PostgreSQL lines
  included, within the `--begin`/`--end` window), rounded to the second
  like the text report.
  `last_full_expired?`/`last_diff_expired?`/`last_incr_expired?`: the label
  of a more recent successful backup of that type that this log's expire
  runs removed.
- `commands?` `[{command, runs, succeeded, failed, total_duration_ms,
  max_duration_ms}]`: the runs whose `command begin` is in the log (in the
  window); a run still going at the `--end` bound is neither succeeded nor
  failed. `total_duration_ms`/`max_duration_ms` cover every run that wrote
  its `command end`, succeeded or failed (see `duration_ms` above); a
  killed run counts in `runs` and `failed` but in no duration, one still
  going in `runs` only.
- `archive_push`/`archive_get` `{logged, async_logged, pushed,
  pushed_async, found, found_async, not_found, not_found_async, dropped,
  dropped_wal?: [{segment?, date?}], failed, failed_async, timeouts,
  failures?: [{segment?, code?, first_line?, first_date?, last_date?,
  attempts, recovery?, recovered_by?, cause?, path?}], last_push?,
  last_found?}`, a stretch's `cause` being its short cause ("archive.info
  permission denied"). archive-get's stretches are per data directory,
  `path` (its `--pg1-path`, or the directory a `[032]` names), `segment`
  the first WAL segment it asked for, `recovery` `"recovered"` when a
  segment was later found for that directory, else `"still_failing"`;
  `last_found` is when archive-get last found a segment. `logged`/`async_logged` say whether the synchronous command
  / the async worker logged its own begin/end lines: when `logged` is false
  and a counter is 0, that counter was not logged (the reports say "not
  logged"), not zero. `failed` counts the synchronous command PostgreSQL
  runs (comparable with `pg_archive_failures`); `failed_async` counts the
  async worker and is never summed into it. `pushed_async` counts the
  segments pushed through the async worker: those of its successful runs
  (its own log), less the ones it dropped, plus those the synchronous call
  reported "pushed ... asynchronously" that no such run covers; a segment
  seen in both logs counts once. `dropped` counts one drop seen
  in two logs once; `dropped_wal` names those segments, which PostgreSQL
  was told were archived. `archive_push` only: `last_push`
  `{segment?, date?, mode}` is the latest successful push of a WAL
  segment (a later `.backup` or `.history` file is passed over), `mode` being
  `"sync"` (archive-push pushed it), `"async"` (the async worker's
  successful run, by its last segment, or the synchronous call's
  "pushed ... asynchronously") or `"postgresql"` (PostgreSQL's DEBUG1
  "archived write-ahead log file"); absent when none was logged. Each
  push stretch's `recovery` is `"recovered"` when a successful push of
  its segment or a later one followed its last attempt (`recovered_by`,
  same shape as `last_push`, is the first such push; an async batch
  counts by the last segment it did push and never recovers a segment
  that failed in it), `"still_failing"`
  when successful pushes are logged and none followed,
  `"no_later_push"` when none followed for this stanza but another stanza
  of the input pushed that same segment after it, `"unknown"` when
  they are not logged, the lines carry no timestamp, or `--end` cut the
  input.
- `expire_runs?` `[{date?, repo?, expired?, archive_removed, warnings?,
  succeeded, error_code?, error_line?, error_detail?, set_label?}]`. `repo`
  is `"all"` for a run given no `--repo` while several repositories are
  configured: pgBackRest then expires each of them. `warnings` holds the
  WARN lines the run wrote and the INFO saying `repoN-retention-archive`
  is not set. `expired` lists the
  backup labels removed ("remove expired backup X"). `set_label` is the
  backup the run was asked to remove (`--set=`), absent for a
  retention-driven run. A failed run has `succeeded: false`, the code of
  its `aborted with exception [NNN]`, its last error line and the lines
  under it (`error_detail`, as for `backups`).
- `retention_warnings?` `[{text, count, repos, first_date?, last_date?}]`:
  the `warnings` of every expire run grouped by text, in order of first
  run: `count` runs wrote it, `repos` are their distinct `repo` (`""` for
  a run that named none). Every report states each text once from it.
- `operations?` `[{command, date?, summary?, succeeded, has_outcome,
  duration_ms?, error_code?, crashed?, signal?, detail?, path?, recovery?}]`: check, stanza-create,
  stanza-upgrade, restore, verify, start, stop; `detail` is the summary
  shortened (a restore's backup set, destination and target); `path` a
  restore's `--pg1-path`, `recovery` what stopped its recovery
  ("archive-get failed [032]", "WAL ... missing"), from the archive-get
  stretches and `missing_wal` of that path. `has_outcome: false` means
  no `command end` gave the outcome (the run was still going at the
  `--end` bound); `succeeded` is always present, as for `backups`;
  `duration_ms` as for `backups`.
- `errors?`/`warnings?` `[{id, label, code?, message, example?, repo_line?, detail?,
  count, first_date?, last_date?, commands?, dates?}]`, one entry per code,
  normalized message and cause (the first `detail` line): two `[103]`
  errors with different causes are two entries. `id` is the
  `--event-detail` handle (`er-`/`wa-` and 4 characters, hashed from the
  stanza and the grouping), shown by the CLI only; `label` the short
  label ("archive.info permission denied", "repo1-retention-full not
  set"). `message` is normalized
  (paths shortened to their last two segments, WAL segments and labels
  replaced, quoted or not); `example` is the first occurrence, with its
  repo line when it has one; `repo_line` is a multi-repo error's
  failing-repository line (`repo1: [...]`); `detail` holds the lines
  written under the first occurrence, `HINT:` lines left out. `dates`
  holds the date of each occurrence, in time order (none for a
  `log-timestamp=n` file's).
- `pg_archive_failures?` `[{segment?, exit_code?, signal?, date?}]`:
  PostgreSQL's own archive_command failures; `signal` is the signal number
  when the command was terminated by one.
- `pg_archive_failure_groups?` `[{segment?, cause, count, first_date?,
  last_date?}]`: `pg_archive_failures` grouped by segment and cause
  (`"exit N"`, `"signal N"` or `"exit unknown"`), in order of first
  failure, as every report lists them.

pgBackRest-only runs (no PostgreSQL input) have their own shapes:

| Producer | Top-level keys |
|---|---|
| CLI `--json` / `--yaml` | `pgbackrest` only |
| CLI `--html` | `summary`, `meta`, `pgbackrest`, `pgbackrest_only: true` |
| Browser build (file dropped in the page) | `meta`, `pgbackrest`, `pgbackrest_only: true` |

`renderResults` in `web/app.js` checks `pgbackrest_only` first and renders
only the pgBackRest sections.

## Gated keys

Every section except `summary` and `meta` is conditional — `buildJSONData`
skips a key when its section has no data, so renderers must null-check
(pgBackRest-only payloads differ, see `pgbackrest` above):

| Key | Emitted only when |
|---|---|
| `events`, `top_events` | at least one event summary / top event |
| `temp_files` | temp-file count > 0 |
| `locks` | lock events > 0 |
| `maintenance` | vacuum or analyze count > 0 |
| `replication` | replication markers present (absent from the reference fixture) |
| `patroni` | Patroni logs given, full report (absent from the reference fixture) |
| `pgbackrest` | a pgBackRest log file was analyzed, or a PostgreSQL log carried embedded pgBackRest/archive-failure content |
| `pgbackrest_only` | `true` only in the HTML and browser payloads of a pgBackRest-only run |
| `checkpoints` | completed checkpoints or frequency warnings > 0 |
| `connections` | connection/disconnection events present |
| `clients` | at least one unique database, user, app or host |
| `users`, `apps`, `databases`, `hosts` | each list emitted separately, skipped when its dimension is empty or all-`UNKNOWN` |
| `server` | server lifecycle events present |
| `sql_overview`, enriched `sql_performance` (`queries`, `executions`) | full mode — always true for `--html` |
| `sql_performance` at all | at least one parsed query |

`js/report-filter.js` keeps a deep copy of the original payload and, on time
filtering, re-aggregates every section that carries per-item timestamps:
`sql_performance` (from `executions`, including its `queries` table),
`temp_files` (totals **and** the `queries` table, from the temp events),
`checkpoints`, `connections`, `top_events` (clipping each event's
`timestamps[]` to the window), and `sql_overview` category/type/total counts
(joining the filtered executions back to `sql_performance.queries`). It also
rewrites `summary.start_date/end_date/duration`.

Sections whose figures cannot be rebuilt client-side from the payload are left
at whole-log values and explicitly flagged so the UI can annotate them, rather
than silently presenting stale numbers as scoped. The filter sets
`_timeFiltered`/`_filterRange` and a `_wholeLog` marker listing them; their
renderers show a "whole-log" badge (`wholeLogBadge()` in `js/utils.js`). These
are: `events` severity distribution and noise counters (only `top_events`
carry per-occurrence timestamps), `locks` (headline counts depend on the
backend's wait-episode collapsing, which the raw event array cannot reproduce),
`maintenance` (counter maps only), `sql_overview`'s
`by_database/by_user/by_host/by_app` tables (executions carry no per-execution
dimension), and `pgbackrest` (its counters, archive stats and grouped issues have no
per-occurrence timestamp the filter could re-scope them by). `_timeFiltered`/
`_filterRange`/`_wholeLog` are client-side-only markers, absent from the Go
payload.

`pgbackrest` itself is still not re-aggregated by `js/report-filter.js` (its
figures always cover the whole log, filtered or not) — only the "whole-log"
badge is wired in, on every stanza's section header, like the sections above.
Re-scoping pgBackRest's own figures to a time window remains future work.
