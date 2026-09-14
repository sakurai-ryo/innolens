package innodb

import (
	"strings"
	"testing"
)

// TestCreateTable compares the rebuilt statement with what SHOW CREATE TABLE
// printed for the fixture tables on MySQL 8.0 and 8.4, which agree. The
// AUTO_INCREMENT counter is left out of the expectation: it lives in the data
// dictionary's dynamic metadata, not in the SDI. redo_new never reached disk,
// so it has no SDI to rebuild from.
func TestCreateTable(t *testing.T) {
	want := map[string]string{
		"types": "CREATE TABLE `types` (\n" +
			"  `id` int NOT NULL AUTO_INCREMENT,\n" +
			"  `c_tinyint` tinyint DEFAULT NULL,\n" +
			"  `c_smallint` smallint DEFAULT NULL,\n" +
			"  `c_mediumint` mediumint unsigned DEFAULT NULL,\n" +
			"  `c_int` int DEFAULT NULL,\n" +
			"  `c_bigint` bigint unsigned DEFAULT NULL,\n" +
			"  `c_char` char(8) DEFAULT NULL,\n" +
			"  `c_varchar` varchar(64) NOT NULL,\n" +
			"  `c_latin1` varchar(16) CHARACTER SET latin1 COLLATE latin1_swedish_ci DEFAULT NULL,\n" +
			"  `c_ascii` varchar(16) CHARACTER SET ascii COLLATE ascii_general_ci DEFAULT NULL,\n" +
			"  `c_binary` varbinary(16) DEFAULT NULL,\n" +
			"  `c_sjis` varchar(16) CHARACTER SET sjis COLLATE sjis_japanese_ci DEFAULT NULL,\n" +
			"  `c_datetime` datetime DEFAULT NULL,\n" +
			"  `c_timestamp` timestamp NULL DEFAULT NULL,\n" +
			"  `c_date` date DEFAULT NULL,\n" +
			"  `c_decimal` decimal(10,2) DEFAULT NULL,\n" +
			"  `c_float` float DEFAULT NULL,\n" +
			"  `c_text` text,\n" +
			"  PRIMARY KEY (`id`),\n" +
			"  KEY `idx_varchar` (`c_varchar`)\n" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
		"instant": "CREATE TABLE `instant` (\n" +
			"  `id` int NOT NULL,\n" +
			"  `b` varchar(16) DEFAULT NULL,\n" +
			"  `c` int DEFAULT '42',\n" +
			"  PRIMARY KEY (`id`)\n" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
	}
	for _, ver := range versions {
		for name, w := range want {
			_, tbl := openTable(t, ver, name)
			if got := strings.Join(tbl.DDL, "\n"); got != w {
				t.Errorf("%s/%s:\n got:\n%s\nwant:\n%s", ver, name, got, w)
			}
		}
	}
}

