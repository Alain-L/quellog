# ptlab - Patroni test lab for quellog fixtures

Reproduces the 5 Patroni saga runs (A/B/C/D/E) used as real-log fixtures for quellog's
Patroni report. Built and run on `dalibo-pc` (Debian 13, Podman 5.4.2, docker-compatible
CLI aliased to `docker`).

## Images

Built from `build/Containerfile` (postgres:17 Debian base + a Python venv with Patroni,
psycopg3, python-json-logger):

```
docker build --build-arg PG_VERSION=17 --build-arg PATRONI_VERSION=4.1.5 \
  -t localhost/ptlab-patroni:4.1.5-pg17 -f build/Containerfile build/
docker build --build-arg PG_VERSION=17 --build-arg PATRONI_VERSION=3.3.11 \
  -t localhost/ptlab-patroni:3.3.11-pg17 -f build/Containerfile build/
```

`patroni[etcd3]` extra pulls in `python-etcd` (used by Patroni's etcd/etcd3 DCS
handlers for common helpers) plus `python-etcd3`-style REST/JSON-gateway access
implemented internally by Patroni itself over `requests`. No grpc dependency needed.

## Topology

- etcd v3.5.17 (quay.io/coreos/etcd), 3 nodes per run: `<prefix>-etcd1/2/3`
- 3 Patroni nodes per run: `<prefix>-pg1/2/3`, scope `main`
- Two podman networks per run: `<prefix>-dcs` (etcd + patroni, used for DCS traffic)
  and `<prefix>-repl` (patroni nodes only, used for postgres streaming/client
  traffic). This lets us isolate a node from etcd alone (`docker network disconnect
  <prefix>-dcs <prefix>-pgN`) without touching PostgreSQL streaming replication.
- Container names/networks are all prefixed `ptlab-<run>-...` per run id (a/b/c/d/e),
  never touching any other lab on the host.
- All containers run with `TZ=UTC`.
- Run E's pg containers additionally get `--cap-add=NET_ADMIN` (`NEED_NET_ADMIN=true`)
  so `iptables` can run as root inside them (`docker exec -u root`), used to physically
  cut the replication TCP stream (port 5432) between two replicas and the leader while
  leaving the DCS/REST networks untouched - see "Lessons learned" below.

## Config

- `scripts/common.sh`: all lab functions (network/etcd/patroni bring-up, teardown,
  patronictl/psql wrappers, saga helpers: burst_write, generate_wal_mb,
  pause/resume replay, isolate/rejoin DCS, kill/start node).
- `scripts/common.sh:gen_patroni_conf()` generates each node's `patroni.yml` from a
  single template (scope `main`, `etcd3:` DCS section, `use_pg_rewind: true`,
  `use_slots: true`, `ttl: 30`, `loop_wait: 10`, `retry_timeout: 10`,
  `maximum_lag_on_failover: 1048576`). `synchronous_mode: true` is added for run B.
  `log: type: json` is added for run D (Patroni 4.1.5's documented default JSON
  fields: `asctime`, `levelname`, `message`, since `log.format` is left at Patroni's
  default `%(asctime)s %(levelname)s: %(message)s` and the JSON formatter turns
  that into field names).
- PostgreSQL parameters set via Patroni's `bootstrap.dcs.postgresql.parameters`:
  `log_line_prefix = '%m [%p] %q%u@%d '`, `log_connections/log_disconnections off`,
  `log_checkpoints on`, `logging_collector on`, `log_directory=/var/log/postgresql`.
  `log_filename` and rotation params are left at PostgreSQL's own compiled-in
  defaults (NOT overridden), so a postmaster restart (e.g. after pg_rewind or
  reinit) creates a new dated log file per run's node - these were concatenated in
  chronological order into one `pgN.postgresql.log` per node for the fixtures.
- Bootstrap superuser is also used as the pg_rewind user (simplification for a test
  lab; not a production recommendation).
- `postgresql.parameters.unix_socket_directories` is `/tmp` (**not** `.`/PGDATA - see
  "Lessons learned" below).

## Saga scripts

