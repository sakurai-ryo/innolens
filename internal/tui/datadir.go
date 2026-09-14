package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

type tableRef struct{ path string }

// pageRef is a page of the open tablespace. ix is the B+tree it was reached
// through, which is what a key search started on this row walks; it is nil for
// the pages that belong to no tree.
type pageRef struct {
	no uint32
	ix *innodb.IndexDef
}

type redoRef struct{ dir string }

// pageJump is a redo record pointing at the page it modified.
type pageJump struct{ space, page uint32 }

// datadir is the left pane tree plus the space_id -> .ibd map that redo records
// are resolved through.
type datadir struct {
	root    *node
	spaces  map[uint32]string
	undo    map[uint8]string // undo space number, which is what a DB_ROLL_PTR carries
	redoDir string
}

// scanDatadir builds the left pane tree. The system tablespace is listed but
// not selectable: it has no SDI, and its pages are not what innolens shows.
func scanDatadir(dir string) (*datadir, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	d := &datadir{root: &node{}, spaces: map[uint32]string{}, undo: map[uint8]string{}}
	for _, e := range ents {
		name := e.Name()
		switch {
		case e.IsDir() && name == "#innodb_redo":
			d.redoDir = filepath.Join(dir, name)
			d.root.children = append(d.root.children, &node{label: name, icon: ic.redo, hkey: name, data: redoRef{d.redoDir}})
		case e.IsDir() && !strings.HasPrefix(name, "#"):
			if s := d.schemaNode(dir, name); s != nil {
				d.root.children = append(d.root.children, s)
			}
		case !e.IsDir() && undoTablespace(name):
			d.root.children = append(d.root.children, d.undoNode(filepath.Join(dir, name), name, entrySize(e)))
		case !e.IsDir() && strings.HasPrefix(name, "ibdata"):
			d.root.children = append(d.root.children, &node{label: name, icon: ic.system, note: entrySize(e), dim: true})
		case !e.IsDir() && strings.HasSuffix(name, ".ibd"):
			// mysql.ibd, or a general tablespace: many tables in one file.
			path := filepath.Join(dir, name)
			if id, err := innodb.SpaceID(path); err == nil {
				d.spaces[id] = path
			}
			d.root.children = append(d.root.children, &node{label: name, icon: ic.system, hkey: "shared tablespace", note: entrySize(e), data: tableRef{path}})
		}
	}
	if len(d.root.children) == 0 {
		return nil, fmt.Errorf("no tablespace found under %s", dir)
	}
	return d, nil
}

// undoTablespace matches the two implicit undo tablespaces and any explicit one
// created with CREATE UNDO TABLESPACE.
func undoTablespace(name string) bool {
	return strings.HasPrefix(name, "undo_") || strings.HasSuffix(name, ".ibu")
}

// undoNode registers the undo tablespace under its space number, which is how a
// DB_ROLL_PTR names it, and makes it selectable.
func (d *datadir) undoNode(path, name, size string) *node {
	n := &node{label: name, icon: ic.undo, hkey: "undo tablespace", note: size, data: tableRef{path}}
	id, err := innodb.SpaceID(path)
	if err != nil {
		return n
	}
	d.spaces[id] = path
	if num := innodb.UndoSpaceNum(id); num != 0 {
		d.undo[num] = path
	}
	return n
}

func entrySize(e os.DirEntry) string {
	fi, err := e.Info()
	if err != nil {
		return ""
	}
	n := fi.Size()
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (d *datadir) schemaNode(dir, schema string) *node {
	ents, err := os.ReadDir(filepath.Join(dir, schema))
	if err != nil {
		return nil
	}
	n := &node{label: schema, icon: ic.schema, hkey: "schema"}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".ibd") {
			continue
		}
		path := filepath.Join(dir, schema, name)
		if id, err := innodb.SpaceID(path); err == nil {
			d.spaces[id] = path
		}
		n.children = append(n.children, &node{
			label: strings.TrimSuffix(name, ".ibd"),
			icon:  ic.table,
			hkey:  "table",
			note:  entrySize(e),
			data:  tableRef{path},
		})
	}
	if len(n.children) == 0 {
		return nil
	}
	return n
}

// createTableNode heads the page tree with the table definition, folded: one
// line of CREATE TABLE per row once opened.
func createTableNode(t *innodb.Table) *node {
	n := &node{label: "CREATE TABLE " + t.Name, icon: ic.table, hkey: "CREATE TABLE",
		note: fmt.Sprintf("%d line(s), from the SDI", len(t.DDL))}
	for _, l := range t.DDL {
		n.children = append(n.children, &node{label: l, hkey: "CREATE TABLE"})
	}
	return n
}

