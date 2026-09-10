# pgBouncer Reports

quellog analyzes [pgBouncer](https://www.pgbouncer.org/) connection-pooler logs and renders a dedicated report. The format is auto-detected (see [Log Formats](formats.md#pgbouncer)) — point quellog at a pgBouncer log and it produces the pooler report instead of the PostgreSQL one:

```bash
quellog /var/log/pgbouncer/pgbouncer.log
quellog /var/log/pgbouncer/pgbouncer.log --html -o report.html
quellog /var/log/pgbouncer/pgbouncer.log --json
```

Time filters (`--begin`, `--end`, `--last`, `--window`) apply. The SQL-specific flags (`--sql-summary`, `--sql-detail`, `--split`, …) do not apply to pgBouncer logs.

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
| `LOG stats: <n> xacts/s, <n> queries/s, …, in <n> B/s, out <n> B/s, xact <n> us, query <n> us, wait <n> us` | Request throughput, network I/O, and transaction/query/wait timing (per-second averages over the pooler's `stats_period`) |
| `LOG C-0x…: db/user@host login attempt: …` | Client connections (login attempts) and the per-database/user/host breakdown |
| `LOG S-0x…: … new connection to server …` | Server connections |
| `LOG …: … closing because: <reason> (age=Ns)` | Completed sessions, the session-time histogram, and per-database/user/host breakdown |
| `WARNING`/`ERROR`/`FATAL …` | The ranked error list (including `pooler error: …` and `taking connection from reserve_pool`) |

## Report sections

Text (default), Markdown (`--md`), JSON (`--json`), and HTML (`--html`) all expose the same information:

- **Summary** — time range, log lines, stats snapshots, login attempts, server connections, completed sessions, reserve-pool activations, errors.
- **Request throughput** — average and peak transactions/s and queries/s.
- **Network I/O** — average and peak bytes in/out per second.
- **Query timing** — average and peak transaction, query, and wait time. pgBouncer reports these in microseconds; quellog keeps that resolution (`842 µs`, `3.4 ms`, `2.4 s`) rather than rounding to milliseconds.
- **Sessions** — completed session count, average session time, a session-time histogram (same buckets as the PostgreSQL connection report), and sessions per database/user/host.
- **Connections** — login attempts and server connections, plus connections per database/user/host.
- **Most frequent errors** — errors and warnings ranked by frequency, with the variable socket id, peer, and session age stripped so the same failure collapses into one bucket.

The HTML report adds three interactive time-series charts (throughput, network I/O, timing) drawn from the `stats:` snapshots.

## JSON keys

`--json` emits a flat object. Notable keys:

- `stats_sample_count`, `avg_xacts_per_sec`, `max_xacts_per_sec`, `avg_queries_per_sec`, `max_queries_per_sec`
- `avg_in_bytes_per_sec`, `max_in_bytes_per_sec`, `avg_out_bytes_per_sec`, `max_out_bytes_per_sec`
- `avg_xact_us`, `max_xact_us`, `avg_query_us`, `max_query_us`, `avg_wait_us`, `max_wait_us`
- `login_attempts`, `server_connections`, `sessions`, `total_session_seconds`, `session_distribution`
- `connections_per_database`, `connections_per_user`, `connections_per_host`
- `sessions_per_database`, `sessions_per_user`, `sessions_per_host`
- `reserve_pool_activations`, `errors`, `total_errors`
- `stats_series` — the full per-snapshot time series (used to draw the HTML charts)
