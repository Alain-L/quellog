# Run S4 -- logging settings that hide (or reveal) information

Captured 2026-10-10 on lab machine `dalibo-pc` (Debian 13, podman 5.4.2 rootless).
All timestamps below are UTC, taken with `date -u +%FT%T.%3NZ` on the lab
machine immediately before/after each action. Full raw orchestration log with
every timestamp: `MANIFEST_timeline.log` (produced by the `_lab/s4_*.sh`
scripts, which are the exact scripts that were run -- nothing here was
hand-edited).

## Versions / topology

- pgBackRest 2.59.3 (`/usr/bin/pgbackrest`, current) and pgBackRest 2.48
  (`/usr/local/bin/pgbackrest-old`, pre-2.53), same image as prior sagas.
- PostgreSQL 17.11, one podman container `quellog-s4`, port 5432 inside the
  container, `logging_collector=on`, `log_line_prefix='%m [%p] '`,
  `log_checkpoints=on`, `archive_mode=on`,
  `archive_command='pgbackrest --stanza=main archive-push %p'` (constant for
  incidents 1-5; switched to point at `pgbackrest-old` for incident 6 only,
  the one unavoidable exception noted in the brief).
- One stanza `main`, one real repo `repo1`. One container reused across all
  six incidents (conf rewritten/reverted between incidents, never a container
  rebuild).
- Data: `pgbench -i -s 5` (500k rows in `pgbench_accounts`) loaded before
  incident 4, reused for 4/5.

## Incident 1 -- `log-level-file=warn` vs default `info` (backup)

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1 | 13:55:19.750 -> 13:55:31.568 | `pgbackrest --stanza=main backup --type=full` (default `log-level-file=info`) | full `main-backup.log` with begin/INFO-per-stage/end | banner + 10x `P00 INFO:` (`backup command begin`, `execute backup start`, `backup start archive=...`, `check archive for prior segment`, `execute backup stop...`, `backup stop archive=...`, `check archive for segment(s)...`, `new backup label = 20261010-135519F`, `full backup size = 29.3MB, file total = 1265`, `backup command end: completed successfully (11819ms)`) + 1x `P00 WARN:` (retention hint) |
| 2 | 13:55:32.230 -> 13:55:33 | `pgbackrest --stanza=main --log-level-file=warn backup --type=diff` | no INFO lines at all in the file, only WARN/ERROR | new `PROCESS START` banner, then **only** the same `P00 WARN:` retention-hint line -- no `backup command begin`, no `new backup label`, no `backup command end` -- even though the diff backup **did** complete successfully (confirmed separately via `pgbackrest info`: `diff backup: 20261010-135519F_20261010-135532D`) |

Reproduced cleanly. Key literal proof, `test/testdata/.../S4/01_logfile_file/main-backup.combined.log` lines 13-17 (the second banner onward):
```
-------------------PROCESS START-------------------
2026-10-10 13:55:32.230 P00   WARN: option 'repo1-retention-full' is not set for 'repo1-retention-full-type=count', the repository may run out of space
                                    HINT: to retain full backups indefinitely (without warning), set option 'repo1-retention-full' to the maximum.
```
That is the entire content of the log file for a backup that actually ran and succeeded -- for quellog, `log-level-file=warn` makes a successful `backup` invocation **structurally indistinguishable from one that did nothing**, in the log file alone.

## Incident 2 -- `log-timestamp=n`

| Step | UTC | Action | Expected | Observed |
|---|---|---|---|---|
| 1 | 13:56:09.514 | `pgbackrest --stanza=main --log-level-console=info check` (default `log-timestamp=y`) | lines start with a date/time | `2026-10-10 13:56:09.514 P00   INFO: check command begin 2.59.3: ...` |
| 2 | 13:56:09.7xx | same command + `--log-timestamp=n` | lines start directly with `P00`, no date/time at all | `P00   INFO: check command begin 2.59.3: --exec-id=124-d52c2834 --log-level-console=info --log-level-file=info --no-log-timestamp ...` |

