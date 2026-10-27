# Run S5 -- two-repo pgBackRest, one repo fails

Captured 2026-10-10 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2 rootless).
All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on the lab machine
immediately before/after each action. Full raw orchestration log with every
timestamp: `MANIFEST_timeline.log` (produced by `_lab/s5_00_setup.sh` +
`s5_02_archive_fail.sh` + `s5_03_backup_fail.sh` + `s5_04_info_broken.sh` +
`s5_05_targeted_repo.sh`, plus a handful of one-off commands run directly for
the repo2-seed backup and the final recovery check -- their exact commands and
timestamps are reproduced in the timeline and in this manifest).

## Versions / topology

- pgbackrest 2.59.3 (PGDG), PostgreSQL 16.15, single podman container
  `quellog-s5` (base image `localhost/quellog-pgbr-lab:trixie`, not rebuilt).
- One stanza `main`, one PG cluster, port 5432, db `bench`.
- Two POSIX repos in the same `pgbackrest.conf`: `repo1-path`/`repo2-path`,
  both plain local directories inside the container, `repo{1,2}-retention-full=2`.
  `start-fast=y` globally (see S1/S2's `_lab/README.md` gotcha: without it,
  `backup` can hang up to `checkpoint_timeout`). `log-level-file=info`
  globally, plus `[global:archive-push] log-level-console=info` so
  archive-push's INFO lines land in the PG server log via its captured
  stderr (per the known lab gotcha: `archive-push`/`check`'s own log *file*
  does not reliably persist in this environment -- see `_lab/README.md`).
- PostgreSQL: `logging_collector=on`, `log_line_prefix='%m [%p] '`,
  `log_checkpoints=on`, `archive_mode=on`,
  `archive_command='pgbackrest --stanza=main archive-push %p'`.
- Config used: `pgbackrest_conf/pgbackrest.conf` (no secrets to strip --
  POSIX repos need none).
