package innodb

import (
	"testing"
)

// TestSimulateInsert puts keys into the types table and checks each lands
// between its neighbours, sized by one of them, and that a page that cannot
// take the record is cut where btr_page_split_and_insert would cut it.
func TestSimulateInsert(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "types")
			pk := tbl.Indexes[0]

			// 1250 was purged: its bytes sit on the free list, so the key is
			// free again and the record goes between 1249 and 1251.
			pl, err := s.SimulateInsert(tbl, pk, "1250", nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.Dup != nil {
				t.Fatalf("1250 was purged but is reported as a duplicate of %v", fields(pl.Dup))
			}
			if pl.Key(pl.Prev) != "1249" || pl.Key(pl.Next) != "1251" {
				t.Errorf("1250 goes between %s and %s", pl.Key(pl.Prev), pl.Key(pl.Next))
			}
			if pl.Size != pl.Prev.size() || pl.Size == 0 {
				t.Errorf("size %d, want that of the record before, %d", pl.Size, pl.Prev.size())
			}
			// The head of the free list is reused only when the record fits it
			// whole; the rows differ in c_varchar, so either way is possible.
			want := "heap"
			if pl.Leaf.Free[0].size() >= pl.Size {
				want = "free list"
			}
			if pl.Place != want {
				t.Errorf("place %q with a free record of %d bytes for %d", pl.Place, pl.Leaf.Free[0].size(), pl.Size)
			}
			if pl.FreeReorganized < pl.FreeNow {
				t.Errorf("compacting the page would free %d bytes, less than the %d it has", pl.FreeReorganized, pl.FreeNow)
			}
			if pl.Owner == nil || pl.Owner.NOwned == 0 {
				t.Errorf("owner = %+v, want a record that owns a directory slot", pl.Owner)
			}
			leaf := pl.Steps[len(pl.Steps)-1]
			if leaf.Level != 0 || leaf.PageNo != pl.Leaf.No || leaf.Found {
				t.Errorf("leaf step = %+v", leaf)
			}

			// 1225 is delete-marked but still there: a unique index reports it,
			// and the insert rewrites it rather than adding a record.
			pl, err = s.SimulateInsert(tbl, pk, "1225", nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.Dup == nil || !pl.Dup.Deleted() || pl.Place != "" {
				t.Errorf("1225: dup %v, place %q", pl.Dup, pl.Place)
			}
			pl, _ = s.SimulateInsert(tbl, pk, "1234", nil)
			if pl.Dup == nil || pl.Dup.Deleted() {
				t.Errorf("1234 is there and live, got dup %v", pl.Dup)
			}

			// Off both ends of the tree.
			pl, err = s.SimulateInsert(tbl, pk, "99999", nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.Next.Status != REC_STATUS_SUPREMUM || pl.Leaf.FIL.Next != FIL_NULL {
				t.Errorf("99999 goes before %s on page %d, whose next is %d", pl.Key(pl.Next), pl.Leaf.No, pl.Leaf.FIL.Next)
			}
			pl, err = s.SimulateInsert(tbl, pk, "0", nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.Prev.Status != REC_STATUS_INFIMUM || pl.Key(pl.Next) != "1" || pl.Size != pl.Next.size() {
				t.Errorf("0 goes between %s and %s, sized %d", pl.Key(pl.Prev), pl.Key(pl.Next), pl.Size)
			}

			// A gap lock on the record after the position blocks the insert; a
			// record-only lock does not.
			gap := []Lock{{Index: pk, PageNo: pl.Leaf.No, HeapNo: pl.Next.HeapNo, Mode: "X"}}
			if pl, _ = s.SimulateInsert(tbl, pk, "0", gap); pl.Blocked == nil {
				t.Error("a next-key lock on the record after the position does not block the insert")
			}
			gap[0].Mode = "S,REC_NOT_GAP"
			if pl, _ = s.SimulateInsert(tbl, pk, "0", gap); pl.Blocked != nil {
				t.Error("a record-only lock blocks the insert")
			}

			// A non-unique index takes a second copy of a key, after the first.
			sec := tbl.Indexes[1]
			dup := fieldValue(pl.Next, sec.Cols[0].Name)
			pl, err = s.SimulateInsert(tbl, sec, dup, nil)
			if err != nil {
				t.Fatal(err)
			}
			if pl.Dup != nil {
				t.Errorf("%s is not unique but reports a duplicate", sec.Name)
			}
			if got := recKey(pl.Prev); got != dup {
				t.Errorf("the copy of %s goes after %s", dup, got)
			}

			// A duplicate waits for an X lock on the record it found.
			pl, _ = s.SimulateInsert(tbl, pk, "1234", nil)
			x := []Lock{{Index: pk, PageNo: pl.Leaf.No, HeapNo: pl.Dup.HeapNo, Mode: "X,REC_NOT_GAP"}}
			if pl, _ = s.SimulateInsert(tbl, pk, "1234", x); pl.Blocked == nil {
				t.Error("the duplicate check does not wait for the X lock on the record")
			}
			x[0].Mode = "X,GAP"
			if pl, _ = s.SimulateInsert(tbl, pk, "1234", x); pl.Blocked != nil {
				t.Error("a gap lock blocks the duplicate check")
			}
		})
	}
}

