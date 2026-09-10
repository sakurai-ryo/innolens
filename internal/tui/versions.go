package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// maxVersions bounds a chain walk; a purged or reused undo page can point back
// into itself.
const maxVersions = 64

// readView is the transaction the version chains are read as.
//
// A real read view also holds the ids of the transactions that were running
// when it was taken, and treats those as invisible whatever their id. innolens
// only has what is on disk, so it uses the low-water mark alone: everything
// with a smaller id counts as committed before the view.
type readView struct {
	set   bool
	trxID uint64
}

func (v readView) String() string {
	if !v.set {
		return ""
	}
	return fmt.Sprintf("read view trx_id %d", v.trxID)
}

// setReadView takes the trx id typed into the `v` prompt and marks the version
// chains against it. Empty text clears it.
func (m *Model) setReadView(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		m.view = readView{}
	} else {
		id, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			m.status.err = fmt.Sprintf("read view: %q is not a transaction id", text)
			return
		}
		m.view = readView{set: true, trxID: id}
	}
	m.status.view = m.view.String()
	if m.page == nil {
		return
	}
	// Marking in place rather than rebuilding the page keeps the chains the
	// marks land on expanded; a rebuild folds every one of them away again.
	n := m.remarkVersions(m.ann.root)
	switch {
	case !m.view.set:
		m.status.notice = "read view cleared"
	case n == 0:
		m.status.notice = m.view.String() + ": expand a record's versions to see which one it reads"
	default:
		m.status.notice = fmt.Sprintf("%s marks %d version chain(s) on this page", m.view, n)
	}
	m.ann.refresh()
}

// remarkVersions re-marks every chain that has already been walked and reports
// how many. One not yet expanded is marked by walkVersions when it loads.
func (m *Model) remarkVersions(n *node) int {
	if n == nil {
		return 0
	}
	count := 0
	if n.tag == "versions" && n.loaded {
		m.markVisible(n.children)
		count++
	}
	for _, c := range n.children {
		count += m.remarkVersions(c)
	}
	return count
}

// colVal is one column of a row image being rebuilt version by version.
type colVal struct{ name, value string }

// addVersions hangs a version chain under every record of an index page that
// has a previous version in the undo log. The walk runs when the row is
// expanded, because it reads one undo page per version.
//
// Only index pages get one: an undo record also carries a DB_ROLL_PTR, but the
// values around it are one change rather than a row.
func (m *Model) addVersions(tree *node, p *innodb.Page) {
	if m.pageTable == nil || len(m.pageTable.Indexes) == 0 || p.FIL.Type != innodb.FIL_PAGE_INDEX {
		return
	}
	for _, sec := range tree.children {
		if sec.label != "Records" {
			continue
		}
		for _, rec := range sec.children {
			in, ok := rec.data.(*innodb.Node)
			if !ok {
				continue
			}
			rp, ok := rollPtrOf(in)
			if !ok {
				continue
			}
			vals, trxID := rowImage(in)
			rec.children = append(rec.children, m.versionsNode(vals, trxID, rp, strings.Contains(in.Value, "delete-marked")))
		}
	}
}

// rollPtrOf is the DB_ROLL_PTR of a record, which is set as the jump target of
// the field when it names an undo record.
func rollPtrOf(rec *innodb.Node) (innodb.RollPtr, bool) {
	for _, c := range rec.Children {
		if rp, ok := c.Ref.(innodb.RollPtr); ok {
			return rp, true
		}
	}
	return innodb.RollPtr{}, false
}

// rowImage is the column values of a record as it is on the page, which is the
// newest version of the row, plus the transaction that wrote it.
func rowImage(rec *innodb.Node) ([]colVal, uint64) {
	var out []colVal
	var trxID uint64
	for _, c := range rec.Children {
		if recPrefix[c.Name] {
			continue
		}
		if c.Name == "DB_TRX_ID" {
			trxID, _ = strconv.ParseUint(c.Value, 10, 64)
		}
		out = append(out, colVal{c.Name, oneLine(c.Value)})
	}
	return out, trxID
}

func (m *Model) versionsNode(vals []colVal, trxID uint64, rp innodb.RollPtr, deleted bool) *node {
	n := &node{label: "versions", icon: ic.undo, hkey: "versions", tag: "versions", color: colMeta}
	n.load = func(n *node) {
		n.children = m.walkVersions(vals, trxID, rp, deleted)
	}
	return n
}