- `scripts/saga_a.sh`: Run A, Patroni 4.1.5, async, full 10-step saga.
- `scripts/saga_b.sh`: Run B, Patroni 4.1.5, `synchronous_mode: true`, short saga.
- `scripts/saga_c.sh`: Run C, Patroni 3.3.11, async, same full saga as A.
- `scripts/saga_d.sh`: Run D, Patroni 4.1.5, JSON logs, short saga incl. DCS lock loss.
- `scripts/saga_e.sh`: Run E, Patroni 4.1.5, async, dedicated "lag blocks failover" test
  (replaces the invalid attempt at this scenario in run A step 9).

## Lessons learned (read before re-running / extending)

1. **Never put the Unix socket inside `PGDATA`.** The lab originally used
   `unix_socket_directories: '.'`, which (because Patroni execs `postgres` with its cwd
   set to `PGDATA`) put the live `.s.PGSQL.5432` socket inside the data directory.
   `pg_rewind`'s file-copy step cannot open a Unix socket as a regular file and fails
   every time with `could not open target file ".../.s.PGSQL.5432": No such device or
   address`. This broke every rewind in run A (see its MANIFEST.md) until fixed to
   `/tmp`. `pg_basebackup` (used by `patronictl reinit`) correctly skips special files,
   so `reinit` always worked as a fallback.
2. **`pg_wal_replay_pause()` does NOT block a Patroni failover.** Patroni's
   `maximum_lag_on_failover` check is based on each replica's *received* WAL position,
   not its replayed position, so pausing replay alone never creates the lag Patroni
   checks for - and on top of that, Patroni 3.3.11 was observed to proactively call
   `pg_wal_replay_resume()` on a paused standby as part of its own failover evaluation
   (`Resuming paused WAL replay for PostgreSQL 14+`), undoing the pause within a second
   of the leader dying. To genuinely block a failover on lag, cut the replication
   *stream* itself (see run E: `iptables -A OUTPUT -p tcp -d <leader_ip> --dport 5432 -j
   DROP` inside the replica containers) rather than pausing replay.
3. **Isolating only the leader's DCS (etcd) network connection did not trigger the
   expected self-demotion within a ~110s window**, even though the isolation was
   confirmed effective (continuous `failed to resolve host etcd1/2/3` DNS errors in the
   leader's Patroni log). This reproduced identically across Patroni 4.1.5 (runs A and
   D) and 3.3.11 (run C). By contrast, a *full* etcd outage (all 3 etcd nodes stopped -
   connections refused/timed out rather than DNS failing) triggered a correct
   self-demotion within ~18s (`demoting self because DCS is not accessible and I was a
   leader`). This looks like a real, reproducible difference in how these Patroni
   versions handle DNS-resolution failure vs. connection-refused when deciding whether
   the DCS is "unreachable" for the purposes of the `ttl`-based self-demotion timer -
   worth investigating further if this lab is extended, but was left as an honestly
   recorded deviation rather than a fixture to redo (see each run's MANIFEST.md).

Each script logs every step with a UTC timestamp to
`runs/<run>/MANIFEST_events.log`, takes `patronictl list -f json`/pretty snapshots
into `runs/<run>/MANIFEST_snapshots.log`, and dumps `patronictl history` at the end
into `runs/<run>/MANIFEST_history.log`. These raw logs are the ground truth used to
write each run's `MANIFEST.md` in the fixtures directory.

## Re-running

```
ssh dalibo-pc
bash ~/ptlab/scripts/saga_a.sh   # ... _b / _c / _d / _e
bash ~/ptlab/scripts/collect_run.sh <run> ~/ptlab/collected/<run>   # gathers logs + manifests
scp -r dalibo-pc:~/ptlab/collected/<run>/* <local-fixtures>/<RUN-DIR>/
# then, per run:
docker rm -f ptlab-<run>-pg1 ptlab-<run>-pg2 ptlab-<run>-pg3 \
             ptlab-<run>-etcd1 ptlab-<run>-etcd2 ptlab-<run>-etcd3
docker network rm ptlab-<run>-dcs ptlab-<run>-repl
```

Images (`localhost/ptlab-patroni:4.1.5-pg17`, `localhost/ptlab-patroni:3.3.11-pg17`)
are kept on dalibo-pc so re-running does not require rebuilding.
