package tui

import (
	"fmt"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// recRef is a record on a page: Enter opens the page with it selected.
type recRef struct {
	no  uint32
	off int
}

// indexRef marks the row that heads one B+tree, so that a search started
// anywhere inside it knows which tree to walk.
type indexRef struct{ ix *innodb.IndexDef }

// searchable says whether the right pane is showing B+trees a key can be looked
// up in. The redo pane is not one, even while a table is still open behind it.
func (m *Model) searchable() bool {
	return m.space != nil && m.table != nil && m.pagesTitle != "REDO"
}

// currentIndex is the B+tree the cursor is in, falling back to the clustered
// index when it is somewhere else in the page tree.
func (m *Model) currentIndex() *innodb.IndexDef {
	for i := m.pages.cur; i >= 0 && i < len(m.pages.rows); i-- {
		switch d := m.pages.rows[i].n.data.(type) {
		case indexRef:
			return d.ix
		case pageRef:
			if d.ix != nil {
				return d.ix
			}
		}
	}
	if m.table != nil && len(m.table.Indexes) > 0 {
		return m.table.Indexes[0]
	}
	return nil
}

// findKey replaces the page tree with the descent a lookup for key makes: one
// row per page read, root first, ending on the record it stopped at.
func (m *Model) findKey(key string) {
	if key == "" || m.space == nil {
		return
	}
	ix := m.currentIndex()
	if ix == nil {
		m.status.err = "no index to search: this tablespace has no table definition"
		return
	}
	steps, err := m.space.Descend(ix, key)
	root := &node{}
	for i, st := range steps {
		root.children = append(root.children, descentNode(st, i))
	}
	if err != nil {
		root.children = append(root.children, errNode(err.Error()))
	}
	m.pages = newList(root)
	m.pagesTitle = fmt.Sprintf("FIND %s IN %s", key, ix.Name)
	m.focus = focusPages
	m.status.info = fmt.Sprintf("%d page(s) read to look up %s", len(steps), key)
}

// descentNode is one page of the descent: which record it picked and where that
// led. The leaf row opens the page with the record it stopped at selected.
func descentNode(st innodb.DescentStep, depth int) *node {
	tag := indexTag(int(st.Level))
	label := fmt.Sprintf("level %d  page %d  %s  recs %d", st.Level, st.PageNo, tag, st.NRecs)
	n := &node{label: label, icon: ic.page, tag: tag, color: pageColor(innodb.FIL_PAGE_INDEX, int(st.Level)),
		hkey: "descent", data: pageRef{no: st.PageNo}}
	switch {
	case st.Level > 0 && st.Slot >= 0:
		n.note = fmt.Sprintf("record %d (key %s) → page %d", st.Slot, st.Key, st.Child)
	case st.Found:
		n.note = fmt.Sprintf("record @%04x  key %s", st.RecOff, st.Key)
		n.data = recRef{no: st.PageNo, off: st.RecOff}
	case st.Slot >= 0:
		n.note = fmt.Sprintf("not here: the key would sort before record %d (key %s)", st.Slot, st.Key)
	default:
		n.note = "not here: the page holds no user record"
	}
	return n
}
