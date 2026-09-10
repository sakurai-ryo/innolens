package innodb

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// INDEX page header offsets relative to FIL_PAGE_DATA (page0types.h).
const (
	PAGE_N_DIR_SLOTS   = 0
	PAGE_HEAP_TOP      = 2
	PAGE_N_HEAP        = 4
	PAGE_FREE          = 6
	PAGE_GARBAGE       = 8
	PAGE_LAST_INSERT   = 10
	PAGE_DIRECTION     = 12
	PAGE_N_DIRECTION   = 14
	PAGE_N_RECS        = 16
	PAGE_MAX_TRX_ID    = 18
	PAGE_LEVEL         = 26
	PAGE_INDEX_ID      = 28
	PAGE_BTR_SEG_LEAF  = 36
	PAGE_BTR_SEG_TOP   = 46
	PAGE_DATA          = FIL_PAGE_DATA + 56
	PAGE_NEW_INFIMUM   = PAGE_DATA + REC_N_NEW_EXTRA_BYTES
	PAGE_NEW_SUPREMUM  = PAGE_DATA + 2*REC_N_NEW_EXTRA_BYTES + 8
	PAGE_DIR           = PageSize - FIL_PAGE_DATA_END
	PAGE_DIR_SLOT_SIZE = 2
)

var directionNames = map[uint16]string{1: "PAGE_LEFT", 2: "PAGE_RIGHT", 3: "PAGE_SAME_REC", 4: "PAGE_SAME_PAGE", 5: "PAGE_NO_DIRECTION"}

type IndexHeader struct {
	NDirSlots, HeapTop, NHeap, Free, Garbage, LastInsert, Direction, NDirection, NRecs uint16
	MaxTrxID                                                                           uint64
	Level                                                                              uint16
	IndexID                                                                            uint64
}

// IndexPage is a decoded FIL_PAGE_INDEX / FIL_PAGE_SDI page.
type IndexPage struct {
	*Page
	Hdr  IndexHeader
	Recs []*Rec // infimum, user records in list order, supremum
	Free []*Rec // PAGE_FREE list
	Idx  *IndexDef
}

func (p *Page) indexHeader() IndexHeader {
	b := p.Data[FIL_PAGE_DATA:]
	u16 := func(o int) uint16 { return binary.BigEndian.Uint16(b[o:]) }
	return IndexHeader{
		NDirSlots: u16(PAGE_N_DIR_SLOTS), HeapTop: u16(PAGE_HEAP_TOP), NHeap: u16(PAGE_N_HEAP) & 0x7FFF,
		Free: u16(PAGE_FREE), Garbage: u16(PAGE_GARBAGE), LastInsert: u16(PAGE_LAST_INSERT),
		Direction: u16(PAGE_DIRECTION), NDirection: u16(PAGE_N_DIRECTION), NRecs: u16(PAGE_N_RECS),
		MaxTrxID: binary.BigEndian.Uint64(b[PAGE_MAX_TRX_ID:]), Level: u16(PAGE_LEVEL),
		IndexID: binary.BigEndian.Uint64(b[PAGE_INDEX_ID:]),
	}
}

func (p *Page) IsIndex() bool { return p.FIL.Type == FIL_PAGE_INDEX || p.FIL.Type == FIL_PAGE_SDI }

// ParseIndex decodes the record lists. idx may be nil (headers only).
func (p *Page) ParseIndex(idx *IndexDef) (*IndexPage, error) {
	if !p.IsIndex() {
		return nil, fmt.Errorf("page %d is %s, not an index page", p.No, p.TypeName())
	}
	ip := &IndexPage{Page: p, Hdr: p.indexHeader(), Idx: idx}
	// Walk the singly linked record list from infimum. The heap bound guards
	// against corrupt next pointers looping forever.
	off := PAGE_NEW_INFIMUM
	for i := 0; i <= int(ip.Hdr.NHeap)+1; i++ {
		r, err := parseRec(p.Data, off, idx)
		if err != nil {
			return ip, err
		}
		ip.Recs = append(ip.Recs, r)
		if r.Status == REC_STATUS_SUPREMUM || r.Next == 0 {
			break
		}
		off = r.NextOff()
	}
	off = int(ip.Hdr.Free)
	for i := 0; off != 0 && i < int(ip.Hdr.NHeap); i++ {
		r, err := parseRec(p.Data, off, idx)
		if err != nil {
			return ip, err
		}
		ip.Free = append(ip.Free, r)
		if r.Next == 0 {
			break
		}
		off = r.NextOff()
	}
	return ip, nil
}

