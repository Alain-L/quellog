# Run S3 -- backup-command incidents

Captured 2026-10-10 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2 rootless).
All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on the lab machine
immediately before/after each action. Full raw orchestration log with every
timestamp: `MANIFEST_timeline.log`. Driven step-by-step from an interactive
shell (not one monolithic script), because several incidents needed live
diagnosis and retries (see Surprises); the commands actually executed are
preserved both in the timeline and reconstructed, incident-by-incident, in
`_lab/s3_*.sh` (also at `~/quellog-lab/pgbackrest/_lab/` on the lab machine,
shared with later sagas -- `s3_04`, `s3_07` and `s3_08` carry inline comments
on the retries that were needed).

## Versions / topology

- pgBackRest (current) 2.59.3 (PGDG apt package). pgBackRest-old 2.48 not
  used in this saga.
- PostgreSQL 17.11 for `main` and `tl`/`tl_promoted`; PostgreSQL 16.15 for
  `upg16`, PostgreSQL 17.11 for `upg17` (incident 4's mismatch pair).
- Base image: `localhost/quellog-pgbr-lab:trixie`, one podman container
  `quellog-s3`, everything in the container's own writable layer (no bind
  mounts). Results pulled out with `podman cp`.
- Five PostgreSQL instances, each its own stanza:
  - `main` (port 5432, PG17, **`initdb --data-checksums`** -- needed for
    incident 6): incidents 1 (overlap), 2 (offline-while-running), 3
    (wrong `pg1-path`, via CLI override only, conf untouched), 5 (retention
    WARN, fires organically), 6 (checksum corruption), 7 (kill+resume), 9
    (stop/start).
  - `upg16` (port 5440, PG16) -> repointed in place to `upg17` (port 5441,
    PG17) under the same stanza `upg`, for incident 4.
  - `tl` (port 5442, PG17) -> restored into `tl_promoted` (port 5443) on a
    new timeline, then the stanza `tl` repointed at it, for incident 8.
