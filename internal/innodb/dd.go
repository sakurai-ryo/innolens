package innodb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// dd::enum_column_types (sql/dd/types/column.h).
const (
	ddDecimal    = 1
	ddTiny       = 2
	ddShort      = 3
	ddLong       = 4
	ddFloat      = 5
	ddDouble     = 6
	ddTimestamp  = 8
	ddLongLong   = 9
	ddInt24      = 10
	ddDate       = 11
	ddTime       = 12
	ddDatetime   = 13
	ddYear       = 14
	ddNewDate    = 15
	ddVarchar    = 16
	ddBit        = 17
	ddTimestamp2 = 18
	ddDatetime2  = 19
	ddTime2      = 20
	ddNewDecimal = 21
	ddEnum       = 22
	ddSet        = 23
	ddTinyBlob   = 24
	ddMediumBlob = 25
	ddLongBlob   = 26
	ddBlob       = 27
	ddVarString  = 28
	ddString     = 29
	ddGeometry   = 30
	ddJSON       = 31
)

const ddHiddenSE = 2

type ddColumn struct {
	Name              string `json:"name"`
	Type              int    `json:"type"`
	IsNullable        bool   `json:"is_nullable"`
	IsUnsigned        bool   `json:"is_unsigned"`
	IsVirtual         bool   `json:"is_virtual"`
	Hidden            int    `json:"hidden"`
	CharLength        int    `json:"char_length"`
	NumericPrecision  int    `json:"numeric_precision"`
	NumericScale      int    `json:"numeric_scale"`
	DatetimePrecision int    `json:"datetime_precision"`
	ColumnTypeUTF8    string `json:"column_type_utf8"`
	CollationID       int    `json:"collation_id"`
	SePrivateData     string `json:"se_private_data"`
	DefaultValueUTF8  string `json:"default_value_utf8"`
	DefaultNull       bool   `json:"default_value_utf8_null"`
	Elements          []struct {
		Name string `json:"name"`
	} `json:"elements"`
	// What SHOW CREATE TABLE needs beyond the layout.
	IsAutoIncrement     bool   `json:"is_auto_increment"`
	IsExplicitCollation bool   `json:"is_explicit_collation"`
	HasNoDefault        bool   `json:"has_no_default"`
	DefaultOption       string `json:"default_option"`
	UpdateOption        string `json:"update_option"`
	GenerationExprUTF8  string `json:"generation_expression_utf8"`
	Comment             string `json:"comment"`
}

type ddIndex struct {
	Name          string `json:"name"`
	Type          int    `json:"type"` // 1 PRIMARY, 2 UNIQUE, 3 MULTIPLE, 4 FULLTEXT, 5 SPATIAL
	SePrivateData string `json:"se_private_data"`
	Elements      []struct {
		ColumnOpx int  `json:"column_opx"`
		Length    uint `json:"length"`
		Hidden    bool `json:"hidden"`
		Order     int  `json:"order"` // 2 ASC, 3 DESC
	} `json:"elements"`
	Algorithm           int    `json:"algorithm"` // 2 BTREE, 3 RTREE, 4 HASH, 5 FULLTEXT
	IsAlgorithmExplicit bool   `json:"is_algorithm_explicit"`
	IsVisible           bool   `json:"is_visible"`
	Comment             string `json:"comment"`
}

type ddForeignKey struct {
	Name                      string `json:"name"`
	UpdateRule                int    `json:"update_rule"` // 1 NO ACTION, 2 RESTRICT, 3 CASCADE, 4 SET NULL, 5 SET DEFAULT
	DeleteRule                int    `json:"delete_rule"`
	ReferencedTableSchemaName string `json:"referenced_table_schema_name"`
	ReferencedTableName       string `json:"referenced_table_name"`
	Elements                  []struct {
		ColumnOpx            int    `json:"column_opx"`
		ReferencedColumnName string `json:"referenced_column_name"`
	} `json:"elements"`
}

type ddTable struct {
	Name        string     `json:"name"`
	SePrivateID uint64     `json:"se_private_id"`
	SchemaRef   string     `json:"schema_ref"`
	RowFormat   int        `json:"row_format"`
	Columns     []ddColumn `json:"columns"`
	Indexes     []ddIndex  `json:"indexes"`
	// What SHOW CREATE TABLE needs beyond the layout.
	Engine           string         `json:"engine"`
	CollationID      int            `json:"collation_id"`
	Comment          string         `json:"comment"`
	Options          string         `json:"options"`
	PartitionType    int            `json:"partition_type"`
	ForeignKeys      []ddForeignKey `json:"foreign_keys"`
	CheckConstraints []struct {
		Name            string `json:"name"`
		State           int    `json:"constraint_state"` // 1 ENFORCED
		CheckClauseUTF8 string `json:"check_clause_utf8"`
	} `json:"check_constraints"`
}

