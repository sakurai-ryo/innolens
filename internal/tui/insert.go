package tui

import (
	"fmt"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// insertIn simulates an INSERT of key into ix and replaces the page tree with
// the descent to the leaf, then what the insert does to it: the free space it
// takes, the free list or heap it takes it from, or the split it forces. The
// locks of the last `l` are what it may have to wait for.
func (m *Model) insertIn(ix *innodb.IndexDef, key string) {
	if m.space == nil {
		return
	}
	var locks []innodb.Lock
	if m.locks != nil {
		locks = m.locks.locks
	}
	pl, err := m.space.SimulateInsert(m.table, ix, key, locks)
	root := &node{}
	if err != nil {
		if pl != nil {
			for i, st := range pl.Steps {
				root.children = append(root.children, descentNode(st, i))
			}
		}
		root.children = append(root.children, errNode(err.Error()))
	} else {
		for i, st := range pl.Steps[:len(pl.Steps)-1] {
			root.children = append(root.children, descentNode(st, i))
		}
		root.children = append(root.children, insertNodes(pl)...)
	}
	m.pages = newList(root)
	m.pagesTitle = fmt.Sprintf("INSERT %s INTO %s", key, ix.Name)
	m.focus = focusPages
	m.status.err = ""
	if pl != nil {
		m.status.info = fmt.Sprintf("%d page(s) read to place %s", len(pl.Steps), key)
	}
}

// insertNodes is the leaf and what happens on it, one row each.
func insertNodes(pl *innodb.InsertPlan) []*node {
	st := pl.Steps[len(pl.Steps)-1]
	leaf := descentNode(st, len(pl.Steps)-1)
	leaf.data = recRef{no: pl.Leaf.No, off: pl.Next.Off}
	if pl.Prev.Status == innodb.REC_STATUS_ORDINARY {
		leaf.data = recRef{no: pl.Leaf.No, off: pl.Prev.Off}
	}
	out := []*node{leaf}
	add := func(label, value, note string) *node {
		n := &node{label: label, value: value, note: note, hkey: "insert", icon: ic.page, tag: "insert", color: colIndex}
		out = append(out, n)
		return n
	}
	if pl.Dup != nil {
		leaf.note = fmt.Sprintf("record @%04x already has key %s", pl.Dup.Off, pl.Key(pl.Dup))
		leaf.data = recRef{no: pl.Leaf.No, off: pl.Dup.Off}
		n := add("duplicate key", "S lock on the record, then ER_DUP_ENTRY",
			"a unique index checks the key first: next-key under REPEATABLE READ, record-only under READ COMMITTED")
		n.color = colDanger
		if pl.Dup.Deleted() {
			n.value = "the record is delete-marked: it is rewritten in place, no record is added"
			n.note = "row_ins_clust_index_entry_by_modify: the delete mark comes off and the columns are updated, the old version going to undo"
			n.color = colIndex
		}
		return out
	}
	leaf.note = fmt.Sprintf("between %s and %s  as %d bytes", recLabel(pl, pl.Prev), recLabel(pl, pl.Next), pl.Size)
	if pl.Blocked != nil {
		n := add("waits for a lock", fmt.Sprintf("%s on heap_no %d (key %s): insert intention lock queued",
			pl.Blocked.Mode, pl.Blocked.HeapNo, pl.Blocked.Key),
			"a gap lock on the record after the position covers where the new one goes; the insert waits until it is released")
		n.color = colDanger
	}
	h := pl.Leaf.Hdr
	free := fmt.Sprintf("free %d bytes now, %d if reorganized", pl.FreeNow, pl.FreeReorganized)
	switch pl.Place {
	case "free list":
		f := pl.Leaf.Free[0]
		add("reuses the free list", fmt.Sprintf("record @%04x, %d bytes, heap_no %d kept", f.Off, f.End-f.Off+f.Extra, f.HeapNo),
			"the head of PAGE_FREE is big enough; the bytes it had over stay lost until a reorganize  ("+free+")")
	case "heap":
		add("takes the heap", fmt.Sprintf("PAGE_HEAP_TOP %d → %d, heap_no %d", h.HeapTop, int(h.HeapTop)+pl.Size, h.NHeap),
			"nothing on the free list fits, or it is empty, so the bytes come off the top  ("+free+")")
	case "reorganize":
		add("reorganizes first", fmt.Sprintf("PAGE_GARBAGE %d reclaimed, then the heap", h.Garbage),
			"the free space is there but scattered: the page is rebuilt from its records, then the record goes on top  ("+free+")")
	case "split":
		return append(out, splitNodes(pl)...)
	}
	own := fmt.Sprintf("record @%04x n_owned %d → %d", pl.Owner.Off, pl.Owner.NOwned, pl.Owner.NOwned+1)
	if pl.Owner.Status == innodb.REC_STATUS_SUPREMUM {
		own = fmt.Sprintf("supremum n_owned %d → %d", pl.Owner.NOwned, pl.Owner.NOwned+1)
	}
	note := "the new record joins the directory slot of the next record that owns one"
	if pl.SlotSplit {
		own += ": the slot splits in two"
		note = "a slot owns 8 records at most: past that it is split, and every slot after it moves down"
	}
	add("page directory", own, note)
	dir := fmt.Sprintf("PAGE_DIRECTION %s, PAGE_N_DIRECTION %d → %d", innodb.DirectionName(pl.Direction), h.NDirection, pl.NDirection)
	switch pl.Direction {
	case innodb.PAGE_RIGHT:
		add("insert pattern", dir, "right after the previous insert: the page counts an ascending run, which decides where a split cuts")
	case innodb.PAGE_LEFT:
		add("insert pattern", dir, "right before the previous insert: the page counts a descending run, which decides where a split cuts")
	default:
		add("insert pattern", dir, "not next to the previous insert: the run count resets, and a split would cut at the middle")
	}
	return out
}

// splitNodes is the split: the cut, the move, and the node pointer that
// registers the new page with the parent.
func splitNodes(pl *innodb.InsertPlan) []*node {
	sp := pl.Split
	var out []*node
	add := func(label, value, note string) *node {
		n := &node{label: label, value: value, note: note, hkey: "insert split", icon: ic.page, tag: "split", color: colDanger}
		out = append(out, n)
		return n
	}
	add("page split", fmt.Sprintf("%d bytes do not fit: free %d now, %d if reorganized", pl.Size, pl.FreeNow, pl.FreeReorganized),
		"btr_cur_optimistic_insert gives up and btr_page_split_and_insert cuts the page in two")
	if sp.Root {
		add("root raised", fmt.Sprintf("page %d keeps one node pointer; its records move to a new page, which is then split", pl.Leaf.No),
			"the root cannot move, so the tree grows a level: the root becomes a node page above the two halves")
	}
	side, stays := "right", "lower"
	if !sp.Right {
		side, stays = "left", "upper"
	}
	cut := "at the new record"
	if sp.At != nil {
		cut = fmt.Sprintf("at record @%04x (key %s)", sp.At.Off, pl.Key(sp.At))
	}
	n := add("cut "+cut, fmt.Sprintf("%d record(s) move to the new page on the %s", sp.Moved, side), sp.Why)
	if sp.At != nil {
		n.data = recRef{no: pl.Leaf.No, off: sp.At.Off}
	}
	where := "the new page"
	if sp.InsertLeft == sp.Right {
		where = fmt.Sprintf("page %d", pl.Leaf.No)
	}
	half := "lower"
	if !sp.InsertLeft {
		half = "upper"
	}
	add("new record", fmt.Sprintf("goes to %s, the %s half", where, half),
		fmt.Sprintf("the %s half stays on page %d; which half the key sorts into decides", stays, pl.Leaf.No))
	key := "the new key"
	if sp.At != nil {
		key = "key " + pl.Key(sp.At)
	}
	n = add("node pointer", fmt.Sprintf("%s → parent page %d", key, sp.Parent),
		"the parent gets a record for the upper half, its first key and page number; a full parent splits the same way")
	n.data = pageRef{no: sp.Parent}
	return out
}

// recLabel names a neighbour of the new record: its key, or the pseudo-record.
func recLabel(pl *innodb.InsertPlan, r *innodb.Rec) string {
	if r.Status != innodb.REC_STATUS_ORDINARY {
		return pl.Key(r)
	}
	return fmt.Sprintf("@%04x (key %s)", r.Off, pl.Key(r))
}
