package innodb

import (
	"fmt"
	"strings"
)

// createTable rebuilds the CREATE TABLE statement from the SDI, line by line,
// the way SHOW CREATE TABLE prints it. The AUTO_INCREMENT counter is not in the
// SDI, and a PARTITION BY clause is only noted, not spelled out.
func createTable(dt *ddTable) []string {
	tableCS := collations[dt.CollationID]
	var body []string
	for i := range dt.Columns {
		if c := &dt.Columns[i]; c.Hidden != ddHiddenSE && c.Hidden != ddHiddenSQL {
			body = append(body, columnDDL(c, tableCS))
		}
	}
	for i := range dt.Indexes {
		body = append(body, indexDDL(&dt.Indexes[i], dt))
	}
	for _, fk := range dt.ForeignKeys {
		body = append(body, foreignKeyDDL(&fk, dt))
	}
	for _, ck := range dt.CheckConstraints {
		s := "CONSTRAINT " + ident(ck.Name) + " CHECK (" + ck.CheckClauseUTF8 + ")"
		if ck.State != 1 {
			s += " /*!80016 NOT ENFORCED */"
		}
		body = append(body, s)
	}
	out := []string{"CREATE TABLE " + ident(dt.Name) + " ("}
	for i, l := range body {
		if i < len(body)-1 {
			l += ","
		}
		out = append(out, "  "+l)
	}
	tail := ") ENGINE=" + dt.Engine
	opts := sePrivate(dt.Options)
	if tableCS.name != "" {
		tail += " DEFAULT CHARSET=" + tableCS.charset
		if !tableCS.primary || dt.CollationID == collationUTF8MB40900 {
			tail += " COLLATE=" + tableCS.name
		}
	}
	if rf := rowFormatNames[opts["row_type"]]; rf != "" {
		tail += " ROW_FORMAT=" + rf
	}
	if kbs := opts["key_block_size"]; kbs != "" && kbs != "0" {
		tail += " KEY_BLOCK_SIZE=" + kbs
	}
	if dt.Comment != "" {
		tail += " COMMENT=" + literal(dt.Comment)
	}
	out = append(out, tail)
	if dt.PartitionType != 0 {
		out = append(out, "-- partitioned: the PARTITION BY clause is not rebuilt")
	}
	return out
}

const (
	ddHiddenSQL  = 3 // a column a functional index made, not one the user sees
	ddHiddenUser = 4 // INVISIBLE

	collationUTF8MB40900 = 255
)

// rowFormatNames is the row_type option, which is only set when the CREATE
// TABLE named one; the row_format field is what the table actually uses.
var rowFormatNames = map[string]string{"1": "FIXED", "2": "DYNAMIC", "3": "COMPRESSED", "4": "REDUNDANT", "5": "COMPACT"}

func columnDDL(c *ddColumn, tableCS collation) string {
	s := ident(c.Name) + " " + c.ColumnTypeUTF8
	if cs, ok := collations[c.CollationID]; ok && hasCharset(c) {
		// The same rules as sql_show.cc: the charset when it differs from the
		// table's or was written out, the collation when it is not the
		// charset's default, was written out, or is the 8.0 default on a table
		// that has another.
		if cs.charset != tableCS.charset || c.IsExplicitCollation {
			s += " CHARACTER SET " + cs.charset
		}
		if !cs.primary || c.IsExplicitCollation || (c.CollationID == collationUTF8MB40900 && tableCS.name != cs.name) {
			s += " COLLATE " + cs.name
		}
	}
	if c.GenerationExprUTF8 != "" {
		s += " GENERATED ALWAYS AS (" + c.GenerationExprUTF8 + ")"
		if c.IsVirtual {
			s += " VIRTUAL"
		} else {
			s += " STORED"
		}
	}
	switch {
	case !c.IsNullable:
		s += " NOT NULL"
	case c.Type == ddTimestamp2:
		// TIMESTAMP is NOT NULL unless told otherwise, so nullable is spelled out.
		s += " NULL"
	}
	if d, ok := defaultDDL(c); ok {
		s += " DEFAULT " + d
	}
	if c.UpdateOption != "" {
		s += " ON UPDATE " + c.UpdateOption
	}
	if c.IsAutoIncrement {
		s += " AUTO_INCREMENT"
	}
	if c.Hidden == ddHiddenUser {
		s += " /*!80023 INVISIBLE */"
	}
	if c.Comment != "" {
		s += " COMMENT " + literal(c.Comment)
	}
	return s
}

