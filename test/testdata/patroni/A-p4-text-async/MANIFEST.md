# Run A - Patroni 4.1.5, text logs, asynchronous replication, full saga

Ground truth written from direct observation (container logs, `patronictl list`/`history`
snapshots, `date -u` at each step) - not from what the logs claim about themselves.

## Versions / environment

- Patroni: **4.1.5** (pip, `patroni[etcd3]`)
- PostgreSQL: **17.9** (Debian 17.9-1.pgdg13+1, `docker.io/library/postgres:17` base image)
- etcd: **v3.5.17** (`quay.io/coreos/etcd`), 3 nodes
- psycopg: 3.3.6, python-etcd 0.4.5 (etcd3 handler dependency), python-json-logger 4.2.0
- Container image: `localhost/ptlab-patroni:4.1.5-pg17`
- Topology: `ptlab-a-etcd1/2/3` + `ptlab-a-pg1/2/3`, two podman networks
  (`ptlab-a-dcs` = etcd+patroni, `ptlab-a-repl` = patroni-only), all containers TZ=UTC.
- Patroni config: `scope: main`, `ttl: 30`, `loop_wait: 10`, `retry_timeout: 10`,
  `maximum_lag_on_failover: 1048576`, `use_pg_rewind: true`, `use_slots: true`,
  `synchronous_mode` not set (async). Full template in `_lab/scripts/common.sh:gen_patroni_conf`.
- PostgreSQL: `log_line_prefix = '%m [%p] %q%u@%d '`, `log_connections/log_disconnections off`,
  `log_checkpoints on`, `logging_collector on`, `log_directory=/var/log/postgresql`,
  default `log_filename`/rotation (so a postmaster restart creates a new dated file -
  see "log files" below).

### Lab bug discovered and fixed mid-run

This run's config had `unix_socket_directories: '.'`, which - because Patroni execs
`postgres` with its cwd set to `PGDATA` - placed the live `.s.PGSQL.5432` socket **inside**
the data directory. `pg_rewind`'s file-copy step cannot open a Unix-domain socket as a
regular file, so **every `pg_rewind` attempt in this run failed** with:
```
pg_rewind: error: could not open target file "/home/postgres/data/.s.PGSQL.5432": No such device or address
```
(a second occurrence uses the wording `could not open file ".s.PGSQL.5432" for reading`).
This is a real, reproducible pg_rewind pitfall of colocating the socket with `PGDATA`, not a
Patroni bug. It affected step 5 and the post-hoc recovery below. `pg_basebackup` (used by
`patronictl reinit`) correctly **skips** the special file
(`WARNING: skipping special file "./.s.PGSQL.5432"`, visible in `pg2.postgresql.log`), so
reinit always worked. The config was fixed to `unix_socket_directories: '/tmp'` for runs
B, C, D, E; this run was left as originally executed, with the resulting rewind failures and
crash loop kept in the fixture logs (see step 5 and "post-hoc recovery" below).

A separate lab tooling bug (wrong socket path baked into the `psql_on` helper, `-h
/home/postgres` instead of `-h /home/postgres/data`, also fixed to `/tmp` afterwards) meant
**every `psql` call this run's script made (test table creation, the pre-kill burst write,
the step-9 `pg_wal_replay_pause/resume` calls, and the WAL-padding insert) silently failed**
(7 failed `psql` invocations, confirmed in `~/ptlab/runs_saga_a.out` on the lab host - not
included in these fixtures). This means step 9 never actually paused replay or added extra
WAL; see the "step 9" deviation below and run **E**, which redoes the lag-blocks-failover
scenario correctly.

## Step table

