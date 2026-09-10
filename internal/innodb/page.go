package innodb

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Annotate builds the annotation tree of a page: FIL header, the type-specific
// body, then the FIL trailer. idx decodes records on INDEX pages; nil shows headers only.
func (p *Page) Annotate(idx *IndexDef) (*Node, error) {
	root := &Node{Name: fmt.Sprintf("page %d (%s)", p.No, p.TypeName()), Len: PageSize}
	p.annotateFILHeader(root)
	var err error
	var usage *Node
	switch p.FIL.Type {
	case FIL_PAGE_TYPE_FSP_HDR:
		p.annotateFSP(root)
	case FIL_PAGE_INDEX:
		var ip *IndexPage
		ip, err = p.ParseIndex(idx)
		if ip != nil {
			ip.annotate(root)
			usage = ip.usage()
		}
	case FIL_PAGE_UNDO_LOG:
		var up *UndoPage
		up, err = p.ParseUndo(idx)
		if up != nil {
			up.annotate(root, idx)
		}
	case FIL_PAGE_SDI:
		var ip *IndexPage
		ip, err = p.ParseIndex(sdiIndex)
		if ip != nil {
			ip.annotate(root)
			p.annotateSDIJSON(root, ip)
			usage = ip.usage()
		}
	}
	p.annotateFILTrailer(root)
	if usage != nil {
		root.Children = append([]*Node{usage}, root.Children...)
	}
	return root, err
}

// annotateSDIJSON attaches the decompressed, indented JSON of each SDI record.
func (p *Page) annotateSDIJSON(root *Node, ip *IndexPage) {
	g := root.Group("SDI JSON")
	for _, r := range ip.UserRecs() {
		rec, err := sdiRecord(p.Data, r)
		if err != nil {
			g.Add(fmt.Sprintf("record @%04x", r.Off), r.Off, 0, err.Error())
			continue
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, rec.JSON, "", "  ") != nil {
			pretty.Write(rec.JSON)
		}
		g.Add(fmt.Sprintf("type %d id %d", rec.Type, rec.ID), r.Off, r.End-r.Off, pretty.String())
	}
}
