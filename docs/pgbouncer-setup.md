# pgBouncer Setup

## Recommended Configuration

```ini
# pgbouncer.ini, [pgbouncer] section

# Log destination
logfile = /var/log/pgbouncer/pgbouncer.log
syslog = 0                              # syslog captures are not read (see below)

# Client connection logging
log_connections = 1
log_disconnections = 1
log_pooler_errors = 1

# Telemetry cadence
stats_period = 15                       # seconds; default 60

# The mode the report cannot see
pool_mode = transaction
```

Apply with `RELOAD;` on the admin console (`psql -p 6432 pgbouncer`). `logfile`
and `syslog` need a restart; `log_connections`, `log_disconnections`,
`log_pooler_errors` and `stats_period` take effect on reload.

!!! warning "Three settings decide how much of the report can exist"
    Unlike PostgreSQL, pgBouncer logs no schema of what it is not logging. With
    `log_connections = 0` the Sessions and Connections sections have no input at
    all; with a `stats_period` longer than the capture, the throughput, timing
    and pool-wait figures have none. quellog names the setting in each empty
    section rather than dropping it, but it cannot recover the lines.

!!! tip "Volume"
    `log_connections` / `log_disconnections` cost two lines per client
    connection, which is cheap under transaction pooling and expensive under a
    workload that reconnects per query. `stats_period` costs one line per
    period regardless of traffic: lowering it from 60 to 15 quadruples a
    negligible number.

!!! danger "verbose"
    `verbose = 1` and `verbose = 2` add DEBUG and NOISE lines — in a real
    capture, 1 686 lines for six transactions. quellog reads them and reports
    their share on its own summary line, but they drown the operational signal
    and the file grows by two orders of magnitude. Leave `verbose` unset outside
    a debugging window.


## What Each Setting Enables

| Setting | quellog section | Notes |
|---------|----------------|-------|
| `log_connections` | Connections | Login attempts and the per-database/user/host breakdown. `0` empties the section entirely — there is no other source for a client connection |
| `log_disconnections` | Sessions | Completed sessions, session-time histogram, per-database/user/host breakdown. Session duration comes from the `(age=Ns)` field of the disconnection line, so `0` also removes every duration |
| `log_pooler_errors` | Most Frequent Errors | Pooler-side failures (`query_wait_timeout`, `client_idle_timeout`, authentication failures) and `taking connection from reserve_pool` |
| `stats_period` | Request Throughput, Query Timing, Pool Wait | One `stats:` line per period, each averaging that period. Controls **coverage, not detail**: a capture shorter than one period holds no line at all, and a period far longer than the capture yields a single startup snapshot whose figures describe the idle interval before the traffic |
| `logfile` | all | A file, not `syslog`. See the limitation below |
| `pool_mode` | Pool Wait (interpretation only) | Not readable from the log. See below |

### Why `stats_period` is the one to lower

Every throughput, timing and pool-wait figure in the report is computed from
`stats:` lines and from nothing else. pgBouncer emits one per `stats_period`,
each carrying averages over the period that just elapsed, and logs nothing
between two of them. At the default `stats_period = 60`:

- a capture of under a minute has **no** telemetry at all;
- a capture of a few minutes has two or three points, so the charts have two or
  three points and a burst is flattened across a whole minute;
- a pooler restarted shortly before the capture contributes a first snapshot of
  all zeros — the period before it really was idle — which, alone, reads as
  "no traffic".

quellog states the coverage when the snapshots account for less than half of
the window, and says when there is no snapshot at all, but a short period is
what produces a report worth reading. `15` gives four points a minute at one
line a minute in cost.

### `pool_mode` is not in the log

pgBouncer writes `pool_mode` to no log line, and the report of a given traffic
is the same under `session`, `transaction` and `statement` pooling. It matters
anyway, because it changes what two figures mean:

- **Pool wait** is pgBouncer's mean time per *server assignment*. Under session
  pooling that is one wait per client session, under transaction pooling one per
  transaction, under statement pooling one per statement. The same `3 ms` is a
  once-per-session cost in the first case and a per-statement cost in the third.
- **Server connections opened** counts `new connection to server` lines. Under
  session pooling a burst of short-lived clients can force the pool to grow;
  under transaction pooling the same burst reuses a handful of backends.

Read both against the `pool_mode` in your `pgbouncer.ini`; quellog cannot tell
you which one produced the file.


## Limitations

- **`syslog = 1` captures are not read.** The syslog envelope replaces the
  severity word (`LOG`, `WARNING`, `FATAL`) with the protocol priority, so the
  lines match neither the pooler format nor the PostgreSQL one and the file is
  rejected. Log to a `logfile`.
- **Standard input is not detected** as a pooler log; point quellog at the file.
- See [pgBouncer Reports](pgbouncer.md) for what the report does with all of
  this.
