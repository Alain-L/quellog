# Run S2 -- WAL-archiving incidents

Captured 2026-10-10 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2
rootless). All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on
the lab machine immediately before/after each action. Full raw orchestration
log with every timestamp: `MANIFEST_timeline.log` (concatenation of the
`_lab/s2_*.sh` scripts' own logging -- nothing here was hand-edited).

## Versions / topology

- PostgreSQL 17.11 (Debian 17.11-1.pgdg13+2, PGDG apt), single podman
  container `quellog-s2` from the shared base image
  `localhost/quellog-pgbr-lab:trixie`.
- pgBackRest 2.59.3 (PGDG package, `pgbackrest`) for everything -- the older
  2.48 build was not used in this saga (not required for S2).
- One PGDATA at `/var/lib/pg/main`, port 5432, db `bench`.
- `logging_collector=on`, `log_line_prefix='%m [%p] '`, `log_checkpoints=on`,
  `archive_mode=on`. `archive_command` varied per incident (see each step
  table -- always restored to
  `pgbackrest --stanza=main archive-push %p` between incidents).
- Two stanzas configured in `/etc/pgbackrest/pgbackrest.conf`, sharing the
  same `repo1-path=/var/lib/pgbackrest/repo1`:
  - `main` -- `stanza-create`'d, used for all real archiving/backups.
  - `ghost` -- never `stanza-create`'d, used only to reproduce the
    wrong-stanza incident (S2-2).
- `pgbackrest.conf` evolved across the run (secrets: none to strip, this is
  a lab). Three states, diffed below instead of shipped as three files:
  1. **Baseline** (setup, S2-1, S2-2, S2-3): `[global] repo1-path=...,
     log-level-file=info, start-fast=y` + `[global:archive-push]
     log-level-console=info` + stanzas `main`/`ghost`.
  2. **S2-4**: baseline's `[global:archive-push]` gains `archive-async=y`,
     `spool-path=/var/spool/pgbackrest`, `archive-push-queue-max=16384`
     (16KB -- deliberately smaller than any real 16MB WAL segment).
  3. **S2-5**: same as (2) but `archive-push-queue-max=1GB` -- raised back
     up so pushes aren't dropped purely for queue-size reasons, isolating
     the repo-unreachable behaviour from S2-4's queue-size behaviour. This
     is the final state shipped as `pgbackrest_conf/pgbackrest.conf.final`.

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 0. Setup | 13:24:51.619 -> 13:25:01.799 | container start, initdb PG17, configure archive_mode/archive_command, start PG, create db `bench`, `stanza-create --stanza=main`, `check --stanza=main` | clean stanza, no backups yet | PG 17.11 up, `check` silent/clean; `pgbackrest info` baseline: `status: error (no valid backups)`, wal archive min/max `.../000000010000000000000002` |
| 1. Repo made unreachable (`chmod 000`) | 13:25:36.260 -> 13:27:39.070 | `chmod 000` on `repo1-path`, 6x `pg_switch_wal()` ~15s apart while blocked, then `chmod 750` restore + 1 more switch | repeating `archive command failed with exit code N` / DETAIL pairs, eventually the "failed too many times" WARNING, then catch-up | repo1 blocked at 13:25:36.690; WAL segment `...03` retried every ~1s, `ERROR: [103]: unable to find a valid repository` / `FileOpenError: ... Permission denied` each time; **WARNING fired twice**, at 13:26:40.005 and again at 13:26:55.240 (not just once -- see Surprises); restored at 13:27:08.510, segment `...03` through `...09` all pushed successfully within the same second (13:27:28-29, ~80-130ms each); final info: `wal archive min/max .../000000010000000000000009` |
| 2a. Direct commands against never-created stanza `ghost` | 13:31:54.560 -> 13:31:55.007 | `pgbackrest --stanza=ghost archive-push <fake wal>` and `pgbackrest --stanza=ghost backup`, run manually from `/` | Sample-A-shaped `[103]`/`[055]` | `archive-push` actually hit **`ERROR: [032]: PostgreSQL working directory '/' is not the same as option pg1-path`** first (manual invocation, wrong cwd -- see Surprises, not what we were after); `backup` against `ghost` cleanly gave **`ERROR: [055]: unable to load info file '.../repo1/backup/ghost/backup.info'`**, `FileMissingError` x2, HINT "has a stanza-create been performed?" -- matches Sample A's described 055 variant exactly |
| 2b. Real `archive_command` pointed at `ghost` | 13:31:55.009 -> 13:32:17.082 | `ALTER SYSTEM SET archive_command='pgbackrest --stanza=ghost archive-push %p'` + reload, real `pg_switch_wal()`, wait 15s, revert + reload + catch-up switch | Sample-A-shaped `[103]` via the real archive_command path | `ERROR: [103]: unable to find a valid repository`, `FileMissingError: unable to open missing file '.../repo1/archive/ghost/archive.info'` (clean FileMissingError here, vs. S2-1's FileOpenError/Permission-denied -- same code 103, different sub-cause, nice contrast); PG log: `archive command failed with exit code 103` / `DETAIL: ... pgbackrest --stanza=ghost archive-push pg_wal/00000001000000000000000A`, repeated 3x at 1s intervals before the revert; reverted cleanly, segment pushed within 1s |
| 3. `archive-timeout` too low during `backup` | 13:32:52.193 -> 13:34:27.592 | stall archiving (two attempts, see Surprises), then `pgbackrest backup --archive-timeout=5` | `ArchiveTimeoutError [082]` during backup's wait-for-WAL step | **1st attempt** (`archive_command='/bin/false'`): pgbackrest's own sanity check fired instead -- `ERROR: [068]: archive_command '/bin/false' must contain pgbackrest`, never reached the timeout logic; **2nd attempt** (`archive_command='pgbackrest --stanza=main --repo1-path=/var/lib/pgbackrest/doesnotexist archive-push %p'`, passes the 068 check, fails for real): clean `ERROR: [082]: WAL segment 00000001000000000000000F was not archived before the 5000ms timeout` + the 3 HINT lines, matching Sample B exactly; revert + diff backup right after succeeded (`EXIT=0`) |
| 4. Async archiving, tiny queue-max, WAL burst | 13:35:22.213 -> 13:35:41.007 | `archive-async=y`, `spool-path=/var/spool/pgbackrest`, `archive-push-queue-max=16384` (16KB), then 5x `pg_switch_wal()` back-to-back with no delay | literal `dropped WAL file '...' because archive queue exceeded ...` WARN + async spool log's own `PROCESS START` / `archive-push:async` shape | **Only 1 of the 5 `pg_switch_wal()` calls actually produced a new segment** (see Surprises); that one (`...13`) was dropped immediately: PG log shows `WARN: dropped WAL file '000000010000000000000013' because archive queue exceeded 16KB` then, in the SAME synchronous archive-push invocation, `INFO: pushed WAL file '000000010000000000000013' to the archive asynchronously` and `command end: completed successfully` -- i.e. **PostgreSQL is told the push succeeded even though the file was dropped** (see Surprises); `main-archive-push-async.log` got its first-ever entry here: `-------------------PROCESS START-------------------` / `archive-push:async command begin 2.59.3: [...] --archive-async --archive-push-queue-max=16384 ...` / `push 1 WAL file(s) to archive: 000000010000000000000013` / the same WARN / `command end: completed successfully (35ms)` -- matches Sample D's banner shape, Sample C's WARN text verbatim |
| 5. Repo unreachable while async is running | 13:37:13.042 -> 13:38:01.058 | raise `archive-push-queue-max` to 1GB (isolate from S2-4), warm-up switch, `chmod 000` repo1, 2x real WAL switch (table insert + `pg_switch_wal()`, learned from S2-4 that a bare switch is a no-op), restore, catch-up switch | divergence between what the sync foreground call reports to PG and what the async background process's own log shows | **Sync side** (PG log): same shape as S2-1/S2-2b -- repeating `ERROR: [103]` / `FileOpenError ... Permission denied`, `archive command failed with exit code 103`, `WARNING: ... failed too many times` once at 13:37:21.414, caught up cleanly once restored (segments `...15`-`...17` pushed at 13:37:50-51); **async side** (`main-archive-push-async.log`): **every failed push attempt gets its own full `PROCESS START` block**, including the complete `ERROR: [103]` detail (FileOpenError/Permission denied), ending `archive-push:async command end: aborted with exception [103]` -- unlike the dropped-for-queue-size case in S2-4, here the sync call's own console *also* reports the real failure (no silent "pushed ... asynchronously" lie), because this is a genuine push error, not a queue-size drop |

Every step above starts at least 30s after the previous step's teardown
(see `MANIFEST_timeline.log` for exact gaps); S2-2 through S2-5 are closer
together than S1's steps since no load-generation wait was needed between
most of them.

## Raw counts

`pg_logs_main/postgresql.log` (479 lines):
- `archive command failed with exit code`: 30
- `archiving write-ahead log file ... failed too many times`: 10 (2x in
  S2-1, 3x in S2-2b, 1x in S2-5 -- counts differ from the number of distinct
  stall episodes because the WARNING itself can repeat within one episode,
  see Surprises)
- `archive-push command begin`: 55
- `archive-push command end: completed successfully`: 25
- `archive-push command end: aborted`: 30
- `ERROR: [103]`: 30 (all archive-push failures seen through the PG log are
  code 103 -- the 055/068/082/032 codes only ever appear in the direct
  manual `pgbackrest` console transcripts captured in `MANIFEST_timeline.log`,
  never through `archive_command`, since those are `backup`/sanity-check
  codes, not archive-push codes)
- `dropped WAL file`: 1 (S2-4's single genuine queue-exceeded drop)
- `checkpoint starting` / `checkpoint complete`: 4 each

`pgbackrest_logs/main-archive-push-async.log` (93 lines):
- `PROCESS START` banners: 9
- `archive-push:async command begin`: 9
- `archive-push:async command end: completed successfully`: 3
- `ERROR`: 6 (all `[103]`, from S2-5's repo-unreachable window)
- `dropped WAL file`: 1 (S2-4)

`pgbackrest_logs/` other files: `ghost-backup.log` 10 lines, `main-backup.log`
49 lines, `main-expire.log` 9 lines, `main-stanza-create.log` 4 lines.

## Surprises

1. **The archive-push-writes-no-log-file finding from S1 holds exactly, and
   is now precisely scoped.** The plain synchronous `archive-push` command
   (whether invoked by PostgreSQL's real archive_command, or run by hand)
   never once wrote to `/var/log/pgbackrest/main-archive-push.log` across
   this entire saga -- that file simply does not exist in
   `pgbackrest_logs/`. Its activity is visible only via the PG server log's
   `log-level-console=info` capture, exactly as S1 found. **New finding**:
   `archive-push:async` (the background process spawned when
   `archive-async=y`) is a *different code path* and DOES reliably write its
   own log file (`main-archive-push-async.log`, 93 lines, 9 process-start
   banners) -- so the "no log file" property is specific to the synchronous
   foreground `archive-push` invocation, not to archive-push in general.
2. **The "failed too many times" WARNING is not a one-shot per incident.**
   It fired twice during S2-1's single continuous outage (13:26:40 and
   13:26:55), roughly 15s apart -- consistent with PostgreSQL's archiver
   re-arming the warning after another batch of retries if the failure is
   still ongoing, not just announcing it once per WAL file ever.
3. **`pg_switch_wal()` called back-to-back with no intervening WAL writes is
   a no-op.** S2-4's burst of 5 switches with zero delay and no data written
   between them produced exactly ONE real archive-push event, because
   `pg_switch_wal()` does nothing if the current segment is already empty
   at a boundary. Had to add a small `INSERT` before each switch in S2-5 to
   get genuinely distinct segments. Cheap bonus: this also means WAL volume
   stayed well under budget.
4. **A dropped-for-queue-size WAL file is reported to PostgreSQL as a
   SUCCESSFUL push.** In S2-4, the same synchronous `archive-push` console
   output that printed `WARN: dropped WAL file '...' because archive queue
   exceeded 16KB` immediately followed it with `INFO: pushed WAL file '...'
   to the archive asynchronously` and exited 0 -- `archive_command` returns
   success to PostgreSQL even though the WAL segment was never actually
   archived. The WARN is the only trace; nothing in PostgreSQL's own log
   distinguishes this from a real success except by reading pgBackRest's
   WARN line. This contrasts with S2-5, where a genuine repo-access failure
   under async *does* propagate as a real failure (`ERROR: [103]` on the
   sync console too, `archive command failed with exit code 103` in PG's
   log) -- only the queue-size drop is silent-ish to PostgreSQL.
5. **`archive_command` has its own sanity check, independent of
   `archive-timeout`.** The first attempt at S2-3 used
   `archive_command='/bin/false'` to stall archiving; pgbackrest's `backup`
   command rejected this immediately with `ERROR: [068]: archive_command
   '/bin/false' must contain pgbackrest`, before archive-timeout logic was
   ever reached. Had to switch to a pgbackrest invocation with a bogus
   `--repo1-path` override (still "contains pgbackrest", still fails) to
   actually exercise the `[082]` timeout path.
6. **Manually invoking `archive-push` from the wrong working directory
   produces a different, unrelated error ([032]) before any repository
   check happens.** Running `pgbackrest --stanza=ghost archive-push <path>`
   by hand from `/` (not from PGDATA, unlike how PostgreSQL's archiver
   always invokes it) hit `ERROR: [032]: PostgreSQL working directory '/'
   is not the same as option pg1-path` first. The intended Sample-A-shaped
   `[103]`/`FileMissingError` for `archive-push` against `ghost` was
   confirmed instead via the real `archive_command` path in step 2b, where
   cwd is naturally correct.
7. **Both the 103 and 055 "missing stanza info" variants described in the
   brief were reproduced cleanly, and from the same never-created stanza**:
   `archive-push --stanza=ghost` -> `[103]` (`archive.info` missing,
   via step 2b's real path), `backup --stanza=ghost` -> `[055]`
   (`backup.info` missing, step 2a) -- confirming the brief's claim that 103
   is archive-push's framing ("no valid repository") and 055 is backup's
   framing ("this one repo's info file is missing") of structurally the
   same root cause.

## Disk

`df -h ~`: 8.5GB free before this saga's work began, 7.9GB free after
(incidents 1-5 inclusive, before final collection/cleanup) -- well within
the ~1.5GB budget. See the agent's final report for the post-cleanup number.
