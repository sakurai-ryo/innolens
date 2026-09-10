package innodb

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// File list and file segment header sizes (fut0lst.h, fsp0types.h).
const (
	FLST_NODE_SIZE      = 12
	FLST_BASE_NODE_SIZE = 16
	FSEG_HEADER_SIZE    = 10
)

// Undo log page, segment and log header offsets (trx0undo.h).
const (
	TRX_UNDO_PAGE_HDR      = FIL_PAGE_DATA
	TRX_UNDO_PAGE_TYPE     = 0
	TRX_UNDO_PAGE_START    = 2
	TRX_UNDO_PAGE_FREE     = 4
	TRX_UNDO_PAGE_NODE     = 6
	TRX_UNDO_PAGE_HDR_SIZE = 6 + FLST_NODE_SIZE

	TRX_UNDO_SEG_HDR      = TRX_UNDO_PAGE_HDR + TRX_UNDO_PAGE_HDR_SIZE
	TRX_UNDO_STATE        = 0
	TRX_UNDO_LAST_LOG     = 2
	TRX_UNDO_FSEG_HEADER  = 4
	TRX_UNDO_PAGE_LIST    = 4 + FSEG_HEADER_SIZE
	TRX_UNDO_SEG_HDR_SIZE = 4 + FSEG_HEADER_SIZE + FLST_BASE_NODE_SIZE

	TRX_UNDO_LOG_HDR      = TRX_UNDO_SEG_HDR + TRX_UNDO_SEG_HDR_SIZE
	TRX_UNDO_TRX_ID       = 0
	TRX_UNDO_TRX_NO       = 8
	TRX_UNDO_DEL_MARKS    = 16
	TRX_UNDO_LOG_START    = 18
	TRX_UNDO_FLAGS        = 20
	TRX_UNDO_DICT_TRANS   = 21
	TRX_UNDO_TABLE_ID     = 22
	TRX_UNDO_NEXT_LOG     = 30
	TRX_UNDO_PREV_LOG     = 32
	TRX_UNDO_HISTORY_NODE = 34
)

// Undo log segment types and states (trx0undo.h).
const (
	TRX_UNDO_INSERT = 1
	TRX_UNDO_UPDATE = 2
)

var undoSegTypes = map[uint16]string{TRX_UNDO_INSERT: "TRX_UNDO_INSERT", TRX_UNDO_UPDATE: "TRX_UNDO_UPDATE"}

var undoStates = map[uint16]string{
	1: "TRX_UNDO_ACTIVE", 2: "TRX_UNDO_CACHED", 3: "TRX_UNDO_TO_FREE", 4: "TRX_UNDO_TO_PURGE",
	5: "TRX_UNDO_PREPARED_80028", 6: "TRX_UNDO_PREPARED", 7: "TRX_UNDO_PREPARED_IN_TC",
}

// Undo record types and the flags packed into the same byte (trx0rec.h).
const (
	TRX_UNDO_INSERT_REC    = 11
	TRX_UNDO_UPD_EXIST_REC = 12
	TRX_UNDO_UPD_DEL_REC   = 13
	TRX_UNDO_DEL_MARK_REC  = 14
	TRX_UNDO_MODIFY_BLOB   = 0x40
	TRX_UNDO_UPD_EXTERN    = 0x80
	UPD_NODE_NO_ORD_CHANGE = 1
	UNIV_SQL_NULL          = 0xFFFFFFFF
	univExternStorageField = UNIV_SQL_NULL - PageSize
	spatialStatusMask      = 3 << 12
	// A field number this high names a virtual column (rem0types.h).
	recMaxNFields           = 1023
	maxUndoRecsPerUndoPage  = 1024
	maxUndoLogsPerUndoPage  = 64
	maxUndoFieldsPerUndoRec = 1024
)

var undoRecTypes = map[uint8]string{
	TRX_UNDO_INSERT_REC: "TRX_UNDO_INSERT_REC", TRX_UNDO_UPD_EXIST_REC: "TRX_UNDO_UPD_EXIST_REC",
	TRX_UNDO_UPD_DEL_REC: "TRX_UNDO_UPD_DEL_REC", TRX_UNDO_DEL_MARK_REC: "TRX_UNDO_DEL_MARK_REC",
}

func UndoRecTypeName(t uint8) string {
	if s, ok := undoRecTypes[t]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", t)
}

