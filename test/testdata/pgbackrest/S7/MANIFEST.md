# Run S7 -- archive-get: restores, a standby, a WAL lost from the repository

Captured 2026-10-24 in a local Docker container (`postgres:17` image,
PostgreSQL 17.11, pgBackRest 2.59.3 from PGDG), with `_lab/lab.sh`. One
container: the primary on port 5432, every restored instance in
`/var/lib/pg/<name>` on its own port. All times UTC.

## Why this run exists

No other run logs archive-get. Like archive-push, the synchronous
archive-get writes its own log file only when `--log-level-file` is on its
command line: the `restore_command` pgBackRest writes by default
(`pgbackrest --pg1-path=... --stanza=main archive-get %f "%p"`) leaves
`main-archive-get.log` empty, as `restore1` shows. Every other restore here
passes `--recovery-option=restore_command=... --log-level-file=info ...`.
The async worker (`archive-get:async`) logs from `pgbackrest.conf`.

## Setup

- Primary: `archive_mode=on`, `archive_command='pgbackrest --stanza=main
  --log-level-file=info archive-push %p'` (set after the package is
  installed: the first segment fails once with `[103] archive.info missing`,
  before stanza-create).
- `pgbackrest.conf`: `repo1-path=/var/lib/pgbackrest`,
  `repo1-retention-full=2`, `log-level-file=info`, `start-fast=y`,
  `spool-path=/var/spool/pgbackrest`; `[global:archive-get]`
  `archive-async=y`, `archive-get-queue-max=64MB` during step 4 only.
- Each restored instance: `archive_mode=off`, `logging_collector=on`,
  `log_line_prefix='%m [%p] '`, its log in `pg_logs_<name>/`.

## Timeline

| Time | Step | Observed |
|---|---|---|
| 20:38:32 | stanza-create, full backup `20261024-203832F` | ok |
| 20:38:41 | PITR target 1 (4000 rows) | |
| 20:38:46 | diff backup `20261024-203832F_20261024-203846D` | ok |
| 20:38:56 | 1. `restore1`, latest, default restore_command | promoted, 10000 rows; nothing in `main-archive-get.log` |
| 20:39:00 | 2a. `restorebad`, PITR, hand-written restore_command without `--pg1-path` | archive-get `[032]` twice, PostgreSQL does not start |
| 20:39:03 | 2b. `restore2`, PITR to target 1 | promoted, 4000 rows |
| 20:39:08 | 3. `standby`, `--type=standby`, async archive-get | replays the primary's WAL; polls "unable to find ... asynchronously" at the end of the archive |
| 20:39:25 -> 20:39:48 | outage: `chmod 000` on `archive.info` and its copy | archive-get `[103]` 8 times (the async worker only WARNs); archive-push fails too (one stretch, 9 attempts, recovered) |
| 20:40:05 | standby stopped (21000 rows, primary 22000) | |
| 20:40:12 | `00000001000000000000001C` deleted from the repository (pushed at 20:40:06) | |
| 20:40:14 | PITR target 2 (28000 rows) | |
| 20:40:21 | 4a. `restore3`, PITR to target 2 | "unable to find 00000001000000000000001C in the archive", then PostgreSQL FATAL "recovery ended before configured recovery target was reached" |
| 20:40:25 | 4b. `restore4`, latest | same "unable to find", then PostgreSQL promotes: 21000 rows, the WAL after the hole silently lost |

## Expected (pgbackrest_logs/ alone)

- archive-get: 99 runs (89 completed, 8 aborted `[103]`, 2 aborted `[032]`);
  39 "found ... in the repo1", 15 "found ... asynchronously", 4 "unable to
  find" a WAL segment (all `00000001000000000000001C`, the deleted one),
  14 "unable to find ... asynchronously" (the standby at the end of the
  archive), 17 "unable to find" a `.history` file (every recovery probes
  the next timeline: expected).
- archive-get:async: 27 runs, all completed, 4 with the outage's WARN.
- 6 restore runs, all completed: pgBackRest's restore only lays the files
  down; whether recovery reached its target is in each instance's
  PostgreSQL log.
