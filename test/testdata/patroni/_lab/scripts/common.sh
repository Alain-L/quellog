#!/bin/bash
# ptlab common functions - sourced by run scripts
set -uo pipefail

PTLAB_IMG_41="localhost/ptlab-patroni:4.1.5-pg17"
PTLAB_IMG_33="localhost/ptlab-patroni:3.3.11-pg17"
ETCD_IMG="quay.io/coreos/etcd:v3.5.17"
PG_BINDIR="/usr/lib/postgresql/17/bin"

# $1 = run id (lowercase, e.g. a, b, c, d)
run_prefix() { echo "ptlab-$1"; }

log_event() {
  # $1=run  $2=message
  local run="$1"; shift
  local ts
  ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  echo "${ts} $*" | tee -a "$HOME/ptlab/runs/${run}/MANIFEST_events.log"
}

setup_networks() {
  local run="$1"
  local p; p=$(run_prefix "$run")
  docker network create "${p}-dcs" >/dev/null
  docker network create "${p}-repl" >/dev/null
}

teardown_networks() {
  local run="$1"
  local p; p=$(run_prefix "$run")
  docker network rm "${p}-dcs" "${p}-repl" >/dev/null 2>&1 || true
}

start_etcd_cluster() {
  local run="$1"
  local p; p=$(run_prefix "$run")
  local cluster="etcd1=http://etcd1:2380,etcd2=http://etcd2:2380,etcd3=http://etcd3:2380"
  for i in 1 2 3; do
    docker run -d --name "${p}-etcd${i}" --network "${p}-dcs" --network-alias "etcd${i}" \
      -e TZ=UTC \
      "$ETCD_IMG" etcd \
      --name "etcd${i}" \
      --data-dir /etcd-data \
      --initial-advertise-peer-urls "http://etcd${i}:2380" \
      --listen-peer-urls "http://0.0.0.0:2380" \
      --listen-client-urls "http://0.0.0.0:2379" \
      --advertise-client-urls "http://etcd${i}:2379" \
      --initial-cluster-token "${p}-etcd" \
      --initial-cluster "$cluster" \
      --initial-cluster-state new >/dev/null
  done
  # wait for cluster to be healthy
  local tries=0
  while [ $tries -lt 30 ]; do
    if docker exec "${p}-etcd1" etcdctl endpoint health --cluster >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
    tries=$((tries+1))
  done
  echo "WARNING: etcd cluster did not report healthy in time" >&2
  return 1
}

stop_etcd_cluster() {
  local run="$1"
  local p; p=$(run_prefix "$run")
  for i in 1 2 3; do docker stop -t 5 "${p}-etcd${i}" >/dev/null 2>&1; done
}

# generate patroni.yml for a node
# args: run node(pg1..) image sync(true/false) logtype(plain/json)
gen_patroni_conf() {
  local run="$1" node="$2" sync="$3" logtype="$4"
  local outdir="$HOME/ptlab/conf/${run}"
  mkdir -p "$outdir"
  local syncblock=""
  if [ "$sync" = "true" ]; then
    syncblock="    synchronous_mode: true"
  fi
  local logblock="  dir: /var/log/patroni
  level: INFO"
  if [ "$logtype" = "json" ]; then
    logblock="  dir: /var/log/patroni
  level: INFO
  type: json"
  fi
  cat > "${outdir}/${node}.yml" <<YAML
scope: main
namespace: /ptlab/
name: ${node}

restapi:
  listen: 0.0.0.0:8008
  connect_address: ${node}:8008

etcd3:
  hosts: etcd1:2379,etcd2:2379,etcd3:2379

bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 1048576
${syncblock}
    postgresql:
      use_pg_rewind: true
      use_slots: true
      parameters:
        wal_level: replica
        hot_standby: "on"
        max_wal_senders: 10
        max_replication_slots: 10
        wal_keep_size: "256MB"
        logging_collector: "on"
        log_destination: "stderr"
        log_directory: "/var/log/postgresql"
        log_line_prefix: "%m [%p] %q%u@%d "
        log_connections: "off"
        log_disconnections: "off"
        log_checkpoints: "on"

  initdb:
    - encoding: UTF8
    - data-checksums

  pg_hba:
    - host replication replicator 0.0.0.0/0 md5
    - host all all 0.0.0.0/0 md5

postgresql:
  listen: 0.0.0.0:5432
  connect_address: ${node}:5432
  data_dir: /home/postgres/data
  bin_dir: ${PG_BINDIR}
  pgpass: /home/postgres/.pgpass
  authentication:
    replication:
      username: replicator
      password: repl_pw
    superuser:
      username: postgres
      password: postgres_pw
    rewind:
      username: postgres
      password: postgres_pw
  parameters:
    unix_socket_directories: '/tmp'

log:
${logblock}

tags:
  nofailover: false
  noloadbalance: false
  clonefrom: false
  nosync: false
YAML
}

start_pg_node() {
  # args: run node image
  local run="$1" node="$2" image="$3"
  local p; p=$(run_prefix "$run")
  gen_patroni_conf "$run" "$node" "${SYNC_MODE:-false}" "${LOG_TYPE:-plain}"
  local capargs=()
  if [ "${NEED_NET_ADMIN:-false}" = "true" ]; then
    capargs=(--cap-add=NET_ADMIN)
  fi
  docker run -d --name "${p}-${node}" \
    --network "${p}-dcs" --network-alias "${node}" \
    "${capargs[@]}" \
    -e TZ=UTC \
    -v "$HOME/ptlab/conf/${run}/${node}.yml:/home/postgres/conf/patroni.yml:ro,Z" \
    "$image" >/dev/null
  docker network connect --alias "${node}" "${p}-repl" "${p}-${node}" >/dev/null
}

