# Run P1 -- one pooler, full story

Captured 2026-09-25 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2 rootless).
All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on the lab machine
immediately before/after each action. Full raw orchestration log with every
timestamp: `MANIFEST_timeline.log` (produced by `_lab/run_p1.sh`, which is the exact
script that was run -- nothing here was hand-edited).

## Versions / topology

- pgbouncer 1.25.2 (`/usr/sbin/pgbouncer`, apt.postgresql.org build), libevent
  2.1.13-stable (epoll), adns c-ares 1.34.5, TLS OpenSSL 3.5.7.
- PostgreSQL 17.9 (`docker.io/library/postgres:17`), single podman container
  `quellog-pg-p1`, port 15432 on the host, bind-mounted `pgdata`/`pglogs`.
- One pgbouncer process, port 6432, `pool_mode = transaction`.
- One database entry `bench` -> `host=127.0.0.1 port=15432 dbname=bench`.
- Auth: `auth_type = md5`, static `userlist.txt`, single role `bench`
  (superuser, bootstrap role of the image), `password_encryption = md5` set
  explicitly on the server so pgbouncer's md5 auth works end-to-end (the
  Docker image bootstraps the role under SCRAM by default -- see Surprises).
- `stats_period = 10`, `log_connections = 1`, `log_disconnections = 1`,
  `log_pooler_errors = 1`.
- Traffic: pgbench, dataset `pgbench -i -s 5` (500k rows in
  `pgbench_accounts`), driven entirely through the pooler (port 6432), never
  directly against PostgreSQL except for setup.