// Undo tablespace ids are handed out from a bank per space number, so the
// number a DB_ROLL_PTR carries has to be computed back from the id
// (dict0dict.h, trx0purge.h).
const (
	maxUndoSpaces  = 127
	logSpaceID     = 0xFFFFFFF0
	maxUndoSpaceID = logSpaceID - 1
	minUndoSpaceID = logSpaceID - maxUndoSpaces*400000
)

func IsUndoSpace(id uint32) bool { return id >= minUndoSpaceID && id <= maxUndoSpaceID }

// UndoSpaceNum is undo::id2num: the rseg id of a DB_ROLL_PTR is the number of
// the undo tablespace holding the record, and this maps an id back to it.
func UndoSpaceNum(id uint32) uint8 {
	if !IsUndoSpace(id) {
		return 0
	}
	return uint8((maxUndoSpaceID-id)%maxUndoSpaces + 1)
}

// RollPtr is a decoded DB_ROLL_PTR: where the previous version of a row was
// logged. RsegID is the undo tablespace number, not a slot in it.
type RollPtr struct {
	Insert bool
	RsegID uint8
	PageNo uint32
	Offset uint16
}

// DecodeRollPtr reads the 7 bytes a clustered index record stores.
func DecodeRollPtr(b []byte) (RollPtr, bool) {
	if len(b) < 7 {
		return RollPtr{}, false
	}
	var v uint64
	for _, c := range b[:7] {
		v = v<<8 | uint64(c)
	}
	return rollPtr(v), true
}

func rollPtr(v uint64) RollPtr {
	return RollPtr{
		Insert: v>>55&1 == 1,
		RsegID: uint8(v >> 48 & 0x7F),
		PageNo: uint32(v >> 16),
		Offset: uint16(v),
	}
}

func (r RollPtr) String() string {
	kind := "update"
	if r.Insert {
		kind = "insert"
	}
	return fmt.Sprintf("%s undo, rseg %d, page %d, offset %d", kind, r.RsegID, r.PageNo, r.Offset)
}

// decRollPtr renders DB_ROLL_PTR as the place the previous version was logged.
func decRollPtr(b []byte, steps *[]Step) string {
	r, ok := DecodeRollPtr(b)
	if !ok {
		return fmt.Sprintf("%x", b)
	}
	if steps != nil {
		*steps = append(*steps,
			Step{"raw", fmt.Sprintf("%x", b)},
			Step{"is_insert", fmt.Sprint(boolInt(r.Insert))},
			Step{"rseg_id", fmt.Sprint(r.RsegID)},
			Step{"page_no", fmt.Sprint(r.PageNo)},
			Step{"offset", fmt.Sprint(r.Offset)})
	}
	return r.String()
}

// Zero reports the roll pointer written for a row that has no earlier version.
func (r RollPtr) Zero() bool { return r.PageNo == 0 && r.Offset == 0 }

// UndoField is one column value stored in an undo record.
type UndoField struct {
	Name   string
	Extern bool
	Value  string
}

// UndoRec is one record of an undo log: what a transaction needs to put a row
// back the way it was.
type UndoRec struct {
	Off, End int
	Type     uint8
	CmplInfo uint8
	Extern   bool
	UndoNo   uint64
	TableID  uint64
	// Set on everything but an insert: the state of the row before the change.
	HasSys   bool
	InfoBits uint8
	TrxID    uint64
	Roll     RollPtr
	Key      []UndoField // the primary key of the row this undoes
	Upd      []UndoField // the columns whose old value is stored
	Err      error
}

func (r *UndoRec) TypeName() string { return UndoRecTypeName(r.Type) }

// Label is the one-line summary shown in a tree.
func (r *UndoRec) Label() string {
	s := fmt.Sprintf("%s undo_no %d", r.TypeName(), r.UndoNo)
	if len(r.Key) > 0 {
		var parts []string
		for _, f := range r.Key {
			parts = append(parts, f.Value)
		}
		s += "  key " + strings.Join(parts, ", ")
	}
	if r.HasSys {
		s += fmt.Sprintf("  trx_id %d", r.TrxID)
	}
	return s
}

// UndoPage is a decoded FIL_PAGE_UNDO_LOG page.
type UndoPage struct {
	*Page
	Start, Free uint16
	// SegHdr is true on the first page of a segment, which is the one carrying
	// the segment header and the undo log headers.
	SegHdr bool
	Recs   []*UndoRec
}

