package innodb

import (
	"fmt"
	"strings"
)

// Page directory and free-space arithmetic of page0page.ic.
const (
	PAGE_NEW_SUPREMUM_END     = PAGE_NEW_SUPREMUM + 8
	PAGE_DIR_SLOT_MIN_N_OWNED = 4
	PAGE_DIR_SLOT_MAX_N_OWNED = 8
	PAGE_LEFT                 = 1
	PAGE_RIGHT                = 2
	PAGE_NO_DIRECTION         = 5
	// pageEmptyFree is page_get_free_space_of_empty: what an empty page has
	// for records, once the headers, infimum, supremum and their two
	// directory slots are taken out.
	pageEmptyFree = PageSize - PAGE_NEW_SUPREMUM_END - FIL_PAGE_DATA_END - 2*PAGE_DIR_SLOT_SIZE
	// btrPageReorganizeLimit is BTR_CUR_PAGE_REORGANIZE_LIMIT: below this much
	// free space a reorganize is not worth trying before a split.
	btrPageReorganizeLimit = PageSize / 32
	// spaceReserve is dict_index_get_space_reserve: what a run of inserts
	// leaves free on a clustered index leaf for the updates to come.
	spaceReserve = PageSize / 16
)

// dirReserved is page_dir_calc_reserved_space: the directory slots n records
// need at the minimum of 4 records per slot, which is what a fill check
// charges them.
func dirReserved(n int) int {
	return (PAGE_DIR_SLOT_SIZE*n + PAGE_DIR_SLOT_MIN_N_OWNED - 1) / PAGE_DIR_SLOT_MIN_N_OWNED
}

// maxInsertSize is page_get_max_insert_size(page, n): the bytes n new records
// can take from the heap as it stands.
func (h IndexHeader) maxInsertSize(n int) int {
	occupied := int(h.HeapTop) - PAGE_NEW_SUPREMUM_END + dirReserved(n+int(h.NHeap)-2)
	return max(pageEmptyFree-occupied, 0)
}

// maxInsertSizeAfterReorganize is the same once the garbage is compacted away
// and the heap holds nothing but the live records.
func (h IndexHeader) maxInsertSizeAfterReorganize(n int) int {
	occupied := int(h.HeapTop) - PAGE_NEW_SUPREMUM_END - int(h.Garbage) + dirReserved(n+int(h.NRecs))
	return max(pageEmptyFree-occupied, 0)
}

func (r *Rec) size() int { return r.End - r.Off + r.Extra }

// InsertPlan is what an INSERT of key into idx would do to the tree, worked
// out from the pages as btr_cur_optimistic_insert, page_cur_insert_rec_low and
// btr_page_split_and_insert would.
type InsertPlan struct {
	Steps     []DescentStep
	Leaf      *IndexPage
	Clustered bool
	// The new record goes between Prev and Next: infimum and supremum at the
	// ends of the page.
	Prev, Next *Rec
	// Size is the bytes the record is assumed to take: as many as Prev, or
	// Next when Prev is the infimum. Only the key is typed, so the other
	// columns are sized by their neighbour.
	Size int
	// Dup is the record that already holds the key on a single-column unique
	// index. When it is delete-marked the insert rewrites it in place instead
	// of adding a record, so nothing below applies.
	Dup *Rec
	// Blocked is the lock, among those the caller passed in, the insert
	// would wait for: a gap lock on Next that its insert intention lock
	// conflicts with, or an X lock on Dup that the S lock of the duplicate
	// check does.
	Blocked *Lock
	// Place is where the bytes go: "free list" reuses the head of PAGE_FREE,
	// "heap" takes them from the top, "reorganize" compacts the page first,
	// "split" moves half the records to a new page.
	Place string
	// FreeNow is what the heap can give now, FreeReorganized once compacted:
	// both as page_get_max_insert_size charges a record, directory slot included.
	FreeNow, FreeReorganized int
	// Owner is the record whose directory slot the new one falls under, and
	// SlotSplit whether that slot then holds too many and is split in two.
	Owner     *Rec
	SlotSplit bool
	// Direction and NDirection are what the page header records about the
	// insert pattern afterwards.
	Direction  uint16
	NDirection uint16
	Split      *SplitPlan
}

// SplitPlan is btr_page_split_and_insert's choice: where the page is cut and
// which half the new record lands in.
type SplitPlan struct {
	// Right is FSP_UP: the new page is allocated after this one and takes the
	// upper half. Otherwise it takes the lower half and sits before it.
	Right bool
	// At is the first record of the upper half. Nil means the new record
	// itself is: everything on the page stays where it is.
	At *Rec
	// Moved is how many records the new page receives, the new one aside.
	Moved int
	// InsertLeft is whether the new record goes into the lower half.
	InsertLeft bool
	// Root is set when the leaf is the root of a one-page tree: the records
	// move to a fresh page first and the root keeps one node pointer, so the
	// tree grows a level before the split below.
	Root bool
	// Parent is the page the node pointer for the upper half is inserted
	// into; the root itself when Root is set.
	Parent uint32
	Why    string
}