// walkVersions follows DB_ROLL_PTR back through the undo log, rebuilding the
// row as it was before each change. Applying the old values an undo record
// carries to the newer image is what a consistent read does.
func (m *Model) walkVersions(vals []colVal, trxID uint64, rp innodb.RollPtr, deleted bool) []*node {
	out := []*node{versionNode("current", trxID, vals, innodb.RollPtr{}, rp, deleted)}
	idx := m.pageTable.Indexes[0]
	for i := 0; i < maxVersions && !rp.Zero(); i++ {
		rec, err := m.undoRec(rp, idx)
		if err != nil {
			out = append(out, errNode(err.Error()))
			break
		}
		if rec.Type == innodb.TRX_UNDO_INSERT_REC {
			out = append(out, &node{label: "no earlier version", hkey: "no earlier version",
				value: fmt.Sprintf("trx %d inserted the row; the undo record only holds its key", rec.TrxID),
				dim:   true})
			break
		}
		// The info bits of the undo record are the ones the row carried before
		// the change, so a delete-marked row shows the version where it was not.
		vals = applyUndo(vals, rec)
		out = append(out, versionNode(rec.TypeName(), rec.TrxID, vals, rp, rec.Roll,
			rec.InfoBits&innodb.REC_INFO_DELETED_FLAG != 0))
		if rec.Err != nil {
			out = append(out, errNode(rec.Err.Error()))
			break
		}
		rp = rec.Roll
	}
	m.markVisible(out)
	return out
}

// undoRec reads the undo record a roll pointer names.
func (m *Model) undoRec(rp innodb.RollPtr, idx *innodb.IndexDef) (*innodb.UndoRec, error) {
	path, ok := m.dir.undo[rp.RsegID]
	if !ok {
		return nil, fmt.Errorf("rseg %d: no undo tablespace for it under %s", rp.RsegID, m.path)
	}
	s, err := innodb.Open(path)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	p, err := s.Page(rp.PageNo)
	if err != nil {
		return nil, err
	}
	up, err := p.ParseUndo(idx)
	if err != nil {
		return nil, err
	}
	for _, rec := range up.Recs {
		if rec.Off == int(rp.Offset) {
			return rec, nil
		}
	}
	return nil, fmt.Errorf("undo page %d has no record at offset %d: it has been purged", rp.PageNo, rp.Offset)
}

// applyUndo rolls the row image back one version: the undo record holds the
// value every column it changed had before.
func applyUndo(vals []colVal, rec *innodb.UndoRec) []colVal {
	out := append([]colVal(nil), vals...)
	for _, f := range rec.Upd {
		for i := range out {
			if out[i].name == f.Name {
				out[i].value = f.Value
			}
		}
	}
	return out
}

// versionNode is one row version: its transaction and the columns it held. at
// is the undo record holding it, which enter opens, and next points at the
// version before it.
func versionNode(kind string, trxID uint64, vals []colVal, at, next innodb.RollPtr, deleted bool) *node {
	n := &node{label: fmt.Sprintf("%s  trx_id %d", kind, trxID), icon: ic.record,
		tag: kind, color: colData, hkey: "row version", data: at, value: summary(vals, deleted)}
	for _, v := range vals {
		n.children = append(n.children, &node{label: v.name, value: v.value, hkey: "row version"})
	}
	if !next.Zero() {
		n.children = append(n.children, &node{label: "DB_ROLL_PTR", value: next.String(),
			hkey: "DB_ROLL_PTR", data: next})
	}
	return n
}

// summary is the leading columns of a version, enough to tell two apart. A
// delete of an unchanged row leaves every column alone, so the mark leads.
func summary(vals []colVal, deleted bool) string {
	parts := []string{}
	if deleted {
		parts = append(parts, "delete-marked")
	}
	for _, v := range vals {
		if strings.HasPrefix(v.name, "DB_") {
			continue
		}
		parts = append(parts, v.name+"="+v.value)
		if len(parts) == 4 {
			break
		}
	}
	return strings.Join(parts, "  ")
}

// markVisible notes which version a read view would return: the newest one
// written by a transaction it counts as committed.
func (m *Model) markVisible(versions []*node) {
	seen := false
	for _, n := range versions {
		if n.hkey != "row version" {
			continue
		}
		if !m.view.set {
			n.note = ""
			continue
		}
		id := trxIDOf(n.label)
		switch {
		case id > m.view.trxID:
			n.note = "too new for the read view"
		case !seen:
			n.note = "← " + m.view.String() + " sees this"
			seen = true
		default:
			n.note = "older than what the read view sees"
		}
	}
}

// trxIDOf reads the id back out of a version label.
func trxIDOf(label string) uint64 {
	_, rest, ok := strings.Cut(label, "trx_id ")
	if !ok {
		return 0
	}
	id, _ := strconv.ParseUint(rest, 10, 64)
	return id
}