// ParseUndo decodes the record list of an undo log page. idx is the clustered
// index of the table the records belong to; nil decodes their structure only.
func (p *Page) ParseUndo(idx *IndexDef) (*UndoPage, error) {
	if p.FIL.Type != FIL_PAGE_UNDO_LOG {
		return nil, fmt.Errorf("page %d is %s, not an undo log page", p.No, p.TypeName())
	}
	b := p.Data
	up := &UndoPage{
		Page:  p,
		Start: binary.BigEndian.Uint16(b[TRX_UNDO_PAGE_HDR+TRX_UNDO_PAGE_START:]),
		Free:  binary.BigEndian.Uint16(b[TRX_UNDO_PAGE_HDR+TRX_UNDO_PAGE_FREE:]),
	}
	up.SegHdr = int(up.Start) > TRX_UNDO_SEG_HDR
	if int(up.Start) < TRX_UNDO_SEG_HDR || int(up.Free) > PageSize {
		return up, fmt.Errorf("undo page %d: record area %d..%d is out of range", p.No, up.Start, up.Free)
	}
	for off := int(up.Start); off < int(up.Free) && len(up.Recs) < maxUndoRecsPerUndoPage; {
		r := parseUndoRec(b, off, idx, nil)
		up.Recs = append(up.Recs, r)
		if r.Err != nil || r.End <= off {
			break
		}
		off = r.End
	}
	return up, nil
}

// parseUndoRec decodes one undo record. When n is non-nil every field is also
// recorded there, so the annotation and the values come from a single pass.
func parseUndoRec(b []byte, off int, idx *IndexDef, n *Node) *UndoRec {
	r := &UndoRec{Off: off}
	p := &rp{b: b, i: off, n: n}
	// The first 2 bytes point past the record, which is where the next one starts.
	next := int(p.u16("next record offset"))
	tc := p.u8("type_cmpl")
	r.Type, r.CmplInfo, r.Extern = tc&0x0F, tc>>4&3, tc&TRX_UNDO_UPD_EXTERN != 0
	p.note(fmt.Sprintf("%#02x cmpl_info %d blob_undo=%d upd_extern=%d (%s)",
		tc, r.CmplInfo, boolInt(tc&TRX_UNDO_MODIFY_BLOB != 0), boolInt(r.Extern), r.TypeName()))
	if tc&TRX_UNDO_MODIFY_BLOB != 0 {
		p.u8("undo_rec_flags")
	}
	r.UndoNo = p.u64much("undo_no")
	r.TableID = p.u64much("table_id")
	if r.Type != TRX_UNDO_INSERT_REC {
		r.HasSys = true
		r.InfoBits = p.u8("info_bits")
		p.note(fmt.Sprintf("%#02x (delete=%d min_rec=%d)", r.InfoBits, r.InfoBits>>5&1, r.InfoBits>>4&1))
		r.TrxID = p.u64comp("DB_TRX_ID")
		v := p.u64comp("DB_ROLL_PTR")
		r.Roll = rollPtr(v)
		p.note(r.Roll.String())
		p.ref(r.Roll)
	}
	// One undo segment carries every record of a transaction, so a transaction
	// that touched several tables leaves records of all of them on this page.
	if idx == nil || (idx.TableID != 0 && idx.TableID != r.TableID) {
		// Without the matching table definition there is no way to know how many
		// key fields follow, so the record body cannot be walked.
		why := "not decoded: reach this record through a row's DB_ROLL_PTR"
		if idx != nil {
			why = fmt.Sprintf("not decoded: table_id %d is not the table being browsed (%d)", r.TableID, idx.TableID)
		}
		p.add("columns", p.i, 0, why)
		r.End = next
		return r
	}
	r.Key = p.undoFields(idx, "key fields", keyCols(idx))
	if r.Type == TRX_UNDO_UPD_EXIST_REC || r.Type == TRX_UNDO_UPD_DEL_REC {
		r.Upd = p.undoVector(idx, "old values")
	}
	if r.Type == TRX_UNDO_DEL_MARK_REC || r.CmplInfo&UPD_NODE_NO_ORD_CHANGE == 0 {
		p.undoOrdFields(idx)
	}
	r.Err = p.err
	r.End = next
	if next <= off || next > len(b) {
		r.End = p.i
		if r.Err == nil {
			r.Err = fmt.Errorf("next record offset %d does not follow the record at %d", next, off)
		}
	}
	return r
}

