#!/bin/bash
# Run D: Patroni 4.1.5, JSON logs, async replication, subset saga
set -uo pipefail
RUN="d"
source "$HOME/ptlab/scripts/common.sh"

mkdir -p "$HOME/ptlab/runs/${RUN}"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_events.log"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_snapshots.log"

export SYNC_MODE=false
export LOG_TYPE=json

wait_leader_change() {
  local old="$1" timeout="${2:-90}" waited=0 cur
  while [ $waited -lt $timeout ]; do
    cur=$(current_leader "$RUN")
    if [ -n "$cur" ] && [ "$cur" != "$old" ]; then echo "$cur"; return 0; fi
    sleep 3; waited=$((waited+3))
  done
  echo ""; return 1
}

log_event "$RUN" "=== RUN D START (Patroni 4.1.5, pg17, JSON logs, async) ==="

step "$RUN" "1. bootstrap"
setup_networks "$RUN"
start_etcd_cluster "$RUN"
start_pg_node "$RUN" pg1 "$PTLAB_IMG_41"
start_pg_node "$RUN" pg2 "$PTLAB_IMG_41"
start_pg_node "$RUN" pg3 "$PTLAB_IMG_41"
settle 45
ensure_test_table "$RUN" pg1
snapshot_list "$RUN" "after bootstrap"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "bootstrap complete, leader=$LEADER"

step "$RUN" "2. planned switchover"
CAND=$(current_replicas "$RUN" | head -1)
log_event "$RUN" "cmd: patronictl switchover --leader $LEADER --candidate $CAND --force"
pctl "$RUN" switchover --leader "$LEADER" --candidate "$CAND" --force
settle 75
snapshot_list "$RUN" "after switchover"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "post-switchover leader=$LEADER"

step "$RUN" "3. failover by leader kill"
burst_write "$RUN" "$LEADER" 30 "prekill"
OLD_LEADER="$LEADER"
log_event "$RUN" "cmd: docker kill -s KILL ptlab-d-${LEADER}"
kill_node "$RUN" "$LEADER"
NEW_LEADER=$(wait_leader_change "$OLD_LEADER" 90)
log_event "$RUN" "new leader after kill: ${NEW_LEADER:-NONE}"
settle 30
snapshot_list "$RUN" "after failover kill"
log_event "$RUN" "cmd: docker start ptlab-d-${OLD_LEADER}"
start_stopped_node "$RUN" "$OLD_LEADER"
settle 75
snapshot_list "$RUN" "after old leader rejoined"

step "$RUN" "4. leader loses DCS lock (isolate from etcd > ttl)"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "cmd: docker network disconnect ptlab-d-dcs ptlab-d-${LEADER}"
isolate_from_dcs "$RUN" "$LEADER"
ISO_LEADER="$LEADER"
settle 40
NEW_LEADER2=$(wait_leader_change "$ISO_LEADER" 60)
log_event "$RUN" "new leader after DCS isolation of ${ISO_LEADER}: ${NEW_LEADER2:-NONE}"
log_event "$RUN" "cmd: docker network connect --alias ${ISO_LEADER} ptlab-d-dcs ptlab-d-${ISO_LEADER}"
rejoin_dcs "$RUN" "$ISO_LEADER"
settle 60
snapshot_list "$RUN" "after DCS isolation episode"

step "$RUN" "5. final steady state"
settle 120
snapshot_list "$RUN" "final steady state"
log_event "$RUN" "cmd: patronictl history"
pctl "$RUN" history | tee -a "$HOME/ptlab/runs/${RUN}/MANIFEST_history.log"
log_event "$RUN" "=== RUN D END ==="
echo "RUN D DONE"