Reproduced exactly per format.md §2's unit-test-pinned grammar. Files:
`02_timestamp/check_console_default.out`, `02_timestamp/check_console_no_timestamp.out`.
Note for quellog: a `log-timestamp=n` line carries **zero** time information of
its own -- there is no fallback/implicit timestamp to recover, so a parser
must either inherit time from context (e.g. the enclosing PostgreSQL log
line, when embedded there) or leave it unknown.

## Incident 3 -- `log-level-stderr`: `off` vs `warn`, `log-level-console=off` both times (THE key empirical question)

Both episodes ran through the **real** `archive_command` path (PostgreSQL's
archiver actually invoking `pgbackrest`), not a manual simulation, with
`repo1` made inaccessible (`chmod 000`, the proven S2 technique) so every
push fails with `[FileOpenError] ... Permission denied`.

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1a | 13:57:53 -> 13:58:22 | `[global:archive-push] log-level-console=off, log-level-stderr=off`; manual direct `archive-push` invocation with repo1 chmod 000, capturing stdout/stderr separately; then `repo1` chmod 000 + 2x `pg_switch_wal`, wait ~23s | manual run: **nothing** on either stream; PG log: only PostgreSQL's own generic lines | manual `manual_stdout.off` and `manual_stderr.off` both **empty** (confirmed: process exits 103 but writes nothing anywhere); PG log excerpt: 6x `LOG: archive command failed with exit code 103` / `DETAIL: The failed archive command was: ...` pairs + 2x `WARNING: ... failed too many times, will try again later` -- **zero** lines of pgBackRest's own text |
| 1b | 13:58:30 -> 13:59:03 | same, `log-level-stderr=warn` | manual run: bare `ERROR: [103]: ...` multi-line block on stderr, nothing on stdout; PG log: that **exact same bare block literally interleaved** with PG's generic lines | confirmed verbatim -- see excerpt below |

Reproduced cleanly, and this is the definitive answer:

**`log-level-stderr=off` (isolated by `log-level-console=off`): nothing of pgBackRest's own text reaches the PostgreSQL log.** `03_stderr/off/pg_log_excerpt.log`, full content:
```
2026-10-10 13:58:11.881 UTC [45] LOG:  archive command failed with exit code 103
2026-10-10 13:58:11.881 UTC [45] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/00000001000000000000000F
```
(repeated 3x per batch, then `WARNING: ... failed too many times`) -- purely PostgreSQL's own generic wording, never any `pgbackrest`-authored text.

**`log-level-stderr=warn`: pgBackRest's own bare `ERROR:` block literally appears inside the PostgreSQL log file**, byte for byte identical to what the manual invocation wrote to its own stderr, interleaved immediately *before* each of PostgreSQL's generic lines for the same failed attempt. `03_stderr/warn/pg_log_excerpt.log`, one cycle verbatim:
```
ERROR: [103]: unable to find a valid repository:
       repo1: [FileOpenError] unable to load info file '/var/lib/pgbackrest/repo1/archive/main/archive.info' or '/var/lib/pgbackrest/repo1/archive/main/archive.info.copy':
       FileOpenError: unable to open file '/var/lib/pgbackrest/repo1/archive/main/archive.info' for read: [13] Permission denied
       FileOpenError: unable to open file '/var/lib/pgbackrest/repo1/archive/main/archive.info.copy' for read: [13] Permission denied
       HINT: archive.info cannot be opened but is required to push/get WAL segments.
       HINT: is archive_command configured correctly in postgresql.conf?
       HINT: has a stanza-create been performed?
       HINT: use --no-archive-check to disable archive checks during backup if you have an alternate archiving scheme.
2026-10-10 13:58:53.513 UTC [45] LOG:  archive command failed with exit code 103
2026-10-10 13:58:53.513 UTC [45] DETAIL:  The failed archive command was: pgbackrest --stanza=main archive-push pg_wal/000000010000000000000012
```
Note the bare shape: no timestamp, no `P<pid>`, continuation lines indented exactly `len("ERROR: ")` = 7 spaces -- exactly the stderr-destination grammar predicted by format.md §9, and it is **not** PostgreSQL relaying/paraphrasing anything -- it is pgBackRest's own raw stderr bytes, physically inside the same file, because under `logging_collector=on` the archiver (and the shell/pgbackrest child it `system()`s) inherits fd 1/2 from postmaster, which postmaster had already redirected to the syslogger pipe before forking. **Hypothesis from the research confirmed precisely, with literal proof.**

