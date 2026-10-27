# Run S1 -- nominal life of a stanza

Captured 2026-10-10 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2 rootless).
All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on the lab machine
immediately before/after each action. Full raw orchestration log with every
timestamp: `MANIFEST_timeline.log`. Unlike P1, this run was driven step-by-step
(not a single monolithic script) because several steps needed live diagnosis
and config fixes along the way (see Surprises); the exact commands executed are
preserved in `_lab/s1_*.sh` (also at `~/quellog-lab/pgbackrest/_lab/` on the lab
machine, shared with later sagas) and in the per-step shell invocations recorded
in the timeline.

## Versions / topology

- pgBackRest (current) 2.59.3 (PGDG apt package, Debian trixie build).
- pgBackRest (old, source build, for later sagas only, not used in S1):
  2.48 (`/usr/local/bin/pgbackrest-old`, tag `release/2.48`).
- PostgreSQL 16.15 (Debian 16.15-1.pgdg13+2) -- used for this saga's `main`
  instance and the two restores. PostgreSQL 17.11 also present in the base
  image for later sagas, not used here.
- Base image: `localhost/quellog-pgbr-lab:trixie`, one podman container
  `quellog-s1`, everything inside the container's own writable layer (no
  bind mounts -- see `_lab/README.md` for why). Results pulled out with
  `podman cp`.
- One PostgreSQL instance (`main`, port 5432) for the whole "nominal life"
  story: `stanza-create` through 3 full backups / 1 diff / 1 incr /
  `expire` / `verify` / stop-start / two restores.
- A second, same-instance stanza `main2` (pg1-path/port identical to `main`)
  used only to capture the "no prior backup, incr changed to full" WARN.
- Two restore targets, both fresh data directories inside the same
  container: `restore1` (port 5433, plain restore, no PITR) and `restore2`
  (port 5434, PITR to a specific time).
- pgBackRest config (`pgbackrest.conf`, no secrets -- trust/peer auth only,
  nothing to strip): `repo1-path=/var/lib/pgbackrest/repo1` (local posix),
  `log-level-file=info` globally, `log-level-console=info` for
  `archive-push` specifically (`[global:archive-push]` section, exactly as
  briefed), `start-fast=y` (added mid-run, see Surprises).
- PostgreSQL settings: `logging_collector=on`, `log_line_prefix='%m [%p] '`,
  `log_checkpoints=on`, `archive_mode=on`,
  `archive_command='pgbackrest --stanza=main archive-push %p'`.
