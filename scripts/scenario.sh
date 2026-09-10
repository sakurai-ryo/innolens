#!/bin/sh
# Usage: scripts/scenario.sh [-v 80|84] [scenario.sql] [outdir]   (default: 80, scripts/scenario.sql, ./scenario)
#
# Runs a scenario against a throwaway MySQL in Docker and keeps two snapshots of
# its datadir: `before`, taken with everything flushed, and `after`, taken from a
# killed server so the redo log still holds what the statement wrote.
#
# The scenario file is split at a line reading `-- step`: what comes before sets
# the stage, what comes after is the statement being studied.
#
#   innolens <outdir>/after <outdir>/before
#
# opens the result with every page diffed against the same page of `before`.
set -eu
cd "$(dirname "$0")/.."

ver=80
case ${1:-} in -v) ver=$2; shift 2 ;; esac
sql=${1:-scripts/scenario.sql}
out=${2:-scenario}
case $ver in 80) image=mysql:8.0 ;; 84) image=mysql:8.4 ;; *) echo "unknown version: $ver" >&2; exit 1 ;; esac
[ -f "$sql" ] || { echo "no such scenario file: $sql" >&2; exit 1; }
grep -qx -- '-- step' "$sql" || { echo "$sql has no '-- step' line to split at" >&2; exit 1; }

c=innolens-scenario
mysql="docker exec -i $c mysql -h127.0.0.1 -uroot --default-character-set=utf8mb4"

wait_ready() {
  # TCP only: the image's init-time temporary server listens on the socket alone.
  until docker exec "$c" mysqladmin -h127.0.0.1 -uroot ping --silent >/dev/null 2>&1; do sleep 1; done
}

# snapshot copies the parts of the datadir innolens reads. The schema list is
# passed in because the server is already dead by the time the second snapshot
# is taken.
snapshot() {
  dst=$1
  rm -rf "$dst"
  mkdir -p "$dst"
  for s in $schemas; do
    docker cp "$c:/var/lib/mysql/$s" - | tar -x -C "$dst" --exclude='*.cfg'
  done
  docker cp "$c:/var/lib/mysql/#innodb_redo" - | tar -x -C "$dst" --exclude='*_tmp'
  for u in undo_001 undo_002; do
    docker cp "$c:/var/lib/mysql/$u" "$dst/$u" 2>/dev/null || true
  done
}

trap 'docker rm -f "$c" >/dev/null 2>&1' EXIT
docker rm -f "$c" >/dev/null 2>&1 || true
# The same flags as the test fixtures: a small redo log, and a page cleaner that
# does not run while the server is idle, so the checkpoint cannot move past the
# step before the snapshot is taken.
docker run -d --name "$c" -e MYSQL_ALLOW_EMPTY_PASSWORD=yes "$image" \
  --innodb-redo-log-capacity=8388608 --innodb-idle-flush-pct=0 \
  --innodb-rollback-segments=1 >/dev/null
wait_ready

awk '/^-- step$/{exit} {print}' "$sql" | $mysql
schemas=$($mysql -Nse "SELECT schema_name FROM information_schema.schemata
                       WHERE schema_name NOT IN ('mysql','sys','performance_schema','information_schema')")
[ -n "$schemas" ] || { echo "the setup created no schema" >&2; exit 1; }

# Drain the buffer pool so `before` is complete on disk. The checkpoint this
# writes is also what keeps the `after` redo log down to the step alone.
$mysql -e 'SET GLOBAL innodb_idle_flush_pct = 100; SET GLOBAL innodb_max_dirty_pages_pct = 0'
i=0
while [ "$($mysql -Nse "SELECT VARIABLE_VALUE FROM performance_schema.global_status
                        WHERE VARIABLE_NAME = 'Innodb_buffer_pool_pages_dirty'")" != "0" ] && [ $i -lt 60 ]; do
  i=$((i + 1))
  sleep 1
done
snapshot "$out/before"

$mysql -e 'SET GLOBAL innodb_idle_flush_pct = 0; SET GLOBAL innodb_max_dirty_pages_pct = 90'
awk 'f{print} /^-- step$/{f=1}' "$sql" | $mysql
# SIGKILL, so no shutdown checkpoint is written over the step's redo records.
docker kill "$c" >/dev/null
snapshot "$out/after"
docker rm -f "$c" >/dev/null

echo
echo "wrote $out/before and $out/after"
echo "  innolens $out/after $out/before"