// UserRecs returns the records between infimum and supremum.
func (ip *IndexPage) UserRecs() []*Rec {
	if len(ip.Recs) < 2 {
		return nil
	}
	return ip.Recs[1 : len(ip.Recs)-1]
}

// Children returns child page numbers of a non-leaf page in key order.
func (ip *IndexPage) Children() []uint32 {
	var out []uint32
	for _, r := range ip.UserRecs() {
		if r.Status == REC_STATUS_NODE_PTR {
			out = append(out, r.Child)
		}
	}
	return out
}

// FirstKey is a short description of the first user record, for tree labels.
func (ip *IndexPage) FirstKey() string {
	recs := ip.UserRecs()
	if len(recs) == 0 || len(recs[0].Fields) == 0 {
		return ""
	}
	n := ip.Idx.NUniqueInTree
	if n > len(recs[0].Fields) {
		n = len(recs[0].Fields)
	}
	var parts []string
	for _, f := range recs[0].Fields[:n] {
		parts = append(parts, f.Value)
	}
	return strings.Join(parts, ", ")
}

func (ip *IndexPage) annotate(root *Node) {
	h := root.Group("INDEX header")
	r := reader{ip.Data, h}
	base := FIL_PAGE_DATA
	r.u16("PAGE_N_DIR_SLOTS", base+PAGE_N_DIR_SLOTS)
	r.u16("PAGE_HEAP_TOP", base+PAGE_HEAP_TOP)
	nh := r.u16("PAGE_N_HEAP", base+PAGE_N_HEAP)
	r.note(fmt.Sprintf("%d (compact=%d)", nh&0x7FFF, nh>>15))
	r.u16("PAGE_FREE", base+PAGE_FREE)
	r.u16("PAGE_GARBAGE", base+PAGE_GARBAGE)
	r.u16("PAGE_LAST_INSERT", base+PAGE_LAST_INSERT)
	d := r.u16("PAGE_DIRECTION", base+PAGE_DIRECTION)
	r.note(fmt.Sprintf("%d (%s)", d, directionNames[d]))
	r.u16("PAGE_N_DIRECTION", base+PAGE_N_DIRECTION)
	r.u16("PAGE_N_RECS", base+PAGE_N_RECS)
	r.u64("PAGE_MAX_TRX_ID", base+PAGE_MAX_TRX_ID)
	r.u16("PAGE_LEVEL", base+PAGE_LEVEL)
	r.u64("PAGE_INDEX_ID", base+PAGE_INDEX_ID)
	if ip.Idx != nil {
		r.note(fmt.Sprintf("%d (%s)", ip.Hdr.IndexID, ip.Idx.Name))
	}
	f := root.Group("FSEG header")
	r = reader{ip.Data, f}
	r.fsegHeader("PAGE_BTR_SEG_LEAF", base+PAGE_BTR_SEG_LEAF)
	r.fsegHeader("PAGE_BTR_SEG_TOP", base+PAGE_BTR_SEG_TOP)

	recs := root.Group("Records")
	for _, rec := range ip.Recs {
		rec.Annotate(recs)
	}
	free := root.Group("PAGE_FREE list")
	for _, rec := range ip.Free {
		n := rec.Annotate(free)
		n.Value += ", " + freeReason(rec)
	}

	dir := root.Group("Page directory")
	r = reader{ip.Data, dir}
	for i := 0; i < int(ip.Hdr.NDirSlots); i++ {
		off := PAGE_DIR - (i+1)*PAGE_DIR_SLOT_SIZE
		v := r.u16(fmt.Sprintf("slot %d", i), off)
		if int(v) >= REC_N_NEW_EXTRA_BYTES && int(v) < PageSize {
			r.note(fmt.Sprintf("%d (n_owned %d)", v, ip.Data[int(v)-5]&0x0F))
		}
	}
}

