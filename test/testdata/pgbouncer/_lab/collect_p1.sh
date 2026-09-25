#!/bin/bash
set -uo pipefail
WORKDIR=~/pgb-lab/p1
OUT=~/pgb-lab/p1/collected
mkdir -p "$OUT"
podman unshare chown -R 0:0 "$WORKDIR/pglogs"
cp "$WORKDIR/p1.pgbouncer.log" "$OUT/p1.pgbouncer.log"
cp "$WORKDIR/pglogs/postgresql.log" "$OUT/p1.postgresql.log"
cp "$WORKDIR/conf/pgbouncer.ini" "$OUT/p1.pgbouncer.ini.raw"
cp "$WORKDIR/TIMELINE.log" "$OUT/TIMELINE.log"
cp "$WORKDIR/MANIFEST_snapshots.log" "$OUT/MANIFEST_snapshots.log"
cp "$WORKDIR/bg_traffic.log" "$OUT/" 2>/dev/null
cp "$WORKDIR/saturation_pgbench.log" "$OUT/" 2>/dev/null
cp "$WORKDIR/auth_attempts.log" "$OUT/" 2>/dev/null
cp "$WORKDIR/idle_client.log" "$OUT/" 2>/dev/null
echo "== line counts =="
wc -l "$OUT"/*.log
