# Run P2 -- two poolers in front of one PostgreSQL

Captured 2026-09-25 on lab machine `dalibo-pc`. All timestamps UTC. Full
orchestration log: `MANIFEST_timeline.log` (produced by `_lab/run_p2.sh`).

## Versions / topology

- pgbouncer 1.25.2, two independent processes against the same PostgreSQL:
  - `pgb-a`: port 6432, `pool_mode = session`, log `pgb-a.pgbouncer.log`
    (+ rotated predecessor `pgb-a.pgbouncer.log.1`).
  - `pgb-b`: port 6433, `pool_mode = transaction`, log `pgb-b.pgbouncer.log`.
- PostgreSQL 17.9, single podman container `quellog-pg-p2`, port 15433 on
  the host.
- Same `bench` role/db/auth setup as run P1 (md5, single userlist shared by
  both poolers).
- Traffic: `pgb-a` gets a steady light load for the whole run
  (`pgbench -c 3 -j 1 -T 15` loop); `pgb-b` is otherwise idle and only used
  for the saturation episode(s).

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| A. Baseline | 09:15:45.648 -> 09:17:45.675 | `pgb-a` steady light traffic only | periodic connect/disconnect + stats on pgb-a, nothing on pgb-b | 34 `LOG stats:` lines on pgb-a during the run; pgb-b log stays essentially empty except its own periodic stats |
| B. Log rotation (pgb-a) | 09:17:45.676 -> 09:17:47.686 | `mv pgb-a.pgbouncer.log pgb-a.pgbouncer.log.1` then `kill -HUP <pgb-a pid>` | pgbouncer reopens the log at the same path | see Surprises: the `LOG got SIGHUP, re-reading config` line itself is still written to the **old** (renamed) file at 09:17:45.679; the new `pgb-a.pgbouncer.log` is only created ~9s later (09:17:54.625), at the next scheduled `stats_period` tick, not synchronously with the signal |
| C. Saturation (pgb-b only) | 09:18:47.690 -> 09:19:32.359, **corrected supplemental burst 09:20:47.317 -> 09:21:20.421** | shrink pgb-b's pool (`default_pool_size=2, reserve_pool_size=2, reserve_pool_timeout=2, query_wait_timeout=3`) + `RELOAD;`, then `pgbench -f sleep.sql -c 40 -j 8 -T 40` (then a corrected `-T 30` supplemental run, see Surprises), restore, `RELOAD;` | reserve pool activations and `query_wait_timeout` closures on pgb-b, nothing on pgb-a | across both windows: 2x `taking connection from reserve_pool`, 12x `closing because: query_wait_timeout`; pgb-a log shows zero saturation-related lines (isolation between the two poolers confirmed) |
| D. Cool-down | 09:19:32.360 -> 09:21:32.381 | `pgb-a` steady light traffic continues | pgb-a unaffected by pgb-b's saturation | pgb-a stats lines continue at their normal steady rate through and after pgb-b's episode |

## Surprises

1. **The first saturation attempt used the wrong `query_wait_timeout`.**
   `run_p2.sh` was corrected locally (`query_wait_timeout` lowered from the
   brief's example value of 8 to 3, for the same reason as run P1 -- see
   P1's MANIFEST) but the corrected file was not re-copied to the lab
   machine before this run was launched, so the first 40s saturation window
   (09:18:47 -> 09:19:32) actually ran with `query_wait_timeout=8` and
   produced only 2 reserve-pool activations and zero timeout closures --
   exactly the same failure mode already seen and fixed in run P1. This was
   caught immediately from the live log, and a second, corrected 30s
   saturation burst was run directly against the still-live `pgb-b` process
   at 09:20:47 -> 09:21:20 (same pool settings, `query_wait_timeout=3`),
   which did produce the expected 12 timeout closures, appended to the same
   `pgb-b.pgbouncer.log`. Both windows are visible in the log and in
   `MANIFEST_timeline.log`; the counts in this manifest are the sum of both.
2. **Log reopen on SIGHUP is lazy, not immediate.** As detailed in step B:
   pgbouncer's own signal-receipt log line still lands in the file that was
   just renamed away, and the actual new file only appears at the next
   periodic log write (here, the next `stats_period=10` tick, ~9 seconds
   later). A monitoring script that watches for the new file right after
   sending `SIGHUP`/`RELOAD` needs to allow for this delay.
3. Session mode (`pgb-a`) vs transaction mode (`pgb-b`) is visible directly
   in the connection churn: pgb-a's 3 steady pgbench clients produce far
   more `login attempt`/`closing because: client close request` pairs per
   minute than would be expected in transaction mode for the same workload,
   because in session mode PostgreSQL server connections are only released
   when the client disconnects, and here each pgbench client reconnects on
   every loop iteration (every ~17s) -- this is a workload/loop artifact of
   the light-traffic driver, not a pgbouncer distinction, but it is exactly
   the kind of thing an operator watching a session-mode pooler needs to
   read correctly.

## Raw counts (grep on the raw files)

`pgb-a.pgbouncer.log` + `pgb-a.pgbouncer.log.1` combined (146 + 84 = 230
lines):
- `login attempt`: 93
- `closing because` (total): 95 -- 89 `client close request`, 3 `client
  unexpected eof`, 3 `client disconnect while server was not ready`
- `LOG stats:`: 34
- `process up` / `listening on`: 1 each (only at initial start; the log
  *file* was rotated in step B, but the pgbouncer *process* was not
  restarted, so there is only one `process up` for the whole run)
- `got SIGHUP, re-reading config`: 1

`pgb-b.pgbouncer.log` (249 lines):
- `login attempt`: 95
- `closing because` (total): 94 -- 82 `client close request`, 12
  `query_wait_timeout`
- `taking connection from reserve_pool`: 2
- `RELOAD command issued`: 4 (shrink/restore x2, matching the two saturation
  windows from Surprise 1)

`p2.postgresql.log` (74 lines):
- `connection received`: 12, `disconnection:`: 12
- `database system is ready to accept connections`: 2 (both part of the
  image's own bootstrap sequence -- this run never restarts PostgreSQL)
- `database system is shut down`: 3 (2 bootstrap + final teardown)

## SHOW STATS / SHOW POOLS snapshots

`MANIFEST_snapshots.log` (104 lines), for both `pgb-a` and `pgb-b` side by
side, taken at: step A end (baseline, pgb-a active / pgb-b idle), step C
right after the (first, uncorrected) shrink RELOAD, and step C right after
that saturation load, and step D end (final state, both poolers back to
their resting configuration).
