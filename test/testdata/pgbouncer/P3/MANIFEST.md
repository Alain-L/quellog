# Run P3 -- pgbouncer 1.18.0 (old version), short saturation episode

Captured 2026-09-25 on lab machine `dalibo-pc`, using the pgbouncer 1.18.0
build already present at `~/pgb-src/old-install/bin/pgbouncer` (still runs
fine on Debian 13). All timestamps UTC. Orchestration log:
`MANIFEST_timeline.log` (produced by `_lab/run_p3.sh`).

## Versions / topology

- pgbouncer **1.18.0** (`PgBouncer 1.18.0, libevent 2.1.13-stable (epoll),
  adns: evdns2` -- note: `evdns2`, not `c-ares` as in 1.25.2 -- the adns
  backend itself changed between these versions).
- PostgreSQL 17.9, single podman container `quellog-pg-p3`, port 15434.
- One pooler, port 6434, `pool_mode = transaction`, same `bench` role/auth
  setup as P1/P2 (md5). Dataset: `pgbench -i -s 3` (smaller than P1/P2 to
  keep this short run light).

## Why this run exists

pgbouncer 1.23.0 (2024-07-03) shipped a fix for the `avg_wait_time` metric
calculation (`NEWS.md` 1.23.0 "Fixes" section, referencing PR #727). 1.18.0
(2022-12-12) predates that fix, so its `wait`/`avg_wait_time` figures (in
both the periodic `LOG stats:` line and `SHOW STATS`) carry the older,
pre-fix semantics. quellog's pgbouncer parser needs a fixture that actually
exercises this older version to validate that the version-dependent
handling is exercised, not just assumed from the changelog.

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1. Baseline | 09:23:03.564 -> 09:23:48.581 | light traffic loop (`pgbench -c 3 -j 1 -T 10`) | normal connect/stats | stats lines every 10s from the start |
| 2. Saturation | 09:24:03.586 -> 09:24:46.490 | shrink to `default_pool_size=2, reserve_pool_size=2, reserve_pool_timeout=2, query_wait_timeout=3` + `RELOAD;`, `pgbench -f sleep.sql -c 40 -j 8 -T 40` | reserve pool activations and `query_wait_timeout` closures, same as P1/P2 | 1x `taking connection from reserve_pool`, 17x `closing because: query_wait_timeout`; pgbench: 336 transactions in 40s, tps=7.85, avg latency 5098ms, run aborted (some clients permanently dropped by pgbouncer, same pattern as P1/P2) |
| 3. Final traffic | 09:25:01.492 -> 09:25:46.514 | 45s more light traffic | clean windup | stats continue normally, no errors |

## Raw counts (`p3-old.pgbouncer.log`, 238 lines; `p3-old.postgresql.log`, 60 lines)

pgbouncer:
- `login attempt`: 98
- `closing because` (total): 96 -- 79 `client close request`, 17
  `query_wait_timeout`
- `taking connection from reserve_pool`: 1
- `process up`: 1 -- exact banner: `LOG process up: PgBouncer 1.18.0,
  libevent 2.1.13-stable (epoll), adns: evdns2, tls: OpenSSL 3.5.7 9 Jun
  2026`
- `RELOAD command issued`: 2 (shrink + restore)

PostgreSQL:
- `database system is ready to accept connections`: 2 (both part of the
  image bootstrap; this run never restarts PostgreSQL)
- `database system is shut down`: 2, `checkpoint starting`/`checkpoint
  complete`: 2 each

## Format differences vs. 1.25.2 (P1/P2), confirmed directly from the log

- The periodic `LOG stats:` line in 1.18.0 has **fewer fields** than 1.25.2:
  no `client parses/s`, `server parses/s`, or `binds/s` counters (added in
  a later release for extended-protocol / prepared-statement tracking).
  Example from this run: `LOG stats: 1975 xacts/s, 13828 queries/s, in
  842325 B/s, out 368004 B/s, xact 1346 us, query 182 us, wait 1539 us` --
  compare to a 1.25.2 line in P1: `LOG stats: 2116 xacts/s, 14814 queries/s,
  0 client parses/s, 0 server parses/s, 0 binds/s, in 903931 B/s, out
  394261 B/s, xact 1184 us, query 159 us, wait 0 us`.
- `SHOW STATS` in 1.18.0 similarly has no
  `*_client_parse_count`/`*_server_parse_count`/`*_bind_count` columns and
  no `total_server_assignment_count`/`avg_server_assignment_count` columns
  present in 1.25.2's output (see `MANIFEST_snapshots.log`, compare to P1's
  `MANIFEST_snapshots.log`).
- `adns` backend in the process-up banner: `evdns2` (1.18.0) vs. `c-ares`
  (1.25.2) -- a purely informational difference but a real one that shows
  up verbatim in the log.

## Surprises

1. First attempt at this run reused the *uncorrected* `run_p3.sh` (still
   carrying `query_wait_timeout=8` from the original draft, the same
   deployment slip already made once for P1 and once for P2 -- the fixed
   local file was not re-copied to the lab machine before launching). This
   was caught within seconds from the live timeline (before the saturation
   step even started) and the whole run was restarted cleanly with the
   corrected script; nothing from that aborted attempt is in the shipped
   logs.
2. Old pgbouncer 1.18.0 still starts and runs without any compatibility
   issue on Debian 13 against a modern PostgreSQL 17.9 server and against
   the same `auth_type = md5` config format used for 1.25.2 -- no ini/config
   syntax changes were needed between the two versions for this scenario.

## SHOW STATS / SHOW POOLS snapshots

`MANIFEST_snapshots.log` (52 lines), taken at step1 baseline, step2 right
after the shrink RELOAD, and step2 right after the saturation load.
