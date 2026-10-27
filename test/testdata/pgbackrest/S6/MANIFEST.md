# Run S6 -- archive-push logged twice

Captured 2026-10-20 in a local Docker container (`postgres:17` image,
pgBackRest 2.59.3 from PGDG), with `_lab/lab.sh`.

## Why this run exists

The synchronous `archive-push` writes its own log file only when
`--log-level-file` is given on its command line or through an environment
variable, never from `pgbackrest.conf` (pgBackRest `src/config/config.c`,
`cfgLogFile`). Here `archive_command` passes it:

    archive_command = 'pgbackrest --stanza=main --log-level-file=info archive-push %p'

Each run is then written twice: to `pgbackrest_logs/main-archive-push.log`
and, through its console output captured by `logging_collector`, to
`pg_logs/postgresql.log`. Reading both must count each run once.

## Setup

- `postgres -c archive_mode=on -c archive_command=... -c logging_collector=on
  -c log_line_prefix='%m [%p] '`.
- `pgbackrest.conf`: `repo1-path=/var/lib/pgbackrest`,
  `repo1-retention-full=2`, `log-level-file=info`, `start-fast=y`.
- The first WAL segments fail with exit 127 (`pgbackrest: not found`) until
  the package is installed: PostgreSQL-side noise only.

## Timeline (UTC)

1. stanza-create, full backup.
2. Phase A, `log-level-console=warn` (the default): 3 WAL switches, then
   `chmod 000` on `archive.info` and its copy, 2 switches, 8 s, permissions
   restored, 65 s, 2 switches. The PostgreSQL log gets only the ERROR lines
   of the failed pushes; the file gets every run.
3. Phase B, `log-level-console=info`: the same sequence. The PostgreSQL log
   gets every run in full, like the file.
4. incr backup, expire.

## Expected (main-archive-push.log alone is the truth)

- `grep -c 'command begin' main-archive-push.log` = 33 archive-push runs.
- `grep -c 'pushed WAL file'` = 21, `grep -c 'P00  *ERROR'` = 12 ([103],
  permission denied), in two failure stretches of 6 attempts, both recovered.
- Reading `pg_logs/` and `pgbackrest_logs/` together gives the same counts,
  whatever the order.