Both episodes recovered cleanly after `chmod 750`: `pgbackrest info` showed `status: ok` in both cases, confirming the chmod/restore cycle didn't corrupt stanza state. Also confirmed (again) the S1/S2 finding: `main-archive-push.log` never exists in this environment regardless of `log-level-stderr` (`ls: cannot access ... No such file or directory` both times) -- pgBackRest's own log *file* for `archive-push` is simply not usable as ground truth here; the PostgreSQL log (or, for manual runs, the captured stdout/stderr) is the only reliable evidence.

## Incident 4 -- `process-max=4`, `log-level-file=detail` (parallel backup)

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1 | 13:56:20 -> 13:56:31 | `pgbench -i -s 5` against `bench` | 500k rows, ~1265 relation files on disk | `done in 0.87 s`, confirmed by backup's `file total = 1265` |
| 2 | 13:56:31 -> 13:56:41 | `pgbackrest --stanza=main --process-max=4 --log-level-file=detail backup --type=full` | interleaved `P01`-`P04` DETAIL per-file lines alongside `P00` INFO orchestrator lines | `main-backup.detail.log`: 20x `P00 INFO:`, 3x `P00 WARN:`/`DETAIL:`, and 307/308/332/330 `DETAIL:` lines from `P01`/`P02`/`P03`/`P04` respectively, genuinely interleaved by arrival time (not grouped by worker) |

Reproduced cleanly. Literal sample (`04_process_max/main-backup.detail.log`):
```
2026-10-10 13:56:33.081 P03 DETAIL: backup file /var/lib/pg/main/base/5/1255 (792KB, 0.74%) checksum 62e6cd419838856c0857896f3efbf2f08f2f9564
2026-10-10 13:56:33.091 P04 DETAIL: backup file /var/lib/pg/main/base/4/1255 (792KB, 1.48%) checksum 62e6cd419838856c0857896f3efbf2f08f2f9564
2026-10-10 13:56:33.121 P03 DETAIL: backup file /var/lib/pg/main/base/16384/1255 (792KB, 2.22%) checksum d9a76742fd8a51f3a265675f5cb75fa58efe481e
```
No `bundle N/M` token -- file bundling is a separate option (`repo1-bundle`), off by default, not implied by `process-max` alone; messages.md §2's "bundle" variant was not produced here (noted, not a bug).

## Incident 5 -- `log-subprocess=y` vs default `off`