- Config used: `p1.pgbouncer.ini` (as it stood at the very end of the run,
  after all `RELOAD`s -- see the per-step table for the values in effect at
  each point). Passwords stripped (userlist path only, no secrets in the ini
  itself; the actual `userlist.txt` is not shipped).

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1. Normal traffic | 09:05:30.898 -> 09:07:00.916 | pgbouncer + PG start, `pgbench -i -s 5`, then a loop of `pgbench -c 3 -j 1 -T 15` (light traffic) against port 6432 | baseline connect/disconnect + periodic stats | 191 total `login attempt` over the whole run, `LOG stats:` every 10s from the start (e.g. `2116 xacts/s, 14814 queries/s ... wait 0 us` right after start) |
| 2. RELOAD after config change | 09:07:30.918 -> 09:07:30.926 | edited `default_pool_size` 5 -> 20 in the ini, `RELOAD;` on the admin console | `RELOAD command issued` in the log, no service interruption | `2026-09-25 09:07:30.924 UTC [637577] LOG RELOAD command issued`, traffic uninterrupted |
| 3. Saturation | 09:08:05.928 -> 09:09:08.964 | ini set to `default_pool_size=2, reserve_pool_size=2, reserve_pool_timeout=2, query_wait_timeout=3` + `RELOAD;`, then `pgbench -f sleep.sql -c 42 -j 8 -T 60` (each transaction: 1 read + `pg_sleep(0.5)`) | reserve pool activations and `query_wait_timeout` closures | 1x `taking connection from reserve_pool`, 22x `closing because: query_wait_timeout (age=3s)`; pgbench: 496 transactions in 60s, tps=7.88, avg latency 5333 ms, "Run was aborted" at the end (some client threads got permanently closed by pgbouncer's `query_wait_timeout` and pgbench does not reconnect them) |
| 4. PAUSE / RESUME | 09:09:38.975 -> 09:09:59.016 | `PAUSE bench;` on the admin console, wait 20s under traffic, `RESUME bench;` | server connections released immediately, new client connections held until resume | `LOG PAUSE 'bench' command issued` followed immediately by 3x `closing because: pause mode`; `SHOW POOLS` during the pause shows the `bench` pool still listed but server side drained; `RESUME 'bench' command issued` 20s later, traffic resumes |
| 5. Auth failures | 09:10:29.019 -> 09:10:29.042 | 3x wrong password, 1x unknown database (`nosuchdb`), 1x unknown user (`ghostuser`) via `psql -h 127.0.0.1 -p 6432` | rejections logged, ideally distinguishable reasons | 3x `closing because: password authentication failed`; `closing because: no such database: nosuchdb`; for the unknown user: pgbouncer logs the true reason as a plain `LOG` line `no such user: ghostuser`, but the actual disconnect/pooler-error line it emits is the generic `closing because: password authentication failed` / `pooler error: password authentication failed` -- see Surprises |
| 6. PostgreSQL restart | 09:10:59.044 -> 09:11:02.406 | `podman stop`/`sleep 2`/`podman start` on `quellog-pg-p1` under traffic (a plain `podman restart` was tried first and failed with a pasta port-rebind race -- see Surprises) | pgbouncer reports server-side failures, PostgreSQL logs shutdown+startup | pgbouncer: `WARNING server login failed: FATAL the database system is shutting down`, several `closing because: server conn crashed?` and `closing because: the database system is shutting down`; PostgreSQL: `received fast shutdown request` @09:10:59.097, `database system is shut down` @09:10:59.244, `starting PostgreSQL 17.9 ...` @09:11:01.402, `database system is ready to accept connections` @09:11:01.412 (~2.3s downtime); pgbouncer reconnected and traffic resumed within the same second reported ("postgres back up after 2s") |
| 7. pgbouncer restart | 09:11:32.411 -> 09:11:34.450 | `kill -INT` on the pgbouncer pid (clean/safe shutdown), then start again with the same `logfile =` path (append) | pgbouncer drains, exits, restarts, log continues in the same file | `LOG got SIGINT, shutting down, waiting for all servers connections to be released`, then `LOG server connections dropped, exiting`; new process (`process up: PgBouncer 1.25.2 ...`) appended to the same `p1.pgbouncer.log` 1s later, no gap in the file |
| 8. client_idle_timeout | 09:12:04.453 -> 09:12:29.467 | ini `client_idle_timeout=20` + `RELOAD;`, then one client runs `SELECT 1;`, sits idle 25s (`\! sleep 25`), then tries `SELECT 2;` | pgbouncer closes the idle client after 20s | `closing because: client_idle_timeout (age=20s)`; client sees `FATAL: client_idle_timeout` then `server closed the connection unexpectedly` |
| 9. Final traffic | 09:12:59.472 -> 09:14:29.490 | 90s more of the light traffic loop | normal stats, clean windup | stats lines continue every 10s (last one before teardown: `1727 xacts/s, 12095 queries/s ...`); no errors |

Every step above is separated by at least 30s of normal traffic (see
`MANIFEST_timeline.log` for the exact gaps).

## Raw counts (grep/awk on `p1.pgbouncer.log`, 513 lines, and
`p1.postgresql.log`, 124 lines)

pgbouncer:
- `login attempt`: 191
- `closing because` (total): 204, broken down:
  - `client close request`: 157
  - `query_wait_timeout`: 22
  - `pause mode`: 7
  - `server conn crashed?`: 4
  - `password authentication failed`: 4
  - `client unexpected eof`: 3
  - `client disconnect while server was not ready`: 3
  - `the database system is shutting down`: 1
  - `no such database`: 1
  - `idle server got dirty`: 1
  - `client_idle_timeout`: 1
- `new connection to server`: 15
- `LOG stats:`: 53
- `WARNING` lines: 33
- `ERROR` lines: 0
- `taking connection from reserve_pool`: 1
- `process up`: 2 (initial start + restart in step 7)
- `RELOAD command issued`: 4 (steps 2, 3-shrink, 3-restore, 8)
- `PAUSE 'bench' command issued`: 1
- `RESUME 'bench' command issued`: 1
- `SHUTDOWN command issued`: 1 (final teardown)
- `no such user`: 1 (the ghostuser attempt, logged internally even though the
  client-facing reason is generic)

