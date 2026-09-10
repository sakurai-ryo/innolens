package innodb

import (
	"strings"
	"testing"
)

// TestDescend looks a key up the way the tree is meant to be read: down the
// node pointers, one page per level, ending on the leaf that holds the record.
func TestDescend(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "types")
			pk := tbl.Indexes[0]

			steps, err := s.Descend(pk, "1234")
			if err != nil {
				t.Fatalf("descend: %v", err)
			}
			if len(steps) < 2 {
				t.Fatalf("the tree is %d level(s) deep, expected a root and a leaf", len(steps))
			}
			if steps[0].PageNo != pk.RootPage {
				t.Errorf("started at page %d, want the root %d", steps[0].PageNo, pk.RootPage)
			}
			for i, st := range steps[:len(steps)-1] {
				if st.Level != steps[i+1].Level+1 {
					t.Errorf("step %d is level %d, the next is level %d", i, st.Level, steps[i+1].Level)
				}
				if st.Child != steps[i+1].PageNo {
					t.Errorf("step %d descends into page %d but the next step read page %d",
						i, st.Child, steps[i+1].PageNo)
				}
			}
			leaf := steps[len(steps)-1]
			if !leaf.Found || leaf.Key != "1234" {
				t.Fatalf("leaf step = %+v, want the record with key 1234", leaf)
			}
			p, err := s.Page(leaf.PageNo)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := parseRec(p.Data, leaf.RecOff, pk)
			if err != nil {
				t.Fatal(err)
			}
			if got := fields(rec)["id"]; got != "1234" {
				t.Errorf("the record at the offset the search returned has id %q", got)
			}

			// The fixture purges every 50th row, so this key is not in the tree.
			if _, err := s.Descend(pk, "1250"); err == nil {
				t.Error("descend found a purged row")
			}

			// A secondary index is searched by its own first key column, which
			// is a string here rather than a number.
			var sec *IndexDef
			for _, ix := range tbl.Indexes {
				if ix.Name == "idx_varchar" {
					sec = ix
				}
			}
			if sec == nil {
				t.Fatal("the fixture has no idx_varchar")
			}
			steps, err = s.Descend(sec, "varchar-1234-"+strings.Repeat("x", 1234%20))
			if err != nil {
				t.Fatalf("descend %s: %v", sec.Name, err)
			}
			if last := steps[len(steps)-1]; !last.Found {
				t.Errorf("%s lookup ended at %+v", sec.Name, last)
			}
		})
	}
}