| # | Step | Command | UTC start | UTC end | Expected | Observed |
|---|------|---------|-----------|---------|----------|----------|
| 1 | Bootstrap | 3x `docker run` (etcd), 3x `docker run` (patroni) | 13:43:43 | 13:44:32 | pg1 becomes leader | pg1 leader, pg2/pg3 streaming, as expected |
| 2 | Planned switchover | `patronictl switchover --leader pg1 --candidate pg2 --force` | 13:44:32 | 13:45:51 | pg2 becomes leader | pg2 leader (TL1->TL2), pg1 graceful demote, clean |
| 3 | Pause / resume | `patronictl pause main` / `patronictl resume main` | 13:45:51 | 13:46:12 | "PAUSE: no action..." logged, no HA action | exactly that; pg2 logged `PAUSE: no action. I am (pg2), the leader with the lock` every loop while paused |
| 4 | Failover by leader death | pre-kill burst write (**silently failed**, see bug above) + `docker kill -s KILL ptlab-a-pg2` | 13:47:13 | 13:47:47 | a replica wins the race | pg1 promoted itself 33s later (`promoted self to leader by acquiring session lock`) |
| 5 | Old leader restart -> pg_rewind | `docker start ptlab-a-pg2` | 13:48:08 | 13:49:39 | pg2 rewinds and rejoins as replica | pg2 tried `pg_rewind` from pg1 and **failed repeatedly** (socket-in-PGDATA bug, see above); cluster carried pg2 as broken until step 6's reinit fixed it |
| 6 | Reinit of a replica | `patronictl reinit main pg2 --force` | 13:49:39 | 13:50:55 | pg2 fully resynced | success; this reinit is what actually recovered pg2 from step 5's stuck rewind (pg_basebackup skips the socket file) |
| 7 | Leader loses DCS lock | `docker network disconnect ptlab-a-dcs ptlab-a-pg1` (isolate leader from etcd only, ~113s), then reconnect | 13:50:55 | 13:53:50 | pg1 demotes after `ttl`, another node takes the lock | **did not happen** - pg1 kept logging `no action. I am (pg1), the leader with the lock` (from stale in-memory cluster state) for the full window despite continuous `failed to resolve host etcd1/2/3` errors; `patronictl history` shows no timeline change between the step-4 and step-8 events; see "deviations" |
| 8 | DCS totally unreachable | `docker stop` all 3 etcd, wait ~67s, `docker start` all 3 | 13:53:50 | 13:56:28 | leader demotes / cluster read-only, then recovers | worked exactly as expected: pg1 logged `demoting self because DCS is not accessible and I was a leader` at 13:54:08 (~18s after etcd stopped), then `promoted self to leader because I had the session lock` at 13:54:59 once etcd was back (recorded as its own timeline bump in history) |
| 9 | Lag blocking failover | `pg_wal_replay_pause()` on pg2/pg3 (**silently failed**) + bulk insert (**silently failed**) + `docker kill -s KILL ptlab-a-pg1` | 13:56:28 | 13:57:16 | replicas refuse to promote, citing lag | **invalid run** - because the `psql` calls silently failed (see bug above), replicas were never actually paused and no extra WAL was written; pg2 promoted itself normally 21s after the kill (`promoted self to leader by acquiring session lock`, 13:56:51). Redone correctly in **run E**. |
| - | Unplanned: pg1 crash loop | (discovered post-hoc) | 13:57:17 | 14:06:xx (ongoing when discovered) | - | pg1, restarted implicitly as part of step 9's "bring old leader back", hit the **same pg_rewind/socket bug**, then got stuck in an 8+ minute `failed to start postgres` crash loop (lock-file/timeline errors), producing ~30 tiny dated PostgreSQL log files (`Database cluster state: shut down in recovery`, repeated `requested starting point ... is not in this server's history`) |
| 10 | Final steady state (as scripted) | - | 13:58:47 | 14:01:18 | quiescent cluster | pg1 was still broken (see above) at the scripted end of the saga |
| - | Post-hoc recovery | `patronictl reinit main pg1 --force` | 14:07:34 | 14:08:41 | fix pg1 | success; cluster ended healthy: pg2 leader, pg1/pg3 streaming replicas |

## Leader timeline (from `patronictl history`, both dumps identical)

```
+----+-----------+------------------------------+----------------------------------+------------+
| TL |       LSN | Reason                       | Timestamp                        | New Leader |
+----+-----------+------------------------------+----------------------------------+------------+
|  1 |  67109288 | no recovery target specified | 2026-09-24T13:44:34.806352+00:00 | pg2        |
|  2 |  67109608 | no recovery target specified | 2026-09-24T13:47:47.155226+00:00 | pg1        |
|  3 | 100874040 | no recovery target specified | 2026-09-24T13:54:59.778128+00:00 | pg1        |
|  4 | 100874416 | no recovery target specified | 2026-09-24T13:56:51.373770+00:00 | pg2        |
+----+-----------+------------------------------+----------------------------------+------------+
```

Leader-by-wallclock (who actually held the lock, from raw logs and snapshots):
`pg1` (13:43:43-13:44:34) -> `pg2` (13:44:34-13:47:47, planned switchover) ->
`pg1` (13:47:47-13:54:08, failover) -> *no leader* (13:54:08-13:54:59, total DCS outage) ->
`pg1` (13:54:59-13:56:51, self-reacquired) -> `pg2` (13:56:51-end, failover). pg1 rejoined
only as a (broken, then reinit'd) replica.

## Counts (ground truth)

- Leader/timeline changes recorded by Patroni: **4**
- Planned switchovers: **1** (step 2)
- Unplanned failovers (leader killed, another node promoted): **2** (step 4, step 9)
- Self-demotions due to DCS being unreachable: **1** (pg1, step 8; step 7's single-node
  isolation produced **0** demotions - see deviations)
- `pg_rewind` attempts: **4**, all **failed** (2 for pg2 in step 5, 2 for pg1 in the
  post-step-9 crash loop) - see the socket-in-PGDATA bug above
- `reinit` operations: **2** (step 6, planned, on pg2; post-hoc, unplanned, on pg1)
- pause/resume cycles: **1** (step 3)
- DCS-full-outage windows: **1** (step 8, ~67s)
- DCS single-node-isolation windows: **1** (step 7, ~113s, no observable effect)

## Deviations from the plan

1. **Step 7 (leader loses DCS lock) did not produce a leader change.** Isolating pg1
   from the `dcs` network genuinely cut its access to etcd (confirmed: continuous
   `failed to resolve host etcd1/2/3: [Errno -2] Name or service not known` in
   `pg1.patroni.log`), but pg1's HA loop kept reporting `no action. I am (pg1), the leader
   with the lock` every cycle for the full ~113s window, and no other node ever saw the lock
   as free (`pg2.patroni.log` shows `no action. I am (pg2), a secondary, and following a
   leader (pg1)` throughout). By contrast, step 8's *full* etcd outage (connections
   refused/timed out rather than DNS failing) triggered a correct self-demotion within
   ~18s. This suggests DNS-resolution failure (as opposed to connection-refused/timeout)
   is handled differently by this Patroni version's DCS-unreachable detection and does not
   reliably trip the `ttl`-based self-demotion within one to a few `loop_wait` cycles - a
   real, reproducible finding, not staged.
2. **Step 9 (lag blocking failover) is invalid as executed** - a lab tooling bug (wrong
   Unix-socket path passed to `psql`) caused all of that step's `psql` calls to fail
   silently, so replicas were never actually paused and no extra WAL was written. The
   observed fast failover (21s) reflects normal failover, not a lag scenario. See run
   **E** (`E-p4-text-lag/`) for the corrected reproduction (replication stream physically
   cut via `iptables`, not `pg_wal_replay_pause()`).
3. **`pg_rewind` never succeeded in this run** due to the `unix_socket_directories: '.'`
   config bug described above; every divergence in this run was resolved via full
   `reinit`/`pg_basebackup` instead. Fixed for runs B/C/D/E.

## File sizes / line counts / time ranges

| File | Lines | First timestamp | Last timestamp |
|------|------:|------------------|-----------------|
| pg1.patroni.log | 2680 | 2026-09-24 13:43:45,214 | 2026-09-24 14:10:12,993 |
| pg2.patroni.log | 1150 | 2026-09-24 13:43:45,496 | 2026-09-24 14:10:12,402 |
| pg3.patroni.log | 647  | 2026-09-24 13:43:45,796 | 2026-09-24 14:10:12,993 |
| pg1.postgresql.log | 328 | 2026-09-24 13:43:52.230 UTC | 2026-09-24 14:07:36.264 UTC |
| pg2.postgresql.log | 164 | 2026-09-24 13:43:55.607 UTC | 2026-09-24 14:07:34.767 UTC |
| pg3.postgresql.log | 128 | 2026-09-24 13:43:55.794 UTC | 2026-09-24 14:07:40.697 UTC |

`pgN.postgresql.log` is the chronological concatenation of every dated PostgreSQL log file
PostgreSQL produced on that node (see `pgN.postgresql.filelist.txt` for the exact list and
order); each `docker start`/crash-loop restart created a new dated file since `log_filename`
was left at PostgreSQL's own default. pg1 alone produced ~30 tiny (~548 byte) files during
its post-step-9 crash loop.

Total directory size: ~516 KB.

Raw event/snapshot logs from the lab driver script are kept alongside for traceability:
`MANIFEST_events.log` (UTC-timestamped step log), `MANIFEST_snapshots.log` (`patronictl
list` before/after each step), `MANIFEST_history.log` (final `patronictl history`, dumped
twice: once at the scripted end of the saga, once after the post-hoc pg1 recovery).
