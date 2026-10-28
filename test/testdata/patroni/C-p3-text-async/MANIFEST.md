# Run C - Patroni 3.3.11, text logs, asynchronous replication, full saga

Same full 10-step saga as run A, replayed on Patroni **3.3.11** to compare log wording and
behaviour across major versions, with the socket-in-PGDATA bug already fixed (this run
started after the `unix_socket_directories: '/tmp'` fix).

## Versions / environment

- Patroni: **3.3.11** (pip, `patroni[etcd3]`)
- PostgreSQL: **17.9** (Debian 17.9-1.pgdg13+1)
- etcd: **v3.5.17**
- Image: `localhost/ptlab-patroni:3.3.11-pg17`
- Config: identical to run A (`ttl: 30`, `loop_wait: 10`, `retry_timeout: 10`,
  `maximum_lag_on_failover: 1048576`, `use_pg_rewind: true`, `use_slots: true`, async,
  `unix_socket_directories: '/tmp'`). Topology: `ptlab-c-etcd1/2/3` + `ptlab-c-pg1/2/3`.

## Step table

| # | Step | Command | UTC start | UTC end | Expected | Observed |
|---|------|---------|-----------|---------|----------|----------|
| 1 | Bootstrap | 3x etcd + 3x patroni `docker run` | 14:09:25 | 14:10:15 | pg1 leader | pg1 leader, pg2/pg3 streaming |
| 2 | Planned switchover | `patronictl switchover --leader pg1 --candidate pg2 --force` | 14:10:16 | 14:11:35 | pg2 becomes leader | pg2 leader (TL1, 14:10:18.803032+00:00) |
| 3 | Pause / resume | `patronictl pause main` / `patronictl resume main` | 14:11:35 | 14:11:56 | "PAUSE: no action..." logged | exactly that: `PAUSE: no action. I am (pg2), the leader with the lock` every loop while paused |
| 4 | Failover by leader death | pre-kill burst write + `docker kill -s KILL ptlab-c-pg2` | 14:12:57 | 14:13:31 | a replica wins the race | pg1 promoted itself 33s later (TL2, 14:13:31.369868+00:00) |
| 5 | Old leader restart -> pg_rewind | `docker start ptlab-c-pg2` | 14:13:52 | - | pg2 rewinds and rejoins | **clean pg_rewind** (socket bug fixed): `exit code=0`, `pg_rewind: Done!` at 14:13:53,895 |
| 6 | Reinit of a replica | `patronictl reinit main pg2 --force` | 14:15:23 | 14:16:39 | pg2 fully resynced | success (pg2 was already healthy after step 5's clean rewind; this reinit is an extra full resync on top, as scripted) |
| 7 | Leader loses DCS lock | `docker network disconnect ptlab-c-dcs ptlab-c-pg1` for ~112s, then reconnect | 14:16:40 | 14:19:33 | pg1 demotes, another node takes over | **did not happen** - same deviation as runs A/D: pg1 logged `no action. I am (pg1), the leader with the lock` throughout, despite `failed to resolve host etcd1/2/3` DNS errors; `patronictl history` shows no timeline change in this window (confirms this is a Patroni-version-independent behaviour, not specific to 4.1.5) |
| 8 | DCS totally unreachable | `docker stop` all 3 etcd, wait ~68s, `docker start` all 3 | 14:19:33 | 14:22:12 | leader demotes, then recovers | worked as expected: TL3 entry at 14:20:42.430583+00:00, "new leader pg1" (pg1 demoted then reacquired once etcd was back, same pattern as run A) |
| 9 | Lag blocking failover | `pg_wal_replay_pause()` on pg2/pg3 (succeeded this time - socket bug fixed) + bulk insert + `docker kill -s KILL ptlab-c-pg1` | 14:22:12 | 14:23:00 | replicas refuse to promote | **still not blocked**, but for a different, more precise reason than run A: Patroni **itself** un-paused WAL replay on the standbys as part of its own failover evaluation - `pg3.patroni.log`: `Resuming paused WAL replay for PostgreSQL 14+` at 14:22:13,996, i.e. **immediately** after the kill, well before our own scripted `pg_wal_replay_resume()` call at 14:22:59. pg2 promoted itself at 14:22:43 (TL4). This independently confirms the coordinator's diagnosis (`pg_wal_replay_pause()` cannot be used to simulate lag for a failover test) and is the reason run **E** uses an `iptables`-based replication cut instead. |
| 10 | Final steady state | - | 14:24:31 | 14:27:01 | quiescent, healthy cluster | pg1 rewound cleanly from pg2 (`pg_rewind: Done!` at 14:23:01,982) and rejoined as streaming replica; cluster ended healthy: pg2 leader, pg1/pg3 streaming |

## Leader timeline (from `patronictl history`)

```
+----+-----------+------------------------------+----------------------------------+------------+
| TL |       LSN | Reason                       | Timestamp                        | New Leader |
+----+-----------+------------------------------+----------------------------------+------------+
|  1 |  67284072 | no recovery target specified | 2026-09-24T14:10:18.803032+00:00 | pg2        |
|  2 |  67315944 | no recovery target specified | 2026-09-24T14:13:31.369868+00:00 | pg1        |
|  3 | 100874040 | no recovery target specified | 2026-09-24T14:20:42.430583+00:00 | pg1        |
|  4 | 104332000 | no recovery target specified | 2026-09-24T14:22:43.838694+00:00 | pg2        |
+----+-----------+------------------------------+----------------------------------+------------+
```

Leader-by-wallclock: `pg1` (bootstrap-14:10:18) -> `pg2` (switchover, 14:10:18-14:13:31) ->
`pg1` (failover, 14:13:31-14:20:03ish, self-demoted during DCS outage) -> `pg1` (reacquired,
14:20:42-14:22:43) -> `pg2` (failover, 14:22:43-end). pg1 rewound and rejoined healthy.

## Counts

- Planned switchovers: **1**
- Unplanned failovers: **2** (step 4, step 9)
- Self-demotions due to full DCS outage: **1** (step 8)
- DCS single-node-isolation windows: **1** (step 7, ~112s, **no observable effect** -
  reproduces the same deviation seen in runs A and D on a different Patroni major version)
- `pg_rewind` runs: **2**, **both succeeded** (pg2 at step 5, pg1 at step 10) - contrast with
  run A where the same steps failed due to the (now-fixed) socket bug
- `reinit` operations: **1** (step 6, planned)
- pause/resume cycles: **1** (step 3)
- DCS-full-outage windows: **1** (step 8, ~68s)

## Representative log lines (verbatim)

```
2026-09-24 14:10:18,775 INFO: promoted self to leader by acquiring session lock
2026-09-24 14:11:36,226 INFO: PAUSE: no action. I am (pg2), the leader with the lock
2026-09-24 14:16:45,689 ERROR: Request to server http://etcd2:2379 failed: ReadTimeoutError("HTTPConnectionPool(host='etcd2', port=2379): Read timed out. (read timeout=3.3331882133400845)")
2026-09-24 14:18:32,398 INFO: no action. I am (pg1), the leader with the lock
2026-09-24 14:13:53,895 INFO: pg_rewind exit code=0
2026-09-24 14:22:13,996 INFO: Resuming paused WAL replay for PostgreSQL 14+
2026-09-24 14:22:43,810 INFO: promoted self to leader by acquiring session lock
```

## File sizes / line counts / time ranges

| File | Lines | First timestamp | Last timestamp |
|------|------:|------------------|-----------------|
| pg1.patroni.log | 835 | 2026-09-24 14:09:29,051 | 2026-09-24 14:27:25,655 |
| pg2.patroni.log | 760 | 2026-09-24 14:09:29,333 | 2026-09-24 14:27:25,058 |
| pg3.patroni.log | 675 | 2026-09-24 14:09:29,658 | 2026-09-24 14:27:25,650 |
| pg1.postgresql.log | 137 | 2026-09-24 14:09:31.999 UTC | 2026-09-24 14:23:07.386 UTC |
| pg2.postgresql.log | 126 | 2026-09-24 14:09:35.614 UTC | 2026-09-24 14:23:02.380 UTC |
| pg3.postgresql.log | 131 | 2026-09-24 14:09:35.644 UTC | 2026-09-24 14:25:20.481 UTC |

No node produced more than one PostgreSQL log file in this run (no crash loop, unlike run
A). Total directory size: ~408 KB.