// keyCols is how many leading columns of the clustered index the undo record
// stores to identify the row: the primary key.
func keyCols(idx *IndexDef) int { return idx.NUniqueInTree }

// undoFields reads n column values in index order. n == 0 with an unknown index
// means the layout cannot be followed, so nothing is read.
func (p *rp) undoFields(idx *IndexDef, group string, n int) []UndoField {
	if n <= 0 {
		return nil
	}
	defer p.sub(group)()
	var out []UndoField
	for i := 0; i < n; i++ {
		out = append(out, p.undoField(colName(idx, i), idx, i))
	}
	return out
}

// undoVector reads the update vector: the old value of each changed column,
// each one preceded by its position in the clustered index.
func (p *rp) undoVector(idx *IndexDef, group string) []UndoField {
	defer p.sub(group)()
	n := int(p.comp("n_fields"))
	if n < 0 || n > maxUndoFieldsPerUndoRec {
		p.fail("update vector claims %d fields", n)
		return nil
	}
	var out []UndoField
	for i := 0; i < n && p.err == nil; i++ {
		pos := int(p.comp("field_no"))
		if pos >= recMaxNFields {
			p.fail("unsupported: the update vector holds a virtual column")
			return out
		}
		p.note(fmt.Sprintf("%d (%s)", pos, colName(idx, pos)))
		f := p.undoField(colName(idx, pos), idx, pos)
		out = append(out, f)
		if f.Extern {
			// An off-page column is followed by the LOB partial update it made.
			p.fail("unsupported: the update vector holds an off-page column")
			return out
		}
	}
	return out
}

// undoOrdFields reads the trailing section holding the old values of every
// column that orders some index; purge needs them to find the secondary index
// entries. The section starts with its own byte length.
func (p *rp) undoOrdFields(idx *IndexDef) {
	if p.err != nil || p.i+2 > len(p.b) {
		return
	}
	defer p.sub("index columns")()
	start := p.i
	ln := int(p.u16("section length"))
	end := start + ln
	if ln < 2 || end > len(p.b) {
		p.fail("index column section length %d does not fit the page", ln)
		return
	}
	for p.i < end && p.err == nil {
		pos := int(p.comp("field_no"))
		p.note(fmt.Sprintf("%d (%s)", pos, colName(idx, pos)))
		p.undoField(colName(idx, pos), idx, pos)
	}
}

func colName(idx *IndexDef, pos int) string {
	if idx == nil || pos < 0 || pos >= len(idx.Cols) {
		return fmt.Sprintf("field %d", pos)
	}
	return idx.Cols[pos].Name
}

// undoField reads one length-prefixed column value (trx_undo_rec_get_col_val).
func (p *rp) undoField(name string, idx *IndexDef, pos int) UndoField {
	f := UndoField{Name: name}
	ln := p.comp(name + " len")
	switch {
	case ln == UNIV_SQL_NULL:
		f.Value = "NULL"
		p.add(name, p.i, 0, "NULL")
		return f
	case ln == univExternStorageField:
		// An ordering column stored off-page: the original length, then the
		// prefix the undo record carries.
		p.comp(name + " orig len")
		ln = p.comp(name + " len")
		f.Extern = true
		ln &^= spatialStatusMask
	case ln > univExternStorageField:
		f.Extern = true
		ln = (ln - univExternStorageField) &^ spatialStatusMask
	}
	data := p.hexn(name, int(ln))
	if data == nil {
		return f
	}
	f.Value = decodeUndoValue(idx, pos, data)
	if f.Extern {
		f.Value += " (off-page prefix)"
	}
	p.note(f.Value)
	return f
}

func decodeUndoValue(idx *IndexDef, pos int, data []byte) string {
	if idx != nil && pos >= 0 && pos < len(idx.Cols) && idx.Cols[pos].Decode != nil {
		c := idx.Cols[pos]
		// A fixed-width decoder slices its own width off the front unchecked.
		// Unlike a record on an index page, the length here comes out of the
		// undo record itself, so a damaged one must not reach the decoder.
		if c.Fixed > 0 && len(data) != c.Fixed {
			return fmt.Sprintf("%x (%s wants %d bytes, the record stores %d)", data, c.Name, c.Fixed, len(data))
		}
		return c.Decode(data, nil)
	}
	return fmt.Sprintf("%x", data)
}

