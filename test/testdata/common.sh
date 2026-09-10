# Shared by gen.sh and locks.sh: a throwaway MySQL in Docker holding the
# innolens fixture schema. Callers set c (container name) and mysql first.

image_for() {
  case $1 in 80) echo mysql:8.0 ;; 84) echo mysql:8.4 ;; *) echo "unknown version: $1" >&2; exit 1 ;; esac
}

start_server() {
  # 8MB is the minimum redo capacity: keeps the committed #ib_redo* files small.
  # idle-flush-pct=0 stops the page cleaner flushing while idle, so the checkpoint
  # cannot advance past the phase-2 DML before we kill the server.
  # One rollback segment per undo tablespace instead of 128 keeps the undo
  # fixtures small: each segment otherwise reserves pages spread over the 16MB.
  docker run -d --name "$c" -e MYSQL_ALLOW_EMPTY_PASSWORD=yes "$(image_for "$1")" \
    --innodb-redo-log-capacity=8388608 --innodb-idle-flush-pct=0 \
    --innodb-rollback-segments=1 >/dev/null
  wait_ready
}

# populate loads the schema and rows every fixture is taken from, and leaves
# the id % 50 = 25 rows delete-marked behind an open read view.
populate() {
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
}

wait_ready() {
  # TCP only: the image's init-time temporary server listens on the socket alone.
  until docker exec "$c" mysqladmin -h127.0.0.1 -uroot ping --silent >/dev/null 2>&1; do sleep 1; done
}
