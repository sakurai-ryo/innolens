package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/sakurai-ryo/innolens/internal/innodb"
)

func TestPageColor(t *testing.T) {
	for _, c := range []struct {
		typ   uint16
		level int
		tag   string
		want  lipgloss.Color
	}{
		{innodb.FIL_PAGE_INDEX, 0, "INDEX leaf", colData},
		{innodb.FIL_PAGE_INDEX, 2, "INDEX node", colStruct},
		{innodb.FIL_PAGE_INDEX, -1, "INDEX", colStruct},
		{innodb.FIL_PAGE_SDI, -1, "INDEX", colMeta},
		{innodb.FIL_PAGE_TYPE_ALLOCATED, -1, "INDEX", colVoid},
		{innodb.FIL_PAGE_INODE, -1, "INDEX", colMuted},
	} {
		if got := pageColor(c.typ, c.level); got != c.want {
			t.Errorf("pageColor(%d, %d) = %q, want %q", c.typ, c.level, got, c.want)
		}
		if got := indexTag(c.level); got != c.tag {
			t.Errorf("indexTag(%d) = %q, want %q", c.level, got, c.tag)
		}
	}
}

func TestRedoColor(t *testing.T) {
	for _, c := range []struct {
		typ  uint8
		want lipgloss.Color
	}{
		{innodb.MLOG_REC_INSERT, colData},
		{innodb.MLOG_COMP_REC_INSERT_8027, colData},
		{innodb.MLOG_REC_DELETE, colDanger},
		{innodb.MLOG_REC_CLUST_DELETE_MARK, colDanger},
		{innodb.MLOG_REC_UPDATE_IN_PLACE, colIndex},
		{innodb.MLOG_COMP_PAGE_CREATE, colStruct},
		{innodb.MLOG_PAGE_REORGANIZE, colStruct},
		{innodb.MLOG_UNDO_INSERT, colMuted},  // undo is noise, not a row insert
		{innodb.MLOG_FILE_DELETE, colMuted},  // a tablespace op, not a row delete
		{innodb.MLOG_WRITE_STRING, colMuted}, // raw byte write
	} {
		if got := redoColor(c.typ); got != c.want {
			t.Errorf("redoColor(%s) = %q, want %q", innodb.MLogName(c.typ), got, c.want)
		}
	}
}

// TestHexRegions checks that every byte a section covers is painted, that
// records alternate shades, and that unaccounted bytes stay unpainted.
func TestHexRegions(t *testing.T) {
	root := &innodb.Node{}
	fil := root.Group("FIL header")
	fil.Add("FIL_PAGE_OFFSET", 4, 4, "")
	recs := root.Group("Records")
	r0 := recs.Add("record @100", 100, 10, "")
	r0.Add("var lengths", 100, 1, "")
	r0.Add("header", 101, 5, "")
	recs.Add("record @110", 110, 10, "")
	recs.Add("record @120", 120, 10, "")
	root.Group("Page directory").Add("slot 0", innodb.PageSize-10, 2, "")
	root.Group("nothing to see here").Add("x", 200, 4, "")

	regs := hexRegions(root)
	if len(regs) != innodb.PageSize {
		t.Fatalf("regions cover %d bytes, want %d", len(regs), innodb.PageSize)
	}
	for _, c := range []struct {
		off  int
		want uint8
	}{
		{4, regStruct}, {7, regStruct}, {8, regNone},
		{100, regStruct}, {105, regStruct}, // the prefix reads as structure
		{106, regRecs}, {109, regRecs},
		{110, regRecsAlt}, {119, regRecsAlt},
		{120, regRecs},
		{innodb.PageSize - 10, regDir},
		{200, regNone}, // unknown section names are left alone
	} {
		if got := regs[c.off]; got != c.want {
			t.Errorf("region at %d = %d, want %d", c.off, got, c.want)
		}
	}
	if got := sectionColor("Records"); got != colData {
		t.Errorf("sectionColor(Records) = %q, want %q", got, colData)
	}
	if got := sectionColor("nothing to see here"); got != "" {
		t.Errorf("sectionColor of an unknown section = %q, want empty", got)
	}
}
