#!/bin/bash
# The run behind web/demo-pgbackrest.log.zst, the demo page's pgBackRest
# example. Run it in a postgres:17 container with this directory's parent
# mounted on /lab:
#   docker run -d --name demo -e POSTGRES_HOST_AUTH_METHOD=trust -v $PWD:/lab postgres:17 \
#     -c archive_mode=on -c logging_collector=on -c "log_line_prefix=%m [%p] "
#   docker exec demo bash /lab/demo-pgbackrest-lab.sh
# then: cd out && cat $(ls pg_logs_main/*.log pg_logs_reporting/*.log | sort) pgbackrest_logs/*.log | zstd -19
# Two stanzas
# (main on the container's cluster, reporting on a second cluster), every
# pgBackRest line written once: archive-push logs to the console only
# (PostgreSQL's log), archive-get to its own file (--log-level-file).
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null && apt-get install -y -qq pgbackrest >/dev/null
D=/var/lib/postgresql/data; R=/var/lib/pg/reporting
mkdir -p /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest /var/lib/pg /lab/out
chown -R postgres /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest /var/lib/pg /lab/out
conf() {
cat > /etc/pgbackrest.conf <<C
[global]
repo1-path=/var/lib/pgbackrest
repo1-retention-full=2
log-level-file=info
start-fast=y
spool-path=/var/spool/pgbackrest
[global:archive-push]
log-level-console=info
[global:archive-get]
$1
[main]
pg1-path=$D
[reporting]
pg1-path=$R
pg1-port=5440
C
}
conf ""
U() { runuser -u postgres -- "$@"; }
P() { U psql -qAt -p "${2:-5432}" -c "$1"; }
B() { U pgbackrest "$@"; }
ts() { echo "== $(date -u +%T.%3N) $*"; }
sw() { for i in $(seq 1 "$1"); do P "create table if not exists t(x int); insert into t select generate_series(1,20000); select pg_switch_wal()" "${2:-5432}" >/dev/null; sleep 1; done; }
RC() { echo "--recovery-option=restore_command=pgbackrest --stanza=main --pg1-path=/var/lib/pg/$1 --log-level-file=info archive-get %f \"%p\""; }
start() { # name port extra
  mkdir -p /var/lib/pg/log_$1 && chown postgres /var/lib/pg/log_$1
  printf "port = %s\nlogging_collector = on\nlog_directory = '/var/lib/pg/log_%s'\nlog_line_prefix = '%%m [%%p] '\narchive_mode = off\n%s\n" "$2" "$1" "$3" >> /var/lib/pg/$1/postgresql.auto.conf
  U pg_ctl -D /var/lib/pg/$1 -w -t 90 start >/dev/null || ts "start $1 failed"; sleep 2
}
# The reporting cluster: a second instance, its own stanza.
U initdb -D $R >/dev/null
cat >> $R/postgresql.conf <<C
port = 5440
archive_mode = on
archive_command = 'pgbackrest --stanza=reporting archive-push %p'
logging_collector = on
log_directory = 'log'
log_line_prefix = '%m [%p] '
C
U pg_ctl -D $R -w start >/dev/null
P "alter system set archive_command = 'pgbackrest --stanza=main archive-push %p'"; P "select pg_reload_conf()" >/dev/null

ts "stanzas, first full backups"
B --stanza=main stanza-create; B --stanza=reporting stanza-create
B --stanza=main backup --type=full; sw 3
B --stanza=reporting backup --type=full; sw 2 5440
ts "diff, incr"
B --stanza=main backup --type=diff; sw 3; B --stanza=main backup --type=incr; sw 2
ts "a backup killed (SIGTERM) while running"
P "select pg_sleep(0)" >/dev/null
( B --stanza=main backup --type=full >/dev/null 2>&1 & echo $! > /tmp/bk; wait ) & sleep 2; kill -TERM $(pgrep -f "pgbackrest --stanza=main backup" | head -1) 2>/dev/null || true; sleep 3
ts "two backups at once: the second cannot take the lock"
( B --stanza=main backup --type=full >/dev/null 2>&1 ) & sleep 1; B --stanza=main backup --type=incr || ts "lock refused"; wait
sw 2
ts "archiving outage: archive.info unreadable for 30s"
chmod 000 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sw 2; sleep 28
chmod 640 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sleep 65; sw 3
TARGET=$(P "select now()"); sw 2
ts "reporting: a backup while PostgreSQL is down fails, then succeeds"
U pg_ctl -D $R -w stop >/dev/null; B --stanza=reporting backup --type=incr || ts "backup refused"
U pg_ctl -D $R -w start >/dev/null; sleep 2; B --stanza=reporting backup --type=incr; sw 2 5440
ts "full backup, expire by retention"
B --stanza=main backup --type=full; B --stanza=main expire; sw 2; sleep 3
ts "PITR restore to $TARGET"
B --stanza=main --pg1-path=/var/lib/pg/pitr restore --type=time "--target=$TARGET" --target-action=promote "$(RC pitr)"
start pitr 5433 ""; U pg_ctl -D /var/lib/pg/pitr -w stop >/dev/null || true
ts "standby replaying the archive, async archive-get"
conf "archive-async=y
archive-get-queue-max=64MB"
B --stanza=main --pg1-path=/var/lib/pg/standby restore --type=standby "$(RC standby)"
start standby 5435 ""; sw 6; sleep 10
U pg_ctl -D /var/lib/pg/standby -w stop >/dev/null || true
conf ""
ts "a WAL segment deleted from the repository, restore to the end of the archive"
S=$(P "select pg_walfile_name(pg_current_wal_lsn())"); sw 3; sleep 3
rm -v /var/lib/pgbackrest/archive/main/*/*/$S-*
sw 2; sleep 3
B --stanza=main --pg1-path=/var/lib/pg/latest restore "$(RC latest)"
start latest 5436 ""; U pg_ctl -D /var/lib/pg/latest -w stop >/dev/null || true
B --stanza=main check; B --stanza=reporting check
ts done
mkdir -p /lab/out/pgbackrest_logs /lab/out/pg_logs_main /lab/out/pg_logs_reporting
cp /var/log/pgbackrest/*.log /lab/out/pgbackrest_logs/; cp $D/log/* /lab/out/pg_logs_main/; cp $R/log/* /lab/out/pg_logs_reporting/
chmod -R a+rX /lab/out
