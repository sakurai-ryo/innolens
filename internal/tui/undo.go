package tui

import (
	"fmt"
	"path/filepath"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// jumpToUndo opens the undo record a DB_ROLL_PTR points at: the previous
// version of the row. The undo record carries a DB_ROLL_PTR of its own, so
// pressing enter again walks one more step back the version chain.
func (m *Model) jumpToUndo(rp innodb.RollPtr) {
	path, ok := m.dir.undo[rp.RsegID]
	if !ok {
		m.status.err = fmt.Sprintf("rseg %d: no undo tablespace for it under %s", rp.RsegID, m.path)
		return
	}
	open := func() (*innodb.Page, error) {
		s, err := innodb.Open(path)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		return s.Page(rp.PageNo)
	}
	p, err := open()
	if err != nil {
		m.status.err = err.Error()
		return
	}
	id, err := innodb.SpaceID(path)
	if err != nil {
		m.status.err = err.Error()
		return
	}
	m.pagePath, m.pageOpen, m.diff, m.rep = path, open, nil, nil
	m.showPage(id, m.pageTable, p, filepath.Base(path))
	m.status.info = ""
	if rp.Insert {
		m.status.info = "insert undo is discarded at commit, so this may no longer be the record"
	}
	m.selectRecord(int(rp.Offset))
}

// selectRecord puts the cursor on the record starting at off and expands it.
// The label is what is matched: a record on an index page covers the bytes
// before its origin as well, so its node does not start where the record does.
func (m *Model) selectRecord(off int) {
	want := fmt.Sprintf("record @%04x", off)
	for i, r := range m.ann.rows {
		if _, ok := r.n.data.(*innodb.Node); ok && r.n.label == want {
			m.ann.cur = i
			m.ann.expand()
			m.followSelection()
			return
		}
	}
	m.status.err = fmt.Sprintf("page %d has no record at offset %d: it may have been purged", m.page.No, off)
}