- "Breaking" a repo = `chmod 000` on its top-level directory inside the
  container (same technique as S2's repo1 incident), restored with
  `chmod 750` at the end.

## Steps

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1. Setup | 13:58:07.613 -> 13:58:17.054 | container start, two-repo conf written, initdb, PG start, `createdb bench` | clean boot | PG 16.15 up, `bench` created |
| 2. stanza-create | 13:58:17.055 -> 13:58:17.518 | `pgbackrest --stanza=main stanza-create` (default log-level-console=warn, no `--repo`) | creates on both repos | silent success (no stdout at all -- console log level is `warn` by default, so a clean two-repo stanza-create produces **no per-repo lines** unless you raise `--log-level-console`; `main-stanza-create.log` confirms both repos initialized) |
| 3. check | 13:58:17.520 -> 13:58:18.501 | `pgbackrest --stanza=main check` | both repos checked | silent success, same reason (warn-level console) |
| 4. backup, no `--repo` | 13:58:18.503 -> 13:58:37.095 | `pgbackrest --stanza=main --type=full backup` | ambiguous -- which repo? | silent at warn level; re-run with `--log-level-console=info` (step 1b below) confirms the exact wording |
| 1b. backup, verbose, no `--repo` | 13:59:22.526 -> 13:59:33.179 | same full backup, `--log-level-console=info` | confirm the defaulting line | **`INFO: repo option not specified, defaulting to repo1`** -- literal, confirmed empirically. Backup completed to repo1 only in 10.4s |
| 1c. backup `--repo=2`, full | 13:59:40.985 -> 13:59:52.973 | seed repo2 with its own full backup (a plain two-repo setup only ever gets automatic backups on repo1; repo2 needs an explicit `--repo=2` run to have a valid backup of its own) | repo2 backup succeeds | completed in 11.7s, `new backup label = 20261010-135941F` |
| 5. `info` healthy baseline | 13:59:53.100 | `pgbackrest info` + `--output=json info`, both repos now have a valid backup | both repos `ok` | text: `status: ok`; json: `repo[0].status.code=0`, `repo[1].status.code=0`, top `status.message="ok"` -- see `cmd_output/s5_info_healthy.txt` / `.json` |
| S5-2. archive-push, repo2 broken | 13:59:58.486 -> 14:01:00.338 | `chmod 000 repo2`, 6x `pg_switch_wal()` ~10s apart | **task's working assumption**: WAL still gets archived overall to repo1, with a per-repo warning for repo2 | **contradicted** -- see Surprises #1below: `archive-push` aborts the *whole command* (`ERROR: [104]: archive-push command encountered error(s): repo2: [FileOpenError] unable to load info file ...archive.info... Permission denied`), repeated once per PostgreSQL archiver retry; `pgbackrest info` mid-incident: `status: mixed`, `repo1: ok`, `repo2: error (other) [PathOpenError] unable to list file info for path '/var/lib/pgbackrest/repo2/backup': [13] Permission denied` |
| S5-3. backup, no `--repo`, repo2 broken | 14:01:43.831 -> 14:02:44.264 | `pgbackrest --stanza=main --type=diff backup` (repo2 still chmod 000) | does it fail outright or degrade to repo1 with a warning? | **fails outright**, but not for the reason you'd guess: `ERROR: [082]: WAL segment ...0B was not archived before the 60000ms timeout` -- the backup's own archive-check never completes, because the upstream `archive-push` is still failing the whole command (see S5-2), so PostgreSQL's archiver never reports the segment as archived to *either* repo even though repo1 actually has the file on disk. `EXIT_CODE=82` |
| S5-4. `info`, repo2 broken | 14:02:59.414 -> 14:02:59.933 | `pgbackrest info` + `--output=json info` | clearly flags the broken repo vs the healthy one | text: `status: mixed`, `repo1: ok`, `repo2: error (other)` + the `PathOpenError` detail line; json: `repo[1].status={"code":99,"message":"[PathOpenError] ..."}`, top `status={"code":4,"message":"different across repos"}` -- see `cmd_output/s5_info_broken.txt` / `.json` |
| S5-5a. `backup --repo=2`, repo2 broken | 14:03:06.489 -> 14:03:06.736 | target the broken repo explicitly | isolated, clean error | **yes, clean and immediate**: `ERROR: [041]: unable to load info file '/var/lib/pgbackrest/repo2/backup/main/backup.info' ... Permission denied`, `EXIT_CODE=41`, 0.2s (no WAL wait at all -- fails before the archive-check phase) |
| S5-5b. `backup --repo=1`, repo2 broken | 14:03:06.737 -> 14:04:07.184 | target the *healthy* repo explicitly -- does scoping to `--repo=1` dodge the problem? | expected: succeeds, since repo1 is fine | **no** -- still `ERROR: [082]: WAL segment ...0C was not archived before the 60000ms timeout`, `EXIT_CODE=82`. See Surprises #2 |
| S5-recovery. restore + sanity check | 14:04:23.286 -> 14:04:30.587 | `chmod 750 repo2`, `pg_switch_wal()`, `backup --repo=1 --type=diff` | clean recovery, no lingering lock/stuck state | `EXIT_CODE=0` in 1.9s, `pgbackrest info` back to `status: ok` for both repos immediately -- no manual unstick needed once the permission is fixed |

## Surprises

1. **One broken repo does not "degrade gracefully" at the archive-push
   layer -- it fails the whole command, even though the healthy repo's
   copy is actually written to disk.** Verified directly: during S5-2,
   `find .../repo1/archive/main/16-1/...` shows segment
   `000000010000000000000008` fully present and checksummed on repo1, yet
   every `archive-push` invocation for that same segment still exits
   `[104]` because repo2's `archive.info` can't be read. PostgreSQL's
   archiver only sees the exit code, not the partial success, so it logs
   `archive command failed with exit code 104` and retries the *same*
   segment indefinitely (idempotent re-writes to repo1, permanently
   blocked on repo2). The task brief's framing ("confirm WAL still gets
   archived successfully overall") does not hold for this version/config:
   with no `--repo` on `archive_command`, a single broken repo can stall
   archiving system-wide, not just for itself.
2. **That stall cascades into `backup --repo=1` too.** Explicitly scoping
   a backup to the healthy repo does *not* bypass the problem in S5-5b,
   because `backup`'s archive-check waits on PostgreSQL's own archiver
   state, which is blocked by the unscoped `archive_command` failing as a
   whole (Surprise #1). The only way to truly isolate the healthy repo
   found in this run was `backup --repo=2` (S5-5a) targeting the *broken*
   repo, which fails immediately and cleanly on `backup.info` before ever
   reaching the archive-check phase -- the inverse of what you'd want. A
   real two-repo deployment protecting against exactly this would need
   `archive_command='pgbackrest --stanza=main --repo=1 archive-push %p'`
   (scoped) plus a second, separate push path for repo2, rather than the
   brief's assumption that unscoped `archive_command` pushes to both and
   tolerates one failing.
3. **`stanza-create`/`check`/`backup` are silent on success at the
   default console log level**, two-repo or not -- no `repo1:`/`repo2:`
   banner lines appear unless `--log-level-console=info` (or higher) is
   passed explicitly. The "repo option not specified, defaulting to
   repo1" line and all per-repo `[FileOpenError]`/`[PathOpenError]`
   aggregation lines only ever surfaced at INFO console level or above,
   or in an actual error (errors are always shown, which is itself the
   informative bit: pgbackrest promotes any repo failure to visible
   output even when nothing else is).
4. **Recovery is immediate and clean**, no stuck lock or stale-state
   issue: the moment `repo2` permissions were restored, the very next
   `backup --repo=1` succeeded in under 2 seconds and `pgbackrest info`
   reported `status: ok` for both repos with no extra step needed.

## Incidents reproduced (5/5)

All five incidents from the brief were reproduced, though #2 and #5 landed
on the *opposite* conclusion from the brief's working assumption -- see
Surprises above. That is itself the useful finding for quellog: a
per-repo error line (`repo2: [...]`) in an archive-push or backup log
does **not** imply "the other repo is fine, carry on" -- it can mean the
whole command aborted.

## Files in this directory

- `pgbackrest_conf/pgbackrest.conf` -- the two-repo conf as it stood at
  the end of the run (no secrets to strip).
- `pgbackrest_logs/` -- `main-stanza-create.log`, `main-backup.log`,
  `main-expire.log` (reliable per the lab's known gotcha; `archive-push`/
  `check` do not produce a persistent log file in this environment --
  their activity is only visible via the PG server log below, and via
  the command transcripts in `cmd_output/`).
- `pg_logs/postgresql.log`, `pg_logs/startup.log` -- PG's own log, which
  is where every `archive-push` INFO/ERROR line actually shows up
  (captured stderr, `[global:archive-push] log-level-console=info`).
- `cmd_output/` -- raw transcripts of every pgbackrest invocation run
  directly (not only through the `_lab/s5_*.sh` scripts), including both
  `info` snapshots in text and JSON, healthy and broken.
- `MANIFEST_timeline.log` -- full timestamped action log (116 lines).
- `_lab/s5_00_setup.sh`, `s5_02_archive_fail.sh`, `s5_03_backup_fail.sh`,
  `s5_04_info_broken.sh`, `s5_05_targeted_repo.sh` -- the exact scripts
  run (originals also left in the shared `~/quellog-lab/pgbackrest/_lab/`
  per saga convention). A few one-off commands (the verbose defaulting
  backup, the repo2-seed backup, and the final recovery check) were run
  directly rather than scripted; their exact commands and timestamps are
  in `MANIFEST_timeline.log` and this manifest's step table.
