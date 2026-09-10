#!/bin/sh
# Usage: test/testdata/locks.sh [80|84 ...]   (default: both)
# Records what performance_schema.data_locks shows while each statement in
# `cases` is still open, as test/testdata/<ver>/locks/<name>.tsv. The lock
# simulator is tested against these files.
#
# The table is rebuilt with the same SQL the fixture came from instead of being
# imported: IMPORT TABLESPACE purges delete-marked records, and several cases
# are about those. The same inserts in the same order give the same pages and
# heap numbers; the test checks the key at each locked heap_no to be sure.
set -eu
cd "$(dirname "$0")"
. ./common.sh

# name, index, prompt: the prompt is what `l` takes in the page tree. Rows with
# id % 50 = 0 are purged and id % 50 = 25 delete-marked, which the cases lean on.
cases='
pk-eq                  PRIMARY      x = 101
pk-eq-share            PRIMARY      share = 101
pk-eq-miss             PRIMARY      x = 150
pk-eq-deleted          PRIMARY      x = 125
pk-between             PRIMARY      update 10..13
pk-between-miss-end    PRIMARY      x 147..150
pk-between-deleted     PRIMARY      x 124..126
pk-between-pages       PRIMARY      x 150..260
pk-gt                  PRIMARY      x > 1995
pk-ge                  PRIMARY      x >= 1997
pk-ge-miss             PRIMARY      x >= 150
pk-lt                  PRIMARY      x < 3
pk-le                  PRIMARY      x <= 3
pk-delete              PRIMARY      delete = 101
pk-delete-between      PRIMARY      delete 10..12
sec-eq                 idx_varchar  x = varchar-101-x
sec-eq-share           idx_varchar  share = varchar-101-x
sec-eq-miss            idx_varchar  x = varchar-100-x
sec-eq-deleted         idx_varchar  x = varchar-125-xxxxx
sec-between            idx_varchar  x varchar-1998-..varchar-1999-
sec-lt                 idx_varchar  x < varchar-1000
sec-gt                 idx_varchar  x > varchar-999
sec-delete             idx_varchar  delete = varchar-101-x
rc-pk-eq               PRIMARY      rc x = 101
rc-pk-eq-miss          PRIMARY      rc x = 150
rc-pk-eq-deleted       PRIMARY      rc x = 125
rc-pk-between          PRIMARY      rc update 10..13
rc-pk-between-deleted  PRIMARY      rc x 124..126
rc-pk-gt               PRIMARY      rc x > 1995
rc-sec-eq              idx_varchar  rc x = varchar-101-x
rc-sec-lt              idx_varchar  rc x < varchar-1000
rc-sec-delete          idx_varchar  rc delete = varchar-101-x
'

# lit quotes a value unless it is a number, the way the prompt reads it.
lit() { case $1 in ''|*[!0-9]*) echo "'$1'" ;; *) echo "$1" ;; esac; }

# sql_for is the statement a prompt stands for, without its `rc` prefix. It
# must produce the same text as LockStmt.SQL in internal/innodb/lock.go: the
# test compares the two so that the recording and the simulator cannot drift.
sql_for() {
  case $1 in PRIMARY) col=id ;; idx_varchar) col=c_varchar ;; *) echo "no column for index $1" >&2; exit 1 ;; esac
  op=$2
  shift 2
  case $# in
    1) where="$col BETWEEN $(lit "${1%%..*}") AND $(lit "${1##*..}")" ;;
    2) where="$col $1 $(lit "$2")" ;;
    *) echo "bad predicate: $*" >&2; exit 1 ;;
  esac
  case $op in
    share) echo "SELECT * FROM types WHERE $where LOCK IN SHARE MODE" ;;
    x) echo "SELECT * FROM types WHERE $where FOR UPDATE" ;;
    update) echo "UPDATE types SET c_tinyint = 0 WHERE $where" ;;
    delete) echo "DELETE FROM types WHERE $where" ;;
    *) echo "unknown operation: $op" >&2; exit 1 ;;
  esac
}

# The lock rows of the table, with the page and heap number pulled out of
# ENGINE_LOCK_ID. A record lock's id ends in page:heap:lock, after a trx id
# that is one or two fields long depending on the version, so both are counted
# from the end.
dump="SELECT LOCK_TYPE, INDEX_NAME, LOCK_MODE, LOCK_STATUS,
  IF(LOCK_TYPE = 'RECORD', SUBSTRING_INDEX(SUBSTRING_INDEX(ENGINE_LOCK_ID, ':', -3), ':', 1), '') AS page_no,
  IF(LOCK_TYPE = 'RECORD', SUBSTRING_INDEX(SUBSTRING_INDEX(ENGINE_LOCK_ID, ':', -2), ':', 1), '') AS heap_no,
  IFNULL(LOCK_DATA, '') AS lock_data
FROM performance_schema.data_locks
WHERE OBJECT_SCHEMA = 'innolens' AND OBJECT_NAME = 'types'
ORDER BY LOCK_TYPE, INDEX_NAME, page_no + 0, heap_no + 0, LOCK_MODE"

# sleepers counts sessions parked on the SLEEP that keeps a case's transaction
# open. The read view populate leaves behind sleeps for a different length.
sleepers() {
  $mysql -Nse "SELECT COUNT(*) FROM information_schema.processlist WHERE info LIKE 'SELECT SLEEP(601)%'"
}

record() {
  ver=$1
  c=innolens-locks-$ver
  mysql="docker exec -i $c mysql -h127.0.0.1 -uroot --default-character-set=utf8mb4"
  trap 'docker rm -f "$c" >/dev/null 2>&1' EXIT
  docker rm -f "$c" >/dev/null 2>&1 || true
  start_server "$ver"
  populate
  mkdir -p "$ver/locks"

  # The loop reads its cases from fd 3: docker exec -i would otherwise swallow
  # the rest of them from stdin.
  while read -r name index prompt <&3; do
    [ -n "$name" ] || continue
    iso=REPEATABLE-READ
    stmt=$prompt
    case $prompt in rc\ *) iso=READ-COMMITTED; stmt=${prompt#rc } ;; esac
    sql=$(sql_for "$index" $stmt)
    docker exec -d "$c" mysql -h127.0.0.1 -uroot innolens \
      -e "SET SESSION transaction_isolation = '$iso'; BEGIN; $sql; SELECT SLEEP(601)"
    i=0
    until [ "$(sleepers)" = 1 ]; do
      i=$((i + 1))
      [ $i -lt 30 ] || { echo "$name: the statement did not reach its SLEEP" >&2; exit 1; }
      sleep 1
    done
    {
      printf '# %s\t%s\n# %s\n' "$index" "$prompt" "$sql"
      $mysql -e "$dump"
    } > "$ver/locks/$name.tsv"
    $mysql -e "KILL $($mysql -Nse "SELECT id FROM information_schema.processlist WHERE info LIKE 'SELECT SLEEP(601)%'")"
    until [ "$($mysql -Nse "SELECT COUNT(*) FROM performance_schema.data_locks")" = 0 ]; do sleep 1; done
    echo "$name: $(grep -c RECORD "$ver/locks/$name.tsv") record lock(s)"
  done 3<<EOF
$cases
EOF
  docker rm -f "$c" >/dev/null
}

for v in ${@:-80 84}; do record "$v"; done
