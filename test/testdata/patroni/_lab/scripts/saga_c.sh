#!/bin/bash
# Run C: Patroni 3.3.11, text logs, async replication, full saga
set -uo pipefail
RUN="c"
source "$HOME/ptlab/scripts/common.sh"

mkdir -p "$HOME/ptlab/runs/${RUN}"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_events.log"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_snapshots.log"

export SYNC_MODE=false
export LOG_TYPE=plain

wait_leader_change() {
  local old="$1" timeout="${2:-90}" waited=0 cur
  while [ $waited -lt $timeout ]; do
    cur=$(current_leader "$RUN")
    if [ -n "$cur" ] && [ "$cur" != "$old" ]; then echo "$cur"; return 0; fi
    sleep 3; waited=$((waited+3))
  done
  echo ""; return 1
}

log_event "$RUN" "=== RUN C START (Patroni 3.3.11, pg17, text logs, async) ==="

step "$RUN" "1. bootstrap: create networks, etcd cluster, 3 patroni nodes"
setup_networks "$RUN"
start_etcd_cluster "$RUN"
start_pg_node "$RUN" pg1 "$PTLAB_IMG_33"
start_pg_node "$RUN" pg2 "$PTLAB_IMG_33"
start_pg_node "$RUN" pg3 "$PTLAB_IMG_33"
settle 45
ensure_test_table "$RUN" pg1
snapshot_list "$RUN" "after bootstrap"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "bootstrap complete, leader=$LEADER"

step "$RUN" "2. planned switchover ${LEADER} -> other node"
CAND=$(current_replicas "$RUN" | head -1)
log_event "$RUN" "cmd: patronictl switchover --leader $LEADER --candidate $CAND --force"
pctl "$RUN" switchover --leader "$LEADER" --candidate "$CAND" --force
settle 75
snapshot_list "$RUN" "after switchover"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "post-switchover leader=$LEADER"

step "$RUN" "3. pause then resume"
log_event "$RUN" "cmd: patronictl pause main"
pctl "$RUN" pause main
settle 20
log_event "$RUN" "cmd: patronictl resume main"
pctl "$RUN" resume main
settle 60
snapshot_list "$RUN" "after pause/resume"

step "$RUN" "4. failover by leader death (SIGKILL on leader, after a burst write)"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "current leader before kill: $LEADER"
burst_write "$RUN" "$LEADER" 30 "prekill"
log_event "$RUN" "cmd: docker kill -s KILL ptlab-c-${LEADER}"
kill_node "$RUN" "$LEADER"
OLD_LEADER="$LEADER"
NEW_LEADER=$(wait_leader_change "$OLD_LEADER" 90)
log_event "$RUN" "new leader after kill: ${NEW_LEADER:-NONE}"
settle 20
snapshot_list "$RUN" "after failover kill of ${OLD_LEADER}"

step "$RUN" "5. old leader (${OLD_LEADER}) restarted, expect pg_rewind"
log_event "$RUN" "cmd: docker start ptlab-c-${OLD_LEADER}"
start_stopped_node "$RUN" "$OLD_LEADER"
settle 90
snapshot_list "$RUN" "after old leader restart / rewind window"

step "$RUN" "6. reinit of a replica"
REPL=$(current_replicas "$RUN" | head -1)
log_event "$RUN" "cmd: patronictl reinit main $REPL --force"
pctl "$RUN" reinit main "$REPL" --force
settle 75
snapshot_list "$RUN" "after reinit of ${REPL}"

step "$RUN" "7. leader loses DCS lock (isolate leader from etcd network > ttl)"
LEADER=$(current_leader "$RUN")
log_event "$RUN" "cmd: docker network disconnect ptlab-c-dcs ptlab-c-${LEADER}"
isolate_from_dcs "$RUN" "$LEADER"
ISO_LEADER="$LEADER"
settle 40
NEW_LEADER2=$(wait_leader_change "$ISO_LEADER" 60)
log_event "$RUN" "new leader after DCS isolation of ${ISO_LEADER}: ${NEW_LEADER2:-NONE}"
log_event "$RUN" "cmd: docker network connect --alias ${ISO_LEADER} ptlab-c-dcs ptlab-c-${ISO_LEADER}"
rejoin_dcs "$RUN" "$ISO_LEADER"
settle 60
snapshot_list "$RUN" "after DCS isolation episode"

step "$RUN" "8. DCS totally unreachable (stop all etcd ~60s)"
log_event "$RUN" "cmd: docker stop ptlab-c-etcd1 ptlab-c-etcd2 ptlab-c-etcd3"
stop_etcd_cluster "$RUN"
settle 65
log_event "$RUN" "cmd: docker start ptlab-c-etcd1 ptlab-c-etcd2 ptlab-c-etcd3"
docker start ptlab-c-etcd1 ptlab-c-etcd2 ptlab-c-etcd3 >/dev/null
settle 90
snapshot_list "$RUN" "after DCS outage/recovery"

step "$RUN" "9. lag blocking failover"
LEADER=$(current_leader "$RUN")
REPLS=$(current_replicas "$RUN")
log_event "$RUN" "leader=$LEADER replicas=[$REPLS]"
for r in $REPLS; do
  log_event "$RUN" "cmd: SELECT pg_wal_replay_pause(); on $r"
  pause_replay "$RUN" "$r"
done
log_event "$RUN" "cmd: bulk insert ~3MB WAL on $LEADER"
generate_wal_mb "$RUN" "$LEADER" 3
log_event "$RUN" "cmd: docker kill -s KILL ptlab-c-${LEADER}"
kill_node "$RUN" "$LEADER"
OLD_LEADER2="$LEADER"
settle 45
snapshot_list "$RUN" "during lag-blocked failover attempt (replicas paused)"
for r in $REPLS; do
  log_event "$RUN" "cmd: SELECT pg_wal_replay_resume(); on $r"
  resume_replay "$RUN" "$r" || true
done
NEW_LEADER3=$(wait_leader_change "$OLD_LEADER2" 90)
log_event "$RUN" "leader after lag resolved: ${NEW_LEADER3:-NONE}"
log_event "$RUN" "cmd: docker start ptlab-c-${OLD_LEADER2} (bring old leader back)"
start_stopped_node "$RUN" "$OLD_LEADER2"
settle 90
snapshot_list "$RUN" "after lag scenario recovery"

step "$RUN" "10. final steady state"
settle 150
snapshot_list "$RUN" "final steady state"
log_event "$RUN" "cmd: patronictl history"
pctl "$RUN" history | tee -a "$HOME/ptlab/runs/${RUN}/MANIFEST_history.log"
log_event "$RUN" "=== RUN C END ==="
echo "RUN C DONE"
