package tui

import (
	"fmt"
	"strings"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// recRef is a record on a page: Enter opens the page with it selected.
type recRef struct {
	no  uint32
	off int
}

// clustRef is the primary key a secondary index record carries: Enter looks it
// up in the clustered index, the second tree read a query that needs more than
// the index makes.
type clustRef struct {
	ix  *innodb.IndexDef
	key string
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

// findWizard is the `f` picker, and the `i` one: the index is chosen from a
// popup, then the key is typed.
type findWizard struct {
	picker
	ix     *innodb.IndexDef
	insert bool // `i`: simulate an insert of the key rather than look it up
}

// verb is the word the picker title and the footer use for what it does.
func (w *findWizard) verb() string {
	if w.insert {
		return "INSERT"
	}
	return "FIND"
}

// findChoice is one index row: Enter picks it and asks for the key.
type findChoice struct{ ix *innodb.IndexDef }

// startFind opens the picker over the page tree, with the index the cursor
// was in already selected.
func (m *Model) startFind(insert bool) {
	if m.space == nil || m.table == nil {
		m.status.err = "no index to search: this tablespace has no table definition"
		return
	}
	w := &findWizard{insert: insert}
	w.title = w.verb() + "  choose the index"
	note, hkey := "look the key up in this B+tree; the key is its first column", "find index"
	if insert {
		note, hkey = "put a record with the key into this B+tree; the key is its first column", "insert index"
	}
	root := &node{}
	cur := 0
	for i, ix := range m.table.Indexes {
		if ix == m.currentIndex() {
			cur = i
		}
		root.children = append(root.children, &node{label: ix.Name + "  (" + ix.Cols[0].Name + ")",
			note: note, hkey: hkey, data: findChoice{ix}})
	}
	w.list = newList(root)
	w.list.cur = cur
	m.findWiz = w
}

// findPick takes the chosen index and asks for the key.
func (m *Model) findPick(c findChoice) {
	if m.findWiz == nil {
		return
	}
	m.findWiz.ix = c.ix
	m.findWiz.title = m.findWiz.verb() + "  " + c.ix.Name + " (" + c.ix.Cols[0].Name + ")"
	m.prompt = prompt{kind: promptFind}
}

// findBack is esc: from the key back to the index list, from the list back
// out of the picker.
func (m *Model) findBack() {
	w := m.findWiz
	if w.ix != nil {
		w.ix, w.title = nil, w.verb()+"  choose the index"
		return
	}
	m.findWiz = nil
}

// findKey runs the lookup, or the insert, the picker was set up for.
func (m *Model) findKey(key string) {
	w := m.findWiz
	if w == nil || w.ix == nil {
		return
	}
	if key = strings.TrimSpace(key); key == "" {
		m.status.err = "a key is needed"
		m.prompt = prompt{kind: promptFind}
		return
	}
	m.findWiz = nil
	if w.insert {
		m.insertIn(w.ix, key)
		return
	}
	m.findIn(w.ix, key)
}

// findIn is the lookup itself, for a tree the caller already knows.
func (m *Model) findIn(ix *innodb.IndexDef, key string) {
	if m.space == nil {
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
	m.status.err = ""
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

// addClusterLinks hangs a jump to the clustered index under every leaf record of
// a secondary index. Such a record stores its key columns and the primary key,
// and nothing else, so the PK is the whole of what the row itself is found with.
func (m *Model) addClusterLinks(tree *node, p *innodb.Page) {
	if m.space == nil || m.pageSpace != m.space.ID || p.FIL.Type != innodb.FIL_PAGE_INDEX {
		return
	}
	ix := indexFor(m.pageTable, p)
	if ix == nil || ix == m.pageTable.Indexes[0] {
		return
	}
	clust := m.pageTable.Indexes[0]
	// Node pointer records of a secondary index carry the PK too, but the row
	// they lead to is on a leaf, so only a leaf record gets the link.
	if ip, err := p.ParseIndex(nil); err != nil || ip.Hdr.Level > 0 {
		return
	}
	// A record on the free list still holds the key it was written with, so it
	// gets the link too: what the lookup finds is whether the row outlived the
	// split or purge that freed these bytes.
	for _, sec := range tree.children {
		if sec.label != "Records" && sec.label != "PAGE_FREE list" {
			continue
		}
		for _, rec := range sec.children {
			in, ok := rec.data.(*innodb.Node)
			if !ok {
				continue
			}
			if vals := clustKey(in, clust); len(vals) > 0 {
				rec.children = append(rec.children, clustNode(clust, vals))
			}
		}
	}
}

// clustKey is what a record holds for the key columns of the clustered index,
// in key order. Nil when it does not hold all of them, which is how infimum and
// supremum are skipped.
func clustKey(rec *innodb.Node, clust *innodb.IndexDef) []colVal {
	var out []colVal
	for _, c := range clust.Cols[:clust.NUniqueInTree] {
		f := field(rec, c.Name)
		if f == nil {
			return nil
		}
		out = append(out, colVal{c.Name, oneLine(f.Value)})
	}
	return out
}

func field(rec *innodb.Node, name string) *innodb.Node {
	for _, c := range rec.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// clustNode is the link row. The descent it starts narrows on the first key
// column only, so a composite primary key reaches the right leaf page but may
// stop on a neighbouring record; the row shows every column to compare against.
func clustNode(clust *innodb.IndexDef, vals []colVal) *node {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v.name + "=" + v.value
	}
	return &node{label: "clustered index", icon: ic.index, tag: "clustered index", color: colIndex,
		hkey: "clustered index", value: clust.Name + "  " + strings.Join(parts, "  "),
		data: clustRef{ix: clust, key: strings.Trim(vals[0].value, `"`)}}
}