PostgreSQL:
- `database system is ready to accept connections`: 3 (image bootstrap runs
  a short-lived temporary instance to create the `bench` role/db, shuts it
  down, then starts the real one that stays up -- that is 2 of the 3; the
  3rd is the deliberate step-6 restart)
- `database system is shut down`: 3 (same breakdown)
- `connection received`: 21, `connection authorized`: 20, `disconnection:`: 20
- `checkpoint starting` / `checkpoint complete`: 4 each
- No line crossed `log_min_duration_statement = 1000` (all pgbench statements
  are sub-millisecond except the deliberate `pg_sleep(0.5)`, which is not
  logged as "duration" by `log_min_duration_statement` because pg_sleep
  itself is fast to plan/execute -- the wall-clock time is spent inside the
  function, not measured against that GUC the way a slow query would be)

## SHOW STATS / SHOW POOLS / SHOW CLIENTS snapshots

See `MANIFEST_snapshots.log` (105 lines), taken at: step1 (normal traffic,
3 active clients/servers), step3 right after the pool shrink RELOAD (pool
still draining), step3 right after the 60s saturation load (pool back at
rest), step4 during the pause, and step9 (final state). Values are
consistent with the narrative: e.g. at step1, `sv_active=3` matching the 3
light-traffic pgbench clients; the pause snapshot shows the `bench` pool
present but empty server-side.

## Surprises

1. **SCRAM vs md5 on first boot.** The official `postgres:17` image bootstraps
   its initial role's password *before* the custom `-c password_encryption=md5`
   command-line flag is honoured for that specific step, so `bench`'s
   password was first stored as SCRAM-SHA-256 even though the running server
   reported `password_encryption = md5`. First connection attempt through
   pgbouncer failed with `cannot do SCRAM authentication: wrong password
   type`. Fixed by an explicit `ALTER ROLE bench WITH PASSWORD '...'` right
   after the server came up (now baked into `_lab/run_p1.sh`), which
   re-hashes the password using the now-active `password_encryption`.
2. **`podman restart` is not safe for the PostgreSQL-restart step.** A first
   attempt with the atomic `podman restart quellog-pg-p1` failed outright:
   `Error: pasta failed with exit code 1: Failed to bind port 15432 (Address
   already in use)`. Because the container never came back, PostgreSQL
   stayed down for the rest of that run (steps 7-9 all logged
   `client_login_timeout (server down)`) -- that whole run had to be
   discarded and redone. Splitting it into `podman stop`, `sleep 2`,
   `podman start` avoided the race reliably. `pg_ctl restart -m fast` run
   with `podman exec` inside the container was also tried and rejected: in
   this image postgres itself is PID 1, so `pg_ctl`'s stop phase kills PID 1
   and the whole container exits before the start phase can run.
3. **The example `query_wait_timeout=8` from the brief never actually fires.**
   With `default_pool_size=2` + `reserve_pool_size=2` (4 server slots) and
   42 clients each holding a slot for ~0.5s, the pool settles into a steady
   state with average client wait around 5.3s -- under an 8s timeout, so
   nothing gets cancelled and only reserve-pool grabs are visible. Lowered to
   `query_wait_timeout=3` to reliably produce cancellations while still
   leaving room for `reserve_pool_timeout=2` to fire first.
4. **Unknown user is not reported as such to the client.** pgbouncer's
   internal log clearly says `no such user: ghostuser`, but the
   client-facing `closing because:` / `pooler error:` line is the same
   generic `password authentication failed` used for a wrong password --
   i.e. only the raw `LOG` stream distinguishes "no such user" from "wrong
   password"; the WARNING pooler-error line does not.
5. `log_min_duration_statement=1000` never fired: the saturation episode's
   cost is 100% pooler queueing time (visible in pgbouncer's own `wait`
   accounting and in pgbench's client-side latency), not PostgreSQL query
   duration, so quellog's PostgreSQL-side slow-query detection has nothing
   to see for this run -- that gap is entirely visible only from the
   pgbouncer log side, which is the point of the run.