// SimulateInsert works out where a record with key would go in idx. locks are
// the record locks another statement holds, as SimulateLocks lists them: an
// insert has to wait for any gap lock on the record after its position.
func (s *Space) SimulateInsert(t *Table, idx *IndexDef, key string, locks []Lock) (*InsertPlan, error) {
	if t == nil || len(t.Indexes) == 0 || idx == nil {
		return nil, fmt.Errorf("no index definition to insert into")
	}
	steps, leaf, pos, err := s.descend(idx, key, false)
	if err != nil {
		return &InsertPlan{Steps: steps}, err
	}
	recs := leaf.UserRecs()
	pl := &InsertPlan{Steps: steps, Leaf: leaf, Clustered: idx == t.Indexes[0],
		Prev: leaf.Recs[0], Next: leaf.Recs[len(leaf.Recs)-1]}
	st := &pl.Steps[len(pl.Steps)-1]
	if pos < len(recs) {
		st.Slot, st.Key, st.RecOff = pos, recKey(recs[pos]), recs[pos].Off
		st.Found = compareKey(key, st.Key) == 0
	}
	// Only a whole key can be a duplicate; on a composite one the typed
	// column may be shared by rows that differ in the rest.
	if st.Found && idx.Unique && idx.NKey == 1 {
		pl.Dup = recs[pos]
		pl.Blocked = lockOn(locks, idx, leaf.No, pl.Dup.HeapNo, func(mode string) bool {
			return strings.HasPrefix(mode, "X") && !strings.HasSuffix(mode, ",GAP")
		})
		return pl, nil
	}
	// Equal keys on a non-unique index are told apart by the primary key
	// they carry, which is not typed: the new one is put after them.
	for pos < len(recs) && compareKey(key, recKey(recs[pos])) == 0 {
		pos++
	}
	if pos > 0 {
		pl.Prev = recs[pos-1]
	}
	if pos < len(recs) {
		pl.Next = recs[pos]
	}
	pl.Blocked = lockOn(locks, idx, leaf.No, pl.Next.HeapNo, func(mode string) bool {
		return !strings.HasSuffix(mode, ",REC_NOT_GAP")
	})
	switch {
	case pl.Prev.Status == REC_STATUS_ORDINARY:
		pl.Size = pl.Prev.size()
	case pl.Next.Status == REC_STATUS_ORDINARY:
		pl.Size = pl.Next.size()
	default:
		return pl, fmt.Errorf("page %d holds no record to size the new one by", leaf.No)
	}
	pl.place()
	return pl, nil
}

// lockOn is the first of locks on the given record whose mode conflicts.
func lockOn(locks []Lock, idx *IndexDef, page uint32, heapNo uint16, conflicts func(mode string) bool) *Lock {
	for i := range locks {
		l := &locks[i]
		if l.Index.ID == idx.ID && l.PageNo == page && l.HeapNo == heapNo && conflicts(l.Mode) {
			return l
		}
	}
	return nil
}

// place is btr_cur_optimistic_insert's decision. The page is split when a
// compacted page could not take the record; with garbage on the page the
// compaction is skipped, and the split taken, when it would gain too little.
// A run of inserts on a clustered index leaf splits early, keeping a
// sixteenth of the page for the updates that follow. Otherwise
// page_cur_insert_rec_low takes the head of the free list if it is big
// enough, then the heap, and a reorganize is the fallback when the heap alone
// is too fragmented.
func (pl *InsertPlan) place() {
	h := pl.Leaf.Hdr
	pl.FreeNow, pl.FreeReorganized = h.maxInsertSize(1), h.maxInsertSizeAfterReorganize(1)
	run := pl.run()
	switch {
	case h.Garbage > 0 && (pl.FreeReorganized < pl.Size || pl.FreeReorganized < btrPageReorganizeLimit) && h.NRecs > 1 && pl.FreeNow < pl.Size,
		h.Garbage == 0 && pl.FreeReorganized < pl.Size,
		pl.Clustered && h.NRecs >= 2 && spaceReserve+pl.Size > pl.FreeReorganized && run != 0:
		pl.Place = "split"
		pl.split(run)
		return
	case len(pl.Leaf.Free) > 0 && pl.Leaf.Free[0].size() >= pl.Size:
		pl.Place = "free list"
	case pl.FreeNow >= pl.Size:
		pl.Place = "heap"
	default:
		// A reorganize rebuilds the directory and the insert pattern
		// before the record goes in, so neither is worked out from the
		// page as it stands.
		pl.Place = "reorganize"
		return
	}
	// The record joins the slot of the next record that owns one; the
	// supremum always does, so the walk ends there unless the record
	// chain never reached it.
	for i := pl.index(pl.Next); i < len(pl.Leaf.Recs); i++ {
		if pl.Leaf.Recs[i].NOwned > 0 {
			pl.Owner = pl.Leaf.Recs[i]
			break
		}
	}
	pl.SlotSplit = pl.Owner != nil && int(pl.Owner.NOwned)+1 > PAGE_DIR_SLOT_MAX_N_OWNED
	switch {
	case h.LastInsert == 0:
		pl.Direction = PAGE_NO_DIRECTION
	case run == PAGE_RIGHT && h.Direction != PAGE_LEFT:
		pl.Direction, pl.NDirection = PAGE_RIGHT, h.NDirection+1
	case run == PAGE_LEFT && h.Direction != PAGE_RIGHT:
		pl.Direction, pl.NDirection = PAGE_LEFT, h.NDirection+1
	default:
		pl.Direction = PAGE_NO_DIRECTION
	}
}

