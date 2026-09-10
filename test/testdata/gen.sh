#!/bin/sh
# Usage: test/testdata/gen.sh [80|84 ...]   (default: both)
# Generates test/testdata/<ver>/innolens/*.ibd and test/testdata/<ver>/#innodb_redo/ from Docker MySQL.
set -eu
cd "$(dirname "$0")"
. ./common.sh

gen() {
  ver=$1
  out=$ver
  c=innolens-fixture-$ver
  mysql="docker exec -i $c mysql -h127.0.0.1 -uroot --default-character-set=utf8mb4"

  trap 'docker rm -f "$c" >/dev/null 2>&1' EXIT
  docker rm -f "$c" >/dev/null 2>&1 || true
  mkdir -p "$out"
  start_server "$ver"

  # Phase 1: schema + data. FLUSH TABLES FOR EXPORT stops purge and flushes the
  # ibd files; a clean shutdown would let purge remove the delete-marked rows.
  populate
  docker exec -d "$c" mysql -h127.0.0.1 -uroot -e 'FLUSH TABLES innolens.types, innolens.instant FOR EXPORT; SELECT SLEEP(600)'
  until docker exec "$c" test -f /var/lib/mysql/innolens/instant.cfg; do sleep 1; done
  # FLUSH ... FOR EXPORT flushes the tables but not the undo pages the rows point
  # at, so drain the whole buffer pool. The read view above still blocks purge,
  # which keeps the undo records the delete-marked rows refer to.
  $mysql -e 'SET GLOBAL innodb_idle_flush_pct = 100; SET GLOBAL innodb_max_dirty_pages_pct = 0'
  i=0
  while [ "$($mysql -Nse "SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_buffer_pool_pages_dirty'")" != "0" ] && [ $i -lt 60 ]; do
    i=$((i + 1))
    sleep 1
  done
  $mysql -Nse 'SELECT VERSION()' > "$out/VERSION"
  docker cp "$c:/var/lib/mysql/innolens" - | tar -x -C "$out" --exclude='*.cfg'
  for u in undo_001 undo_002; do
    docker cp "$c:/var/lib/mysql/$u" "$out/$u"
    trim_zero_pages "$out/$u"
  done

  # Phase 2: DML for the redo fixture, then SIGKILL so no final checkpoint is written.
  # The kill/start cycle also drops the two sessions holding the snapshot and the export lock.
  docker kill "$c" >/dev/null
  docker start "$c" >/dev/null
  wait_ready
  $mysql <<'SQL'
USE innolens;
INSERT INTO types (c_varchar) VALUES ('redo-insert');
UPDATE types SET c_tinyint = 100 WHERE id = 1;
BEGIN; INSERT INTO types (c_varchar) VALUES ('redo-rollback'); ROLLBACK;
CREATE TABLE redo_new (id INT PRIMARY KEY, v VARCHAR(16));
INSERT INTO redo_new VALUES (1, 'x');
SQL
  docker kill "$c" >/dev/null
  # The file numbers move with the LSN, so old #ib_redoN would otherwise be
  # left behind and read as part of the log.
  rm -rf "$out/#innodb_redo"
  docker cp "$c:/var/lib/mysql/#innodb_redo" - | tar -x -C "$out" --exclude='*_tmp'
  docker cp "$c:/var/lib/mysql/innolens/redo_new.ibd" - | tar -x -C "$out/innolens"
  docker rm -f "$c" >/dev/null
}

# trim_zero_pages cuts the all-zero pages off the end of a tablespace. Undo
# tablespaces start at 16MB and this workload touches the first few pages, so
# the fixture would otherwise be almost entirely zeros.
trim_zero_pages() {
  python3 - "$1" <<'EOF'
import sys
p = sys.argv[1]
with open(p, 'r+b') as f:
    b = f.read()
    n = len(b) // 16384
    while n > 1 and b[(n - 1) * 16384:n * 16384] == bytes(16384):
        n -= 1
    f.truncate(n * 16384)
EOF
}


for v in ${@:-80 84}; do gen "$v"; done
