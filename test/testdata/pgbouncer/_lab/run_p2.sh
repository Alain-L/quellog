#!/bin/bash
# Run P2 -- two poolers (session mode pgb-a, transaction mode pgb-b) in front of one PostgreSQL.
set -uo pipefail

WORKDIR=~/pgb-lab/p2
PGUSER=bench
PGPASSWORD=benchpw123
PGDB=bench
PGPORT_CONTAINER=15433
CONTAINER=quellog-pg-p2

APORT=6432
BPORT=6433

TIMELINE="$WORKDIR/TIMELINE.log"
SNAPSHOTS="$WORKDIR/MANIFEST_snapshots.log"
AINI="$WORKDIR/conf/pgb-a.ini"
BINI="$WORKDIR/conf/pgb-b.ini"
ALOG="$WORKDIR/pgb-a.pgbouncer.log"
BLOG="$WORKDIR/pgb-b.pgbouncer.log"

ts() { date -u +%FT%T.%3NZ; }
note() { echo "[$(ts)] $*" | tee -a "$TIMELINE"; }
psqlA() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $APORT -U $PGUSER -d pgbouncer -c "$1" 2>&1 | tee -a "$TIMELINE"; }
psqlB() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $BPORT -U $PGUSER -d pgbouncer -c "$1" 2>&1 | tee -a "$TIMELINE"; }
snap() {
  echo "=== $(ts) : $1 ===" >> "$SNAPSHOTS"
  echo "--pgb-a--" >> "$SNAPSHOTS"
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $APORT -U $PGUSER -d pgbouncer -c "SHOW STATS;" >> "$SNAPSHOTS" 2>&1
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $APORT -U $PGUSER -d pgbouncer -c "SHOW POOLS;" >> "$SNAPSHOTS" 2>&1
  echo "--pgb-b--" >> "$SNAPSHOTS"
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $BPORT -U $PGUSER -d pgbouncer -c "SHOW STATS;" >> "$SNAPSHOTS" 2>&1
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $BPORT -U $PGUSER -d pgbouncer -c "SHOW POOLS;" >> "$SNAPSHOTS" 2>&1
}
set_ini_b() { sed -i "s/^$1 = .*/$1 = $2/" "$BINI"; }
bench_direct() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER "$@" $PGDB; }
bench_a() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $APORT -U $PGUSER "$@" $PGDB; }
bench_b() { PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $BPORT -U $PGUSER "$@" $PGDB; }

BGPID=""
start_bg_traffic_a() {
  ( while true; do
      bench_a -c 3 -j 1 -T 15 >/dev/null 2>>"$WORKDIR/bg_traffic_a.log"
      sleep 2
    done ) &
  BGPID=$!
  note "pgb-a steady light traffic loop started (pid $BGPID)"
}
stop_bg_traffic_a() {
  if [ -n "$BGPID" ]; then
    pkill -P "$BGPID" 2>/dev/null
    kill "$BGPID" 2>/dev/null
    wait "$BGPID" 2>/dev/null
    note "pgb-a traffic loop stopped"
    BGPID=""
  fi
}

mkdir -p "$WORKDIR"/{pgdata,pglogs,conf}
: > "$TIMELINE"
: > "$SNAPSHOTS"
note "=== RUN P2 SETUP START ==="

podman unshare chown -R 999:999 "$WORKDIR/pgdata" "$WORKDIR/pglogs"

HASH=$(printf '%s' "${PGPASSWORD}${PGUSER}" | md5sum | awk '{print $1}')
printf '"%s" "md5%s"\n' "$PGUSER" "$HASH" > "$WORKDIR/userlist.txt"

cat > "$AINI" <<EOF
[databases]
bench = host=127.0.0.1 port=$PGPORT_CONTAINER dbname=$PGDB

[pgbouncer]
listen_addr = 127.0.0.1
listen_port = $APORT
unix_socket_dir =
auth_type = md5
auth_file = $WORKDIR/userlist.txt
logfile = $ALOG
pidfile = $WORKDIR/pgb-a.pid
admin_users = $PGUSER
stats_users = $PGUSER
pool_mode = session
default_pool_size = 10
reserve_pool_size = 0
client_idle_timeout = 0
max_client_conn = 200
stats_period = 10
log_connections = 1
log_disconnections = 1
log_pooler_errors = 1
EOF

