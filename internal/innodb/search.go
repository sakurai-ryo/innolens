package innodb

import (
	"fmt"
	"strconv"
	"strings"
)

// maxTreeHeight bounds the descent so that a corrupt node pointer cannot loop.
const maxTreeHeight = 16

// DescentStep is one page a search visited on the way from the root to the leaf
// that would hold the key.
type DescentStep struct {
	PageNo uint32
	Level  uint16
	NRecs  uint16
	// Slot is the record the search picked among the user records of the page,
	// and Key is its first key column. Slot is -1 when the page holds none.
	Slot  int
	Key   string
	Child uint32 // page the search descended into; 0 on a leaf
	// Leaf only: whether the key is on the page, and where the record it stopped
	// at starts. A miss stops at the record the key would sort before.
	Found  bool
	RecOff int
}

// Descend walks idx from its root to the leaf that would hold key, recording
// every page it reads. This is a B+tree lookup: at each level it takes the last
// child whose first key does not sort after the one being looked for.
//
// Keys are compared on the first key column, as the value the column decodes
// to: numerically when both sides parse as numbers, otherwise as text. A
// multi-column key is only narrowed down to its first column, and a collation
// that does not sort like Go's string compare will disagree on the boundary.
func (s *Space) Descend(idx *IndexDef, key string) ([]DescentStep, error) {
	if idx == nil {
		return nil, fmt.Errorf("no index definition to search with")
	}
	var out []DescentStep
	for no := idx.RootPage; ; {
		p, err := s.Page(no)
		if err != nil {
			return out, err
		}
		ip, err := p.ParseIndex(idx)
		if err != nil {
			return out, fmt.Errorf("page %d: %w", no, err)
		}
		st := DescentStep{PageNo: no, Level: ip.Hdr.Level, NRecs: ip.Hdr.NRecs, Slot: -1}
		recs := ip.UserRecs()
		if ip.Hdr.Level == 0 {
			for i, rec := range recs {
				c := compareKey(key, recKey(rec))
				if c > 0 {
					continue
				}
				st.Slot, st.Key, st.RecOff, st.Found = i, recKey(rec), rec.Off, c == 0
				break
			}
			out = append(out, st)
			if !st.Found {
				return out, fmt.Errorf("no record with key %q in %s", key, idx.Name)
			}
			return out, nil
		}
		for i, rec := range recs {
			// The first record of a non-leaf page carries the min_rec flag: it
			// stands for every key below the second child, whatever it stores.
			if i > 0 && compareKey(key, recKey(rec)) < 0 {
				break
			}
			st.Slot, st.Key, st.Child = i, recKey(rec), rec.Child
		}
		if st.Slot < 0 {
			out = append(out, st)
			return out, fmt.Errorf("page %d has no node pointer to follow", no)
		}
		out = append(out, st)
		if len(out) >= maxTreeHeight {
			return out, fmt.Errorf("the tree is deeper than %d levels: the node pointers loop", maxTreeHeight)
		}
		no = st.Child
	}
}

// recKey is the first key column of a record, as the text it decodes to.
func recKey(r *Rec) string {
	if len(r.Fields) == 0 {
		return ""
	}
	return strings.Trim(r.Fields[0].Value, `"`)
}

// compareKey orders the key being looked for against a key on a page.
func compareKey(want, got string) int {
	a, err1 := strconv.ParseFloat(want, 64)
	b, err2 := strconv.ParseFloat(got, 64)
	if err1 == nil && err2 == nil {
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	}
	return strings.Compare(want, got)
}
