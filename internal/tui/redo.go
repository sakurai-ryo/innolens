package tui

import (
	"fmt"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

type pageKey struct{ space, page uint32 }

// redoIndex is the redo log scanned once and kept in memory: the mtr list for
// the right pane, and the (space_id, page_no) index for the page detail.
type redoIndex struct {
	stream *innodb.LogStream
	mtrs   []*innodb.MTR
	byPage map[pageKey][]*innodb.RedoRec
	info   string
}

func loadRedo(dir string) (*redoIndex, error) {
	r, err := innodb.OpenRedo(dir)
	if err != nil {
		return nil, err
	}
	s, err := r.Scan()
	r.Close() // Scan buffered everything it could read; the files are done with.
	if err != nil {
		return nil, err
	}
	ri := &redoIndex{
		stream: s,
		byPage: map[pageKey][]*innodb.RedoRec{},
		info: fmt.Sprintf("checkpoint lsn %d  lsn %d..%d  %d bytes  (%s)",
			r.CheckpointLSN, s.StartLSN, s.EndLSN, len(s.Buf), s.Stop),
	}
	ri.mtrs = s.MTRs()
	for _, m := range ri.mtrs {
		for _, rec := range m.Recs {
			if rec.HasPage() {
				k := pageKey{rec.SpaceID, rec.PageNo}
				ri.byPage[k] = append(ri.byPage[k], rec)
			}
		}
	}
	return ri, nil
}

// redoTree is the right pane for #innodb_redo: one node per mtr, records loaded
// on expand.
func (m *Model) redoTree(ri *redoIndex) *node {
	root := &node{}
	for _, mtr := range ri.mtrs {
		mtr := mtr
		label := fmt.Sprintf("mtr lsn %d..%d  %d records", mtr.StartLSN, mtr.EndLSN, len(mtr.Recs))
		n := &node{label: label, icon: ic.mtr, hkey: "mtr"}
		n.load = func(n *node) {
			for _, rec := range mtr.Recs {
				n.children = append(n.children, m.redoRecNode(ri, rec))
			}
			if mtr.Err != nil {
				n.children = append(n.children, errNode("error: "+mtr.Err.Error()))
			}
		}
		root.children = append(root.children, n)
	}
	if len(root.children) == 0 {
		root.children = append(root.children, &node{label: "no mtr after the checkpoint", dim: true})
	}
	return root
}

func (m *Model) redoRecNode(ri *redoIndex, rec *innodb.RedoRec) *node {
	label := fmt.Sprintf("%s  lsn %d  len %d", rec.TypeName(), rec.LSN, rec.Len)
	n := &node{label: label, icon: ic.record, tag: rec.TypeName(), color: redoColor(rec.Type)}
	if rec.HasPage() {
		n.label = fmt.Sprintf("%s  space %d page %d  lsn %d  len %d",
			rec.TypeName(), rec.SpaceID, rec.PageNo, rec.LSN, rec.Len)
		n.data = pageJump{rec.SpaceID, rec.PageNo}
	}
	n.load = func(n *node) {
		for _, c := range rec.Annotate(ri.stream.Buf).Children {
			n.children = append(n.children, annTree(c))
		}
		if rec.Err != nil {
			n.children = append(n.children, errNode("error: "+rec.Err.Error()))
		}
		if img := m.imageNode(ri, rec); img != nil {
			n.children = append(n.children, img)
		}
	}
	return n
}

// imageNode decodes the record image of MLOG_REC_INSERT into columns, using the
// table's SDI when the tablespace is in this datadir and the layout logged with
// the record otherwise.
func (m *Model) imageNode(ri *redoIndex, rec *innodb.RedoRec) *node {
	if rec.Ins == nil {
		return nil
	}
	n := &node{label: "record image", tag: "record image", color: colData}
	page, idx := m.pageAndIndex(rec.SpaceID, rec.PageNo)
	if idx == nil {
		idx = rec.Idx
		n.label += " (columns from redo, no SDI)"
	}
	img, err := rec.RecordImage(ri.stream.Buf, page, idx)
	if err != nil {
		n.value = err.Error()
		return n
	}
	n.value = fmt.Sprintf("%s heap_no %d%s", img.StatusName(), img.HeapNo, deletedSuffix(img))
	for _, c := range img.AnnotateImage().Children {
		n.children = append(n.children, annTree(c))
	}
	return n
}

func deletedSuffix(r *innodb.Rec) string {
	if r.Deleted() {
		return " delete-marked"
	}
	return ""
}

// pageAndIndex opens the tablespace of a redo record just long enough to read
// the target page and the index definition of the B+tree it belongs to.
func (m *Model) pageAndIndex(space, pageNo uint32) (*innodb.Page, *innodb.IndexDef) {
	path, ok := m.dir.spaces[space]
	if !ok {
		return nil, nil
	}
	s, err := innodb.Open(path)
	if err != nil {
		return nil, nil
	}
	defer s.Close()
	p, err := s.Page(pageNo)
	if err != nil {
		return nil, nil
	}
	ip, err := p.ParseIndex(nil)
	if err != nil {
		return p, nil
	}
	t, err := s.ReadTable()
	if err != nil {
		return p, nil
	}
	return p, t.Index(ip.Hdr.IndexID)
}

// redoSection lists the records that modified this page, loaded on expand so
// that opening a page detail does not depend on the redo log.
func (m *Model) redoSection(space, pageNo uint32) *node {
	n := &node{label: "redo", icon: ic.redo}
	n.load = func(n *node) {
		ri, err := m.loadRedoOnce()
		if err != nil {
			n.children = append(n.children, errNode("error: "+err.Error()))
			return
		}
		recs := ri.byPage[pageKey{space, pageNo}]
		if len(recs) == 0 {
			n.children = append(n.children, &node{label: "no record for this page", dim: true})
			return
		}
		for _, rec := range recs {
			n.children = append(n.children, m.redoRecNode(ri, rec))
		}
	}
	return n
}
