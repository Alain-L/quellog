#!/bin/bash
# Run P1 -- one pooler, full story. See test/testdata/pgbouncer/_lab/README.md
set -uo pipefail

WORKDIR=~/pgb-lab/p1
PGUSER=bench
PGPASSWORD=benchpw123
PGDB=bench
PGPORT_CONTAINER=15432
PGBPORT=6432
CONTAINER=quellog-pg-p1

TIMELINE="$WORKDIR/TIMELINE.log"
SNAPSHOTS="$WORKDIR/MANIFEST_snapshots.log"
PGBLOG="$WORKDIR/p1.pgbouncer.log"
INI="$WORKDIR/conf/pgbouncer.ini"

ts() { date -u +%FT%T.%3NZ; }
note() { echo "[$(ts)] $*" | tee -a "$TIMELINE"; }
snap() {
  echo "=== $(ts) : $1 ===" >> "$SNAPSHOTS"
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "SHOW STATS;" >> "$SNAPSHOTS" 2>&1
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "SHOW POOLS;" >> "$SNAPSHOTS" 2>&1
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "SHOW CLIENTS;" >> "$SNAPSHOTS" 2>&1
}
admin() {
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d pgbouncer -c "$1" 2>&1 | tee -a "$TIMELINE"
}
set_ini() {
  # set_ini key value
  sed -i "s/^$1 = .*/$1 = $2/" "$INI"
}
bench_direct() {
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER "$@" $PGDB
}
bench_pooled() {
  PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/pgbench -h 127.0.0.1 -p $PGBPORT -U $PGUSER "$@" $PGDB
}

BGPID=""
start_bg_traffic() {
  ( while true; do
      bench_pooled -c 3 -j 1 -T 15 >/dev/null 2>>"$WORKDIR/bg_traffic.log"
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
note "=== RUN P1 SETUP START ==="

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
default_pool_size = 5
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

# re-hash password now that password_encryption=md5 is active (bootstrap set it as scram)
PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER -d $PGDB -c "ALTER ROLE $PGUSER WITH PASSWORD '$PGPASSWORD';" >> "$TIMELINE" 2>&1

pkill -f "pgbouncer -d $INI" 2>/dev/null
sleep 1
env TZ=UTC /usr/sbin/pgbouncer -d "$INI"
sleep 1
note "pgbouncer started (pid $(cat "$WORKDIR/pgbouncer.pid" 2>/dev/null))"

note "initializing pgbench dataset (scale 5) directly against postgres container"
bench_direct -i -s 5 --quiet >> "$TIMELINE" 2>&1

cat > "$WORKDIR/sleep.sql" <<'EOF'
\set aid random(1, 100000 * :scale)
BEGIN;
SELECT abalance FROM pgbench_accounts WHERE aid = :aid;
SELECT pg_sleep(0.5);
COMMIT;
EOF

note "=== RUN P1 SETUP DONE ==="

### Step 1 -- normal traffic
note "STEP1 BEFORE -- start light traffic"
start_bg_traffic
sleep 90
snap "step1 normal traffic"
note "STEP1 AFTER"
sleep 30

### Step 2 -- RELOAD after changing default_pool_size
note "STEP2 BEFORE -- change default_pool_size 5 -> 20, RELOAD"
set_ini default_pool_size 20
admin "RELOAD;"
note "STEP2 AFTER"
sleep 35

### Step 3 -- saturation episode
note "STEP3 BEFORE -- shrink pool, saturate with 42 clients + pg_sleep(0.5) for 60s"
set_ini default_pool_size 2
set_ini reserve_pool_size 2
set_ini reserve_pool_timeout 2
set_ini query_wait_timeout 3
admin "RELOAD;"
snap "step3 just after RELOAD (pool shrunk)"
bench_pooled -f "$WORKDIR/sleep.sql" -c 42 -j 8 -T 60 > "$WORKDIR/saturation_pgbench.log" 2>&1
snap "step3 right after saturation load"
note "STEP3 AFTER -- restoring pool settings"
set_ini default_pool_size 20
set_ini reserve_pool_size 2
set_ini reserve_pool_timeout 5
set_ini query_wait_timeout 120
admin "RELOAD;"
sleep 30

### Step 4 -- PAUSE / RESUME
note "STEP4 BEFORE -- PAUSE bench for ~20s under traffic"
admin "PAUSE bench;"
snap "step4 during pause"
sleep 20
admin "RESUME bench;"
note "STEP4 AFTER"
sleep 30

### Step 5 -- authentication failures
note "STEP5 BEFORE -- auth failures (wrong password x3, unknown db, unknown user)"
for i in 1 2 3; do
  PGPASSWORD=totallywrongpw /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d $PGDB -c 'select 1' >> "$WORKDIR/auth_attempts.log" 2>&1
done
PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d nosuchdb -c 'select 1' >> "$WORKDIR/auth_attempts.log" 2>&1
PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U ghostuser -d $PGDB -c 'select 1' >> "$WORKDIR/auth_attempts.log" 2>&1
note "STEP5 AFTER"
sleep 30

### Step 6 -- PostgreSQL restart under traffic
note "STEP6 BEFORE -- podman stop/start $CONTAINER under traffic"
podman stop $CONTAINER >> "$TIMELINE" 2>&1
sleep 2
for attempt in 1 2 3; do
  if podman start $CONTAINER >> "$TIMELINE" 2>&1; then
    break
  fi
  note "podman start attempt $attempt failed, retrying"
  sleep 3
done
for i in $(seq 1 30); do
  if PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGPORT_CONTAINER -U $PGUSER -d $PGDB -c 'select 1' >/dev/null 2>&1; then
    note "postgres back up after ${i}s"
    break
  fi
  sleep 1
done
note "STEP6 AFTER"
sleep 30

### Step 7 -- pgbouncer restart (clean stop, same log file)
note "STEP7 BEFORE -- pgbouncer clean restart (SIGINT then restart, append log)"
stop_bg_traffic
PGBPID=$(cat "$WORKDIR/pgbouncer.pid" 2>/dev/null)
kill -INT "$PGBPID" 2>/dev/null
for i in $(seq 1 10); do
  kill -0 "$PGBPID" 2>/dev/null || break
  sleep 1
done
env TZ=UTC /usr/sbin/pgbouncer -d "$INI"
sleep 1
note "pgbouncer restarted (pid $(cat "$WORKDIR/pgbouncer.pid" 2>/dev/null))"
start_bg_traffic
note "STEP7 AFTER"
sleep 30

### Step 8 -- client_idle_timeout
note "STEP8 BEFORE -- set client_idle_timeout=20, RELOAD, open idle client"
set_ini client_idle_timeout 20
admin "RELOAD;"
( PGPASSWORD=$PGPASSWORD /usr/lib/postgresql/17/bin/psql -h 127.0.0.1 -p $PGBPORT -U $PGUSER -d $PGDB <<'EOF' >> "$WORKDIR/idle_client.log" 2>&1
select 1;
\! sleep 25
select 2;
EOF
) &
IDLEPID=$!
wait "$IDLEPID"
note "STEP8 AFTER"
sleep 30

### Step 9 -- normal traffic to the end
note "STEP9 BEFORE -- final normal traffic window"
sleep 90
snap "step9 final"
note "STEP9 AFTER"

### Teardown
stop_bg_traffic
note "=== RUN P1 TEARDOWN ==="
admin "SHUTDOWN;"
sleep 1
podman stop $CONTAINER >> "$TIMELINE" 2>&1
note "=== RUN P1 DONE ==="
