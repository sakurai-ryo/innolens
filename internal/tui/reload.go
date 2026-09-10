package tui

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// pageDiff is what changed between two reads of the same page.
type pageDiff struct {
	chg []bool
	n   int
	// against names the other side of the comparison in the status bar; empty
	// means the previous read of this same file.
	against string
	sect    *innodb.Node
}

func (d *pageDiff) at(off int) bool {
	return d != nil && off >= 0 && off < len(d.chg) && d.chg[off]
}

// newDiff compares two reads of a page and lists the ranges that differ. Nil
// means nothing changed, which is also what a short read gives.
func newDiff(old, cur []byte) *pageDiff {
	if len(old) != len(cur) {
		return nil
	}
	d := &pageDiff{chg: make([]bool, len(cur))}
	for i := range cur {
		if old[i] != cur[i] {
			d.chg[i], d.n = true, d.n+1
		}
	}
	if d.n == 0 {
		return nil
	}
	d.sect = &innodb.Node{Name: "changes"}
	for i := 0; i < len(d.chg); {
		if !d.chg[i] {
			i++
			continue
		}
		j := i
		for j < len(d.chg) && d.chg[j] {
			j++
		}
		d.sect.Add(fmt.Sprintf("@%04x", i), i, j-i,
			fmt.Sprintf("%s -> %s", hex.EncodeToString(old[i:j]), hex.EncodeToString(cur[i:j])))
		i = j
	}
	return d
}

// baselineDiff compares the page with the same page of the baseline datadir,
// the snapshot taken before the statement being studied ran. Anything missing
// on the other side just means no comparison, not an error.
func (m *Model) baselineDiff(p *innodb.Page) *pageDiff {
	if m.base == "" || m.pagePath == "" {
		return nil
	}
	rel, err := filepath.Rel(m.path, m.pagePath)
	if err != nil {
		return nil
	}
	s, err := innodb.Open(filepath.Join(m.base, rel))
	if err != nil {
		return nil
	}
	defer s.Close()
	old, err := s.Page(p.No)
	if err != nil {
		return nil
	}
	d := newDiff(old.Data, p.Data)
	if d != nil {
		d.against = " vs " + filepath.Base(m.base)
	}
	return d
}

// reload re-reads whatever the focused pane is showing. On a page it also marks
// the bytes that changed, which is how a statement run against a live server
// becomes visible.
func (m *Model) reload() {
	switch m.focus {
	case focusDetail:
		m.reloadPage()
	case focusPages:
		switch {
		case m.pagesTitle == "REDO":
			m.redo = nil
			m.openRedo()
		case m.space != nil:
			m.openTable(m.space.Path)
		}
	case focusTables:
		d, err := scanDatadir(m.path)
		if err != nil {
			m.status.err = err.Error()
			return
		}
		m.dir = d
		m.setQuery(m.query)
	}
}

func (m *Model) reloadPage() {
	if m.pageOpen == nil {
		return
	}
	p, err := m.pageOpen()
	if err != nil {
		m.status.err = err.Error()
		return
	}
	m.diff, m.rep = newDiff(m.page.Data, p.Data), nil
	m.showPage(m.pageSpace, m.pageTable, p, "")
	if m.diff != nil {
		// The changes section is the first row, so its first entry is the second.
		m.ann.move(1)
		m.followSelection()
	}
}
