#!/bin/sh
# Usage: test/testdata/gen.sh [80|84 ...]   (default: both)
# Generates test/testdata/<ver>/innolens/*.ibd and test/testdata/<ver>/#innodb_redo/ from Docker MySQL.
set -eu
cd "$(dirname "$0")"

gen() {
  ver=$1
  out=$ver
  c=innolens-fixture-$ver
  mysql="docker exec -i $c mysql -h127.0.0.1 -uroot --default-character-set=utf8mb4"
  case $ver in 80) image=mysql:8.0 ;; 84) image=mysql:8.4 ;; *) echo "unknown version: $ver" >&2; exit 1 ;; esac

  trap 'docker rm -f "$c" >/dev/null 2>&1' EXIT
  docker rm -f "$c" >/dev/null 2>&1 || true
  mkdir -p "$out"
  # 8MB is the minimum redo capacity: keeps the committed #ib_redo* files small.
  # idle-flush-pct=0 stops the page cleaner flushing while idle, so the checkpoint
  # cannot advance past the phase-2 DML before we kill the server.
  # One rollback segment per undo tablespace instead of 128 keeps the undo
  # fixtures small: each segment otherwise reserves pages spread over the 16MB.
  docker run -d --name "$c" -e MYSQL_ALLOW_EMPTY_PASSWORD=yes "$image" \
    --innodb-redo-log-capacity=8388608 --innodb-idle-flush-pct=0 \
    --innodb-rollback-segments=1 >/dev/null
  wait_ready

  # Phase 1: schema + data. FLUSH TABLES FOR EXPORT stops purge and flushes the
  # ibd files; a clean shutdown would let purge remove the delete-marked rows.
  $mysql <<'SQL'
CREATE DATABASE innolens;
USE innolens;
CREATE TABLE types (
  id          INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  c_tinyint   TINYINT,
  c_smallint  SMALLINT,
  c_mediumint MEDIUMINT UNSIGNED,
  c_int       INT,
  c_bigint    BIGINT UNSIGNED,
  c_char      CHAR(8),
  c_varchar   VARCHAR(64) NOT NULL,
  c_latin1    VARCHAR(16) CHARACTER SET latin1,
  c_ascii     VARCHAR(16) CHARACTER SET ascii,
  c_binary    VARBINARY(16),
  c_sjis      VARCHAR(16) CHARACTER SET sjis,
  c_datetime  DATETIME,
  c_timestamp TIMESTAMP NULL,
  c_date      DATE,
  c_decimal   DECIMAL(10,2),
  c_float     FLOAT,
  c_text      TEXT,
  KEY idx_varchar (c_varchar)
) CHARACTER SET utf8mb4;

SET SESSION cte_max_recursion_depth = 10000;
INSERT INTO types (c_tinyint, c_smallint, c_mediumint, c_int, c_bigint, c_char, c_varchar,
                   c_latin1, c_ascii, c_binary, c_sjis, c_datetime, c_timestamp, c_date,
                   c_decimal, c_float, c_text)
WITH RECURSIVE seq (n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 2000)
SELECT n % 128 - 64, n * 3 - 3000, n * 100, n * 1000 - 1000000, n * 100000,
       CONCAT('c', n), CONCAT('varchar-', n, '-', REPEAT('x', n % 20)),
       CONCAT('café', n), CONCAT('ascii', n), UNHEX(MD5(n)), CONCAT('日本語', n),
       '2026-01-01 00:00:00' + INTERVAL n MINUTE,
       IF(n % 10 = 0, NULL, '2026-01-01 00:00:00' + INTERVAL n SECOND),
       DATE('2026-01-01') + INTERVAL n DAY,
       n / 7, n / 3,
       IF(n % 500 = 1, REPEAT('t', 20000), CONCAT('text-', n))
FROM seq;

CREATE TABLE instant (id INT PRIMARY KEY, a INT, b VARCHAR(16));
INSERT INTO instant VALUES (1, 1, 'v0'), (2, 2, 'v0');
ALTER TABLE instant ADD COLUMN c INT DEFAULT 42, ALGORITHM=INSTANT;
INSERT INTO instant VALUES (3, 3, 'v1', 3);
ALTER TABLE instant DROP COLUMN a, ALGORITHM=INSTANT;
INSERT INTO instant VALUES (4, 'v2', 4);

DELETE FROM types WHERE id % 50 = 0;
SQL
  sleep 3 # let purge move the deleted records onto PAGE_FREE
  # An open read view blocks purge, so the next DELETE stays delete-marked in the ibd.
  docker exec -d "$c" mysql -h127.0.0.1 -uroot -e 'START TRANSACTION WITH CONSISTENT SNAPSHOT; SELECT SLEEP(600)'
  sleep 1
  $mysql -e 'DELETE FROM innolens.types WHERE id % 50 = 25'
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

wait_ready() {
  # TCP only: the image's init-time temporary server listens on the socket alone.
  until docker exec "$c" mysqladmin -h127.0.0.1 -uroot ping --silent >/dev/null 2>&1; do sleep 1; done
}

for v in ${@:-80 84}; do gen "$v"; done
