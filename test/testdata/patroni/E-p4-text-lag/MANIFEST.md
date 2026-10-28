# Run E - Patroni 4.1.5, text logs, async - "lag blocks failover" (corrected)

This run redoes the scenario that failed in run A step 9. Two independent bugs made
run A's attempt invalid: (1) `pg_wal_replay_pause()` only pauses WAL *replay*, not WAL
*receive* - Patroni's `maximum_lag_on_failover` check is based on the replica's received
LSN, so a replay pause alone never creates the kind of lag Patroni checks for; (2) a lab
tooling bug made every `psql` call in run A's script fail silently, so in practice nothing
was paused and no extra WAL was written at all. This run instead physically cuts the
replication TCP stream from both replicas to the leader (`iptables` `DROP` on port 5432,
targeted at the leader's IP, run inside the replica containers with `--cap-add=NET_ADMIN`)
while leaving the DCS (etcd) and REST API networks untouched, so the replicas' *received*
LSN genuinely stalls - which is what actually blocks a Patroni failover.

## Versions / environment

Same as run A: Patroni 4.1.5, PostgreSQL 17.9 (Debian 17.9-1.pgdg13+1), etcd v3.5.17,
image `localhost/ptlab-patroni:4.1.5-pg17`. Config identical to run A's async config,
**except** `unix_socket_directories` is `/tmp` (not `.`) - the run-A pg_rewind/socket bug
is fixed here. Topology: `ptlab-e-etcd1/2/3` + `ptlab-e-pg1/2/3`, same two-network split as
run A, plus `--cap-add=NET_ADMIN` on the pg containers so `iptables` can run as root inside
them (via `docker exec -u root`).

## Step table

| # | Step | Command | UTC start | UTC end | Expected | Observed |
|---|------|---------|-----------|---------|----------|----------|
| 1 | Bootstrap | 3x etcd + 3x patroni `docker run` | 14:09:25 | 14:10:16 | pg1 leader | pg1 leader, pg2/pg3 streaming |
| 2 | Cut replication to both replicas | `iptables -A OUTPUT -p tcp -d <pg1_ip> --dport 5432 -j DROP` inside pg2 and pg3, for each of pg1's two network IPs (10.89.7.5 on `dcs`, 10.89.12.2 on `repl`) | 14:10:16 | 14:10:22 | replicas stop receiving new WAL, DCS/REST still reachable | confirmed by patronictl snapshot: Receive LSN frozen from this point on |
| 3 | Generate WAL on leader | bulk insert of ~30000 padded rows (~30 MB) on pg1 | 14:10:22 | 14:10:38 | WAL growth well past the 1 MB `maximum_lag_on_failover` | done in ~16s |
| 4 | Kill the leader | `docker kill -s KILL ptlab-e-pg1` | 14:10:38 | 14:11:30 (52s observation) | replicas refuse to promote, citing lag | **confirmed blocked**: pg2 and pg3 both logged, every loop_wait for 3 consecutive cycles (14:11:02, :12, :22), `My wal position exceeds maximum replication lag` immediately followed by `following a different leader because i am not the healthiest node`; `current_leader` was empty (no member held the lock) for the whole window |
| 5 | Restore replication + restart old leader | `iptables -D ...` on pg2/pg3, then `docker start ptlab-e-pg1` | 14:11:30 | 14:13:01 | pg1 resumes as primary, replicas catch up | pg1 came back as leader (`patronictl history`: 1 entry, TL1, new leader pg1 at 14:11:31.647707+00:00, i.e. right after the restart - no failover to a replica ever happened, confirming the block held for the entire outage), pg2/pg3 resumed streaming |
| 6 | Final steady state | - | 14:13:01 | 14:14:32 | quiescent, healthy cluster | pg1 leader, pg2/pg3 streaming, no further changes |

## Leader timeline / counts

- `patronictl history`: **1** entry - `TL1, new leader pg1, 2026-09-24T14:11:31.647707+00:00`
  (pg1 resuming as primary after its own restart; no other node was ever promoted).
- Failovers attempted: **1** (leader killed at step 4).
- Failovers that actually completed: **0** - this is the point of the run: the failover was
  genuinely **blocked** for the full ~52s observation window (>=3 `loop_wait` cycles).
- `pg_rewind` runs: **0** (no divergence: pg1 is the same node resuming its own timeline;
  nothing else ever wrote new WAL while it was down).
- DCS outages / lock losses: **0** (etcd was left untouched the whole run, by design).

## Representative refusal messages (verbatim, from `pg2.patroni.log` / `pg3.patroni.log`)

```
2026-09-24 14:11:02,312 INFO: My wal position exceeds maximum replication lag
2026-09-24 14:11:02,362 INFO: following a different leader because i am not the healthiest node
2026-09-24 14:11:12,313 INFO: My wal position exceeds maximum replication lag
2026-09-24 14:11:12,414 INFO: following a different leader because i am not the healthiest node
2026-09-24 14:11:22,316 INFO: My wal position exceeds maximum replication lag
2026-09-24 14:11:22,366 INFO: following a different leader because i am not the healthiest node
```
(pg2 and pg3 log byte-for-byte the same two lines, a few ms apart, at each `loop_wait` while
blocked.)

## File sizes / line counts / time ranges

| File | Lines | First timestamp | Last timestamp |
|------|------:|------------------|-----------------|
| pg1.patroni.log | 161 | 2026-09-24 14:09:29,070 | 2026-09-24 14:14:52,610 |
| pg2.patroni.log | 59  | 2026-09-24 14:09:29,426 | 2026-09-24 14:14:53,218 |
| pg3.patroni.log | 59  | 2026-09-24 14:09:29,759 | 2026-09-24 14:14:53,222 |
| pg1.postgresql.log | 39 | 2026-09-24 14:09:32.059 UTC | 2026-09-24 14:11:31.742 UTC |
| pg2.postgresql.log | 42 | 2026-09-24 14:09:35.704 UTC | 2026-09-24 14:14:35.581 UTC |
| pg3.postgresql.log | 42 | 2026-09-24 14:09:35.792 UTC | 2026-09-24 14:14:35.581 UTC |

No node produced more than one PostgreSQL log file in this run (no crash loop, no rewind),
so no concatenation was needed.

Total directory size: well under 100 KB.