cat > "$BINI" <<EOF
[databases]
bench = host=127.0.0.1 port=$PGPORT_CONTAINER dbname=$PGDB

[pgbouncer]
listen_addr = 127.0.0.1
listen_port = $BPORT
unix_socket_dir =
auth_type = md5
auth_file = $WORKDIR/userlist.txt
logfile = $BLOG
pidfile = $WORKDIR/pgb-b.pid
admin_users = $PGUSER
stats_users = $PGUSER
pool_mode = transaction
default_pool_size = 10
reserve_pool_size = 0
reserve_pool_timeout = 5
query_wait_timeout = 120
client_idle_timeout = 0
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

bench_direct -i -s 5 --quiet >> "$TIMELINE" 2>&1

cat > "$WORKDIR/sleep.sql" <<'EOF'
\set aid random(1, 100000 * :scale)
BEGIN;
SELECT abalance FROM pgbench_accounts WHERE aid = :aid;
SELECT pg_sleep(0.5);
COMMIT;
EOF

pkill -f "pgbouncer -d $AINI" 2>/dev/null
pkill -f "pgbouncer -d $BINI" 2>/dev/null
sleep 1
env TZ=UTC /usr/sbin/pgbouncer -d "$AINI"
env TZ=UTC /usr/sbin/pgbouncer -d "$BINI"
sleep 1
note "pgb-a started (pid $(cat "$WORKDIR/pgb-a.pid" 2>/dev/null)), pgb-b started (pid $(cat "$WORKDIR/pgb-b.pid" 2>/dev/null))"

note "=== RUN P2 SETUP DONE ==="

### Step A -- baseline: only pgb-a steady light traffic
note "STEPA BEFORE -- pgb-a steady light traffic baseline"
start_bg_traffic_a
sleep 120
snap "stepA baseline (pgb-a only)"
note "STEPA AFTER"

### Step B -- rotate pgb-a log mid-run
note "STEPB BEFORE -- rotate pgb-a log: rename then kill -HUP"
APID=$(cat "$WORKDIR/pgb-a.pid")
ls -la "$ALOG" >> "$TIMELINE" 2>&1
mv "$ALOG" "${ALOG}.1"
kill -HUP "$APID"
sleep 2
ls -la "$ALOG" "${ALOG}.1" >> "$TIMELINE" 2>&1
note "STEPB AFTER -- pgb-a.pgbouncer.log.1 (old) + pgb-a.pgbouncer.log (new, reopened)"
sleep 60

### Step C -- saturation episode on pgb-b only
note "STEPC BEFORE -- saturate pgb-b (transaction mode) with 40 clients for ~40s"
set_ini_b default_pool_size 2
set_ini_b reserve_pool_size 2
set_ini_b reserve_pool_timeout 2
set_ini_b query_wait_timeout 3
psqlB "RELOAD;"
snap "stepC just after RELOAD (pgb-b pool shrunk)"
bench_b -f "$WORKDIR/sleep.sql" -c 40 -j 8 -T 40 > "$WORKDIR/saturation_pgbench_b.log" 2>&1
snap "stepC right after saturation load on pgb-b"
set_ini_b default_pool_size 10
set_ini_b reserve_pool_size 0
set_ini_b reserve_pool_timeout 5
set_ini_b query_wait_timeout 120
psqlB "RELOAD;"
note "STEPC AFTER"

### Step D -- cool-down, pgb-a steady traffic continues
note "STEPD BEFORE -- cool-down normal traffic (pgb-a only)"
sleep 120
snap "stepD final"
note "STEPD AFTER"

### Teardown
stop_bg_traffic_a
note "=== RUN P2 TEARDOWN ==="
psqlA "SHUTDOWN;"
psqlB "SHUTDOWN;"
sleep 1
podman stop $CONTAINER >> "$TIMELINE" 2>&1
note "=== RUN P2 DONE ==="