- Traffic: `pgbench -i -s 5` (500k rows in `pgbench_accounts`) plus several
  short `pgbench -c 2/3 ... -T 10..20` light-load bursts between backups.

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1. stanza-create, check | 12:54:34 -> 12:56:27 | `initdb` PG16 at `/var/lib/pg/main`, start, `createdb bench`, `pgbackrest stanza-create --stanza=main`, `pgbackrest check --stanza=main` | both succeed | both succeeded (exit 0); `check` triggered 4 real WAL switches/archive-push calls while being poked at different console levels during diagnosis (see Surprises #1) |
| 2. full backup + pgbench -i + light load | 13:05:16 -> 13:05:39 | `pgbackrest --type=full backup` running concurrently with `pgbench -i -s5` then `pgbench -c3 -j1 -T20` | full backup succeeds alongside the load | full backup `20261010-130516F` completed in 21.6s (`full backup size = 29.4MB, file total = 1265`); pgbench -i: 500000 rows in 0.79s; light load: 3547 xacts in 20s, tps=177 |
| 3. diff backup, then incr backup | 13:05:53 -> 13:08:30 (first attempt aborted, retried 13:08:25->13:08:30) | `--type=diff backup` then `--type=incr backup` | both succeed quickly | **first attempt hung >2 min** waiting on the next regular checkpoint (see Surprises #2), killed; after adding `start-fast=y`, diff (`20261010-130516F_20261010-130825D`) and incr (`20261010-130516F_20261010-130828I`) both completed in ~3s each |
| 4. second stanza main2, incr with no prior full | 13:08:36 -> 13:09:37 | `pgbackrest --stanza=main2 stanza-create` then `--type=incr backup` -> expect "no prior backup exists, incr backup has been changed to full" WARN | WARN appears, backup proceeds as full | WARN appeared exactly as expected, **but the backup then failed** with `ERROR: [082]: WAL segment 00000001000000000000001E was not archived before the 60000ms timeout` -- `main2` shares `pg1-path`/port with `main` but `archive_command` is hardcoded to `--stanza=main`, so `main2`'s WAL never reaches its own archive dir (see Surprises #3); final `info main2`: `status: error (no valid backups)` |
| 5. 2nd+3rd full backup, expire --repo1-retention-full=2 | 13:10:13 -> 13:11:20 | two more `--type=full backup` (3 fulls total on `main`), light pgbench bursts between/after for realistic WAL and a PITR marker, then `expire --repo1-retention-full=2` | oldest full + its diff/incr children removed | full#2 `20261010-131023F`, full#3 `20261010-131050F`; before expire, `info` listed 3 fulls + the step-3 diff/incr (all children of full#1); after `expire --repo1-retention-full=2`, full#1 (`20261010-130516F`) **and its diff/incr** were gone, `wal archive min` moved from `...0013` to `...0021` (archive range pruned too); PITR marker captured mid-burst: `2026-10-10 13:11:10.200293` UTC |
| 6. info text + json | 13:11:32 | `pgbackrest info` and `info --output=json` | reflects 2 remaining fulls | both captured (`info_text.txt`, `info_json.json`); text form matches step 5's post-expire listing exactly; JSON form is a single-element array with nested `archive`/`backup` objects, `backrest.version":"2.59.3"` |
| 7. verify | 13:11:32 -> 13:11:33 | `pgbackrest verify` | clean pass | exit 0, no WARN/ERROR; `main-verify.log` 3 lines (begin/one INFO/end) |
| 8. pg_ctl stop, backup while down, pg_ctl start | 13:11:40 -> 13:11:43 | backup refused because PG is down; brief says to expect `StopError` code 062 | **refused with a different error than briefed**: `WARN: unable to check pg1: [DbConnectError] ...`, then `ERROR: [056]: unable to find primary cluster - cannot proceed` (exit code 56, `DbConnectError`/`unable to find primary cluster`), **not** code 062. `pgbackrest stop`'s own "stop file" sentinel was never created in this test (we only ran `pg_ctl stop`, not `pgbackrest stop`) -- see Surprises #4. PG restarted cleanly 1.5s later |
| 9a. restore latest, no PITR | 13:12:33 -> 13:12:48 | restore `20261010-131050F` into `restore1` (port 5433), start, confirm boot | boots as new primary | `restore command end: completed successfully (8670ms)`, `restore size = 105MB, file total = 1279`; after start: `pg_is_in_recovery()=f`, new timeline `000000020000000000000026`, `pgbench_accounts` has 500000 rows |
| 10a. check-equivalent sanity on restore1 | 13:12:46 -> 13:12:48 | confirm the restored instance is independently checkable | works once wired with its own stanza | created a dedicated stanza `restored` (`pg1-path=/var/lib/pg/restore1`, `pg1-port=5433`, its own `archive_command`), `pgbackrest --stanza=restored check` -> exit 0 |
| 9b. restore latest, WITH PITR | 13:14:01 -> 13:14:15 | restore into `restore2` (port 5434) `--type=time --target='2026-10-10 13:11:10.200293'` | recovers to the target, observe archive-get found/not-found at the tail | restore command succeeded (9615ms); PG log: `starting point-in-time recovery to 2026-10-10 13:11:10.200293+00`, `restored log file "000000010000000000000023" from archive`, `restored log file "000000010000000000000024" from archive`, then **`recovery stopping before commit of transaction 9052, time 2026-10-10 13:11:10.206077+00`** and `pausing at the end of recovery` (PostgreSQL's own default `recovery_target_action=pause`, not an immediate promotion); after `pg_wal_replay_resume()`: `redo done at 0/24C759D0`, `last completed transaction was at log time 2026-10-10 13:11:10.187005+00`, `selected new timeline ID: 2`, `archive recovery complete` -- no "not found" anywhere because the target was reached entirely from already-archived WAL, before recovery ever had to probe past the end of the archive (contrast with 9a, which ran all the way to the end and necessarily did hit that probe -- see Surprises #5) |
| 10b. sanity check on restore2 | 13:14:44 | confirm promoted PITR instance is healthy | | `pg_isready` accepting, `pgbench_accounts` count 500000, new walfile `000000020000000000000026` |

## Raw counts

pgBackRest logs (`pgbackrest_logs/*.log`, 175 lines total across 8 files --
`main-backup.log`, `main-expire.log`, `main-restore.log`,
`main-stanza-create.log`, `main-verify.log`, `main2-backup.log`,
`main2-stanza-create.log`, `restored-stanza-create.log`):
- `command begin`: 20 (4 backup-full, 2 backup-diff-ish/incr counts folded in
  below, 6 expire, 2 restore, 3 stanza-create, 1 verify -- see exact list in
  the log files; `--start-fast` appears in every command issued after step 3's
  config fix)
- `WARN:` (total): 11 -- 9x `option 'repo1-retention-full' is not set ...`
  (fires on every `backup`/`expire` invocation since retention was left
  unconfigured in `[global]` by design), 1x `no prior backup exists, incr
  backup has been changed to full` (step 4), 1x `unable to check pg1:
  [DbConnectError] ...` (step 8)
- `ERROR:` (total): 2 -- `[082]` `ArchiveTimeoutError` (step 4),
  `[056]` `DbConnectError`/"unable to find primary cluster" (step 8)
- Backups recorded in the repo across the whole run: 5 total (3 full, 1 diff,
  1 incr), 1 expired away (full#1 + its diff/incr), 2 full remain at the end
- Note: **no `main-check.log`, `main-archive-push.log`, `main-archive-get.log`,
  or `restored-check.log` file exists anywhere**, despite `check`,
  `archive-push`, and `archive-get` all running and succeeding multiple
  times with `log-level-file=info` in effect -- see Surprises #1.

PostgreSQL main log (`pg_logs_main/postgresql.log`, 265 lines):
- `archive-push command begin`: 47, `pushed WAL file`: 47 (1:1, all
  succeeded) -- **these are pgBackRest's own INFO lines, appearing inside
  the PostgreSQL log file**, confirming the briefed hypothesis: with
  `log-level-console=info` set for `archive-push` specifically, its stdout
  is inherited by the archive_command child process, which postmaster's
  logging collector captures into the regular PG log stream (see Surprises
  #6 for the exact mechanism/caveat)
- `checkpoint complete`: 14, `checkpoint starting`: matching count (log_checkpoints=on)
- `restore point "pgBackRest Archive Check" created`: 17 (one per `check`-triggered WAL switch during the diagnostic/step-1/step-10a investigation)
- 0 lines mentioning `restore_command`, `archive-get`, "not found", or
  "unable to restore" -- confirms the research notes' prediction that
  `restore_command` failures/retries are logged at `DEBUG2` by PostgreSQL
  and therefore invisible at the default `log_min_messages=warning`, even
  during the two restores in steps 9a/9b

PostgreSQL restore1/restore2 logs: see Surprises #7 for why `restore1`'s
*own* recovery-era log lines ended up mixed into `pg_logs_main/postgresql.log`
instead of its own file; `restore2`'s log (`pg_logs_restore2/postgresql.log`,
isolated correctly) has the full recovery narrative quoted in step 9b above.

## Surprises

1. **`check` / `archive-push` / `archive-get` do not reliably persist their
   own pgBackRest log *file*, in this environment.** Reproduced 5+ times
   with `log-level-file=info` in effect (confirmed in the command-begin
   banner each time) and the command genuinely succeeding (WAL really
   archived/restored, confirmed via `info`/PG log/console): no file ever
   appears, in `/var/log/pgbackrest` (default) nor in several fresh,
   world-writable scratch directories used to rule out a permissions
   problem. `stanza-create`, `backup`, `expire`, `verify`, and `restore`
   write their log files reliably every single time, including in the same
   container, same user, same run. Root cause not identified (budget did
   not allow deeper C-level investigation; no `strace` in the minimal
   image). Documented at length in `_lab/README.md` for later sagas. For
   this MANIFEST, the ground truth for these three commands' activity is
   instead: the PostgreSQL server log (for `archive-push`, reliably) and our
   own command transcripts / `info` snapshots (for `check`/`archive-get`).
2. **Backups hang on the next regular checkpoint without `start-fast=y`.**
   The brief did not mention this option. The step-2 full backup "got
   lucky" (a natural checkpoint happened within ~20s, probably helped by
   the concurrent pgbench load). The step-3 diff backup, issued right after
   with no concurrent write load, sat for 2+ minutes with
   `execute backup start: backup begins after the next regular checkpoint
   completes` and no further progress (confirmed via `pg_stat_activity`:
   the backend was parked in `pg_backup_start()` waiting on
   `CheckpointDone`) -- killed manually, `start-fast=y` added to
   `[global]`, retried successfully in ~3s. Pre-2.53-era defaults are the
   same here (`start-fast` default has always been `n`), so this is not a
   version-specific finding, just an easy-to-miss default.
3. **Two stanzas sharing one PostgreSQL instance need stanza-aware
   `archive_command`, or the second stanza's first backup hangs for a full
   minute and then fails.** `main2` was deliberately pointed at the same
   `pg1-path`/port as `main` to capture the "changed to full" WARN (per the
   brief), but `archive_command` stayed hardcoded to `--stanza=main`. The
   WARN appeared exactly as briefed, but the backup then ran into
   `ArchiveTimeoutError` (code 082) after the full 60s `archive-timeout`,
   because WAL pushed by the (single, stanza=main) archiver never lands in
   `main2`'s archive directory. This is a realistic, common misconfiguration
   (not a lab bug) and is itself a useful ground-truth artifact: the brief
   only asked for the WARN, this run captured both.
4. **Step 8's error code did not match the brief's expectation.**
   The brief anticipated `StopError` (062) for "backup attempted while PG is
   down". What actually happened, with a plain `pg_ctl stop` (no
   `pgbackrest stop` sentinel involved): `WARN: unable to check pg1:
   [DbConnectError] unable to connect to 'dbname='postgres' port=5432':
   connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed:
   No such file or directory`, immediately followed by
   `ERROR: [056]: unable to find primary cluster - cannot proceed` /
   `HINT: are all available clusters in recovery?` -- i.e. `DbConnectError`
   (056), not `StopError` (062). `StopError` (062) is pgBackRest's own
   "stop file" sentinel mechanism (created by running `pgbackrest stop`),
   which is a distinct, separate concept from PostgreSQL simply being down;
   this run never invoked `pgbackrest stop`, so 062 was never in play. (As
   a side note, `pgbackrest stanza-delete` without a prior `pgbackrest stop`
   hit a related but different error during lab cleanup/reset:
   `ERROR: [055]: stop file does not exist for stanza 'main'` --
   `FileMissingError`, not `StopError` either, since the *check* for the
   stop file's absence is itself what's failing, not the presence of one.)
5. **PITR via `--type=time` stopped cleanly inside already-archived WAL and
   paused (PostgreSQL's default `recovery_target_action=pause`), while the
   plain/latest restore in 9a necessarily ran to the very end of available
   WAL and promoted immediately.** Neither restore produced a single
   "not found" line in the PostgreSQL log (consistent with the
   DEBUG2-level invisibility documented in the research notes), but the
   PITR restore's recovery narrative is cleanly bounded
   (`recovery stopping before commit of transaction 9052, time ... /
   pausing at the end of recovery`, requiring an explicit
   `pg_wal_replay_resume()` to promote) whereas 9a's just runs out and
   promotes on its own. `pgbackrest`'s own `archive-get` log file was, per
   Surprise #1, absent for both restores -- the only visible trace of the
   "replay WAL until it runs out" mechanics is the PostgreSQL log's
   `restored log file "..." from archive` lines (2 of them for restore2,
   both for already-archived segments reached before the target).
6. **Confirmed empirically: `archive-push`'s console output really does
   land inside the PostgreSQL server log**, exactly as the brief
   hypothesized, when `log-level-console=info` is set for `archive-push`
   specifically (`[global:archive-push]` section) while `logging_collector`
   is on. 47 pairs of `archive-push command begin` / `pushed WAL file
   '...' to the archive` INFO lines appear verbatim inside
   `pg_logs_main/postgresql.log`, interleaved with PostgreSQL's own log
   lines, using PostgreSQL's own log format for nothing (they keep
   pgBackRest's own `P00   INFO: ...` shape, not PostgreSQL's
   `%m [%p]` prefix) -- i.e. the archiver child's stdout is inherited
   wholesale and the logging collector just captures raw bytes, it does not
   reformat them.
7. **`restore1`'s postgresql.conf inherited the original `main` instance's
   `log_directory` setting from the backup** (pgBackRest restores
   `postgresql.conf` verbatim as part of the data directory), so its first
   ~2 minutes of recovery-era log lines (the actual PITR-relevant
   `starting archive recovery` / `consistent recovery state reached` /
   `archive recovery complete` narrative for 9a) ended up appended into
   `pg_logs_main/postgresql.log` instead of a separate `restore1` log file.
   Caught and fixed (added an explicit `log_directory` override) before
   starting `restore2`, which has a cleanly separated log
   (`pg_logs_restore2/postgresql.log`). For 9a's full recovery narrative,
   see the tail of `pg_logs_main/postgresql.log` (PIDs 722/726) rather than
   `pg_logs_restore1/postgresql.log` (which only has the later, already-promoted
   restart). This is itself a realistic trap for anyone restoring with
   `--type=preserve`/default config handling and worth quellog being aware
   of: a restored instance's log destination is whatever its backed-up
   `postgresql.conf` said, not automatically separated from the source.

## Files

- `MANIFEST_timeline.log` -- every step's UTC start/end + inline `info`
  snapshots, PITR marker.
- `pgbackrest_logs/` -- all of `/var/log/pgbackrest/` at the end of the run
  (8 files, see Raw counts; deliberately missing the check/archive-push/
  archive-get files, see Surprises #1).
- `pg_logs_main/`, `pg_logs_restore1/`, `pg_logs_restore2/` -- PostgreSQL's
  own logs for the three instances (see Surprises #7 for why restore1's is
  thin).
- `pgbackrest_conf/pgbackrest.conf` -- as it stood at the end of the run
  (after the mid-run `start-fast=y` addition); no secrets to strip.
- `info_text.txt`, `info_json.json` -- step 6's two `info` captures.
- `step8_backup_while_down.txt` -- literal transcript of the step-8 error.
- `pitr_marker.txt` -- the exact PITR target timestamp used in 9b.
- `_lab/` -- `Dockerfile`, `lib.sh`, `s1_*.sh` (the scripts actually run),
  `README.md` (shared notes for later sagas, podman cp / log-file gotchas).