// annotateUndo lays out the page: the page header, the segment and log headers
// on the first page of a segment, then the records.
func (up *UndoPage) annotate(root *Node, idx *IndexDef) {
	b := up.Data
	h := root.Group("undo page header")
	r := reader{b, h}
	base := TRX_UNDO_PAGE_HDR
	t := r.u16("TRX_UNDO_PAGE_TYPE", base+TRX_UNDO_PAGE_TYPE)
	r.note(fmt.Sprintf("%d (%s)", t, undoSegTypes[t]))
	r.u16("TRX_UNDO_PAGE_START", base+TRX_UNDO_PAGE_START)
	r.u16("TRX_UNDO_PAGE_FREE", base+TRX_UNDO_PAGE_FREE)
	r.flstNode("TRX_UNDO_PAGE_NODE", base+TRX_UNDO_PAGE_NODE)

	if up.SegHdr {
		s := root.Group("undo segment header")
		r = reader{b, s}
		st := r.u16("TRX_UNDO_STATE", TRX_UNDO_SEG_HDR+TRX_UNDO_STATE)
		r.note(fmt.Sprintf("%d (%s)", st, undoStates[st]))
		r.u16("TRX_UNDO_LAST_LOG", TRX_UNDO_SEG_HDR+TRX_UNDO_LAST_LOG)
		r.fsegHeader("TRX_UNDO_FSEG_HEADER", TRX_UNDO_SEG_HDR+TRX_UNDO_FSEG_HEADER)
		r.flstBaseNode("TRX_UNDO_PAGE_LIST", TRX_UNDO_SEG_HDR+TRX_UNDO_PAGE_LIST)
		up.annotateLogHdrs(root)
	}

	recs := root.Group("Records")
	for _, rec := range up.Recs {
		g := recs.Add(fmt.Sprintf("record @%04x", rec.Off), rec.Off, rec.End-rec.Off, rec.Label())
		parseUndoRec(b, rec.Off, idx, g)
		if rec.Err != nil {
			g.Add("error", 0, 0, rec.Err.Error())
		}
	}
}

// annotateLogHdrs walks the undo log headers on the segment's first page. One
// segment can be handed to several transactions in turn, each with its own header.
func (up *UndoPage) annotateLogHdrs(root *Node) {
	for off, i := TRX_UNDO_LOG_HDR, 0; off > 0 && i < maxUndoLogsPerUndoPage; i++ {
		if off+TRX_UNDO_HISTORY_NODE+FLST_NODE_SIZE > PageSize {
			return
		}
		// The offset column tells the headers apart; keeping one name lets the
		// hex dump and the ? panel key on it.
		g := root.Group("undo log header")
		r := reader{up.Data, g}
		r.u64("TRX_UNDO_TRX_ID", off+TRX_UNDO_TRX_ID)
		r.u64("TRX_UNDO_TRX_NO", off+TRX_UNDO_TRX_NO)
		r.u16("TRX_UNDO_DEL_MARKS", off+TRX_UNDO_DEL_MARKS)
		r.u16("TRX_UNDO_LOG_START", off+TRX_UNDO_LOG_START)
		flags := r.u8("TRX_UNDO_FLAGS", off+TRX_UNDO_FLAGS)
		r.note(fmt.Sprintf("%#02x (xid=%d gtid=%d xa_prepare_gtid=%d)", flags, flags&1, flags>>1&1, flags>>2&1))
		r.u8("TRX_UNDO_DICT_TRANS", off+TRX_UNDO_DICT_TRANS)
		r.u64("TRX_UNDO_TABLE_ID", off+TRX_UNDO_TABLE_ID)
		next := r.u16("TRX_UNDO_NEXT_LOG", off+TRX_UNDO_NEXT_LOG)
		r.u16("TRX_UNDO_PREV_LOG", off+TRX_UNDO_PREV_LOG)
		r.flstNode("TRX_UNDO_HISTORY_NODE", off+TRX_UNDO_HISTORY_NODE)
		if int(next) <= off {
			return
		}
		off = int(next)
	}
}