// TestInsertSplit forces a record too big for its page and checks the cut
// under each insert pattern the page header can show.
func TestInsertSplit(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "types")
			pk := tbl.Indexes[0]
			pl, err := s.SimulateInsert(tbl, pk, "1250", nil)
			if err != nil {
				t.Fatal(err)
			}
			pl.Size = pageEmptyFree / 2
			sup := len(pl.Leaf.Recs) - 1
			prev, next := pl.index(pl.Prev), pl.index(pl.Next)
			parent := pl.Steps[len(pl.Steps)-2].PageNo

			// No pattern: the middle record starts the new page on the right.
			pl.Leaf.Hdr.LastInsert = 0
			pl.place()
			sp := pl.Split
			if pl.Place != "split" || sp == nil {
				t.Fatalf("place %q", pl.Place)
			}
			mid := (sup + 1) / 2
			if sp.At != pl.Leaf.Recs[mid] || !sp.Right || sp.Moved != sup-mid || sp.Parent != parent || sp.Root {
				t.Errorf("middle split = %+v, want at record %d of %d, %d moved, parent %d", sp, mid, sup-1, sup-mid, parent)
			}
			if sp.InsertLeft != (mid >= next) {
				t.Errorf("insert left %v with the cut at %d and the new record before %d", sp.InsertLeft, mid, next)
			}

			// An ascending run: the cut is two records after the new one.
			pl.Leaf.Hdr.LastInsert = uint16(pl.Prev.Off)
			pl.place()
			sp = pl.Split
			if sp.At != pl.Leaf.Recs[next+1] || !sp.Right || sp.Moved != sup-next-1 || !sp.InsertLeft {
				t.Errorf("ascending split = %+v, want at record %d, %d moved, insert left", sp, next+1, sup-next-1)
			}

			// A descending run: the record before the new one goes with the
			// lower half to a new page on the left.
			pl.Leaf.Hdr.LastInsert = uint16(pl.Next.Off)
			pl.place()
			sp = pl.Split
			if sp.At != pl.Prev || sp.Right || sp.Moved != prev-1 || sp.InsertLeft {
				t.Errorf("descending split = %+v, want at record %d, %d moved left, insert right", sp, prev, prev-1)
			}

			// The reserve rule: a run on a clustered leaf splits while a
			// sixteenth of the page is still free.
			pl.Size = 100
			pl.Leaf.Hdr.LastInsert = uint16(pl.Prev.Off)
			pl.place()
			if want := spaceReserve+pl.Size > pl.FreeReorganized; (pl.Place == "split") != want {
				t.Errorf("place %q with %d free and a run; the reserve rule says split=%v", pl.Place, pl.FreeReorganized, want)
			}
			pl.Clustered = false
			pl.place()
			if pl.Place == "split" {
				t.Errorf("a secondary index leaf keeps no reserve, yet %d bytes split it with %d free", pl.Size, pl.FreeReorganized)
			}
		})
	}
}
