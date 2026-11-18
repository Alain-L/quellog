# pgBackRest fixtures

`S1`..`S5` are curated excerpts of a real lab run on a lab machine (podman
containers, real pgBackRest 2.59.3 — and 2.48 for one incident — against
real PostgreSQL 16/17 instances), not synthetic text. Each `SN/MANIFEST.md`
is kept verbatim: it is the ground truth these fixtures are confronted
against (step-by-step actions, expected vs. observed, raw grep/awk counts,
"Surprises" the lab turned up that contradicted the task's own working
assumptions). Nothing was stripped except noise: `startup*.log`, `build.log`,
`MANIFEST_timeline.log`, command transcripts (`*.out`/`*.txt`) and the `_lab/`
scripts are not copied here — only the `.log` files quellog actually parses,
plus each `MANIFEST.md`.

- `S1` — nominal life of a stanza: `stanza-create`, 3 full + 1 diff + 1 incr
  backup, `expire --repo1-retention-full=2`, `verify`, a backup attempted
  while PostgreSQL was down, two restores (plain and PITR).
- `S2` — WAL-archiving incidents: a real repo-permission outage (`chmod
  000`), a wrong-stanza `archive_command`, an `archive-timeout` too low
  during `backup`, `archive-async` with a tiny `archive-push-queue-max`
  (silent WAL drop), and a repo outage while the async worker is running.
  The saga that pinned every fix below.
- `S3` — backup-command incidents: `LockAcquireError [050]` (backup vs
  backup, backup vs expire), an offline backup attempted against a live
  PostgreSQL, a wrong `pg1-path`, a PG16→PG17 `stanza-upgrade`, a page
  checksum corruption, a killed backup (twice — killing only the top-level
  pid does not stop it), a post-promotion timeline switch, a real
  `StopError [062]` via `pgbackrest stop`.
- `S4` — logging settings that hide or reveal information:
  `log-level-file=warn` (a successful backup leaves no begin/end at all),
  `log-timestamp=n`, `log-level-stderr` off vs warn (the single most
  important finding: `off` does *not* silence pgBackRest in the PostgreSQL
  log by itself — stdout still carries it unless `log-level-console` is
  also lowered), `process-max=4` with per-file `DETAIL` lines, 2.48 vs
  2.59.3 default routing.
- `S5` — two-repo pgBackRest, one repo fails: a broken `repo2` aborts
  `archive-push` as a whole (`[104]`), not just for that repo, and that
  stall cascades into `backup --repo=1` too — the opposite of the "degrades
  gracefully" assumption.
- `S6` — archive-push logged twice (local Docker, `postgres:17`, pgBackRest
  2.59.3): `archive_command` passes `--log-level-file=info`, so each run is
  in `main-archive-push.log` and in the PostgreSQL log; read together they
  must count once. Its `_lab/lab.sh` is kept: it replays the whole run.

`../regressions/pgbackrest/postgresql-pgbackrest.log` is the one synthetic
fixture kept, for what the lab could not conveniently reproduce: a single
small PostgreSQL log, with a normal `SUMMARY` (so it rides the generic
`TestRegressionCorpus` json/md walk; `TestPgBackRest_Corpus` pins its text),
showing in one place a successful embedded console block, a failed bare
multi-line `ERROR:` stderr block, and PostgreSQL's own `archive command
failed`/`DETAIL` pairing — each real lab fixture that shows these (S2, S4)
either has no ordinary PostgreSQL SUMMARY content around it or splits them
across many tiny per-incident files. Every line in it is still copied
verbatim from a cited real sample (see the file's own history in
`docs/pgbackrest.md` and the SPEC's research material), not invented.

Golden files (`*.json.golden`, `*.md.golden`) are regenerated with:

```
go test ./test/ -run TestPgBackRest -update
```
