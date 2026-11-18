#!/bin/bash
# collect_run.sh <run> <outdir>
# Pulls patroni.log and concatenated postgresql logs for pg1/pg2/pg3 of a run
# into local files under outdir (run from dalibo-pc).
set -uo pipefail
RUN="$1"
OUT="$2"
mkdir -p "$OUT"
P="ptlab-${RUN}"

for n in pg1 pg2 pg3; do
  if docker ps -a --format '{{.Names}}' | grep -q "^${P}-${n}$"; then
    docker exec "${P}-${n}" sh -c "cat /var/log/patroni/patroni.log 2>/dev/null" > "${OUT}/${n}.patroni.log" || true
    docker exec "${P}-${n}" sh -c "ls -1 /var/log/postgresql/*.log 2>/dev/null | sort | xargs -r cat" > "${OUT}/${n}.postgresql.log" || true
    docker exec "${P}-${n}" sh -c "ls -1 /var/log/postgresql/*.log 2>/dev/null | sort" > "${OUT}/${n}.postgresql.filelist.txt" || true
  fi
done

cp "$HOME/ptlab/runs/${RUN}/MANIFEST_events.log" "${OUT}/" 2>/dev/null || true
cp "$HOME/ptlab/runs/${RUN}/MANIFEST_snapshots.log" "${OUT}/" 2>/dev/null || true
cp "$HOME/ptlab/runs/${RUN}/MANIFEST_history.log" "${OUT}/" 2>/dev/null || true

echo "collected $RUN -> $OUT"
ls -la "$OUT"
