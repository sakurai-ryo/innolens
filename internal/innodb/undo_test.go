package innodb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// undoSpaces maps the undo space number a DB_ROLL_PTR carries to the file.
func undoSpaces(t *testing.T, ver string) map[uint8]string {
	t.Helper()
	dir := "../../test/testdata/" + ver
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uint8]string{}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "undo_") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		id, err := SpaceID(path)
		if err != nil {
			t.Fatal(err)
		}
		if !IsUndoSpace(id) {
			t.Fatalf("%s: space id %d is not an undo tablespace id", path, id)
		}
		out[UndoSpaceNum(id)] = path
	}
	if len(out) == 0 {
		t.Fatalf("no undo tablespace under %s", dir)
	}
	return out
}

// TestUndoDelMark follows the DB_ROLL_PTR of a delete-marked row into the undo
// tablespace and checks that the record found there undoes that very row.
func TestUndoDelMark(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "types")
			ix := tbl.Indexes[0]
			rec, rp := findDeleted(t, s, ix)
			if rp.Insert {
				t.Fatalf("delete-marked row points at insert undo: %s", rp)
			}
			t.Logf("id %s -> %s", rec.Fields[0].Value, rp)

			path, ok := undoSpaces(t, ver)[rp.RsegID]
			if !ok {
				t.Fatalf("%s: no undo tablespace with that number", rp)
			}
			us, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer us.Close()
			p, err := us.Page(rp.PageNo)
			if err != nil {
				t.Fatal(err)
			}
			up, err := p.ParseUndo(ix)
			if err != nil {
				t.Fatal(err)
			}
			var got *UndoRec
			for _, r := range up.Recs {
				if r.Off == int(rp.Offset) {
					got = r
				}
			}
			if got == nil {
				t.Fatalf("page %d of %s has no undo record at offset %d", rp.PageNo, path, rp.Offset)
			}
			if got.Err != nil {
				t.Fatalf("undo record at %d: %v", rp.Offset, got.Err)
			}
			if got.Type != TRX_UNDO_DEL_MARK_REC {
				t.Errorf("undo record type = %s, want TRX_UNDO_DEL_MARK_REC", got.TypeName())
			}
			if len(got.Key) != 1 || got.Key[0].Name != "id" {
				t.Fatalf("undo key fields = %+v, want the primary key", got.Key)
			}
			if want := rec.Fields[0].Value; got.Key[0].Value != want {
				t.Errorf("undo record is for id %s, want %s", got.Key[0].Value, want)
			}
			if got.TrxID == 0 {
				t.Error("undo record carries no DB_TRX_ID")
			}
			root, err := p.Annotate(ix)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"undo page header", "Records"} {
				if !hasChild(root, want) {
					t.Errorf("undo page annotation has no %q section", want)
				}
			}

			if ix.TableID == 0 {
				t.Fatal("index has no table id, so a foreign undo record cannot be told apart")
			}
			other := *ix
			other.TableID++
			up, err = p.ParseUndo(&other)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range up.Recs {
				if len(r.Key) > 0 {
					t.Errorf("undo record at %d was decoded with another table's index", r.Off)
				}
			}
		})
	}
}

// TestUndoValueShortField checks that a field length the undo record itself
// supplies cannot drive a fixed-width decoder past the end of the data.
func TestUndoValueShortField(t *testing.T) {
	idx := &IndexDef{Cols: []Col{{Name: "d", Fixed: 5, Decode: decDatetime(0)}}}
	if got := decodeUndoValue(idx, 0, []byte{0x99, 0x9a}); !strings.Contains(got, "wants 5 bytes") {
		t.Errorf("decodeUndoValue with 2 bytes = %q, want a length complaint", got)
	}
}

func hasChild(n *Node, name string) bool {
	for _, c := range n.Children {
		if c.Name == name {
			return true
		}
	}
	return false
}

// findDeleted returns the first delete-marked row of the clustered index and
// the roll pointer it carries.
func findDeleted(t *testing.T, s *Space, ix *IndexDef) (*Rec, RollPtr) {
	t.Helper()
	for _, ip := range leaves(t, s, ix) {
		for _, r := range ip.UserRecs() {
			if !r.Deleted() {
				continue
			}
			for _, f := range r.Fields {
				if f.Name != "DB_ROLL_PTR" || f.Len != 7 {
					continue
				}
				if rp, ok := DecodeRollPtr(r.Buf[f.Off : f.Off+7]); ok && !rp.Insert {
					return r, rp
				}
			}
		}
	}
	t.Fatal("no delete-marked row with an update roll pointer in the fixture")
	return nil, RollPtr{}
}