// usage splits the 16KB of an INDEX page into what it is spent on. The parts
// add up to PageSize by construction: everything between the headers and the
// page directory is either a record, garbage or free.
func (ip *IndexPage) usage() *Node {
	dir := int(ip.Hdr.NDirSlots) * PAGE_DIR_SLOT_SIZE
	freeOff := PAGE_DIR - dir
	g := &Node{Name: "page usage"}
	add := func(name string, off, ln, bytes int) {
		n := g.Add(name, off, ln, fmt.Sprintf("%.1f%%", float64(bytes)*100/PageSize))
		n.Bytes = bytes
	}
	add("headers", 0, PAGE_DATA, PAGE_DATA)
	add("records", 0, 0, int(ip.Hdr.HeapTop)-PAGE_DATA-int(ip.Hdr.Garbage))
	add("garbage", 0, 0, int(ip.Hdr.Garbage))
	add("free", int(ip.Hdr.HeapTop), freeOff-int(ip.Hdr.HeapTop), freeOff-int(ip.Hdr.HeapTop))
	add("page directory", freeOff, dir, dir)
	add("FIL trailer", PageSize-FIL_PAGE_DATA_END, FIL_PAGE_DATA_END, FIL_PAGE_DATA_END)
	return g
}

// freeReason is why a record is on the free list, which is what its delete-mark
// bit says. Purge frees a record that was delete-marked first, so that row is
// gone. A page split frees the run of records it moved to the sibling page, and
// a rolled-back insert frees one that was never deleted: those bytes are a
// stale copy of a row that lived on, wherever it is now.
func freeReason(rec *Rec) string {
	if rec.Deleted() {
		return "freed by purge: the row is gone"
	}
	return "freed without a delete: left by a page split or a rolled-back insert"
}

func (rec *Rec) label() string {
	var flags []string
	if rec.Deleted() {
		flags = append(flags, "delete-marked")
	}
	if rec.MinRec() {
		flags = append(flags, "min_rec")
	}
	if rec.Instant() {
		flags = append(flags, "instant")
	}
	if rec.Versioned() {
		flags = append(flags, fmt.Sprintf("version %d", rec.Version))
	}
	s := fmt.Sprintf("%s heap_no %d", rec.StatusName(), rec.HeapNo)
	if len(flags) > 0 {
		s += " [" + strings.Join(flags, ", ") + "]"
	}
	return s
}

// Annotate adds the structure of one record under parent, in byte order: the
// length array and the null bitmap that encode the layout, then the fixed
// header, then the columns.
func (rec *Rec) Annotate(parent *Node) *Node {
	g := parent.Add(fmt.Sprintf("record @%04x", rec.Off), rec.Off-rec.Extra, rec.End-rec.Off+rec.Extra, rec.label())
	rec.annotateVarLens(g)
	rec.annotateNullBitmap(g)
	rec.annotateHeader(g)
	for _, f := range rec.Fields {
		rec.annotateField(g, f)
	}
	return g
}

// AnnotateImage returns the structure of a record rebuilt from a redo record.
// Offsets are relative to the start of the record, because the bytes exist only
// in the buffer the image was assembled in.
func (rec *Rec) AnnotateImage() *Node {
	n := rec.Annotate(&Node{})
	shiftNode(n, -(rec.Off - rec.Extra))
	return n
}