- pgBackRest config (`pgbackrest.conf`, no secrets -- trust/peer auth only):
  `repo1-path=/var/lib/pgbackrest/repo1`, `log-level-file=info` globally,
  `log-level-console=info` for `archive-push` specifically, `start-fast=y`.
  **`repo1-retention-full` deliberately left unset** anywhere in the file,
  by design, to keep incident 5's WARN firing on every `backup`/`expire`.
  Lock files default to `/tmp/pgbackrest/<stanza>-backup-1.lock` (not under
  `repo1-path` -- this surprised us, see Surprises #1).
- PostgreSQL settings on every instance: `logging_collector=on`,
  `log_line_prefix='%m [%p] '`, `log_checkpoints=on`, `archive_mode=on`,
  `archive_command='pgbackrest --stanza=<name> archive-push %p'`.
- Traffic: light `pgbench -i -s2/3/5` per instance plus the deliberate
  file-level corruption/kill actions below. No long-running load.

## Incidents

| # | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1. Overlapping backup/backup | 13:32:07 -> 13:32:27 | `pgbench -i -s3`, full backup A launched detached, full backup B launched ~0.3s later on the same stanza `main` | `LockAcquireError [050]` | `ERROR: [050]: unable to acquire lock on file '/tmp/pgbackrest/main-backup-1.lock': Resource temporarily unavailable` + `HINT: is another pgBackRest process running?` -- backup A completed normally seconds later |
| 1. Overlapping backup/expire | 13:32:55 -> 13:33:14 | full backup C launched detached, `expire` launched ~0.3s later | same `LockAcquireError [050]`, same lock file | **identical wording and identical lock filename** (`main-backup-1.lock`) -- expire and backup share the same per-stanza lock |
| 1. expire-blocks-backup | 13:33:13 / 13:33:33 | detached `expire` then immediate `incr backup`, two attempts | expected a third `[050]` the other way round | **not reproduced**: `expire` with nothing to prune returns in well under 100ms, both attempts let the backup through (`BACKUP_E_EXIT=0`) -- too fast a window to block anything, a genuine negative result, not a bug |
| 2. Offline backup while PG running | 13:33:41 -> 13:33:42 | `backup --no-online` (no `--force`) against live `main` | `PgRunningError [038]` | `WARN: incr backup cannot alter 'checksum-page' option to 'false', reset to 'true' from <prior backup>` then `ERROR: [038]: --no-online passed but postmaster.pid exists - looks like PostgreSQL is running. Shut down PostgreSQL and try again, or use --force.` |
| 3. Wrong `pg1-path` | 13:33:48 -> 13:33:49 | `backup --pg1-path=/tmp/wrong_pgdata` (empty dir) against `main` | unspecified path/missing-file error | `WARN: unable to check pg1: [FileMissingError] unable to open missing file '/tmp/wrong_pgdata/global/pg_control' for read` then `ERROR: [056]: unable to find primary cluster - cannot proceed` / `HINT: are all available clusters in recovery?` -- same outer code 056 as S1's PG-stopped case, but the inner wrapped error is `FileMissingError`, not `DbConnectError`, since there's no postmaster at all on that path |
| 4. Stanza-upgrade (PG16->PG17) | 13:33:59 -> 13:35:31 | stanza `upg` created against PG16 `upg16`, one full backup; repoint `pg1-path`/`pg1-port` at PG17 `upg17` (same stanza); attempt backup; then `stanza-upgrade`; then backup again | `BackupMismatchError [051]`, then upgrade succeeds, then backup succeeds | `ERROR: [051]: PostgreSQL version 17, system-id 7695032147188564306 do not match stanza version 16, system-id 7695031941581701391` / `HINT: is this the correct stanza?`; `stanza-upgrade` logged only `INFO: stanza-upgrade for stanza 'upg' on repo1` (completed in 57ms) -- **neither of the brief's two predicted lines appeared** (no "upgrade repository format from %u to %u", no "already up to date"), see Surprises #2; follow-up full backup on `upg17` succeeded (`completed successfully (19557ms)`) |
| 5. Retention not configured | (fires on every run, see raw counts) | no `repo1-retention-full`/`-full-type` set anywhere | config-load WARN on every `backup`/`expire` | `WARN: option 'repo1-retention-full' is not set for 'repo1-retention-full-type=count', the repository may run out of space` + `HINT: to retain full backups indefinitely (without warning), set option 'repo1-retention-full' to the maximum.` -- 21 occurrences across the whole saga |
| 6. Page checksum corruption | 13:35:49 -> 13:36:40 | `data_checksums=on`, 20k-row table, `CHECKPOINT`, stop PG, flip 4 bytes at page 5 body offset 500 (not the header) in `base/16384/16411`, restart, `incr backup` | WARN "invalid page checksum" | `WARN: invalid page checksum found in file /var/lib/pg/main/base/16384/16411 at page 5` (singular form; backup still completed, exit 0) |
| 6. `--checksum-page-error` (THROW variant) | 13:36:40 | same corrupted file, `incr backup --checksum-page-error` | `ChecksumError [26]` | **not reproducible as briefed**: `ERROR: [031]: invalid option '--checksum-page-error'` -- this flag does not exist in pgbackrest 2.59.3 (confirmed via `pgbackrest help backup`: the only checksum-related options are `--checksum-page` (bool, validate) and `--delta`); no CLI knob escalates a page-checksum mismatch to a hard error during `backup` in this version, see Surprises #3 |
| 7. Kill mid-backup (take 1) | 13:37:16 -> 13:38:52 | full backup slowed with `--compress-type=zst --compress-level=19`, `kill -TERM` the **top-level** pid mid-way | backup aborts, logs SIGTERM, next run resumes | top-level pid became a zombie immediately but its `backup:local` worker child kept running at ~100% CPU and **finished the backup anyway**, unharmed -- SIGTERM to the parent alone does not stop the backup, see Surprises #4; the eventual retry (default `gz` compression, different from the killed attempt's `zst`) got `WARN: backup '20261010-133727F' cannot be resumed: new compression 'gz' does not match resumable compression 'zst'` instead of the brief's resume message |
| 7. Kill mid-backup (take 2) | 13:39:12 -> 13:41:17 | same slow backup, `kill -TERM` **both** the top-level pid and the `backup:local` worker pid together, then retry with the **same** compression settings | backup aborts, logs SIGTERM, next identical-type run resumes | `main-backup.log`: `...check archive for prior segment 000000010000000000000014` then **`terminated on signal SIGTERM`** (process truly stopped this time); retry with matching `--compress-type=zst --compress-level=19`: `WARN: resumable backup 20261010-133912F of same type exists -- invalid files will be removed then the backup will resume`, then completed successfully (87481ms, 107.1MB) |
| 8. Timeline switch after promote | 13:41:35 -> 13:43:53 | stanza `tl`/PG17, one full backup, plain default restore (no PITR) into `tl_promoted`, confirm new timeline, repoint stanza `tl` at it, backup | WARN "timeline switch ... enabling delta checksum" | first `--type=full` backup on `tl_promoted` did **not** trigger it (fresh manifest, no prior-timeline comparison needed), nor did a `--type=diff` chained off that same post-promotion full (also timeline 2, no mismatch) -- see Surprises #5; after expiring that post-promotion full+diff pair and re-running `--type=diff` so it chained off the **original pre-promotion** full instead: `WARN: a timeline switch has occurred since the 20261010-134147F backup, enabling delta checksum` / `HINT: this is normal after restoring from backup or promoting a standby.` |
| 9. Real `StopError [062]` | 13:43:59 -> 13:44:08 | `pgbackrest --stanza=main stop`, then `backup` | `ERROR: [062]` | `ERROR: [062]: stop file exists for stanza main` (exit 62) -- this time via pgbackrest's own stop-file sentinel, confirming S1's hypothesis that the earlier PG-down case (DbConnectError [056]) was simply the wrong mechanism, not a wrong code |
| 9. stop/start edge cases | 13:44:06 -> 13:44:07 | `stop` twice in a row; `start` with no stop file; `start` twice in a row | cheap WARN edge cases | `WARN: stop file already exists for stanza main`; `WARN: stop file does not exist for stanza main`; after `start`, backup succeeded again (`WARN: incr backup cannot alter compress-type option to 'gz', reset to value in 20261010-133912F`, exit 0) |

## Raw counts

pgBackRest logs (`pgbackrest_logs/*.log`, 402 lines across 13 files --
`main-{backup,expire,stanza-create,start,stop}.log`,
`tl-{backup,expire,restore,stanza-create}.log`,
`upg-{backup,expire,stanza-create,stanza-upgrade}.log`):
- `ERROR:` (total): 9 -- 2x `[050]` LockAcquireError (incident 1, both
  orders), 1x `[038]` PgRunningError (incident 2), 1x `[056]`
  (incident 3, wrong path), 1x `[056]` (incidental, during initial setup
  retry before PG main was started -- see Surprises #6), 1x `[051]`
  BackupMismatchError (incident 4), 1x `[062]` StopError (incident 9); plus
  the 2x duplicated `[050]` already counted once per log file (backup and
  expire each log their own copy of the same failure).
- `WARN:` (total): 34 -- 21x the retention WARN (incident 5, organic on
  every backup/expire), 3x invalid-page-checksum (incident 6, including
  the retry that still had the corrupted file), 2x resumable-backup
  related (1x cannot-resume-compression-mismatch, 1x true resume message,
  incident 7), 1x timeline-switch (incident 8), 1x PITR-affected-by-expire
  (housekeeping during incident 8's cleanup), 2x stop-file edge cases
  (incident 9), 2x "cannot alter ... option" (incidents 2 and 9, both
  incr-backup option-pinning warnings unrelated to the briefed incidents
  but captured as-is), 1x the incidental DbConnectError from setup (see
  Surprises #6), 1x FileMissingError wrapper (incident 3).
- No `main-check.log`, `main2-check.log`, `*-archive-push.log`, or
  `*-archive-get.log` file exists anywhere, despite `check` and
  `archive-push` running dozens of times successfully -- same gap S1
  documented in `_lab/README.md`; ground truth for those commands here is
  the PostgreSQL server logs below.
- Backups recorded across the whole run (final `pgbackrest info`, all
  three stanzas): `main` 5 (3 full, 2 incr), `tl` 2 (1 full, 1 diff), `upg`
  2 (1 full on PG16, 1 full on PG17, prior/current db split visible in
  `info`).

PostgreSQL logs (`pg_logs_*/postgresql.log`, 297 lines total):
- `main` (144 lines): `archive-push command begin` 31, `pushed WAL file`
  31 (1:1, all succeeded), `checkpoint starting` 11 -- these are
  pgBackRest's own INFO lines captured via the PG log, same mechanism S1
  documented.
- `tl` (42 lines) / `tl_promoted` (66 lines): recovery narrative for the
  incident-8 restore+promote is in `tl_promoted`'s log (new timeline
  confirmed via `pg_walfile_name()` returning a `000000020...` prefix
  right after promotion, before any WAL was even written on the new
  timeline).
- `upg16` (26 lines) / `upg17` (19 lines): routine startup/checkpoint
  lines only, nothing incident-specific beyond what's already in the
  pgBackRest logs.

## Surprises

1. **The backup/expire lock file lives under `/tmp/pgbackrest/`, not under
   `repo1-path`.** Every `LockAcquireError [050]` in this run names
   `/tmp/pgbackrest/main-backup-1.lock` even though `repo1-path` is
   `/var/lib/pgbackrest/repo1` -- pgBackRest's default `lock-path` is
   independent of the repo and was never overridden here. `backup` and
   `expire` contend on the exact same lock file name for a given stanza,
   confirmed by capturing byte-identical error wording for both
   backup-blocks-backup and backup-blocks-expire.
2. **`stanza-upgrade`'s actual log line matches neither of the brief's two
   predicted forms.** Expected either "upgrade repository format from %u
   to %u" or "stanza '%s' on %s is already up to date". What 2.59.3
   actually logs at INFO for a successful upgrade (version+system-id
   mismatch genuinely present, confirmed by the preceding `[051]` ERROR)
   is simply `stanza-upgrade for stanza 'upg' on repo1` -- a generic
   "command ran" line, not a description of what it changed. The version
   bump alone apparently did not need a repo-format bump either (no
   "upgrade repository format" line at any verbosity tested); whatever
   `stanza-upgrade` actually rewrote on disk, it did not say so.
3. **`--checksum-page-error` does not exist in pgbackrest 2.59.3.** The
   brief's guessed flag name produced `ERROR: [031]: invalid option`.
   `pgbackrest help backup` lists only `--checksum-page` (a boolean
   enabling/disabling the WARN-on-mismatch behavior already observed,
   defaulting from PostgreSQL's own `data_checksums` GUC) and `--delta`.
   No combination of documented backup options escalates a page-checksum
   mismatch to a hard `ChecksumError [26]` during `backup` in this
   version -- if code 26 exists at all, it is not reachable from this
   command via any flag we found. Budget did not allow grepping the
   source for where `ChecksumError` is actually thrown instead.
4. **Killing only the top-level `pgbackrest backup` pid does not stop the
   backup.** The command forks a `backup:local` worker that does the
   actual file I/O; the top-level process becomes a defunct zombie
   immediately on SIGTERM but the worker is reparented and keeps running
   to completion unsupervised, at ~100% CPU, with no interruption logged
   anywhere. Confirmed twice. To genuinely interrupt a backup, SIGTERM
   must be sent to both pids (found via `pgrep -f backup:local`) in the
   same instant. This matters for anyone trying to reproduce "killed
   backup" scenarios: a naive single-pid kill produces a perfectly normal,
   complete backup with zero trace of the attempted interruption.
5. **The timeline-switch WARN only fires for diff/incr backups chained off
   a backup taken on the *old* timeline -- a fresh full backup after
   promotion never sees it.** Our first attempt took a `--type=full`
   backup immediately after repointing the stanza at the promoted
   instance (reasonable instinct, mirrors how the other incidents start),
   which built a brand-new manifest with no prior-timeline reference, so
   no mismatch was possible; a `--type=diff` chained off *that* full also
   missed it, for the same reason (both on timeline 2). Only after
   expiring the post-promotion full+diff pair (`expire --set=<label>`,
   which cascades to its diff child) and re-running `--type=diff` so it
   chained off the original pre-promotion full did the WARN appear. In
   other words: the WARN is about the *manifest comparison*, not about
   "a promotion happened recently" in any absolute sense.
6. **An incidental `[056]` DbConnectError appeared in `main-stanza-create.log`
   before incident 1 even started**, left over from a setup-script retry:
   the first `stanza-create --stanza=main` call ran before PostgreSQL's
   `postgresql.conf` override block had been correctly written (an earlier
   attempt used bash double-quoted heredoc escaping that produced invalid
   PostgreSQL conf syntax -- PostgreSQL's config grammar only accepts
   **single**-quoted string values, not double-quoted ones, unlike shell
   or JSON; fixed by writing the override fragment as a separate local
   file with single quotes and piping it in via `cat >>` over SSH instead
   of nesting it inside layered shell quoting). Left in the collected logs
   as-is since it is genuine (if accidental) pgBackRest output, not
   scrubbed, but it is unrelated to any of the 9 briefed incidents.
