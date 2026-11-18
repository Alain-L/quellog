#!/bin/bash
# Run P3 -- pgbouncer 1.18.0 (old install), short saturation episode.
set -uo pipefail

WORKDIR=~/pgb-lab/p3
PGUSER=bench
PGPASSWORD=benchpw123
PGDB=bench
PGPORT_CONTAINER=15434
PGBPORT=6434
CONTAINER=quellog-pg-p3
PGBBIN=~/pgb-src/old-install/bin/pgbouncer

TIMELINE="$WORKDIR/TIMELINE.log"
SNAPSHOTS="$WORKDIR/MANIFEST_snapshots.log"
PGBLOG="$WORKDIR/p3-old.pgbouncer.log"
INI="$WORKDIR/conf/pgbouncer.ini"

ts() { date -u +%FT%T.%3NZ; }
note() { echo "[$(ts)] $*" | tee -a "$TIMELINE"; }
admin() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "$1" 2>&1 | tee -a "$TIMELINE"; }
snap() {
  echo "=== $(ts) : $1 ===" >> "$SNAPSHOTS"
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "SHOW STATS;" >> "$SNAPSHOTS" 2>&1
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "SHOW POOLS;" >> "$SNAPSHOTS" 2>&1
}
set_ini() { sed -i "s/^$1 = .*/$1 = $2/" "$INI"; }
bench_direct() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER "$@" $PGDB; }
bench_pooled() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $PGBPORT -U $PGUSER "$@" $PGDB; }

BGPID=""
start_bg_traffic() {
  ( while true; do
      bench_pooled -c 3 -j 1 -T 10 >/dev/null 2>>"$WORKDIR/bg_traffic.log"
      sleep 2
    done ) &
  BGPID=$!
  note "background light traffic loop started (pid $BGPID)"
}
stop_bg_traffic() {
  if [ -n "$BGPID" ]; then
    pkill -P "$BGPID" 2>/dev/null
    kill "$BGPID" 2>/dev/null
    wait "$BGPID" 2>/dev/null
    note "background light traffic loop stopped"
    BGPID=""
  fi
}

mkdir -p "$WORKDIR"/{pgdata,pglogs,conf}
: > "$TIMELINE"
: > "$SNAPSHOTS"
note "=== RUN P3 SETUP START ==="

podman unshare chown -R 999:999 "$WORKDIR/pgdata" "$WORKDIR/pglogs"

HASH=$(printf '%s' "${PGPASSWORD}${PGUSER}" | md5sum | awk '{print $1}')
printf '"%s" "md5%s"\n' "$PGUSER" "$HASH" > "$WORKDIR/userlist.txt"

cat > "$INI" <<EOF
[databases]
bench = host=127.0.0.1 port=$PGPORT_CONTAINER dbname=$PGDB

[pgbouncer]
listen_addr = 127.0.0.1
listen_port = $PGBPORT
unix_socket_dir =
auth_type = md5
auth_file = $WORKDIR/userlist.txt
logfile = $PGBLOG
pidfile = $WORKDIR/pgbouncer.pid
admin_users = $PGUSER
stats_users = $PGUSER
pool_mode = transaction
default_pool_size = 10
reserve_pool_size = 0
reserve_pool_timeout = 5
client_idle_timeout = 0
query_wait_timeout = 120
max_client_conn = 200
stats_period = 10
log_connections = 1
log_disconnections = 1
log_pooler_errors = 1
EOF

podman rm -f $CONTAINER >/dev/null 2>&1

note "starting postgres container ($CONTAINER)"
podman run -d --name $CONTAINER \
  -e POSTGRES_USER=$PGUSER -e POSTGRES_PASSWORD=$PGPASSWORD -e POSTGRES_DB=$PGDB \
  -e TZ=UTC \
  -p 127.0.0.1:$PGPORT_CONTAINER:5432 \
  -v "$WORKDIR/pgdata:/var/lib/postgresql/data" \
  -v "$WORKDIR/pglogs:/var/log/postgresql" \
  docker.io/library/postgres:17 \
  postgres \
  -c timezone=UTC -c log_timezone=UTC \
  -c password_encryption=md5 \
  -c logging_collector=on -c log_destination=stderr \
  -c log_directory=/var/log/postgresql -c log_filename=postgresql.log \
  -c "log_line_prefix=%m [%p] %q%u@%d " \
  -c log_connections=on -c log_disconnections=on -c log_checkpoints=on \
  -c log_min_duration_statement=1000 >> "$TIMELINE" 2>&1

for i in $(seq 1 30); do
  if PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER -d $PGDB -c 'select 1' >/dev/null 2>&1; then
    note "postgres ready after ${i}s"
    break
  fi
  sleep 1
done

PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER -d $PGDB -c "ALTER ROLE $PGUSER WITH PASSWORD '$PGPASSWORD';" >> "$TIMELINE" 2>&1

bench_direct -i -s 3 --quiet >> "$TIMELINE" 2>&1

cat > "$WORKDIR/sleep.sql" <<'EOF'
\set aid random(1, 100000 * :scale)
BEGIN;
SELECT abalance FROM pgbench_accounts WHERE aid = :aid;
SELECT pg_sleep(0.5);
COMMIT;
EOF

pkill -f "old-install/bin/pgbouncer -d $INI" 2>/dev/null
sleep 1
env TZ=UTC "$PGBBIN" -d "$INI"
sleep 1
note "pgbouncer 1.18.0 started (pid $(cat "$WORKDIR/pgbouncer.pid" 2>/dev/null))"

note "=== RUN P3 SETUP DONE ==="

### Baseline
note "STEP1 BEFORE -- baseline light traffic"
start_bg_traffic
sleep 45
snap "step1 baseline"
note "STEP1 AFTER"
sleep 15

### Saturation
note "STEP2 BEFORE -- shrink pool, saturate with 40 clients + pg_sleep(0.5) for 40s"
set_ini default_pool_size 2
set_ini reserve_pool_size 2
set_ini reserve_pool_timeout 2
set_ini query_wait_timeout 3
admin "RELOAD;"
snap "step2 just after RELOAD (pool shrunk)"
bench_pooled -f "$WORKDIR/sleep.sql" -c 40 -j 8 -T 40 > "$WORKDIR/saturation_pgbench.log" 2>&1
snap "step2 right after saturation load"
set_ini default_pool_size 10
set_ini reserve_pool_size 0
set_ini reserve_pool_timeout 5
set_ini query_wait_timeout 120
admin "RELOAD;"
note "STEP2 AFTER"
sleep 15

### Final traffic
note "STEP3 BEFORE -- final normal traffic"
sleep 45
snap "step3 final"
note "STEP3 AFTER"

### Teardown
stop_bg_traffic
note "=== RUN P3 TEARDOWN ==="
admin "SHUTDOWN;"
sleep 1
podman stop $CONTAINER >> "$TIMELINE" 2>&1
note "=== RUN P3 DONE ==="
