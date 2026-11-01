#!/bin/bash
# Run E: Patroni 4.1.5, text logs, async - dedicated "lag blocks failover" scenario.
# Unlike run A step 9 (which used pg_wal_replay_pause() and did NOT block failover,
# because Patroni measures lag from *received* WAL, not replayed WAL), this run
# genuinely cuts the replication TCP stream from both replicas to the leader
# (iptables DROP on port 5432, targeted at the leader's IP, inside the replica
# containers) while leaving DCS (etcd) and REST API traffic untouched. This makes
# the replicas' Receive LSN itself stall, which is what Patroni's
# maximum_lag_on_failover check actually looks at.
set -uo pipefail
RUN="e"
source "$HOME/ptlab/scripts/common.sh"

mkdir -p "$HOME/ptlab/runs/${RUN}"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_events.log"
: > "$HOME/ptlab/runs/${RUN}/MANIFEST_snapshots.log"

export SYNC_MODE=false
export LOG_TYPE=plain
export NEED_NET_ADMIN=true

wait_leader_change() {
  local old="$1" timeout="${2:-90}" waited=0 cur
  while [ $waited -lt $timeout ]; do
    cur=$(current_leader "$RUN")
    if [ -n "$cur" ] && [ "$cur" != "$old" ]; then echo "$cur"; return 0; fi
    sleep 3; waited=$((waited+3))
  done
  echo ""; return 1
}

log_event "$RUN" "=== RUN E START (Patroni 4.1.5, pg17, text logs, async, lag-blocks-failover) ==="

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
REPLICAS=$(current_replicas "$RUN")
log_event "$RUN" "bootstrap complete, leader=$LEADER replicas=[$REPLICAS]"

step "$RUN" "2. cut replication stream from both replicas to the leader (iptables, keep DCS up)"
LEADER_IPS=$(container_ips "$RUN" "$LEADER")
log_event "$RUN" "leader ${LEADER} IPs: ${LEADER_IPS}"
log_event "$RUN" "cmd: for each replica, iptables -A OUTPUT -p tcp -d <leader_ip> --dport 5432 -j DROP"
cut_replication "$RUN" "$LEADER" $REPLICAS
settle 5
snapshot_list "$RUN" "immediately after cutting replication"

step "$RUN" "3. generate well over maximum_lag_on_failover (1MB) of WAL on the leader"
log_event "$RUN" "cmd: bulk insert ~30MB of WAL on ${LEADER}"
generate_wal_mb "$RUN" "$LEADER" 30
settle 15
snapshot_list "$RUN" "after WAL generation, before kill (replicas should show stale/frozen Receive LSN)"

step "$RUN" "4. kill the leader, then wait several loop_waits to observe refusal"
OLD_LEADER="$LEADER"
log_event "$RUN" "cmd: docker kill -s KILL ptlab-e-${OLD_LEADER}"
kill_node "$RUN" "$OLD_LEADER"
settle 50
snapshot_list "$RUN" "during blocked-failover window (leader dead, replicas still cut off)"
STILL_LEADER=$(current_leader "$RUN")
if [ -z "$STILL_LEADER" ]; then
  log_event "$RUN" "OBSERVED: failover is blocked - no member has taken the leader lock"
else
  log_event "$RUN" "OBSERVED: current leader reported as '${STILL_LEADER}' (failover was NOT blocked)"
fi

step "$RUN" "5. restore replication and bring the old leader back"
log_event "$RUN" "cmd: for each replica, iptables -D OUTPUT -p tcp -d <leader_ip> --dport 5432 -j DROP"
restore_replication "$RUN" "$OLD_LEADER" $REPLICAS
log_event "$RUN" "cmd: docker start ptlab-e-${OLD_LEADER}"
start_stopped_node "$RUN" "$OLD_LEADER"
settle 90
snapshot_list "$RUN" "after restoring replication / restarting old leader"
RECOVERED_LEADER=$(current_leader "$RUN")
log_event "$RUN" "leader after recovery: ${RECOVERED_LEADER:-NONE}"

step "$RUN" "6. final steady state"
settle 90
snapshot_list "$RUN" "final steady state"
log_event "$RUN" "cmd: patronictl history"
pctl "$RUN" history | tee -a "$HOME/ptlab/runs/${RUN}/MANIFEST_history.log"
log_event "$RUN" "=== RUN E END ==="
echo "RUN E DONE"