// TestCreateTableClauses covers what the fixtures do not declare: the clauses
// that depend on fields other than the type and nullability.
func TestCreateTableClauses(t *testing.T) {
	dt := &ddTable{Name: "t", SchemaRef: "s", Engine: "InnoDB", CollationID: 45, Comment: "why",
		Options: "row_type=5;key_block_size=0;", PartitionType: 7}
	dt.Columns = []ddColumn{
		{Name: "id", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1, HasNoDefault: true},
		{Name: "s", Type: ddVarchar, ColumnTypeUTF8: "varchar(10)", CollationID: 255, CharLength: 40, Hidden: 1, IsNullable: true, DefaultNull: true},
		{Name: "b", Type: ddVarchar, ColumnTypeUTF8: "varchar(10)", CollationID: 46, CharLength: 40, Hidden: 1, IsNullable: true, DefaultNull: true, IsExplicitCollation: true},
		{Name: "ts", Type: ddTimestamp2, ColumnTypeUTF8: "timestamp", CollationID: 63, Hidden: 1, DefaultOption: "CURRENT_TIMESTAMP", UpdateOption: "CURRENT_TIMESTAMP"},
		{Name: "e", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1, IsNullable: true, DefaultOption: "`id` + 1"},
		{Name: "g", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1, IsNullable: true, GenerationExprUTF8: "(`id` * 2)", IsVirtual: true},
		{Name: "h", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: ddHiddenUser, IsNullable: true, DefaultNull: true, Comment: "it's hidden"},
		{Name: "DB_TRX_ID", Type: ddLong, CollationID: 63, Hidden: ddHiddenSE},
	}
	dt.Indexes = []ddIndex{
		{Name: "PRIMARY", Type: 1, IsVisible: true, Elements: []ddIndexElement{{ColumnOpx: 0, Length: 0xFFFFFFFF, Order: 2}, {ColumnOpx: 7, Length: 0xFFFFFFFF, Hidden: true}}},
		{Name: "u", Type: 2, IsVisible: false, Algorithm: 2, IsAlgorithmExplicit: true, Comment: "c", Elements: []ddIndexElement{{ColumnOpx: 1, Length: 12, Order: 3}}},
	}
	dt.ForeignKeys = []ddForeignKey{{Name: "fk", UpdateRule: 1, DeleteRule: 3, ReferencedTableSchemaName: "other", ReferencedTableName: "p",
		Elements: []struct {
			ColumnOpx            int    `json:"column_opx"`
			ReferencedColumnName string `json:"referenced_column_name"`
		}{{ColumnOpx: 0, ReferencedColumnName: "pid"}}}}
	dt.CheckConstraints = append(dt.CheckConstraints, struct {
		Name            string `json:"name"`
		State           int    `json:"constraint_state"`
		CheckClauseUTF8 string `json:"check_clause_utf8"`
	}{Name: "ck", State: 2, CheckClauseUTF8: "(`id` > 0)"})
	want := "CREATE TABLE `t` (\n" +
		"  `id` int NOT NULL,\n" +
		"  `s` varchar(10) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,\n" +
		"  `b` varchar(10) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,\n" +
		"  `ts` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,\n" +
		"  `e` int DEFAULT (`id` + 1),\n" +
		"  `g` int GENERATED ALWAYS AS ((`id` * 2)) VIRTUAL,\n" +
		"  `h` int DEFAULT NULL /*!80023 INVISIBLE */ COMMENT 'it\\'s hidden',\n" +
		"  PRIMARY KEY (`id`),\n" +
		"  UNIQUE KEY `u` (`s`(3) DESC) USING BTREE COMMENT 'c' /*!80000 INVISIBLE */,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`id`) REFERENCES `other`.`p` (`pid`) ON DELETE CASCADE,\n" +
		"  CONSTRAINT `ck` CHECK ((`id` > 0)) /*!80016 NOT ENFORCED */\n" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci ROW_FORMAT=COMPACT COMMENT='why'\n" +
		"-- partitioned: the PARTITION BY clause is not rebuilt"
	if got := strings.Join(createTable(dt), "\n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestClusteredIndexWithoutPrimary: a table with no primary key but a UNIQUE
// NOT NULL index uses it as the clustered index. The SDI still lists it
// first, with DB_TRX_ID appended, and its key is what stands before that.
func TestClusteredIndexWithoutPrimary(t *testing.T) {
	dt := &ddTable{Name: "t", SchemaRef: "s", Engine: "InnoDB", CollationID: 45, RowFormat: 2}
	dt.Columns = []ddColumn{
		{Name: "a", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1},
		{Name: "b", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1},
		{Name: "c", Type: ddLong, ColumnTypeUTF8: "int", CollationID: 63, Hidden: 1, IsNullable: true, DefaultNull: true},
		{Name: "DB_TRX_ID", Type: ddLong, CollationID: 63, Hidden: ddHiddenSE},
		{Name: "DB_ROLL_PTR", Type: ddLong, CollationID: 63, Hidden: ddHiddenSE},
	}
	el := func(opx int, hidden bool) ddIndexElement {
		return ddIndexElement{ColumnOpx: opx, Length: 0xFFFFFFFF, Hidden: hidden, Order: 2}
	}
	dt.Indexes = []ddIndex{
		{Name: "a", Type: 2, IsVisible: true, SePrivateData: "id=10;root=4;",
			Elements: []ddIndexElement{el(0, false), el(1, false), el(3, true), el(4, true), el(2, true)}},
		{Name: "c", Type: 3, IsVisible: true, SePrivateData: "id=11;root=5;",
			Elements: []ddIndexElement{el(2, false), el(0, true), el(1, true)}},
	}
	tbl, err := buildTable(dt)
	if err != nil {
		t.Fatal(err)
	}
	clust, sec := tbl.Indexes[0], tbl.Indexes[1]
	if clust.NKey != 2 || clust.NUniqueInTree != 2 || !clust.Unique || clust.Table != tbl {
		t.Errorf("clustered: NKey %d NUniqueInTree %d Unique %v", clust.NKey, clust.NUniqueInTree, clust.Unique)
	}
	if sec.NKey != 1 || sec.NUniqueInTree != 3 {
		t.Errorf("secondary: NKey %d NUniqueInTree %d", sec.NKey, sec.NUniqueInTree)
	}
	dt.Indexes = dt.Indexes[1:]
	if _, err := buildTable(dt); err == nil {
		t.Error("a table whose first index carries no DB_TRX_ID built as if it had a clustered index")
	}
}