// hasCharset says whether the type carries a character set, which is what
// decides whether one is printed. BINARY, VARBINARY and BLOB are strings with
// the binary collation and carry none.
func hasCharset(c *ddColumn) bool { return isString(c) && c.CollationID != 63 }

func isString(c *ddColumn) bool {
	switch c.Type {
	case ddVarchar, ddString, ddVarString, ddEnum, ddSet, ddTinyBlob, ddMediumBlob, ddLongBlob, ddBlob:
		return true
	}
	return false
}

// defaultDDL is the DEFAULT clause, following print_default_clause: none for
// a generated or auto-increment column or one declared without a default,
// an expression or CURRENT_TIMESTAMP as written, nothing for BLOB and TEXT,
// otherwise the value in quotes, numbers included.
func defaultDDL(c *ddColumn) (string, bool) {
	if c.GenerationExprUTF8 != "" || c.HasNoDefault || c.IsAutoIncrement {
		return "", false
	}
	if c.DefaultOption != "" {
		if strings.HasPrefix(strings.ToUpper(c.DefaultOption), "CURRENT_TIMESTAMP") {
			return c.DefaultOption, true
		}
		return "(" + c.DefaultOption + ")", true
	}
	switch c.Type {
	case ddTinyBlob, ddMediumBlob, ddLongBlob, ddBlob, ddJSON, ddGeometry:
		return "", false
	}
	if c.DefaultNull {
		return "NULL", true
	}
	if c.Type == ddBit {
		return "b'" + c.DefaultValueUTF8 + "'", true
	}
	return literal(c.DefaultValueUTF8), true
}

func indexDDL(ix *ddIndex, dt *ddTable) string {
	var s string
	switch ix.Type {
	case 1:
		s = "PRIMARY KEY"
	case 2:
		s = "UNIQUE KEY " + ident(ix.Name)
	case 4:
		s = "FULLTEXT KEY " + ident(ix.Name)
	case 5:
		s = "SPATIAL KEY " + ident(ix.Name)
	default:
		s = "KEY " + ident(ix.Name)
	}
	var parts []string
	for _, e := range ix.Elements {
		if e.Hidden || e.ColumnOpx >= len(dt.Columns) {
			continue
		}
		c := &dt.Columns[e.ColumnOpx]
		p := ident(c.Name)
		if c.Hidden == ddHiddenSQL {
			p = "(" + c.GenerationExprUTF8 + ")"
		}
		if e.Length != 0xFFFFFFFF && int(e.Length) < c.CharLength && (hasCharset(c) || c.CollationID == 63 && isString(c)) {
			// Lengths are in bytes; the prefix was declared in characters.
			maxLen := collations[c.CollationID].maxLen
			if maxLen == 0 {
				maxLen = 1
			}
			p += fmt.Sprintf("(%d)", int(e.Length)/maxLen)
		}
		if e.Order == 3 {
			p += " DESC"
		}
		parts = append(parts, p)
	}
	s += " (" + strings.Join(parts, ",") + ")"
	if ix.IsAlgorithmExplicit {
		switch ix.Algorithm {
		case 2:
			s += " USING BTREE"
		case 4:
			s += " USING HASH"
		}
	}
	if ix.Comment != "" {
		s += " COMMENT " + literal(ix.Comment)
	}
	if !ix.IsVisible {
		s += " /*!80000 INVISIBLE */"
	}
	return s
}

var fkRuleNames = map[int]string{2: "RESTRICT", 3: "CASCADE", 4: "SET NULL", 5: "SET DEFAULT"}

func foreignKeyDDL(fk *ddForeignKey, dt *ddTable) string {
	var cols, refs []string
	for _, e := range fk.Elements {
		if e.ColumnOpx < len(dt.Columns) {
			cols = append(cols, ident(dt.Columns[e.ColumnOpx].Name))
		}
		refs = append(refs, ident(e.ReferencedColumnName))
	}
	ref := ident(fk.ReferencedTableName)
	if fk.ReferencedTableSchemaName != dt.SchemaRef {
		ref = ident(fk.ReferencedTableSchemaName) + "." + ref
	}
	s := "CONSTRAINT " + ident(fk.Name) + " FOREIGN KEY (" + strings.Join(cols, ",") + ") REFERENCES " + ref + " (" + strings.Join(refs, ",") + ")"
	if r := fkRuleNames[fk.DeleteRule]; r != "" {
		s += " ON DELETE " + r
	}
	if r := fkRuleNames[fk.UpdateRule]; r != "" {
		s += " ON UPDATE " + r
	}
	return s
}

func ident(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

func literal(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}