// Table is the decoded table definition of one tablespace.
type Table struct {
	Schema, Name string
	Indexes      []*IndexDef
	JSON         []byte   // pretty-printed SDI
	DDL          []string // CREATE TABLE as SHOW CREATE TABLE prints it, one line each
}

func (t *Table) Index(id uint64) *IndexDef {
	for _, ix := range t.Indexes {
		if ix.ID == id {
			return ix
		}
	}
	return nil
}

// ReadTable reads the SDI and builds the index definitions.
func (s *Space) ReadTable() (*Table, error) {
	recs, err := s.ReadSDI()
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if r.Type != SDITypeTable {
			continue
		}
		var doc struct {
			Object ddTable `json:"dd_object"`
		}
		if err := json.Unmarshal(r.JSON, &doc); err != nil {
			return nil, fmt.Errorf("SDI table json: %w", err)
		}
		t, err := buildTable(&doc.Object)
		if err != nil {
			return nil, err
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, r.JSON, "", "  "); err == nil {
			t.JSON = pretty.Bytes()
		} else {
			t.JSON = r.JSON
		}
		return t, nil
	}
	return nil, fmt.Errorf("no table SDI record in %s", s.Path)
}

func sePrivate(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func buildTable(dt *ddTable) (*Table, error) {
	// ROW_FORMAT: 1 REDUNDANT, 2 COMPACT, 3 DYNAMIC (dd::Table::enum_row_format)
	if dt.RowFormat != 2 && dt.RowFormat != 3 {
		return nil, fmt.Errorf("unsupported row_format %d (only COMPACT/DYNAMIC)", dt.RowFormat)
	}
	t := &Table{Schema: dt.SchemaRef, Name: dt.Name, DDL: createTable(dt)}
	cols := make([]Col, len(dt.Columns))
	for i := range dt.Columns {
		cols[i] = buildCol(&dt.Columns[i])
	}
	for _, di := range dt.Indexes {
		if di.Type == 4 || di.Type == 5 {
			continue // FULLTEXT / SPATIAL are not B+trees we decode
		}
		sp := sePrivate(di.SePrivateData)
		id, _ := strconv.ParseUint(sp["id"], 10, 64)
		root, _ := strconv.ParseUint(sp["root"], 10, 32)
		ix := &IndexDef{Name: di.Name, TableID: dt.SePrivateID, ID: id, RootPage: uint32(root), Unique: di.Type <= 2}
		nKey := 0
		for _, e := range di.Elements {
			if !e.Hidden {
				nKey++
			}
		}
		ix.NKey = nKey
		switch {
		case di.Type == 1 && hasPhysicalPos(dt.Columns):
			// Instant ADD/DROP rewrites the physical order; dropped columns only exist here.
			type pc struct {
				pos int
				col Col
			}
			var pcs []pc
			for i, c := range dt.Columns {
				if c.IsVirtual {
					continue
				}
				pos, err := strconv.Atoi(sePrivate(c.SePrivateData)["physical_pos"])
				if err != nil {
					return nil, fmt.Errorf("column %s: missing physical_pos", c.Name)
				}
				pcs = append(pcs, pc{pos, cols[i]})
			}
			sort.Slice(pcs, func(a, b int) bool { return pcs[a].pos < pcs[b].pos })
			for _, p := range pcs {
				ix.Cols = append(ix.Cols, p.col)
			}
			ix.NUniqueInTree = nKey
		default:
			for _, e := range di.Elements {
				if e.ColumnOpx >= len(cols) {
					return nil, fmt.Errorf("index %s: column_opx %d out of range", di.Name, e.ColumnOpx)
				}
				c := cols[e.ColumnOpx]
				if e.Length != 0xFFFFFFFF && int(e.Length) < c.MaxLen && c.Fixed == 0 {
					// prefix index: only the first Length bytes are stored
					c.MaxLen = int(e.Length)
				}
				ix.Cols = append(ix.Cols, c)
			}
			if di.Type == 1 {
				ix.NUniqueInTree = nKey
			} else {
				// secondary node pointers carry key columns plus the appended PK columns
				ix.NUniqueInTree = len(ix.Cols)
			}
		}
		t.Indexes = append(t.Indexes, ix)
	}
	if len(t.Indexes) == 0 || t.Indexes[0].Name != "PRIMARY" {
		return nil, fmt.Errorf("table %s: no PRIMARY index (tables without a primary key are unsupported)", dt.Name)
	}
	return t, nil
}

func hasPhysicalPos(cols []ddColumn) bool {
	for _, c := range cols {
		if _, ok := sePrivate(c.SePrivateData)["physical_pos"]; ok {
			return true
		}
	}
	return false
}

var typeLenRe = regexp.MustCompile(`\((\d+)\)`)

func buildCol(c *ddColumn) Col {
	col := Col{Name: c.Name, Nullable: c.IsNullable}
	sp := sePrivate(c.SePrivateData)
	if v, err := strconv.Atoi(sp["version_added"]); err == nil {
		col.VersionAdded = uint8(v)
	}
	if v, err := strconv.Atoi(sp["version_dropped"]); err == nil {
		col.VersionDropped = uint8(v)
	}
	if c.DefaultNull {
		col.Default = "NULL"
	} else {
		col.Default = strconv.Quote(c.DefaultValueUTF8)
	}
	fixed := func(n int, dec func([]byte, *[]Step) string) { col.Fixed, col.Decode = n, dec }
	varlen := func(max int, dec func([]byte, *[]Step) string) { col.MaxLen, col.Decode = max, dec }
	charset := charsetName(c.CollationID)
	switch {
	case c.Hidden == ddHiddenSE && c.Name == "DB_TRX_ID":
		fixed(6, decUint)
	case c.Hidden == ddHiddenSE && c.Name == "DB_ROLL_PTR":
		fixed(7, decRollPtr)
	case c.Hidden == ddHiddenSE && c.Name == "DB_ROW_ID":
		fixed(6, decUint)
	case c.Type == ddTiny:
		fixed(1, intDecoder(c.IsUnsigned))
	case c.Type == ddShort:
		fixed(2, intDecoder(c.IsUnsigned))
	case c.Type == ddInt24:
		fixed(3, intDecoder(c.IsUnsigned))
	case c.Type == ddLong:
		fixed(4, intDecoder(c.IsUnsigned))
	case c.Type == ddLongLong:
		fixed(8, intDecoder(c.IsUnsigned))
	case c.Type == ddYear:
		fixed(1, nil)
	case c.Type == ddNewDate:
		fixed(3, decDate)
	case c.Type == ddDatetime2:
		fixed(5+fracBytes(c.DatetimePrecision), decDatetime(c.DatetimePrecision))
	case c.Type == ddTimestamp2:
		fixed(4+fracBytes(c.DatetimePrecision), decTimestamp(c.DatetimePrecision))
	case c.Type == ddTime2:
		fixed(3+fracBytes(c.DatetimePrecision), nil)
	case c.Type == ddNewDecimal:
		fixed(decimalSize(c.NumericPrecision, c.NumericScale), decDecimal(c.NumericPrecision, c.NumericScale))
	case c.Type == ddFloat:
		fixed(4, nil)
	case c.Type == ddDouble:
		fixed(8, nil)
	case c.Type == ddBit:
		fixed((c.CharLength+7)/8, nil)
	case c.Type == ddEnum && len(c.Elements) > 255:
		fixed(2, decUint)
	case c.Type == ddEnum:
		fixed(1, decUint)
	case c.Type == ddSet && len(c.Elements) > 32:
		fixed(8, nil)
	case c.Type == ddSet:
		fixed((len(c.Elements)+7)/8, nil)
	case c.Type == ddString && charMaxWidth(c) == mbMinLen(c.CollationID):
		// CHAR is fixed-length only when every character has the same byte width.
		fixed(c.CharLength, strDecoder(charset))
	case c.Type == ddString, c.Type == ddVarchar, c.Type == ddVarString:
		varlen(c.CharLength, strDecoder(charset))
	case c.Type == ddTinyBlob, c.Type == ddMediumBlob, c.Type == ddLongBlob, c.Type == ddBlob, c.Type == ddJSON, c.Type == ddGeometry:
		varlen(1<<31, strDecoder(charset))
	default:
		varlen(1<<31, nil)
	}
	if col.Decode == nil && c.ColumnTypeUTF8 != "" {
		col.Decode = rawDecoder(c.ColumnTypeUTF8)
	}
	return col
}

// charMaxWidth derives mbmaxlen from the byte length and the declared CHAR(n).
func charMaxWidth(c *ddColumn) int {
	if m := typeLenRe.FindStringSubmatch(c.ColumnTypeUTF8); m != nil {
		if n, _ := strconv.Atoi(m[1]); n > 0 {
			return c.CharLength / n
		}
	}
	return 1
}

// rawDecoder labels undecoded bytes with the SQL type so the reader knows why.
func rawDecoder(typ string) func([]byte, *[]Step) string {
	return func(b []byte, _ *[]Step) string { return fmt.Sprintf("%s: %x", typ, b) }
}
