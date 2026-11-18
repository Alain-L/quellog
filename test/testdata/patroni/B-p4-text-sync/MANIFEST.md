# Run B - Patroni 4.1.5, text logs, `synchronous_mode: true`

## Versions / environment

Patroni 4.1.5, PostgreSQL 17.9 (Debian 17.9-1.pgdg13+1), etcd v3.5.17, image
`localhost/ptlab-patroni:4.1.5-pg17`. Same base config as run A/E (`ttl: 30`,
`loop_wait: 10`, `retry_timeout: 10`, `maximum_lag_on_failover: 1048576`,
`use_pg_rewind: true`, `use_slots: true`, `unix_socket_directories: '/tmp'` - the run-A
socket bug is fixed here) **plus** `bootstrap.dcs.synchronous_mode: true`. Topology:
`ptlab-b-etcd1/2/3` + `ptlab-b-pg1/2/3`, same two-network split as the other runs.

## Step table

| # | Step | Command | UTC start | UTC end | Expected | Observed |
|---|------|---------|-----------|---------|----------|----------|
| 1 | Bootstrap | 3x etcd + 3x patroni `docker run` | 14:09:25 | 14:10:28 | pg1 leader, a sync standby picked | pg1 leader; `Assigning synchronous standby status to ['pg2']` at 14:09:55, confirmed at 14:09:57 |
| 2 | Planned switchover | `patronictl switchover --leader pg1 --candidate pg2 --force` | 14:10:59 | 14:12:19 | pg2 becomes leader, picks a new sync standby | pg2 leader (TL1); pg2 immediately reassigned: `Assigning synchronous standby status to ['pg3']` at 14:11:13 |
| 3 | Failover by leader kill | pre-kill burst write + `docker kill -s KILL ptlab-b-pg2` | 14:12:19 | 14:12:45 | a standby is promoted | pg3 promoted itself 26s later (`promoted self to leader by acquiring session lock`), immediately assigned pg1 as its new sync standby (`Assigning synchronous standby status to ['pg1']` at 14:12:44) |
| - | Old leader (pg2) rejoins | `docker start ptlab-b-pg2` | 14:13:15 | - | pg2 rewinds and rejoins as a streaming replica | **clean pg_rewind this time** (unlike run A): `pg_rewind exit code=0`, `servers diverged at WAL location 0/407CA18 on timeline 2`, `pg_rewind: Done!` at 14:13:17; pg2 streaming again by 14:13:17 |
| 4 | Final steady state | - | 14:14:31 | 14:16:32 | quiescent, healthy cluster | pg3 leader, pg1/pg2 streaming replicas, no further changes |

## Leader timeline (from `patronictl history`)

```
+----+----------+------------------------------+----------------------------------+------------+
| TL |      LSN | Reason                       | Timestamp                        | New Leader |
+----+----------+------------------------------+----------------------------------+------------+
|  1 | 67587440 | no recovery target specified | 2026-09-24T14:11:02.089614+00:00 | pg2        |
|  2 | 67619352 | no recovery target specified | 2026-09-24T14:12:43.837588+00:00 | pg3        |
+----+----------+------------------------------+----------------------------------+------------+
```

Leader-by-wallclock: `pg1` (14:09:25-14:11:02, bootstrap) -> `pg2` (14:11:02-14:12:43,
planned switchover) -> `pg3` (14:12:43-end, failover). pg2 rejoined afterwards as a healthy
replica.

## Counts

- Planned switchovers: **1**
- Unplanned failovers: **1**
- Synchronous-standby reassignments logged: **3** (initial pick pg2 at bootstrap, pg3 after
  the switchover, pg1 after the failover - each new leader immediately picks a fresh sync
  standby)
- `pg_rewind` runs: **1**, **succeeded** (pg2, after the failover)
- `reinit`/DCS-outage/pause events: **0** (out of scope for this short saga)

## Representative log lines (verbatim)

```
2026-09-24 14:09:45,497 INFO: Enabled synchronous replication
2026-09-24 14:09:55,454 INFO: Assigning synchronous standby status to ['pg2']
2026-09-24 14:09:57,611 INFO: Synchronous standby status assigned to ['pg2']
2026-09-24 14:11:13,367 INFO: Assigning synchronous standby status to ['pg3']
2026-09-24 14:12:43,810 INFO: promoted self to leader by acquiring session lock
2026-09-24 14:12:44,911 INFO: Assigning synchronous standby status to ['pg1']
2026-09-24 14:13:16,685 INFO: running pg_rewind from pg3
2026-09-24 14:13:17,475 INFO: pg_rewind exit code=0
```

## File sizes / line counts / time ranges

| File | Lines | First timestamp | Last timestamp |
|------|------:|------------------|-----------------|
| pg1.patroni.log | 97  | 2026-09-24 14:09:26,922 | 2026-09-24 14:16:47,805 |
| pg2.patroni.log | 253 | 2026-09-24 14:09:27,231 | 2026-09-24 14:16:47,805 |
| pg3.patroni.log | 81  | 2026-09-24 14:09:27,509 | 2026-09-24 14:16:47,219 |
| pg1.postgresql.log | 84 | 2026-09-24 14:09:35.187 UTC | 2026-09-24 14:16:05.813 UTC |
| pg2.postgresql.log | 59 | 2026-09-24 14:09:38.711 UTC | 2026-09-24 14:13:17.854 UTC |
| pg3.postgresql.log | 69 | 2026-09-24 14:09:38.773 UTC | 2026-09-24 14:12:44.961 UTC |

No node produced more than one PostgreSQL log file in this run. Total directory size well
under 100 KB.
