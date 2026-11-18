# Run D - Patroni 4.1.5, JSON logs, async - bootstrap/switchover/failover/lock-loss/steady

## Versions / environment

Patroni 4.1.5, PostgreSQL 17.9 (Debian 17.9-1.pgdg13+1), etcd v3.5.17, image
`localhost/ptlab-patroni:4.1.5-pg17`. Same async config as run A/E
(`unix_socket_directories: '/tmp'`, socket bug fixed), **plus** `log: type: json` (no
custom `log.format` was set, so Patroni falls back to its documented default plain format
string `%(asctime)s %(levelname)s: %(message)s` and the JSON formatter turns that into one
JSON object per line with exactly the fields `asctime`, `levelname`, `message` - confirmed
below). PostgreSQL's own log stays plain text (JSON logging is a Patroni-only setting in
this version; PostgreSQL's `log_destination`/`logging_collector` are unaffected).
Topology: `ptlab-d-etcd1/2/3` + `ptlab-d-pg1/2/3`.

## Step table

| # | Step | Command | UTC start | UTC end | Expected | Observed |
|---|------|---------|-----------|---------|----------|----------|
| 1 | Bootstrap | 3x etcd + 3x patroni `docker run` | 14:09:25 | 14:10:13 | pg1 leader | pg1 leader; one transient bootstrap-time error while etcd was still forming its cluster: `Failed to get list of machines from http://etcd1:2379/v3beta: ValueError("invalid literal for int() with base 10: 'not_decided'")` - harmless, bootstrap completed normally right after |
| 2 | Planned switchover | `patronictl switchover --leader pg1 --candidate pg2 --force` | 14:10:13 | 14:11:32 | pg2 becomes leader | pg2 leader (TL1, 14:10:16) |
| 3 | Failover by leader kill | pre-kill burst write + `docker kill -s KILL ptlab-d-pg2` | 14:11:32 | 14:12:02 | a replica is promoted | pg1 promoted itself 30s later (TL2, 14:12:01.730044+00:00) |
| - | Old leader (pg2) rejoins | `docker start ptlab-d-pg2` | 14:12:33 | - | pg2 rewinds and rejoins | clean `pg_rewind` (`exit code=0`, `pg_rewind: Done!` at 14:12:34,922) |
| 4 | Leader loses DCS lock | `docker network disconnect ptlab-d-dcs ptlab-d-pg1` for ~113s, then reconnect | 14:13:49 | 14:15:42 | pg1 demotes, another node takes over | **did not happen** - same deviation as run A/C: `patronictl history` shows no timeline change during this window, `current_leader` stayed `pg1` throughout |
| 5 | Final steady state | - | 14:16:43 | 14:18:44 | quiescent, healthy cluster | pg1 leader, pg2/pg3 streaming, no further changes |

## Leader timeline (from `patronictl history`)

```
+----+----------+------------------------------+----------------------------------+------------+
| TL |      LSN | Reason                       | Timestamp                        | New Leader |
+----+----------+------------------------------+----------------------------------+------------+
|  1 | 67284072 | no recovery target specified | 2026-09-24T14:10:16.038891+00:00 | pg2        |
|  2 | 67315944 | no recovery target specified | 2026-09-24T14:12:01.730044+00:00 | pg1        |
+----+----------+------------------------------+----------------------------------+------------+
```

## Counts

- Planned switchovers: **1**
- Unplanned failovers: **1**
- `pg_rewind` runs: **1**, succeeded
- DCS single-node-isolation windows: **1** (step 4, ~113s) - **no observable effect**, same
  deviation documented in run A (see that run's MANIFEST for the log evidence: pg1 could not
  resolve the etcd hostnames at all during isolation, yet kept reporting itself as "the
  leader with the lock" for the whole window; no other member ever saw the lock as free)
- DCS full outage / pause / reinit / lag scenario: **0** (out of scope for this run)

## Representative JSON log lines (verbatim, one object per line)

```json
{"asctime": "2026-09-24 14:09:26,581", "levelname": "INFO", "message": "Using default value thread_stack_size = 524288"}
{"asctime": "2026-09-24 14:09:26,620", "levelname": "ERROR", "message": "Failed to get list of machines from http://etcd1:2379/v3beta: ValueError(\"invalid literal for int() with base 10: 'not_decided'\")"}
{"asctime": "2026-09-24 14:12:34,155", "levelname": "INFO", "message": "running pg_rewind from pg1"}
{"asctime": "2026-09-24 14:12:34,922", "levelname": "INFO", "message": "pg_rewind exit code=0"}
{"asctime": "2026-09-24 14:12:34,922", "levelname": "INFO", "message": " stderr=pg_rewind: servers diverged at WAL location 0/40328E8 on timeline 2\npg_rewind: rewinding from last common checkpoint at 0/402AE00 on timeline 2\npg_rewind: Done!\n"}
{"asctime": "2026-09-24 14:18:52,758", "levelname": "INFO", "message": "no action. I am (pg1), the leader with the lock"}
```
Note the last example: a multi-line `stderr=` message is embedded as a single JSON string
with escaped `\n`s, and the earlier one shows an embedded double-quoted `ValueError(...)`
correctly escaped as `\"...\"` - both are worth exercising in a JSON-log parser.

## File sizes / line counts / time ranges

| File | Lines | First timestamp | Last timestamp |
|------|------:|------------------|-----------------|
| pg1.patroni.log | 144 | 2026-09-24 14:09:26,581 | 2026-09-24 14:18:52,758 |
| pg2.patroni.log | 106 | 2026-09-24 14:09:26,826 | 2026-09-24 14:18:53,362 |
| pg3.patroni.log | 102 | 2026-09-24 14:09:27,170 | 2026-09-24 14:18:53,358 |
| pg1.postgresql.log | 83 | 2026-09-24 14:09:34.634 UTC | 2026-09-24 14:17:06.260 UTC |
| pg2.postgresql.log | 59 | 2026-09-24 14:09:38.302 UTC | 2026-09-24 14:17:40.704 UTC |
| pg3.postgresql.log | 78 | 2026-09-24 14:09:38.176 UTC | 2026-09-24 14:14:48.055 UTC |

No node produced more than one PostgreSQL log file in this run. Total directory size well
under 100 KB.