// pageTree builds the right pane: one section per B+tree plus the flat list of
// non-index pages. Only the root of each tree is read up front. The tables of
// a shared tablespace each get a folded section of their own.
func pageTree(s *innodb.Space, ts []*innodb.Table) *node {
	root := &node{}
	if len(ts) == 1 {
		root.children = tableNodes(s, ts[0])
	}
	if len(ts) > 1 {
		for _, t := range ts {
			// The clustered index stands for the table, so the pickers know
			// which table the cursor is on before a tree is opened.
			n := &node{label: t.Schema + "." + t.Name, icon: ic.table, hkey: "table section", data: indexRef{t.Indexes[0]}}
			n.children = tableNodes(s, t)
			root.children = append(root.children, n)
		}
	}
	root.children = append(root.children, &node{
		label: "Other pages",
		icon:  ic.schema,
		hkey:  "Other pages",
		load: func(n *node) {
			for no := uint32(0); no < s.NPages; no++ {
				p, err := s.Page(no)
				if err != nil {
					n.children = append(n.children, errNode(fmt.Sprintf("page %d: %v", no, err)))
					continue
				}
				if p.FIL.Type == innodb.FIL_PAGE_INDEX {
					continue
				}
				n.children = append(n.children, &node{
					label: fmt.Sprintf("page %d  %s", no, p.TypeName()),
					icon:  ic.page,
					tag:   p.TypeName(),
					color: pageColor(p.FIL.Type, -1),
					data:  pageRef{no: no},
				})
			}
		},
	})
	return root
}

// tableNodes is the CREATE TABLE line and one section per B+tree of a table.
func tableNodes(s *innodb.Space, t *innodb.Table) []*node {
	var out []*node
	if len(t.DDL) > 0 {
		out = append(out, createTableNode(t))
	}
	for _, ix := range t.Indexes {
		n := &node{label: fmt.Sprintf("%s (index %d)", ix.Name, ix.ID), icon: ic.index, hkey: "index tree",
			expanded: true, data: indexRef{ix}}
		n.children = append(n.children, pageNode(s, ix, ix.RootPage))
		out = append(out, n)
	}
	return out
}

// errNode is a row that failed to load; the whole line is coloured, since there
// is no type token to carry the colour.
func errNode(label string) *node {
	return &node{label: label, icon: ic.warn, tag: label, color: colDanger}
}

func pageNode(s *innodb.Space, ix *innodb.IndexDef, no uint32) *node {
	p, err := s.Page(no)
	if err != nil {
		return errNode(fmt.Sprintf("page %d: %v", no, err))
	}
	ip, err := p.ParseIndex(ix)
	if err != nil {
		n := errNode(fmt.Sprintf("page %d: %v", no, err))
		n.data = pageRef{no: no, ix: ix}
		return n
	}
	tag := indexTag(int(ip.Hdr.Level))
	label := fmt.Sprintf("page %d  %s  level %d  recs %d", no, tag, ip.Hdr.Level, ip.Hdr.NRecs)
	if k := ip.FirstKey(); k != "" {
		label += "  " + oneLine(k)
	}
	n := &node{label: label, icon: ic.page, tag: tag, color: pageColor(p.FIL.Type, int(ip.Hdr.Level)),
		data: pageRef{no: no, ix: ix}}
	if ip.Hdr.Level > 0 {
		// Re-read on expand instead of holding the parsed page: pages are never cached.
		n.load = func(n *node) {
			p, err := s.Page(no)
			if err != nil {
				return
			}
			ip, err := p.ParseIndex(ix)
			if err != nil {
				return
			}
			for _, c := range ip.Children() {
				n.children = append(n.children, pageNode(s, ix, c))
			}
		}
	}
	return n
}

// annotate picks the index definition matching the page before annotating it.
func annotate(t *innodb.Table, p *innodb.Page) (*innodb.Node, error) {
	return p.Annotate(indexFor(t, p))
}

// indexFor is the index definition a page's records are decoded with.
func indexFor(t *innodb.Table, p *innodb.Page) *innodb.IndexDef {
	switch {
	case t == nil || len(t.Indexes) == 0:
	case p.FIL.Type == innodb.FIL_PAGE_INDEX:
		if ip, err := p.ParseIndex(nil); err == nil {
			return t.Index(ip.Hdr.IndexID)
		}
	case p.FIL.Type == innodb.FIL_PAGE_UNDO_LOG:
		// Undo records name their columns by position in the clustered index.
		return t.Indexes[0]
	}
	return nil
}

func annTree(in *innodb.Node) *node {
	n := &node{label: in.Name, value: in.Value, data: in, size: in.Size()}
	if s := in.Start(); s >= 0 {
		n.off, n.hasOff = s, true
	}
	for _, c := range in.Children {
		n.children = append(n.children, annTree(c))
	}
	return n
}