container_ips() {
  # all IPs of a container across its networks (space separated)
  local run="$1" node="$2"
  local p; p=$(run_prefix "$run")
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "${p}-${node}"
}

cut_replication() {
  # cut_replication run leader replica1 [replica2 ...]
  # drops outbound tcp:5432 from each replica towards every IP of the leader,
  # on every network the leader is attached to (requires replica containers
  # started with NEED_NET_ADMIN=true so iptables can run as root inside them).
  local run="$1" leader="$2"; shift 2
  local p; p=$(run_prefix "$run")
  local ips; ips=$(container_ips "$run" "$leader")
  for r in "$@"; do
    for ip in $ips; do
      docker exec -u root "${p}-${r}" iptables -A OUTPUT -p tcp -d "$ip" --dport 5432 -j DROP
    done
  done
}

restore_replication() {
  # restore_replication run leader replica1 [replica2 ...]
  local run="$1" leader="$2"; shift 2
  local p; p=$(run_prefix "$run")
  local ips; ips=$(container_ips "$run" "$leader")
  for r in "$@"; do
    for ip in $ips; do
      docker exec -u root "${p}-${r}" iptables -D OUTPUT -p tcp -d "$ip" --dport 5432 -j DROP 2>/dev/null || true
    done
  done
}

pctl() {
  # run patronictl inside pg1 (or any live node) of a run
  local run="$1"; shift
  local p; p=$(run_prefix "$run")
  local node="pg1"
  # find any running pg node
  for n in pg1 pg2 pg3; do
    if docker ps --format '{{.Names}}' | grep -q "^${p}-${n}$"; then node="$n"; fi
  done
  docker exec "${p}-${node}" patronictl -c /home/postgres/conf/patroni.yml "$@"
}

pctl_on() {
  # force which node runs patronictl
  local run="$1" node="$2"; shift 2
  local p; p=$(run_prefix "$run")
  docker exec "${p}-${node}" patronictl -c /home/postgres/conf/patroni.yml "$@"
}

psql_on() {
  local run="$1" node="$2"; shift 2
  local p; p=$(run_prefix "$run")
  docker exec -u postgres "${p}-${node}" psql -h /tmp -U postgres -d postgres "$@"
}

snapshot_list() {
  local run="$1" label="$2"
  {
    echo "=== patronictl list @ $(date -u +"%Y-%m-%dT%H:%M:%SZ") ($label) ==="
    pctl "$run" list
    echo
  } >> "$HOME/ptlab/runs/${run}/MANIFEST_snapshots.log"
}

current_leader() {
  local run="$1"
  pctl "$run" list -f json 2>/dev/null | jq -r '.[] | select(.Role=="Leader") | .Member'
}

current_replicas() {
  local run="$1"
  pctl "$run" list -f json 2>/dev/null | jq -r '.[] | select(.Role!="Leader") | .Member'
}

ensure_test_table() {
  local run="$1" node="$2"
  psql_on "$run" "$node" -c "CREATE TABLE IF NOT EXISTS public.ptlab_test (id serial primary key, note text, created_at timestamptz default now());" >/dev/null
}

burst_write() {
  # write N rows quickly on given node, no wait for replication
  local run="$1" node="$2" n="${3:-20}" note="${4:-burst}"
  psql_on "$run" "$node" -c "INSERT INTO public.ptlab_test(note) SELECT '${note}-'||g FROM generate_series(1,${n}) g;" >/dev/null
}

generate_wal_mb() {
  # generate roughly N MB of WAL via a bulk insert with padding
  local run="$1" node="$2" mb="${3:-3}"
  local rows=$((mb * 1000))
  psql_on "$run" "$node" -c "INSERT INTO public.ptlab_test(note) SELECT repeat('x',1000) FROM generate_series(1,${rows});" >/dev/null
}

isolate_from_dcs() {
  local run="$1" node="$2"
  local p; p=$(run_prefix "$run")
  docker network disconnect "${p}-dcs" "${p}-${node}"
}

rejoin_dcs() {
  local run="$1" node="$2"
  local p; p=$(run_prefix "$run")
  docker network connect --alias "${node}" "${p}-dcs" "${p}-${node}"
}

kill_node() {
  local run="$1" node="$2"
  local p; p=$(run_prefix "$run")
  docker kill -s KILL "${p}-${node}"
}

start_stopped_node() {
  local run="$1" node="$2"
  local p; p=$(run_prefix "$run")
  docker start "${p}-${node}"
}

pause_replay() {
  local run="$1" node="$2"
  psql_on "$run" "$node" -c "SELECT pg_wal_replay_pause();" >/dev/null
}

resume_replay() {
  local run="$1" node="$2"
  psql_on "$run" "$node" -c "SELECT pg_wal_replay_resume();" >/dev/null
}

step() {
  # step run "description"
  local run="$1" desc="$2"
  log_event "$run" "STEP: ${desc}"
}

settle() {
  local secs="$1"
  sleep "$secs"
}
