# pgBouncer test material -- lab scripts

These scripts were used to capture real pgbouncer + PostgreSQL logs on a lab
machine (`dalibo-pc`, Debian 13, rootless podman). Nothing under
`test/testdata/pgbouncer/` was hand-written or hand-edited: every log line
comes from an actual pgbouncer/PostgreSQL process, driven by real `pgbench`
traffic and real admin-console commands.

## Requirements on the lab machine

- rootless podman (tested with 5.4.2)
- pgbouncer 1.25.x from apt.postgresql.org (`/usr/sbin/pgbouncer`)
- PostgreSQL 17 client tools (`postgresql-client-17`, giving
  `/usr/lib/postgresql/17/bin/{psql,pgbench}`) -- used on the host to drive
  traffic against the pooler; the server itself runs in a
  `docker.io/library/postgres:17` container.
- For run P3 only: a pgbouncer 1.18.0 build, here expected at
  `~/pgb-src/old-install/bin/pgbouncer`.
- Passwordless `sudo` is not required; only rootless `podman` (including
  `podman unshare`, used to fix bind-mount ownership for the container's
  postgres user) is needed.

## Scripts

- `run_p1.sh` -- Run P1: one pooler, full story (start, RELOAD, saturation,
  PAUSE/RESUME, auth failures, PostgreSQL restart, pgbouncer restart,
  client_idle_timeout, final traffic). ~9-10 minutes real time.
- `run_p2.sh` -- Run P2: two poolers (`pgb-a` session mode :6432, `pgb-b`
  transaction mode :6433) in front of one PostgreSQL, with a mid-run log
  rotation on `pgb-a` and a saturation episode on `pgb-b` only. ~7 minutes.
- `run_p3.sh` -- Run P3: pgbouncer 1.18.0, a short saturation episode so the
  `wait` column carries its pre-1.23 meaning. ~4 minutes.
- `collect_p1.sh` -- fixes bind-mount ownership (`podman unshare chown -R 0:0
  ...` -- note: `0:0`, not the host uid/gid, because inside the `podman
  unshare` namespace uid 0 maps back to the real host user) and copies the
  pgbouncer log, PostgreSQL log, ini and timeline out of `~/pgb-lab/<run>/`
  into a `collected/` subdirectory ready to `scp` back.

Each script is self-contained: it writes its own `pgbouncer.ini`/`userlist.txt`,
starts a dedicated podman container (`quellog-pg-p1`/`p2`/`p3`), initializes a
pgbench dataset, runs the whole scenario with `date -u +%FT%T.%3NZ`-stamped
markers in `~/pgb-lab/<run>/TIMELINE.log`, takes `SHOW STATS`/`SHOW POOLS`/
`SHOW CLIENTS` snapshots into `~/pgb-lab/<run>/MANIFEST_snapshots.log` at a
few key moments, and tears itself down (pgbouncer `SHUTDOWN`, `podman stop`)
at the end. It does not remove the container or the podman image.

## How to replay

```sh
scp run_p1.sh run_p2.sh run_p3.sh dalibo-pc:~/
ssh dalibo-pc 'nohup bash ~/run_p1.sh > ~/run_p1.out 2>&1 & disown'
# wait for "=== RUN P1 DONE ===" in ~/pgb-lab/p1/TIMELINE.log, then:
ssh dalibo-pc 'bash ~/collect_p1.sh'
scp dalibo-pc:~/pgb-lab/p1/collected/p1.pgbouncer.log        P1/
scp dalibo-pc:~/pgb-lab/p1/collected/p1.postgresql.log       P1/
scp dalibo-pc:~/pgb-lab/p1/collected/p1.pgbouncer.ini.raw    P1/p1.pgbouncer.ini
scp dalibo-pc:~/pgb-lab/p1/collected/MANIFEST_snapshots.log  P1/
scp dalibo-pc:~/pgb-lab/p1/collected/TIMELINE.log            P1/MANIFEST_timeline.log
```

Same pattern for P2 (`pgb-a`/`pgb-b` logs, both ini files) and P3.

Only P1 has a dedicated `collect_p1.sh`; P2/P3 artifacts were pulled directly
with `scp` from `~/pgb-lab/p2/` and `~/pgb-lab/p3/` since PostgreSQL's log
file was the only one needing the `podman unshare chown` ownership fix.

## Cleanup

```sh
ssh dalibo-pc '
  pkill -f "pgbouncer -d /home/alain/pgb-lab" 2>/dev/null
  podman rm -f quellog-pg-p1 quellog-pg-p2 quellog-pg-p3
  podman unshare rm -rf ~/pgb-lab
'
```

This removes only the containers and data created by these scripts; no other
container or image on the lab machine is touched. The pgbouncer/postgresql
packages installed via apt are left in place.

## Known pitfalls (see each run's MANIFEST.md for the full story)

- The `postgres:17` image bootstraps its initial role's password before a
  custom `-c password_encryption=md5` takes effect for that step, so the
  role's password must be re-set with `ALTER ROLE ... WITH PASSWORD ...`
  once the server is up, or pgbouncer's md5 auth fails with `cannot do SCRAM
  authentication: wrong password type`.
- `podman restart` on a rootless container can fail with a pasta port-rebind
  race (`Failed to bind port ... Address already in use`). Use `podman stop`
  + a short `sleep` + `podman start` instead. `pg_ctl restart` executed via
  `podman exec` does not work either: postgres is PID 1 in this image, so
  stopping it exits the whole container before the start phase can run.
- Bind-mounted volumes for the container's `postgres` user (uid 999 inside)
  need `podman unshare chown -R 999:999 <dir>` before the container starts,
  and `podman unshare chown -R 0:0 <dir>` (not the host uid/gid) to hand
  them back to the real host user afterwards.
- pgbouncer's own `TZ` is whatever the host process inherits; run it with
  `env TZ=UTC pgbouncer ...` to get UTC timestamps in the log, matching the
  PostgreSQL container (`-e TZ=UTC` there).
