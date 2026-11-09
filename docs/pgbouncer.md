# pgBouncer Reports

quellog analyzes [pgBouncer](https://www.pgbouncer.org/) connection-pooler logs and renders a dedicated report. The format is auto-detected (see [Log Formats](formats.md#pgbouncer)) — point quellog at a pgBouncer log and it produces the pooler report instead of the PostgreSQL one:

```bash
quellog /var/log/pgbouncer/pgbouncer.log
quellog /var/log/pgbouncer/pgbouncer.log --html -o report.html
quellog /var/log/pgbouncer/pgbouncer.log --json
```

Three pgBouncer settings decide how much of that report can exist: `log_connections` and `log_disconnections` gate the client lines, `stats_period` gates the telemetry lines, and `pool_mode` — which pgBouncer logs nowhere — decides what the pool-wait figure is averaged over. [pgBouncer Setup](pgbouncer-setup.md) prescribes a configuration that produces a complete report.

Compressed and archived inputs work as they do for PostgreSQL logs: `.gz`, `.zst`, and `.tar`/`.tgz`/`.tzst`/`.zip`/`.7z` archives are read in place and produce the same report as the plain file. A log read from standard input (`-`) is not: inputs are sorted into the PostgreSQL and the pooler analyzers by file, before anything is read, so a pooler log piped in is refused with a message saying so. Point quellog at the file instead.

pgBouncer running with `syslog = 1` is not supported in this release. The syslog envelope replaces the severity word with the protocol priority, so those lines match neither the pooler format nor the PostgreSQL one and the file is rejected outright.

Time filters (`--begin`, `--end`, `--last`, `--window`) apply. The attribute filters (`--dbname`, `--dbuser`, `--appname`) select PostgreSQL backends through the `log_line_prefix` and are not applied to the pooler track. The SQL-specific flags (`--sql-summary`, `--sql-detail`, …) have nothing to report on pooler telemetry and are ignored; `--split` is rejected (see below).

## Combining with PostgreSQL logs

pgBouncer and PostgreSQL logs are always separate files (separate daemons). When a single invocation mixes both, quellog partitions the inputs by detected format and analyzes each on its own track — the report is the PostgreSQL report followed by a pgBouncer section:

```bash
quellog /var/log/postgresql/*.log /var/log/pgbouncer/pgbouncer.log
quellog /var/log/postgresql/*.log /var/log/pgbouncer/pgbouncer.log --html -o report.html
```

In text and Markdown the pgBouncer report is appended after the PostgreSQL one; in JSON it is a top-level `pgbouncer` list; in HTML it is a section in the same report, sharing the report's chrome and theme. (`--split` is PostgreSQL-only and is rejected when pgBouncer files are present.)

## Several poolers

Every pooler gets a report of its own: the figures of two poolers are never added or averaged together. quellog groups the pooler files it is given into poolers:

- files that follow each other in time and share a PID are one pooler whose log was rotated;
- files that follow each other in time and share a listen address are one pooler restarted into a new file;
- files whose records overlap in time are always two poolers, even on the same address: that is two hosts, or two containers each with its own log.

A pooler is named after its first listen address (`listening on …` at startup), or after its first file when the log holds no such line. Two poolers with the same name get their files appended to it. The text and Markdown reports give one `PGBOUNCER` section per pooler, titled with its name; HTML shows one pgBouncer section per pooler.

## What is parsed

pgBouncer log lines are single-line records with a `timestamp [PID] SEVERITY` prefix. quellog reads five kinds of line:

| Line | Contributes to |
|------|----------------|
| `LOG stats: <n> xacts/s, <n> queries/s, …, in <n> B/s, out <n> B/s, xact <n> us, query <n> us, wait <n> us` | Request throughput, network I/O, and transaction/query/wait timing. The `/s` and `B/s` fields are rates averaged over the pooler's `stats_period`; `xact`, `query` and `wait` are not rates but mean durations per operation, each over its own counter (transactions, queries, and server assignments respectively) |
| `LOG C-0x…: db/user@host login attempt: …` | Client connections (login attempts) and the per-database/user/host breakdown |
| `LOG S-0x…: … new connection to server …` | Server connections |
| `LOG …: … closing because: <reason> (age=Ns)` | Completed sessions, the session-time histogram, and per-database/user/host breakdown |
| `WARNING`/`ERROR`/`FATAL …` | The error list (including `pooler error: …` and `taking connection from reserve_pool`) |
| `LOG process up: PgBouncer X.Y.Z`, `LOG listening on …`, `LOG got SIG…`, `LOG … command issued` | The pooler's name, release and timeline: starts, shutdowns, reloads and admin console commands (`PAUSE`, `RESUME`, `SUSPEND`, `KILL`, …) |

## The report

Each pooler gets one `PGBOUNCER` section, titled with the pooler's name when there are several. Text and Markdown are synthetic by default and hold in 80 columns; `--pgbouncer-detail` gives the detailed report alone, and `--full` includes it in the full report. JSON, YAML and HTML always hold every detail. HTML draws every snapshot as a bar, with the pooler's events (pauses, restarts, reloads, console commands) on the charts.

The default view gives:

- the pooler (its name and release) and the time range;
- one row per subject: the throughput (transactions/s and queries/s), the average transaction and query times, the average pool wait, the clients (login attempts, completed sessions and their average time), the server connections opened and the reserve-pool activations;
- the messages: the error and warning counts, and the five most frequent with their first and last occurrence;
- the **activity** table: one row per snapshot, or per round time bucket beyond twelve rows, with the queries per second as a bar, the worst query time and pool wait of the row, and the pooler's events on the row they fall in (`reload`, `pause 21s`, `restart`). A run of rows with no traffic reads as one.

The detailed view adds the files, the duration, the lines read, those set aside (unrecognized, filtered out, DEBUG/NOISE), the stats snapshots, network in and out, the wait stretches (their count and the longest) and how many sessions the client ended and how many the pooler did, then:

- the **max snapshot averages**: the highest throughput, network, timing and wait of any snapshot, with when it was logged;
- every message with its ID, count and first and last occurrence. `--event-detail` takes these IDs (`wa-`, `er-`, `fa-`), as it takes PostgreSQL's, and prints a message the table had to shorten in full;
- the activity table, up to twenty rows;
- the **session time** histogram (the buckets of the PostgreSQL connection report) and the **sessions per close reason** with the average age of each;
- the **connections per database, user and host**, their ten heaviest rows then `… and N more`.

When fewer than two snapshots leave no activity table, the detailed view lists the **timeline** (starts with the release, shutdowns with the signal, reloads, console commands such as `PAUSE 'db'`) and the **wait stretches** instead.

### Reading the figures

- **The max is a snapshot average, not a peak.** pgBouncer logs one average per `stats_period`, so at the default 60 s a spike is flattened across the minute: the largest value in the capture is the busiest snapshot average, never the busiest instant.
- **Timing averages are weighted.** Transaction, query and wait times are weighted by each snapshot's operation count and skip the snapshots that timed none of that kind, which is why they can rest on fewer snapshots than the report counts. pgBouncer reports them in microseconds; quellog keeps that resolution (`842 µs`, `3.4 ms`, `2.4 s`).
- **Pool wait is not query time.** A long query time is a query problem, a long pool wait is a sizing problem, read against `default_pool_size`, `reserve_pool_size` and `max_db_connections`. What the wait is averaged over depends on `pool_mode` (one wait per session, per transaction or per statement), which pgBouncer logs nowhere.
- **Before pgBouncer 1.23** the `wait` field is the time all clients spent queued per second of wall time, a queue depth: for a pooler whose start banner names an earlier release, the report shows it as the number of clients queued on average, and HTML leaves it out of the chart drawn in microseconds.
- **Server connections** counts the `new connection to server` lines opened over the window, not the connections held: a pool capped at five legitimately opens many more over an hour.
- **Sessions ended by the client** are those whose close reason starts with "client ", such as `client close request`; any other, `client_idle_timeout` included, is the pooler refusing or killing the session.
- **Reserve-pool activations** (`taking connection from reserve_pool`) are warnings in pgBouncer's log, so they are listed among them, and also counted on the server connections row.
- `pooler error: ` heads every error pgBouncer sends a client; the report keeps the reason after it.

### Zero counts say why

A count that depends on a setting says so when it is zero, since an idle pooler and a silenced one produce the same zero:

- **Login attempts and completed sessions** at 0 name `log_connections` or `log_disconnections`, and add "although traffic shows" when the same window proves the pooler served clients (transactions or queries in a `stats:` line, a server connection opened, or an error). The clients may predate the window, so the report states only that.
- **Traffic, timing and pool wait** come from the `stats:` lines only. With none in the window, one row says so and names `stats_period`; a capture shorter than one period legitimately holds none.
- When the snapshots account for **less than half of the window**, or for an unknown share of it, as a single snapshot always does, a **Stats coverage** row states it. `stats_period` is inferred from the median interval between consecutive snapshots; pgBouncer never logs the setting itself.

A capture with no `WARNING`/`ERROR`/`FATAL` line reads `Messages: 0 errors, 0 warnings`, which `log_pooler_errors = 0` also produces on a pooler that is failing.

The HTML report adds four bar charts (throughput, network I/O, timing, pool wait) drawn from the `stats:` snapshots, with the pooler's events marked on them.

## JSON keys

`--json` and `--yaml` carry the poolers as a list under a top-level `pgbouncer` key, one entry per pooler. A pgBouncer-only run emits that key alone; a run that also holds PostgreSQL files adds it to the PostgreSQL document. Each entry holds:

- `name`, `files`, `listen`, `version` — who the pooler is (see [Several poolers](#several-poolers))
- `events` — its timeline, each `{timestamp, kind, detail}`; `kind` is `start`, `shutdown`, `reload`, or the lowercased console command (`pause`, `resume`, …)
- `start_date`, `end_date` — the first and last record kept, after any time filter, named and written as in the PostgreSQL summary; every time in an entry uses the same `"YYYY-MM-DD HH:MM:SS"` form, on the log's clock
- `lines_read`, `lines`, `lines_unrecognized`, `debug_lines` — lines scanned, entries understood, lines no parser could decode, and how many entries are DEBUG/NOISE
- `stats_sample_count`, `avg_xacts_per_sec`, `max_xacts_per_sec`, `avg_queries_per_sec`, `max_queries_per_sec` — every `max_*` rate is the highest snapshot average, not an instantaneous peak
- `avg_in_bytes_per_sec`, `max_in_bytes_per_sec`, `avg_out_bytes_per_sec`, `max_out_bytes_per_sec`
- `avg_xact_us`, `max_xact_us`, `avg_query_us`, `max_query_us`, `avg_wait_us`, `max_wait_us` — before pgBouncer 1.23, `avg_wait_us` and `max_wait_us` are microseconds of queueing per second (see [Reading the figures](#reading-the-figures))
- `wait_episodes` — `{start, end, peak_wait_us, peak_timestamp}` per stretch during which clients queued
- `max_xacts_per_sec_timestamp`, `max_queries_per_sec_timestamp`, `max_in_bytes_per_sec_timestamp`, `max_out_bytes_per_sec_timestamp`, `max_xact_us_timestamp`, `max_query_us_timestamp`, `max_wait_us_timestamp` — when each maximum was logged, omitted when there is none
- `login_attempts`, `server_connections` (connections **opened** to the servers, not held), `sessions`, `total_session_seconds`, `session_distribution`
- `session_reasons` — `{reason, count, total_seconds}` per close reason
- `connections_per_database`, `connections_per_user`, `connections_per_host`
- `sessions_per_database`, `sessions_per_user`, `sessions_per_host`
- `reserve_pool_activations`, `total_errors` (warnings and errors alike), and `errors` — `{id, level, message, count, first, last}` per distinct warning or error, `id` being the `--event-detail` handle
- `stats_series` — the full per-snapshot time series, ordered by time (used to draw the HTML charts)

## Limitations

- `pool_mode` is not readable from the log. pgBouncer writes it to no line, and a report of the same traffic under `session` and under `statement` pooling is identical but for the server-connection count — so quellog does not guess it. The mode changes what that figure is averaged over (one wait per session, per transaction, or per statement) and how to read "server connections opened"; see [pgBouncer Setup](pgbouncer-setup.md).
- The browser demo does not read pgBouncer logs. Its WebAssembly build carries the PostgreSQL parsers only, so a pooler log dropped on the page is recognized and refused with a message rather than parsed as a server log; the command-line tool reads it normally.
- `syslog = 1` captures are not detected (see above).
- A pooler log cannot be read from standard input; it is refused with a message (see above).
- **Grouping files into poolers rests on the log alone.** Two poolers on the same address whose logs do not overlap in time — two hosts' captures from different days — are read as one pooler restarted, and a restart into a new file on another address as two poolers. Pass one pooler's files at a time when that matters.
- **Session ages are whole seconds.** pgBouncer writes `(age=Ns)` with no fraction, so a pooler whose sessions all close in under a second reports `Average session time: 0s` — that is the log's resolution, not a missing measurement.
- **`(nodb)` is not a database.** It is pgBouncer's placeholder for a client that has not been bound to a pool yet, and it appears in the session breakdowns because a `closing because:` line carries no `db=` field to correct it. The connection breakdowns read that field and name the database the client actually asked for, so the two tables can disagree on a rejected login.
- The pool-wait mean is weighted by transactions per second, because the counter pgBouncer averages it over — server assignments — is absent from the log line. Under `pool_mode = transaction` a server is assigned about once per transaction and the mean comes close (on the P2 lab saga, 618 assignments for 630 transactions: 3.54 s against 3.68 s from `SHOW STATS`); under `session` (one per client session) or `statement` (one per statement) pooling it is a rougher approximation.