Incident 4's run (default `log-subprocess=off`, `process-max=4`) already serves
as the "off" baseline: `rexec 'ls /var/log/pgbackrest/ | grep local'` returned
`NO_LOCAL_LOGS`.

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1 | 13:56:41 | (= incident 4's run) check for `main-backup-local-NNN.log` | none exist | `NO_LOCAL_LOGS` |
| 2 | 13:57:04 -> 13:57:05 | `pgbackrest --stanza=main --process-max=4 --log-level-file=detail --log-subprocess=y backup --type=diff` | `main-backup-local-NNN.log` files appear, one per worker | 4 files: `main-backup-local-001.log` through `-004.log`, each with its own `PROCESS START` banner |

Reproduced cleanly. One worker's full content (`05_subprocess/local_logs/main-backup-local-001.log`):
```
-------------------PROCESS START-------------------
2026-10-10 13:57:05.035 P00   INFO: backup:local command begin 2.59.3: --exec-id=168-44ff90a3 --log-level-console=off --log-level-file=detail --log-level-stderr=error --log-subprocess --pg=1 --pg1-path=/var/lib/pg/main --pg1-port=5432 --process=1 --remote-type=pg --repo1-path=/var/lib/pgbackrest/repo1 --stanza=main
2026-10-10 13:57:05.178 P00   INFO: backup:local command end: completed successfully (144ms)
```
Confirms format.md §6 verbatim: the worker's own effective options show `--log-level-console=off` (force-disabled, "used by the protocol") and `--log-level-stderr=error` (force-set) baked into its begin-banner line, regardless of what the user's `--log-level-stderr` was -- only `--log-level-file=detail` (the user's own setting) passed through. (This diff backup copied too few files to show per-file DETAIL lines inside the local log; the mechanism -- file existing at all -- is the point of this incident, not its content volume.)

## Incident 6 -- old (2.48, default `log-level-stderr=warn`) vs current (2.59.3, default `log-level-stderr=off`), pure defaults

`pgbackrest.conf` reset to **zero** `log-level-*` overrides for this incident
(see `06_oldversion/pgbackrest.conf.used`). `archive_command` switched via
`ALTER SYSTEM` + `pg_reload_conf()` between episodes (the one unavoidable
exception to "keep `archive_command` constant", since testing a different
binary requires pointing at it). Same `repo1 chmod 000` failure mode.

| Step | UTC start -> end | Action | Expected | Observed |
|---|---|---|---|---|
| 1 | 14:00:05 -> 14:00:22 | `archive_command='pgbackrest --stanza=main archive-push %p'` (2.59.3, pure defaults), repo1 chmod 000, 2x `pg_switch_wal` | per the brief: "NOTHING of pgBackRest's own text" | **not what happened** -- see Surprises below; `pg_log_excerpt.log`: 3x full-shape `P00  ERROR: [103]: ...` (WITH timestamp and `P00`, the file/stdout grammar, not the bare stderr grammar) + 3x PG's generic `LOG:`/`DETAIL:` pair + 1x `WARNING: ... failed too many times` |
| 2 | 14:01:01 -> 14:01:37 (first batch) + 14:01:35 -> 14:01:37 (retry after catch-up window) | `archive_command='pgbackrest-old --stanza=main archive-push %p'` (2.48, pure defaults), repo1 chmod 000, 2x `pg_switch_wal` | bare `ERROR: [NNN]: ...` (no timestamp, no `P<pid>`) per `log-level-stderr=warn` default | confirmed -- bare `ERROR: [046]: unexpected control version = 1700 and catalog version = 202406281` / `HINT: is this version of PostgreSQL supported?`, 6x, interleaved with PG's generic lines |

Adjusted / partially contradicts the brief's stated expectation -- reported precisely rather than forced to fit:

1. **2.48 failed for an entirely different reason than intended.** `repo1 chmod 000` never got a chance to matter: pgBackRest 2.48 does not understand PostgreSQL 17's control-file format (`unexpected control version = 1700 and catalog version = 202406281`) and rejects the run **before** touching the repository at all. The bare-stderr-shape demonstration is still valid (that part of incident 6 is about default routing, not about which specific error fires), but it is not evidence of a *repo-access* failure the way incident 3 is -- it is a version-incompatibility failure, incidentally also visible, at default `log-level-stderr=warn`, as a bare line in the PG log.
2. **2.59.3's "default `stderr=off`" does NOT mean nothing reaches the PG log.** With **zero** overrides (`log-level-console` left at its own default `warn`), the current binary's `ERROR: [103]: ...` block still lands inside the PostgreSQL log file -- just routed through **stdout** (console) instead of stderr, because `logPost()` falls through to stdout whenever stderr's threshold doesn't admit the message (§9 of the research doc, confirmed live). The shape is the **full file/stdout grammar** (`2026-10-10 14:00:20.306 P00  ERROR: [103]: ...`, timestamp + `P00` + 6-char-padded level), not the bare stderr grammar -- arguably *more* verbose/identifiable than the old binary's bare line, not less. The brief's framing ("2.59.3 puts NOTHING... by default") only holds once `log-level-console` is **also** explicitly turned off, which is exactly incident 3's isolated setup, not incident 6's "touch nothing" setup. **This is the single most important correction this saga produced**: the 2.53 default change (`stderr: warn -> off`) does not, by itself, silence pgBackRest in the PostgreSQL log -- it only changes *which byte-shape* (bare stderr vs full console) the leaked text takes, because stdout was never redirected away from the collector pipe either. An operator (or quellog) relying on "current pgbackrest defaults produce a quiet archive_command failure" is wrong unless they've also lowered `log-level-console`.

## Raw counts

- Incident 1: info-level `main-backup.log`: 10x `P00 INFO:`, 1x `P00 WARN:` (11 lines). warn-level section of the same file: 1x `P00 WARN:` only (0 INFO lines) for a backup that completed successfully.
- Incident 2: `check` command, both runs identical in content, differing only in the presence/absence of the `YYYY-MM-DD HH:MM:SS.mmm ` prefix (4 lines each, byte-identical from `P00` onward).
- Incident 3 / off: 6x `archive command failed with exit code 103` + 2x `failed too many times` in the PG log; 0 lines of pgBackRest's own text anywhere (manual stdout+stderr both empty, 0 bytes).
- Incident 3 / warn: 3x `archive command failed with exit code 103` + 3x bare `ERROR: [103]:` blocks (8 lines each incl. HINTs) interleaved 1:1 before each PG `LOG:`/`DETAIL:` pair, in the PG log.
- Incident 4: `main-backup.detail.log` -- `P00`: 20 INFO + 3 WARN/other; `P01`: 307 DETAIL; `P02`: 308 DETAIL; `P03`: 332 DETAIL; `P04`: 330 DETAIL (1277 per-file lines total for a 1265-file backup, some files logged via secondary match/reference paths).
- Incident 5: 4 `main-backup-local-NNN.log` files created (one per `process-max` worker), 0 before.
- Incident 6 / current (2.59.3, defaults): 3x `archive command failed with exit code 103` + 3x full-shape `P00  ERROR: [103]:` blocks in the PG log (via stdout, not stderr).
- Incident 6 / old (2.48, defaults): 6x `archive command failed with exit code 46` + 6x bare `ERROR: [046]:` blocks in the PG log (via stderr).
- `main-archive-push.log` never existed on disk in any incident (0 bytes, file absent) -- consistent with the S1 finding, reconfirmed 4 more times (off/warn x2 episodes + both incident-6 episodes).
- Both backups in incident 1 and both in incident 4/5 registered successfully in `pgbackrest info` (2 full + 2 diff backups total by the end of the saga), confirming none of the logging-level changes affected actual command success -- only what got *recorded about* that success.

## Surprises

1. **The single biggest finding of this saga**: `log-level-stderr=off` (current pgBackRest's default since 2.53) is *not*, by itself, sufficient to keep pgBackRest's own text out of the PostgreSQL log. Because PostgreSQL's `logging_collector` redirects postmaster's own stdout+stderr to the syslogger pipe *before* forking, and the archiver's `system("pgbackrest ...")` child inherits those same fds, **both** stdout (console level) and stderr (stderr level) physically land in the same log file -- not just stderr. The 2.53 default change only moves pgBackRest's error text from the bare stderr shape to the full timestamped/`P<pid>`-prefixed console shape; it does not silence it. Nailing this down required incident 3's deliberate `log-level-console=off` isolation to see the *true* silent case, which incident 6 (pure defaults) does not represent.
2. pgBackRest's own log *file* for `archive-push` (`main-archive-push.log`) never gets written in this lab environment, regardless of `log-level-file`/`log-level-stderr` -- reconfirms the S1/S2 README gotcha, now also under explicit `log-level-stderr` variation. The PostgreSQL log (or a manual invocation's own captured stdout/stderr) remains the only reliable evidence source for this command's failures here.
3. Incident 1's `log-level-file=warn` makes a **successful** backup's log file content indistinguishable from "nothing happened" -- only the unrelated retention WARN/HINT survives, with no "begin"/"end"/"new backup label" markers at all.
4. Incident 6's old-binary (2.48) episode failed on a PG17 control-version incompatibility before ever reaching the repo-permission check that was supposed to be the forcing failure -- the chmod 000 was, in that specific episode, inert; the bare-stderr-default demonstration still holds, but for a different underlying error than intended (noted so the artifact isn't misread as "2.48 also hits permission-denied").
5. The per-worker subprocess log banner (incident 5) shows the worker's own effective `--log-level-console=off`/`--log-level-stderr=error` baked in as command-line options in its begin-banner, confirming format.md §6's force-override mechanism is visible even from the log content itself, not just inferred from source.