// run is the insert pattern PAGE_LAST_INSERT shows: PAGE_RIGHT when the
// previous insert was the record before the new one, PAGE_LEFT when it was
// the one after, 0 when neither.
func (pl *InsertPlan) run() uint16 {
	switch {
	case pl.Leaf.Hdr.LastInsert == 0:
		return 0
	case int(pl.Leaf.Hdr.LastInsert) == pl.Prev.Off:
		return PAGE_RIGHT
	case int(pl.Leaf.Hdr.LastInsert) == pl.Next.Off:
		return PAGE_LEFT
	}
	return 0
}

func (pl *InsertPlan) index(r *Rec) int {
	for i, x := range pl.Leaf.Recs {
		if x == r {
			return i
		}
	}
	return -1
}

// split picks the cut the way btr_page_split_and_insert does on its first
// try: an ascending run is cut so the new page starts at or just after the
// new record, a descending run just before it, and anything else at the
// middle record.
func (pl *InsertPlan) split(run uint16) {
	sp := &SplitPlan{Right: true}
	pl.Split = sp
	if len(pl.Steps) > 1 {
		sp.Parent = pl.Steps[len(pl.Steps)-2].PageNo
	} else {
		// The records are copied to a fresh page first, which leaves it
		// without a PAGE_LAST_INSERT: the run the root showed is gone.
		sp.Root, sp.Parent, run = true, pl.Leaf.No, 0
	}
	recs := pl.Leaf.Recs
	prev, next := pl.index(pl.Prev), pl.index(pl.Next)
	sup := len(recs) - 1
	switch {
	case run == PAGE_RIGHT:
		// btr_page_get_split_rec_to_right keeps one record after the new
		// one behind, so that the run can check its position on the page
		// it is filling.
		sp.Why = "the previous insert was the record before it: an ascending run, so the new record starts the new page"
		if next+1 < sup {
			sp.At = recs[next+1]
			sp.Why = "the previous insert was the record before it: an ascending run, so the page is cut two records after the new one"
		}
	case run == PAGE_LEFT:
		// btr_page_get_split_rec_to_left takes the record before the new
		// one along too, unless it is the first, so that the run does not
		// move it from page to page.
		sp.Right, sp.At = false, pl.Next
		sp.Why = "the previous insert was the record after it: a descending run, so the new page takes what is before the new record"
		if prev > 1 {
			sp.At = pl.Prev
			sp.Why = "the previous insert was the record after it: a descending run, so the new page takes what is before the record before the new one"
		}
	case sup > 2:
		// page_get_middle_rec: the infimum counts as one of the lower half.
		sp.At = recs[(sup+1)/2]
		sp.Why = "no run of inserts on this page: cut at the middle record"
	case next < sup:
		sp.At = pl.Next
		sp.Why = "one record on the page and the new one sorts before it: the new page takes the new record"
	default:
		sp.Why = "one record on the page and the new one sorts after it: the new page takes the new record"
	}
	if sp.At == nil {
		sp.Moved = sup - 1 - prev
		return
	}
	at := pl.index(sp.At)
	if sp.Right {
		sp.Moved = sup - at
	} else {
		sp.Moved = at - 1
	}
	sp.InsertLeft = at >= next
}

// DirectionName is the PAGE_DIRECTION constant name for d.
func DirectionName(d uint16) string { return directionNames[d] }

// Key is the first key column of a record on the leaf, for labels.
func (pl *InsertPlan) Key(r *Rec) string {
	switch r.Status {
	case REC_STATUS_INFIMUM:
		return "infimum"
	case REC_STATUS_SUPREMUM:
		return "supremum"
	}
	return recKey(r)
}
