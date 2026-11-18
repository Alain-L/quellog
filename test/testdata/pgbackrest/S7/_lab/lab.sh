#!/bin/bash
# Runs inside postgres:17: archive-get through two restores, an archive-only
# standby (async archive-get, then an outage) and a WAL missing from the repository.
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null && apt-get install -y -qq pgbackrest >/dev/null
D=/var/lib/postgresql/data
mkdir -p /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest /var/lib/pg /lab/out
chown -R postgres /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest /var/lib/pg /lab/out
conf() { # $1: extra lines for [global:archive-get]
cat > /etc/pgbackrest.conf <<C
[global]
repo1-path=/var/lib/pgbackrest
repo1-retention-full=2
log-level-file=info
start-fast=y
spool-path=/var/spool/pgbackrest
[global:archive-get]
$1
[main]
pg1-path=$D
C
}
conf ""
U() { runuser -u postgres -- "$@"; }
P() { U psql -qAt -p "${2:-5432}" -c "$1"; }
B() { U pgbackrest --stanza=main "$@"; }
ts() { echo "== $(date -u +%T.%3N) $*"; }
sw() { for i in $(seq 1 "$1"); do P "create table if not exists t(x int); insert into t select generate_series(1,1000); select pg_switch_wal()" >/dev/null; sleep 1; done; }
# A restore_command that logs archive-get to its own file (only a command-line
# --log-level-file does) and names the restored data directory.
RC() { echo "--recovery-option=restore_command=pgbackrest --stanza=main --pg1-path=/var/lib/pg/$1 --log-level-file=info archive-get %f \"%p\""; }
R() { # name port restore-options...
  local n=$1 p=$2; shift 2
  ts "restore $n $*"
  B --pg1-path=/var/lib/pg/$n restore "$@" || ts "restore $n exit $?"
  mkdir -p /var/lib/pg/log_$n && chown postgres /var/lib/pg/log_$n
  cat >> /var/lib/pg/$n/postgresql.auto.conf <<C
port = $p
archive_mode = off
logging_collector = on
log_directory = '/var/lib/pg/log_$n'
log_line_prefix = '%m [%p] '
C
  U pg_ctl -D /var/lib/pg/$n -w -t 90 start >/dev/null || ts "start $n failed"
  sleep 2
  ts "$n in recovery: $(P 'select pg_is_in_recovery()' $p 2>&1 | tail -1), rows: $(P 'select count(*) from t' $p 2>&1 | tail -1)"
}
P "alter system set archive_command = 'pgbackrest --stanza=main --log-level-file=info archive-push %p'"
P "select pg_reload_conf()" >/dev/null
ts stanza-create; B stanza-create
ts full; B backup --type=full
sw 4
TARGET=$(P "select now()"); ts "PITR target $TARGET, rows $(P 'select count(*) from t')"
sleep 1; sw 4
ts diff; B backup --type=diff
sw 3; sleep 3
ts "primary rows $(P 'select count(*) from t')"

# 1. Restore to the end of the archive, default restore_command.
R restore1 5433
U pg_ctl -D /var/lib/pg/restore1 -w stop >/dev/null || true
# 2a. A hand-written restore_command without --pg1-path: archive-get refuses ([032]).
R restorebad 5438 --type=time "--target=$TARGET" --target-action=promote \
  "--recovery-option=restore_command=pgbackrest --stanza=main --log-level-file=info archive-get %f \"%p\""
U pg_ctl -D /var/lib/pg/restorebad -w stop >/dev/null 2>&1 || true
# 2b. Point-in-time restore, archive-get logged.
R restore2 5434 --type=time "--target=$TARGET" --target-action=promote "$(RC restore2)"
U pg_ctl -D /var/lib/pg/restore2 -w stop >/dev/null || true

# 3. Archive-only standby, async archive-get, then the repository unreadable for a while.
conf "archive-async=y
archive-get-queue-max=64MB"
R standby 5435 --type=standby "$(RC standby)"
sw 6; sleep 8
ts "standby rows $(P 'select count(*) from t' 5435), primary $(P 'select count(*) from t')"
ts "outage: archive.info unreadable"
chmod 000 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sw 3; sleep 20
chmod 640 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
ts "outage over"
sw 2; sleep 15
ts "standby rows $(P 'select count(*) from t' 5435), primary $(P 'select count(*) from t')"
U pg_ctl -D /var/lib/pg/standby -w stop >/dev/null || true
conf ""

# 4. A WAL segment lost from the repository.
S=$(P "select pg_walfile_name(pg_current_wal_lsn())")
sw 3; sleep 3
F=$(ls /var/lib/pgbackrest/archive/main/*/*/$S-* ); rm -v "$F"
ts "deleted $S from the repository"
sw 2; TARGET2=$(P "select now()"); sleep 1; sw 1; sleep 3
ts "PITR target 2 $TARGET2, primary rows $(P 'select count(*) from t')"
# 4a. Restore to a time past the hole: recovery cannot reach it.
R restore3 5436 --type=time "--target=$TARGET2" --target-action=promote "$(RC restore3)"
U pg_ctl -D /var/lib/pg/restore3 -w stop >/dev/null 2>&1 || true
# 4b. Restore to the end of the archive: recovery stops at the hole and promotes.
R restore4 5437 "$(RC restore4)"
U pg_ctl -D /var/lib/pg/restore4 -w stop >/dev/null 2>&1 || true

ts done
mkdir -p /lab/out/pgbackrest_logs /lab/out/pg_logs_main
cp /var/log/pgbackrest/*.log /lab/out/pgbackrest_logs/
cp $D/log/* /lab/out/pg_logs_main/
for n in restore1 restorebad restore2 standby restore3 restore4; do mkdir -p /lab/out/pg_logs_$n; cp /var/lib/pg/log_$n/* /lab/out/pg_logs_$n/ 2>/dev/null || true; done
cp /etc/pgbackrest.conf /lab/out/
chmod -R a+rX /lab/out
