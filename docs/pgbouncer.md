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

A pooler is named after its first listen address (`listening on …` at startup), or after its first file when the log holds no such line. Two poolers with the same name get their files appended to it. The text and Markdown reports repeat their sections once per pooler, titled with its name; HTML shows one pgBouncer section per pooler.

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

## Report sections

Text (default), Markdown (`--md`), JSON (`--json`), YAML (`--yaml`), and HTML (`--html`) carry the same figures, with one exception: HTML states each stats-line average, but of the highest-snapshot counterparts the other formats print beside them it shows only the one for queries per second, and it gives no time for any of them. The charts carry that shape instead. The section names below are the text and Markdown ones; HTML groups them into cards and charts, and the serialized formats expose them as the keys listed further down.

- **Summary** — the pooler's name, release and files, time range, lines read, entries understood (plus the lines no parser could decode and the DEBUG/NOISE share of the rest), stats snapshots, login attempts, server connections opened, completed sessions, reserve-pool activations, errors. "Server connections opened" counts the `new connection to server` lines over the window, not the connections held at a point in time: a pool capped at five by `default_pool_size` + `reserve_pool_size` legitimately opens many more over an hour, so the figure is not comparable with a `SHOW POOLS` column. Below it, the **timeline** lists the pooler's starts (with the release), shutdowns (with the signal or the `SHUTDOWN` command), reloads (`RELOAD` or `SIGHUP`) and admin console commands such as `PAUSE 'db'` and `RESUME`, each with its time.
- **Request throughput** — transactions/s, queries/s and bytes in/out per second, each as an average and as the highest snapshot average, with the time of that snapshot. (Network I/O is not a section of its own in text or Markdown; it is a separate chart in HTML.) The second figure is deliberately not called a peak: pgBouncer logs one rate per `stats_period`, so at the default 60 s a real spike is flattened across the minute and the largest value in the capture is the busiest snapshot average, never the busiest instant.
- **Query timing** — transaction and query time. pgBouncer only ever logs one average per snapshot, so the averages are weighted by each snapshot's operation count and skip the snapshots that timed none of that kind (which is why they can rest on fewer snapshots than the summary counts), and the figure beside them is the highest snapshot average, never the slowest single operation. pgBouncer reports these in microseconds; quellog keeps that resolution (`842 µs`, `3.4 ms`, `2.4 s`) rather than rounding to milliseconds.
- **Pool wait** — how long clients queued before a server connection was assigned, averaged the same way. It sits in its own section because it answers a different question than the two above: a long query time is a query problem, a long pool wait is a sizing problem, read against `default_pool_size`, `reserve_pool_size` and `max_db_connections`. In HTML the three curves share one chart, so they can be compared on a common time axis. Below the figure, the stretches during which clients queued are listed with their start, end, duration and worst snapshot: runs of consecutive snapshots with a wait of 100 ms or more, starting at the snapshot before the first, since a `stats:` line describes the period it closes. On the lab captures behind the tests, normal traffic stays under 4 ms and every saturation above 190 ms. Before pgBouncer 1.23 the `wait` field meant something else — the time all clients spent queued per second of wall time, a queue depth — so for a pooler whose start banner names an earlier release the section shows it as the number of clients queued on average, averaged over time like the rates, says why, and HTML leaves it out of the chart drawn in microseconds.
- **Sessions** — completed session count, average session time, a session-time histogram (same buckets as the PostgreSQL connection report), how many sessions the client ended and how many the pooler ended (a reason starting with "client ", such as `client close request`, is the client's; any other, `client_idle_timeout` included, is the pooler refusing or killing the session), the sessions per close reason with the average age of each, and sessions per database/user/host. The close reasons separate populations the average blurs: on a saturated pooler, clients killed in the queue by `query_wait_timeout` after a few seconds sit next to sessions the client ended after a minute. Fed by the `closing because: … (age=Ns)` lines, which `log_disconnections = 0` removes.
- **Connections** — login attempts and server connections opened, plus connections per database/user/host. Fed by the `login attempt` lines, which `log_connections = 0` removes.
- **Most frequent errors** — errors and warnings ranked by frequency, each with its first and last occurrence, with the variable socket id, peer, and session age stripped so the same failure collapses into one bucket. The list is not cut at 80% of the total, as the breakdowns are: the rare error is often the one worth reading, and up to twenty are shown. Reserve-pool activations (`taking connection from reserve_pool`) are ranked here and included in the error total, although they report a capacity event rather than a failure; they are also counted on their own line in the summary.
- **Most Frequent Errors is the one section that is dropped rather than rendered empty.** A capture with no `WARNING`/`ERROR`/`FATAL` line has no section at all, which reads like a healthy pooler — and `log_pooler_errors = 0` produces exactly that on a pooler that is failing. The summary still carries `Errors : 0`, so no figure is wrong, but the section's absence is not the proof of health it looks like.

### Empty sections say why

A section with no input is rendered empty rather than dropped, with the `pgbouncer.ini` setting that governs it. The report has no other way to let a reader tell an idle pooler from a silenced one, and the two produce the same figures — zero:

- **Sessions and Connections** always appear. When the same window carries proof the pooler served clients — transactions or queries in a `stats:` line, a server connection opened, or an error — the placeholder says so explicitly, since traffic without a single client line means the lines are missing rather than the clients. With nothing to contradict an idle pooler, it states the absence and names the setting, without diagnosing a configuration it cannot see.
- **Request throughput, query timing and pool wait** come from the `stats:` lines only. With none in the window, one placeholder covers all three and names `stats_period`; a capture shorter than one period legitimately holds no such line.
- When the snapshots that do exist account for **less than half of the reported window** — or for an unknown share of it, which a single snapshot always does — the figures are shown with the coverage stated above them. `stats_period` is inferred from the median interval between consecutive snapshots; pgBouncer never logs the setting itself.

None of these verdicts adds a JSON key: they are derived from `stats_sample_count`, `stats_series[].time`, `min_timestamp`, `max_timestamp`, `login_attempts`, `sessions`, `max_queries_per_sec`, `max_xacts_per_sec`, `server_connections` and `total_errors`, all of which the document already carries.

The ranked breakdowns (sessions and connections per database/user/host) show their ten heaviest rows and then state the rest as `… and N more`, the same form the PostgreSQL report uses for its own capped tables. On a pooler fronting many tenants that line is the difference between a head and a total: ten rows out of thousands can sum to a few percent, and the section title names the dimension, not the cut.

The HTML report adds three interactive time-series charts (throughput, network I/O, timing) drawn from the `stats:` snapshots.

## JSON keys

`--json` and `--yaml` carry the poolers as a list under a top-level `pgbouncer` key, one entry per pooler. A pgBouncer-only run emits that key alone; a run that also holds PostgreSQL files adds it to the PostgreSQL document. Each entry holds:

- `name`, `files`, `listen`, `version` — who the pooler is (see [Several poolers](#several-poolers))
- `events` — its timeline, each `{timestamp, kind, detail}`; `kind` is `start`, `shutdown`, `reload`, or the lowercased console command (`pause`, `resume`, …)
- `min_timestamp`, `max_timestamp` — the first and last record kept, after any time filter
- `lines_read`, `lines`, `lines_unrecognized`, `debug_lines` — lines scanned, entries understood, lines no parser could decode, and how many entries are DEBUG/NOISE
- `stats_sample_count`, `avg_xacts_per_sec`, `max_xacts_per_sec`, `avg_queries_per_sec`, `max_queries_per_sec` — every `max_*` rate is the highest snapshot average, not an instantaneous peak
- `avg_in_bytes_per_sec`, `max_in_bytes_per_sec`, `avg_out_bytes_per_sec`, `max_out_bytes_per_sec`
- `avg_xact_us`, `max_xact_us`, `avg_query_us`, `max_query_us`, `avg_wait_us`, `max_wait_us` — before pgBouncer 1.23, `avg_wait_us` and `max_wait_us` are microseconds of queueing per second (see Pool wait above)
- `wait_episodes` — `{start, end, peak_wait_us, peak_timestamp}` per stretch during which clients queued
- `max_xacts_per_sec_timestamp`, `max_queries_per_sec_timestamp`, `max_in_bytes_per_sec_timestamp`, `max_out_bytes_per_sec_timestamp`, `max_xact_us_timestamp`, `max_query_us_timestamp`, `max_wait_us_timestamp` — when each maximum was logged, omitted when there is none
- `login_attempts`, `server_connections` (connections **opened** to the servers, not held), `sessions`, `total_session_seconds`, `session_distribution`
- `session_reasons` — `{reason, count, total_seconds}` per close reason
- `connections_per_database`, `connections_per_user`, `connections_per_host`
- `sessions_per_database`, `sessions_per_user`, `sessions_per_host`
- `reserve_pool_activations`, `total_errors`, and `errors` — `{message, count, first, last}` per distinct error
- `stats_series` — the full per-snapshot time series, ordered by time (used to draw the HTML charts)

## Limitations

- `pool_mode` is not readable from the log. pgBouncer writes it to no line, and a report of the same traffic under `session` and under `statement` pooling is identical but for the server-connection count — so quellog states the ambiguity next to the pool-wait figure instead of guessing. The mode changes what that figure is averaged over (one wait per session, per transaction, or per statement) and how to read "server connections opened"; see [pgBouncer Setup](pgbouncer-setup.md).
- The browser demo does not read pgBouncer logs. Its WebAssembly build carries the PostgreSQL parsers only, so a pooler log dropped on the page is recognized and refused with a message rather than parsed as a server log; the command-line tool reads it normally.
- `syslog = 1` captures are not detected (see above).
- A pooler log cannot be read from standard input; it is refused with a message (see above).
- **Grouping files into poolers rests on the log alone.** Two poolers on the same address whose logs do not overlap in time — two hosts' captures from different days — are read as one pooler restarted, and a restart into a new file on another address as two poolers. Pass one pooler's files at a time when that matters.
- **Session ages are whole seconds.** pgBouncer writes `(age=Ns)` with no fraction, so a pooler whose sessions all close in under a second reports `Average session time: 0s` — that is the log's resolution, not a missing measurement.
- **`(nodb)` is not a database.** It is pgBouncer's placeholder for a client that has not been bound to a pool yet, and it appears in the session breakdowns because a `closing because:` line carries no `db=` field to correct it. The connection breakdowns read that field and name the database the client actually asked for, so the two tables can disagree on a rejected login.
- The pool-wait mean is weighted by transactions per second, because the counter pgBouncer averages it over — server assignments — is absent from the log line. Under `pool_mode = transaction` a server is assigned about once per transaction and the mean comes close (on the P2 lab saga, 618 assignments for 630 transactions: 3.54 s against 3.68 s from `SHOW STATS`); under `session` (one per client session) or `statement` (one per statement) pooling it is a rougher approximation.