// annotateVarLens breaks up the length array. Entries follow column order, so
// their offsets run backwards, and a NULL column stores no length at all.
func (rec *Rec) annotateVarLens(parent *Node) {
	lo, hi := 0, 0
	for _, f := range rec.Fields {
		if f.LenBytes == 0 {
			continue
		}
		if lo == 0 || f.LenOff < lo {
			lo = f.LenOff
		}
		if f.LenOff+f.LenBytes > hi {
			hi = f.LenOff + f.LenBytes
		}
	}
	if hi == 0 {
		return
	}
	g := parent.Add("var lengths", lo, hi-lo, hex.EncodeToString(rec.Buf[lo:hi]))
	for _, f := range rec.Fields {
		if !f.Var {
			continue
		}
		if f.LenBytes == 0 {
			g.Add(f.Name, 0, 0, "no length stored (NULL)")
			continue
		}
		v := fmt.Sprintf("%x = %d bytes", rec.Buf[f.LenOff:f.LenOff+f.LenBytes], f.Len)
		if f.LenBytes == 2 {
			v += ", 2-byte length"
		}
		if f.Extern {
			v += ", extern"
		}
		g.Add(f.Name, f.LenOff, f.LenBytes, v)
	}
}

func (rec *Rec) annotateNullBitmap(parent *Node) {
	lo, hi := 0, 0
	for _, f := range rec.Fields {
		if f.NullOff == 0 {
			continue
		}
		if lo == 0 || f.NullOff < lo {
			lo = f.NullOff
		}
		if f.NullOff+1 > hi {
			hi = f.NullOff + 1
		}
	}
	if hi == 0 {
		return
	}
	g := parent.Add("null bitmap", lo, hi-lo, hex.EncodeToString(rec.Buf[lo:hi]))
	for _, f := range rec.Fields {
		if f.NullOff == 0 {
			continue
		}
		state := "0, not null"
		if rec.Buf[f.NullOff]&(1<<f.NullBit) != 0 {
			state = "1, NULL"
		}
		g.Add(f.Name, f.NullOff, 1, fmt.Sprintf("bit %d = %s", f.NullBit, state))
	}
}

func (rec *Rec) annotateHeader(parent *Node) {
	ln := REC_N_NEW_EXTRA_BYTES + boolInt(rec.Versioned())
	h := parent.Add("header", rec.Off-ln, ln, "")
	r := reader{rec.Buf, h}
	if rec.Versioned() {
		r.u8("row_version", rec.Off-6)
	}
	r.u8("info_bits | n_owned", rec.Off-5)
	r.note(fmt.Sprintf("%#02x (delete=%d min_rec=%d version=%d instant=%d n_owned=%d)", rec.Buf[rec.Off-5],
		rec.InfoBits>>5&1, rec.InfoBits>>4&1, rec.InfoBits>>6&1, rec.InfoBits>>7, rec.NOwned))
	r.u16("heap_no | status", rec.Off-4)
	r.note(fmt.Sprintf("heap_no %d, status %d (%s)", rec.HeapNo, rec.Status, rec.StatusName()))
	r.u16("next", rec.Off-2)
	r.note(fmt.Sprintf("%d (-> @%04x)", int16(rec.Next), rec.NextOff()))
}

// annotateField hangs the decoding steps under the column when the type needs
// more than a copy to turn bytes into a value.
func (rec *Rec) annotateField(parent *Node, f FieldVal) {
	n := parent.Add(f.Name, f.Off, f.Len, f.Value)
	if f.dec == nil || f.Null || f.Extern || f.Len == 0 {
		return
	}
	if f.Name == "DB_ROLL_PTR" && f.Len == 7 {
		if rp, ok := DecodeRollPtr(rec.Buf[f.Off : f.Off+7]); ok && !rp.Zero() {
			n.Ref = rp
		}
	}
	var steps []Step
	f.dec(rec.Buf[f.Off:f.Off+f.Len], &steps)
	for _, s := range steps {
		n.Add(s.Name, 0, 0, s.Value)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
