# pgBouncer Reports

quellog analyzes [pgBouncer](https://www.pgbouncer.org/) connection-pooler logs and renders a dedicated report. The format is auto-detected (see [Log Formats](formats.md#pgbouncer)) — point quellog at a pgBouncer log and it produces the pooler report instead of the PostgreSQL one:

```bash
quellog /var/log/pgbouncer/pgbouncer.log
quellog /var/log/pgbouncer/pgbouncer.log --html -o report.html
quellog /var/log/pgbouncer/pgbouncer.log --json
```

Three pgBouncer settings decide how much of that report can exist: `log_connections` and `log_disconnections` gate the client lines, `stats_period` gates the telemetry lines, and `pool_mode` — which pgBouncer logs nowhere — decides what the pool-wait figure is averaged over. [pgBouncer Setup](pgbouncer-setup.md) prescribes a configuration that produces a complete report.

Compressed and archived inputs work as they do for PostgreSQL logs: `.gz`, `.zst`, and `.tar`/`.tgz`/`.tzst`/`.zip`/`.7z` archives are read in place and produce the same report as the plain file. A log read from standard input (`-`) is not: the format is detected from the path, so a pooler log piped in is not recognized and the run fails. Point quellog at the file instead.

pgBouncer running with `syslog = 1` is not supported in this release. The syslog envelope replaces the severity word with the protocol priority, so those lines match neither the pooler format nor the PostgreSQL one and the file is rejected outright.

Time filters (`--begin`, `--end`, `--last`, `--window`) apply. The attribute filters (`--dbname`, `--dbuser`, `--appname`) select PostgreSQL backends through the `log_line_prefix` and are not applied to the pooler track. The SQL-specific flags (`--sql-summary`, `--sql-detail`, …) have nothing to report on pooler telemetry and are ignored; `--split` is rejected (see below).

## Combining with PostgreSQL logs

pgBouncer and PostgreSQL logs are always separate files (separate daemons). When a single invocation mixes both, quellog partitions the inputs by detected format and analyzes each on its own track — the report is the PostgreSQL report followed by a pgBouncer section:

```bash
quellog /var/log/postgresql/*.log /var/log/pgbouncer/pgbouncer.log
quellog /var/log/postgresql/*.log /var/log/pgbouncer/pgbouncer.log --html -o report.html
```

In text and Markdown the pgBouncer report is appended after the PostgreSQL one; in JSON it is a top-level `pgbouncer` key; in HTML it is a section in the same report, sharing the report's chrome and theme. (`--split` is PostgreSQL-only and is rejected when pgBouncer files are present.)

## What is parsed

pgBouncer log lines are single-line records with a `timestamp [PID] SEVERITY` prefix. quellog reads four kinds of line:

| Line | Contributes to |
|------|----------------|
| `LOG stats: <n> xacts/s, <n> queries/s, …, in <n> B/s, out <n> B/s, xact <n> us, query <n> us, wait <n> us` | Request throughput, network I/O, and transaction/query/wait timing. The `/s` and `B/s` fields are rates averaged over the pooler's `stats_period`; `xact`, `query` and `wait` are not rates but mean durations per operation, each over its own counter (transactions, queries, and server assignments respectively) |
| `LOG C-0x…: db/user@host login attempt: …` | Client connections (login attempts) and the per-database/user/host breakdown |
| `LOG S-0x…: … new connection to server …` | Server connections |
| `LOG …: … closing because: <reason> (age=Ns)` | Completed sessions, the session-time histogram, and per-database/user/host breakdown |
| `WARNING`/`ERROR`/`FATAL …` | The ranked error list (including `pooler error: …` and `taking connection from reserve_pool`) |

## Report sections

Text (default), Markdown (`--md`), JSON (`--json`), YAML (`--yaml`), and HTML (`--html`) carry the same figures. The section names below are the text and Markdown ones; HTML groups them into cards and charts, and the serialized formats expose them as the keys listed further down.

- **Summary** — time range, lines read, entries understood (plus the lines no parser could decode and the DEBUG/NOISE share of the rest), stats snapshots, login attempts, server connections opened, completed sessions, reserve-pool activations, errors. "Server connections opened" counts the `new connection to server` lines over the window, not the connections held at a point in time: a pool capped at five by `default_pool_size` + `reserve_pool_size` legitimately opens many more over an hour, so the figure is not comparable with a `SHOW POOLS` column.
- **Request throughput** — transactions/s, queries/s and bytes in/out per second, each as an average and as the highest snapshot average. (Network I/O is not a section of its own in text or Markdown; it is a separate chart in HTML.) The second figure is deliberately not called a peak: pgBouncer logs one rate per `stats_period`, so at the default 60 s a real spike is flattened across the minute and the largest value in the capture is the busiest snapshot average, never the busiest instant.
- **Query timing** — transaction and query time. pgBouncer only ever logs one average per snapshot, so the averages are weighted by each snapshot's operation count and skip the snapshots that timed none of that kind (which is why they can rest on fewer snapshots than the summary counts), and the figure beside them is the highest snapshot average, never the slowest single operation. pgBouncer reports these in microseconds; quellog keeps that resolution (`842 µs`, `3.4 ms`, `2.4 s`) rather than rounding to milliseconds.
- **Pool wait** — how long clients queued before a server connection was assigned, averaged the same way. It sits in its own section because it answers a different question than the two above: a long query time is a query problem, a long pool wait is a sizing problem, read against `default_pool_size`, `reserve_pool_size` and `max_db_connections`. In HTML the three curves share one chart, so they can be compared on a common time axis.
- **Sessions** — completed session count, average session time, a session-time histogram (same buckets as the PostgreSQL connection report), and sessions per database/user/host. Fed by the `closing because: … (age=Ns)` lines, which `log_disconnections = 0` removes.
- **Connections** — login attempts and server connections opened, plus connections per database/user/host. Fed by the `login attempt` lines, which `log_connections = 0` removes.
- **Most frequent errors** — errors and warnings ranked by frequency, with the variable socket id, peer, and session age stripped so the same failure collapses into one bucket. Reserve-pool activations (`taking connection from reserve_pool`) are ranked here and included in the error total, although they report a capacity event rather than a failure; they are also counted on their own line in the summary.

### Empty sections say why

A section with no input is rendered empty rather than dropped, with the `pgbouncer.ini` setting that governs it. The report has no other way to let a reader tell an idle pooler from a silenced one, and the two produce the same figures — zero:

- **Sessions and Connections** always appear. When the same window carries proof the pooler served clients — transactions or queries in a `stats:` line, a server connection opened, or an error — the placeholder says so explicitly, since traffic without a single client line means the lines are missing rather than the clients. With nothing to contradict an idle pooler, it states the absence and names the setting, without diagnosing a configuration it cannot see.
- **Request throughput, query timing and pool wait** come from the `stats:` lines only. With none in the window, one placeholder covers all three and names `stats_period`; a capture shorter than one period legitimately holds no such line.
- When the snapshots that do exist account for **less than half of the reported window** — or for an unknown share of it, which a single snapshot always does — the figures are shown with the coverage stated above them. `stats_period` is inferred from the median interval between consecutive snapshots; pgBouncer never logs the setting itself.

None of these verdicts adds a JSON key: they are derived from `stats_sample_count`, `stats_series[].time`, `min_timestamp`, `max_timestamp`, `login_attempts`, `sessions`, `max_queries_per_sec`, `max_xacts_per_sec`, `server_connections` and `total_errors`, all of which the document already carries.

The ranked breakdowns (sessions and connections per database/user/host, and the error list) show their ten heaviest rows and then state the rest as `… and N more`, the same form the PostgreSQL report uses for its own capped tables. On a pooler fronting many tenants that line is the difference between a head and a total: ten rows out of thousands can sum to a few percent, and the section title names the dimension, not the cut.

The HTML report adds three interactive time-series charts (throughput, network I/O, timing) drawn from the `stats:` snapshots.

## JSON keys

The keys below are those of a pgBouncer-only run, where `--json` and `--yaml` emit a flat object. When the same invocation also holds PostgreSQL files, the document is the PostgreSQL one and these keys sit nested under a top-level `pgbouncer` key instead. Notable keys:

- `min_timestamp`, `max_timestamp` — the first and last record kept, after any time filter
- `lines_read`, `lines`, `lines_unrecognized`, `debug_lines` — lines scanned, entries understood, lines no parser could decode, and how many entries are DEBUG/NOISE
- `stats_sample_count`, `avg_xacts_per_sec`, `max_xacts_per_sec`, `avg_queries_per_sec`, `max_queries_per_sec` — every `max_*` rate is the highest snapshot average, not an instantaneous peak
- `avg_in_bytes_per_sec`, `max_in_bytes_per_sec`, `avg_out_bytes_per_sec`, `max_out_bytes_per_sec`
- `avg_xact_us`, `max_xact_us`, `avg_query_us`, `max_query_us`, `avg_wait_us`, `max_wait_us`
- `login_attempts`, `server_connections` (connections **opened** to the servers, not held), `sessions`, `total_session_seconds`, `session_distribution`
- `connections_per_database`, `connections_per_user`, `connections_per_host`
- `sessions_per_database`, `sessions_per_user`, `sessions_per_host`
- `reserve_pool_activations`, `errors`, `total_errors`
- `stats_series` — the full per-snapshot time series, ordered by time (used to draw the HTML charts)

## Limitations

- `pool_mode` is not readable from the log. pgBouncer writes it to no line, and a report of the same traffic under `session` and under `statement` pooling is identical but for the server-connection count — so quellog states the ambiguity next to the pool-wait figure instead of guessing. The mode changes what that figure is averaged over (one wait per session, per transaction, or per statement) and how to read "server connections opened"; see [pgBouncer Setup](pgbouncer-setup.md).
- The browser demo does not read pgBouncer logs. Its WebAssembly build carries the PostgreSQL parsers only, so a pooler log dropped on the page is not recognized; the command-line tool reads it normally.
- `syslog = 1` captures are not detected (see above).
- A log read from standard input is not detected as a pooler log (see above).
