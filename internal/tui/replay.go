package tui

import (
	"fmt"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// replay steps the redo records that modified one page onto its bytes, the way
// recovery does after a crash. The page is rebuilt from the file every step, so
// going back costs the same as going forward and nothing is written to disk.
type replay struct {
	recs []*innodb.RedoRec
	pos  int    // records applied so far
	note string // what the last step did
}

// replayStep applies the next redo record to the page in the detail view, or
// takes one back.
func (m *Model) replayStep(d int) {
	if m.focus != focusDetail || m.page == nil || m.pageOpen == nil {
		return
	}
	if m.rep == nil {
		ri, err := m.loadRedoOnce()
		if err != nil {
			m.status.err = err.Error()
			return
		}
		recs := ri.byPage[pageKey{m.pageSpace, m.page.No}]
		if len(recs) == 0 {
			m.status.err = fmt.Sprintf("no redo record after the checkpoint touches space %d page %d",
				m.pageSpace, m.page.No)
			return
		}
		m.rep = &replay{recs: recs}
	}
	pos := m.rep.pos + d
	if pos < 0 || pos > len(m.rep.recs) {
		return
	}
	m.replayTo(pos)
}

// replayTo re-reads the page and applies the first n records to it.
func (m *Model) replayTo(n int) {
	p, err := m.pageOpen()
	if err != nil {
		m.status.err = err.Error()
		return
	}
	note := "the page as it is on disk"
	var failed string
	for i := 0; i < n; i++ {
		rec := m.rep.recs[i]
		next, did, err := rec.Apply(p, indexFor(m.pageTable, p))
		if err != nil {
			failed, n = fmt.Sprintf("%s at lsn %d: %v", rec.TypeName(), rec.LSN, err), i
			break
		}
		p, note = next, fmt.Sprintf("%s lsn %d: %s", rec.TypeName(), rec.LSN, did)
	}
	prev := m.page.Data
	m.rep.pos, m.rep.note = n, note
	m.diff = newDiff(prev, p.Data)
	m.showPage(m.pageSpace, m.pageTable, p, "")
	m.status.info = fmt.Sprintf("replay %d/%d  %s", n, len(m.rep.recs), note)
	if failed != "" {
		m.status.err = failed
	}
}

// replaySection lists the redo records of the page in replay order, marking how
// far the replay has got. It replaces the plain redo section while stepping.
func (m *Model) replaySection() *node {
	n := &node{label: "replay", icon: ic.redo, hkey: "replay",
		value: fmt.Sprintf("%d of %d records applied", m.rep.pos, len(m.rep.recs)), expanded: true}
	ri, err := m.loadRedoOnce()
	if err != nil {
		n.children = append(n.children, errNode("error: "+err.Error()))
		return n
	}
	for i, rec := range m.rep.recs {
		c := m.redoRecNode(ri, rec)
		switch {
		case i < m.rep.pos-1:
			c.note = "applied"
		case i == m.rep.pos-1:
			c.note = "applied · " + m.rep.note
		case i == m.rep.pos:
			c.note = "next (n)"
		default:
			c.note = "pending"
		}
		n.children = append(n.children, stripOffs(c))
	}
	return n
}
