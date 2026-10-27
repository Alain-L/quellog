#!/bin/bash
# Runs inside postgres:17: archive-push logged both to its own file and to the PostgreSQL log.
set -e
apt-get update -qq >/dev/null && apt-get install -y -qq pgbackrest >/dev/null
mkdir -p /var/lib/pgbackrest /var/log/pgbackrest /lab/out && chown -R postgres /var/lib/pgbackrest /var/log/pgbackrest /lab/out
conf() {
cat > /etc/pgbackrest.conf <<C
[global]
repo1-path=/var/lib/pgbackrest
repo1-retention-full=2
log-level-file=info
log-level-console=$1
start-fast=y
[main]
pg1-path=/var/lib/postgresql/data
C
}
conf warn
su postgres -c "pgbackrest --stanza=main stanza-create"
su postgres -c "pgbackrest --stanza=main backup --type=full"
sw() { for i in $(seq 1 $1); do su postgres -c "psql -qAt -c 'create table if not exists t(x int); insert into t values(1); select pg_switch_wal()'" >/dev/null; sleep 1; done; }
# Phase A: default console level (warn), with an outage.
sw 3
chmod 000 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sw 2; sleep 8
chmod 640 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sleep 65; sw 2
# Phase B: console at info, with an outage.
conf info
sw 3
chmod 000 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sw 2; sleep 8
chmod 640 /var/lib/pgbackrest/archive/main/archive.info /var/lib/pgbackrest/archive/main/archive.info.copy
sleep 65; sw 2
su postgres -c "pgbackrest --stanza=main backup --type=incr"
cp /var/log/pgbackrest/*.log /lab/out/
chmod -R a+r /lab/out
