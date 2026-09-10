package innodb

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

// mlog_id_t (mtr0types.h). The *_8027 types belong to the pre-8.0.30 redo
// format and are never produced by a supported server.
const (
	MLOG_SINGLE_REC_FLAG = 128

	MLOG_1BYTE                        = 1
	MLOG_2BYTES                       = 2
	MLOG_4BYTES                       = 4
	MLOG_8BYTES                       = 8
	MLOG_REC_INSERT_8027              = 9
	MLOG_REC_CLUST_DELETE_MARK_8027   = 10
	MLOG_REC_SEC_DELETE_MARK          = 11
	MLOG_REC_UPDATE_IN_PLACE_8027     = 13
	MLOG_REC_DELETE_8027              = 14
	MLOG_LIST_END_DELETE_8027         = 15
	MLOG_LIST_START_DELETE_8027       = 16
	MLOG_LIST_END_COPY_CREATED_8027   = 17
	MLOG_PAGE_REORGANIZE_8027         = 18
	MLOG_PAGE_CREATE                  = 19
	MLOG_UNDO_INSERT                  = 20
	MLOG_UNDO_ERASE_END               = 21
	MLOG_UNDO_INIT                    = 22
	MLOG_UNDO_HDR_REUSE               = 24
	MLOG_UNDO_HDR_CREATE              = 25
	MLOG_REC_MIN_MARK                 = 26
	MLOG_IBUF_BITMAP_INIT             = 27
	MLOG_LSN                          = 28
	MLOG_INIT_FILE_PAGE               = 29
	MLOG_WRITE_STRING                 = 30
	MLOG_MULTI_REC_END                = 31
	MLOG_DUMMY_RECORD                 = 32
	MLOG_FILE_CREATE                  = 33
	MLOG_FILE_RENAME                  = 34
	MLOG_FILE_DELETE                  = 35
	MLOG_COMP_REC_MIN_MARK            = 36
	MLOG_COMP_PAGE_CREATE             = 37
	MLOG_COMP_REC_INSERT_8027         = 38
	MLOG_COMP_REC_CLUST_DEL_MARK_8027 = 39
	MLOG_COMP_REC_SEC_DELETE_MARK     = 40
	MLOG_COMP_REC_UPDATE_IN_PLACE8027 = 41
	MLOG_COMP_REC_DELETE_8027         = 42
	MLOG_COMP_LIST_END_DELETE_8027    = 43
	MLOG_COMP_LIST_START_DELETE_8027  = 44
	MLOG_COMP_LIST_END_COPY_CRE_8027  = 45
	MLOG_COMP_PAGE_REORGANIZE_8027    = 46
	MLOG_ZIP_WRITE_NODE_PTR           = 48
	MLOG_ZIP_WRITE_BLOB_PTR           = 49
	MLOG_ZIP_WRITE_HEADER             = 50
	MLOG_ZIP_PAGE_COMPRESS            = 51
	MLOG_ZIP_PAGE_COMPRESS_ND_8027    = 52
	MLOG_ZIP_PAGE_REORGANIZE_8027     = 53
	MLOG_PAGE_CREATE_RTREE            = 57
	MLOG_COMP_PAGE_CREATE_RTREE       = 58
	MLOG_INIT_FILE_PAGE2              = 59
	MLOG_INDEX_LOAD                   = 61
	MLOG_TABLE_DYNAMIC_META           = 62
	MLOG_PAGE_CREATE_SDI              = 63
	MLOG_COMP_PAGE_CREATE_SDI         = 64
	MLOG_FILE_EXTEND                  = 65
	MLOG_TEST                         = 66
	MLOG_REC_INSERT                   = 67
	MLOG_REC_CLUST_DELETE_MARK        = 68
	MLOG_REC_DELETE                   = 69
	MLOG_REC_UPDATE_IN_PLACE          = 70
	MLOG_LIST_END_COPY_CREATED        = 71
	MLOG_PAGE_REORGANIZE              = 72
	MLOG_ZIP_PAGE_REORGANIZE          = 73
	MLOG_ZIP_PAGE_COMPRESS_NO_DATA    = 74
	MLOG_LIST_END_DELETE              = 75
	MLOG_LIST_START_DELETE            = 76
)

var mlogNames = map[uint8]string{
	MLOG_1BYTE: "MLOG_1BYTE", MLOG_2BYTES: "MLOG_2BYTES", MLOG_4BYTES: "MLOG_4BYTES",
	MLOG_8BYTES: "MLOG_8BYTES", MLOG_REC_INSERT_8027: "MLOG_REC_INSERT_8027",
	MLOG_REC_CLUST_DELETE_MARK_8027: "MLOG_REC_CLUST_DELETE_MARK_8027",
	MLOG_REC_SEC_DELETE_MARK:        "MLOG_REC_SEC_DELETE_MARK",
	MLOG_REC_UPDATE_IN_PLACE_8027:   "MLOG_REC_UPDATE_IN_PLACE_8027",
	MLOG_REC_DELETE_8027:            "MLOG_REC_DELETE_8027",
	MLOG_LIST_END_DELETE_8027:       "MLOG_LIST_END_DELETE_8027",
	MLOG_LIST_START_DELETE_8027:     "MLOG_LIST_START_DELETE_8027",
	MLOG_LIST_END_COPY_CREATED_8027: "MLOG_LIST_END_COPY_CREATED_8027",
	MLOG_PAGE_REORGANIZE_8027:       "MLOG_PAGE_REORGANIZE_8027",
	MLOG_PAGE_CREATE:                "MLOG_PAGE_CREATE", MLOG_UNDO_INSERT: "MLOG_UNDO_INSERT",
	MLOG_UNDO_ERASE_END: "MLOG_UNDO_ERASE_END", MLOG_UNDO_INIT: "MLOG_UNDO_INIT",
	MLOG_UNDO_HDR_REUSE: "MLOG_UNDO_HDR_REUSE", MLOG_UNDO_HDR_CREATE: "MLOG_UNDO_HDR_CREATE",
	MLOG_REC_MIN_MARK: "MLOG_REC_MIN_MARK", MLOG_IBUF_BITMAP_INIT: "MLOG_IBUF_BITMAP_INIT",
	MLOG_LSN: "MLOG_LSN", MLOG_INIT_FILE_PAGE: "MLOG_INIT_FILE_PAGE",
	MLOG_WRITE_STRING: "MLOG_WRITE_STRING", MLOG_MULTI_REC_END: "MLOG_MULTI_REC_END",
	MLOG_DUMMY_RECORD: "MLOG_DUMMY_RECORD", MLOG_FILE_CREATE: "MLOG_FILE_CREATE",
	MLOG_FILE_RENAME: "MLOG_FILE_RENAME", MLOG_FILE_DELETE: "MLOG_FILE_DELETE",
	MLOG_COMP_REC_MIN_MARK: "MLOG_COMP_REC_MIN_MARK", MLOG_COMP_PAGE_CREATE: "MLOG_COMP_PAGE_CREATE",
	MLOG_COMP_REC_INSERT_8027:         "MLOG_COMP_REC_INSERT_8027",
	MLOG_COMP_REC_CLUST_DEL_MARK_8027: "MLOG_COMP_REC_CLUST_DELETE_MARK_8027",
	MLOG_COMP_REC_SEC_DELETE_MARK:     "MLOG_COMP_REC_SEC_DELETE_MARK",
	MLOG_COMP_REC_UPDATE_IN_PLACE8027: "MLOG_COMP_REC_UPDATE_IN_PLACE_8027",
	MLOG_COMP_REC_DELETE_8027:         "MLOG_COMP_REC_DELETE_8027",
	MLOG_COMP_LIST_END_DELETE_8027:    "MLOG_COMP_LIST_END_DELETE_8027",
	MLOG_COMP_LIST_START_DELETE_8027:  "MLOG_COMP_LIST_START_DELETE_8027",
	MLOG_COMP_LIST_END_COPY_CRE_8027:  "MLOG_COMP_LIST_END_COPY_CREATED_8027",
	MLOG_COMP_PAGE_REORGANIZE_8027:    "MLOG_COMP_PAGE_REORGANIZE_8027",
	MLOG_ZIP_WRITE_NODE_PTR:           "MLOG_ZIP_WRITE_NODE_PTR",
	MLOG_ZIP_WRITE_BLOB_PTR:           "MLOG_ZIP_WRITE_BLOB_PTR",
	MLOG_ZIP_WRITE_HEADER:             "MLOG_ZIP_WRITE_HEADER",
	MLOG_ZIP_PAGE_COMPRESS:            "MLOG_ZIP_PAGE_COMPRESS",
	MLOG_ZIP_PAGE_COMPRESS_ND_8027:    "MLOG_ZIP_PAGE_COMPRESS_NO_DATA_8027",
	MLOG_ZIP_PAGE_REORGANIZE_8027:     "MLOG_ZIP_PAGE_REORGANIZE_8027",
	MLOG_PAGE_CREATE_RTREE:            "MLOG_PAGE_CREATE_RTREE",
	MLOG_COMP_PAGE_CREATE_RTREE:       "MLOG_COMP_PAGE_CREATE_RTREE",
	MLOG_INIT_FILE_PAGE2:              "MLOG_INIT_FILE_PAGE2", MLOG_INDEX_LOAD: "MLOG_INDEX_LOAD",
	MLOG_TABLE_DYNAMIC_META: "MLOG_TABLE_DYNAMIC_META", MLOG_PAGE_CREATE_SDI: "MLOG_PAGE_CREATE_SDI",
	MLOG_COMP_PAGE_CREATE_SDI: "MLOG_COMP_PAGE_CREATE_SDI", MLOG_FILE_EXTEND: "MLOG_FILE_EXTEND",
	MLOG_TEST: "MLOG_TEST", MLOG_REC_INSERT: "MLOG_REC_INSERT",
	MLOG_REC_CLUST_DELETE_MARK: "MLOG_REC_CLUST_DELETE_MARK", MLOG_REC_DELETE: "MLOG_REC_DELETE",
	MLOG_REC_UPDATE_IN_PLACE:   "MLOG_REC_UPDATE_IN_PLACE",
	MLOG_LIST_END_COPY_CREATED: "MLOG_LIST_END_COPY_CREATED",
	MLOG_PAGE_REORGANIZE:       "MLOG_PAGE_REORGANIZE", MLOG_ZIP_PAGE_REORGANIZE: "MLOG_ZIP_PAGE_REORGANIZE",
	MLOG_ZIP_PAGE_COMPRESS_NO_DATA: "MLOG_ZIP_PAGE_COMPRESS_NO_DATA",
	MLOG_LIST_END_DELETE:           "MLOG_LIST_END_DELETE", MLOG_LIST_START_DELETE: "MLOG_LIST_START_DELETE",
}

func MLogName(t uint8) string {
	if s, ok := mlogNames[t]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", t)
}

// InsertLog is the decoded body of MLOG_REC_INSERT: the record image is stored
// as a suffix, with the first MismatchIndex bytes shared with the record the
// new one is inserted after.
type InsertLog struct {
	CursorOff        int
	EndSegLen        int
	HasHdr           bool // the log carries InfoBits/OriginOffset/MismatchIndex
	InfoBits         uint8
	OriginOffset     int
	MismatchIndex    int
	DataOff, DataLen int
}

// WriteLog is the decoded body of the record types that put bytes straight
// into a page.
type WriteLog struct {
	Off int
	Val []byte
}

// UpdField is one entry of an update vector: the new value of one field of the
// record, named by its position in the index.
type UpdField struct {
	No   int
	Val  []byte
	Null bool
}

// UpdateLog is the decoded body of the record types that change a record where
// it lies: the delete mark, the system columns and the update vector.
type UpdateLog struct {
	RecOff  int
	DelMark int // -1 when the record type carries no delete mark
	SysPos  int // field number of DB_TRX_ID; -1 when no system columns are logged
	TrxID   uint64
	Roll    []byte // the 7 bytes of the new DB_ROLL_PTR
	// HasInfo says whether InfoBits was logged; an update vector carries the
	// info bits the record ends up with.
	HasInfo  bool
	InfoBits uint8
	Fields   []UpdField
}

// RedoRec is one log record inside an mtr.
type RedoRec struct {
	Type     uint8
	Single   bool
	SpaceID  uint32
	PageNo   uint32
	Off, Len int // byte range inside LogStream.Buf
	LSN      uint64
	// EndLSN is where the record ends, which is the LSN a page carries once the
	// record has been applied to it.
	EndLSN uint64
	Idx    *IndexDef  // index definition logged inside the record, if any
	Ins    *InsertLog // set for MLOG_REC_INSERT
	Write  *WriteLog  // set for the types that write bytes at a page offset
	Upd    *UpdateLog // set for the delete-mark and update-in-place types
	Err    error
}

func (r *RedoRec) TypeName() string { return MLogName(r.Type) }

// HasPage is false for the record types that do not address a page.
func (r *RedoRec) HasPage() bool {
	switch r.Type {
	case MLOG_MULTI_REC_END, MLOG_DUMMY_RECORD, MLOG_TABLE_DYNAMIC_META, MLOG_LSN:
		return false
	}
	return true
}

// MTR is one mini-transaction: either a single flagged record, or a run of
// records closed by MLOG_MULTI_REC_END.
type MTR struct {
	Off              int
	StartLSN, EndLSN uint64
	Recs             []*RedoRec
	Err              error
}

// MTRs walks the whole stream. Parsing stops at the first record that cannot
// be decoded, because record lengths are only known by decoding the body.
func (s *LogStream) MTRs() []*MTR {
	var out []*MTR
	for i := 0; i < len(s.Buf); {
		m := &MTR{Off: i, StartLSN: s.LSN(i)}
		for {
			r := parseRedoRec(s.Buf, i, nil)
			r.LSN = s.LSN(r.Off)
			if r.Len > 0 {
				r.EndLSN = s.LSN(r.Off + r.Len)
			}
			m.Recs = append(m.Recs, r)
			if r.Err != nil || r.Len <= 0 {
				m.Err = r.Err
				if m.Err == nil {
					m.Err = fmt.Errorf("zero length record at lsn %d", r.LSN)
				}
				break
			}
			i += r.Len
			if r.Single || r.Type == MLOG_MULTI_REC_END || r.Type == MLOG_DUMMY_RECORD {
				break
			}
			if i >= len(s.Buf) {
				m.Err = fmt.Errorf("mtr is not terminated by MLOG_MULTI_REC_END")
				break
			}
		}
		m.EndLSN = s.LSN(i)
		out = append(out, m)
		if m.Err != nil {
			break
		}
	}
	return out
}

// Annotate re-parses the record and returns its field tree. Offsets are
// relative to the start of the record.
func (r *RedoRec) Annotate(buf []byte) *Node {
	root := &Node{Name: r.TypeName(), Len: r.Len}
	rec := parseRedoRec(buf, r.Off, root)
	if rec.Err != nil {
		root.Add("error", 0, 0, rec.Err.Error())
	}
	for _, c := range root.Children {
		shiftNode(c, -r.Off)
	}
	return root
}

func shiftNode(n *Node, by int) {
	if n.Len > 0 {
		n.Off += by
	}
	for _, c := range n.Children {
		shiftNode(c, by)
	}
}

// rp reads a redo record, optionally recording every field as a Node.
type rp struct {
	b   []byte
	i   int
	n   *Node
	err error
}

func (p *rp) fail(format string, a ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format, a...)
	}
}

func (p *rp) need(n int) bool {
	if p.err != nil {
		return false
	}
	if p.i+n > len(p.b) {
		p.fail("truncated record at offset %d: need %d more bytes", p.i, n)
		return false
	}
	return true
}

func (p *rp) add(name string, off, ln int, v string) {
	if p.n != nil {
		p.n.Add(name, off, ln, v)
	}
}

// note replaces the value of the field that was just read.
func (p *rp) note(v string) {
	if p.n != nil && len(p.n.Children) > 0 {
		p.n.Children[len(p.n.Children)-1].Value = v
	}
}

// ref attaches a jump target to the field that was just read.
func (p *rp) ref(v any) {
	if p.n != nil && len(p.n.Children) > 0 {
		p.n.Children[len(p.n.Children)-1].Ref = v
	}
}

// sub makes following fields children of a new group; call the returned func to
// go back up.
func (p *rp) sub(name string) func() {
	if p.n == nil {
		return func() {}
	}
	old := p.n
	p.n = old.Group(name)
	return func() { p.n = old }
}

func (p *rp) u8(name string) uint8 {
	if !p.need(1) {
		return 0
	}
	v := p.b[p.i]
	p.add(name, p.i, 1, fmt.Sprint(v))
	p.i++
	return v
}

func (p *rp) u16(name string) uint16 {
	if !p.need(2) {
		return 0
	}
	v := binary.BigEndian.Uint16(p.b[p.i:])
	p.add(name, p.i, 2, fmt.Sprint(v))
	p.i += 2
	return v
}

func (p *rp) u32(name string) uint32 {
	if !p.need(4) {
		return 0
	}
	v := binary.BigEndian.Uint32(p.b[p.i:])
	p.add(name, p.i, 4, fmt.Sprint(v))
	p.i += 4
	return v
}

func (p *rp) u64(name string) uint64 {
	if !p.need(8) {
		return 0
	}
	v := binary.BigEndian.Uint64(p.b[p.i:])
	p.add(name, p.i, 8, fmt.Sprint(v))
	p.i += 8
	return v
}

func (p *rp) hexn(name string, n int) []byte {
	if n < 0 {
		p.fail("negative length %d for %s", n, name)
		return nil
	}
	if !p.need(n) {
		return nil
	}
	v := p.b[p.i : p.i+n]
	p.add(name, p.i, n, hex.EncodeToString(v))
	p.i += n
	return v
}

func be24(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }

// comp reads mach_read_next_compressed: a 32-bit value in 1..5 bytes.
func (p *rp) comp(name string) uint32 {
	if !p.need(1) {
		return 0
	}
	b := p.b[p.i:]
	var v uint32
	var n int
	switch h := b[0]; {
	case h < 0x80:
		v, n = uint32(h), 1
	case h < 0xC0:
		n = 2
	case h < 0xE0:
		n = 3
	case h < 0xF0:
		n = 4
	case h < 0xF8:
		n = 5
	case h < 0xFC:
		n = 2
	case h < 0xFE:
		n = 3
	default:
		n = 4
	}
	if !p.need(n) {
		return 0
	}
	switch h := b[0]; {
	case h < 0x80:
	case h < 0xC0:
		v = uint32(binary.BigEndian.Uint16(b)) & 0x3FFF
	case h < 0xE0:
		v = be24(b) & 0x1FFFFF
	case h < 0xF0:
		v = binary.BigEndian.Uint32(b) & 0xFFFFFFF
	case h < 0xF8:
		v = binary.BigEndian.Uint32(b[1:])
	case h < 0xFC:
		v = uint32(binary.BigEndian.Uint16(b))&0x3FF | 0xFFFFFC00
	case h < 0xFE:
		v = be24(b)&0x1FFFF | 0xFFFE0000
	default:
		v = be24(b[1:]) | 0xFF000000
	}
	p.add(name, p.i, n, fmt.Sprint(v))
	p.i += n
	return v
}

// u64comp reads mach_u64_parse_compressed: a compressed high half plus 4 bytes.
func (p *rp) u64comp(name string) uint64 {
	start := p.i
	saved := p.n
	p.n = nil
	hi := p.comp("")
	if !p.need(4) {
		p.n = saved
		return 0
	}
	lo := binary.BigEndian.Uint32(p.b[p.i:])
	p.i += 4
	p.n = saved
	v := uint64(hi)<<32 | uint64(lo)
	p.add(name, start, p.i-start, fmt.Sprint(v))
	return v
}

// u64much reads mach_read_next_much_compressed.
func (p *rp) u64much(name string) uint64 {
	if !p.need(1) {
		return 0
	}
	start := p.i
	saved := p.n
	p.n = nil
	var v uint64
	if p.b[p.i] != 0xFF {
		v = uint64(p.comp(""))
	} else {
		p.i++
		hi := p.comp("")
		lo := p.comp("")
		v = uint64(hi)<<32 | uint64(lo)
	}
	p.n = saved
	p.add(name, start, p.i-start, fmt.Sprint(v))
	return v
}

// path reads a 2-byte length followed by a NUL-terminated file name.
func (p *rp) path(name string) string {
	ln := int(p.u16(name + " len"))
	if !p.need(ln) {
		return ""
	}
	s := cstring(p.b[p.i : p.i+ln])
	p.add(name, p.i, ln, s)
	p.i += ln
	return s
}

// sysVals reads the DB_TRX_ID position, DB_ROLL_PTR and DB_TRX_ID of an update.
func (p *rp) sysVals(u *UpdateLog) {
	u.SysPos = int(p.comp("sys field pos"))
	u.Roll = p.hexn("DB_ROLL_PTR", 7)
	u.TrxID = p.u64comp("DB_TRX_ID")
}

func parseRedoRec(b []byte, off int, ann *Node) *RedoRec {
	r := &RedoRec{Off: off}
	p := &rp{b: b, i: off, n: ann}
	if off >= len(b) {
		r.Err = fmt.Errorf("record offset %d past end of log stream", off)
		return r
	}
	t := b[off]
	r.Single = t&MLOG_SINGLE_REC_FLAG != 0
	r.Type = t &^ MLOG_SINGLE_REC_FLAG
	p.add("type", off, 1, fmt.Sprintf("%s (%d)%s", MLogName(r.Type), r.Type, singleSuffix(r.Single)))
	p.i++

	switch r.Type {
	case MLOG_MULTI_REC_END, MLOG_DUMMY_RECORD:
		if r.Single {
			r.Err = fmt.Errorf("%s must not carry MLOG_SINGLE_REC_FLAG", MLogName(r.Type))
		}
		r.Len = 1
		return r
	case MLOG_TABLE_DYNAMIC_META:
		p.u64much("table id")
		p.u64much("version")
		p.dynamicMeta()
	default:
		r.SpaceID = p.comp("space id")
		r.PageNo = p.comp("page no")
		p.body(r)
	}
	r.Len = p.i - off
	if p.err != nil {
		r.Err = p.err
	}
	return r
}

func singleSuffix(single bool) string {
	if single {
		return " | MLOG_SINGLE_REC_FLAG"
	}
	return ""
}

// dynamicMeta reads a persister payload (dict0dict.cc).
func (p *rp) dynamicMeta() {
	switch typ := p.u8("persistent type"); typ {
	case 1: // PM_INDEX_CORRUPTED
		n := int(p.u8("n indexes"))
		for i := 0; i < n; i++ {
			p.u32("space id")
			p.u64("index id")
		}
	case 2: // PM_TABLE_AUTO_INC
		p.u64much("autoinc")
	default:
		p.fail("unsupported dynamic metadata type %d", typ)
	}
}

func (p *rp) body(r *RedoRec) {
	switch r.Type {
	case MLOG_1BYTE, MLOG_2BYTES, MLOG_4BYTES:
		off := int(p.u16("page offset"))
		v := p.comp("value")
		// The type number is the width: MLOG_1BYTE is 1, MLOG_4BYTES is 4.
		p.write(r, off, beBytes(uint64(v), int(r.Type)))
	case MLOG_8BYTES:
		off := int(p.u16("page offset"))
		p.write(r, off, beBytes(p.u64comp("value"), 8))
	case MLOG_WRITE_STRING:
		off := int(p.u16("page offset"))
		n := int(p.u16("len"))
		p.write(r, off, p.hexn("value", n))
	case MLOG_PAGE_CREATE, MLOG_COMP_PAGE_CREATE, MLOG_PAGE_CREATE_RTREE,
		MLOG_COMP_PAGE_CREATE_RTREE, MLOG_PAGE_CREATE_SDI, MLOG_COMP_PAGE_CREATE_SDI,
		MLOG_INIT_FILE_PAGE, MLOG_INIT_FILE_PAGE2, MLOG_IBUF_BITMAP_INIT,
		MLOG_UNDO_ERASE_END, MLOG_LSN:
		// no body
	case MLOG_REC_INSERT:
		r.Idx = p.index()
		p.insert(r)
	case MLOG_REC_CLUST_DELETE_MARK:
		r.Idx = p.index()
		u := &UpdateLog{SysPos: -1}
		p.u8("flags")
		u.DelMark = int(p.u8("delete mark"))
		p.sysVals(u)
		u.RecOff = int(p.u16("rec offset"))
		p.update(r, u)
	case MLOG_REC_SEC_DELETE_MARK:
		u := &UpdateLog{SysPos: -1}
		u.DelMark = int(p.u8("delete mark"))
		u.RecOff = int(p.u16("rec offset"))
		p.update(r, u)
	case MLOG_REC_UPDATE_IN_PLACE:
		r.Idx = p.index()
		u := &UpdateLog{DelMark: -1, SysPos: -1}
		p.u8("flags")
		p.sysVals(u)
		u.RecOff = int(p.u16("rec offset"))
		u.Fields = p.updateVector(u)
		p.update(r, u)
	case MLOG_REC_DELETE, MLOG_LIST_END_DELETE, MLOG_LIST_START_DELETE:
		r.Idx = p.index()
		p.u16("rec offset")
	case MLOG_LIST_END_COPY_CREATED:
		r.Idx = p.index()
		n := int(p.u32("log data len"))
		p.hexn("records", n)
	case MLOG_PAGE_REORGANIZE:
		r.Idx = p.index()
	case MLOG_ZIP_PAGE_REORGANIZE:
		r.Idx = p.index()
		p.u8("compression level")
	case MLOG_REC_MIN_MARK, MLOG_COMP_REC_MIN_MARK:
		p.u16("rec offset")
	case MLOG_UNDO_INSERT:
		n := int(p.u16("len"))
		p.hexn("undo record", n)
	case MLOG_UNDO_INIT:
		p.comp("undo page type")
	case MLOG_UNDO_HDR_CREATE, MLOG_UNDO_HDR_REUSE:
		p.u64comp("trx id")
	case MLOG_INDEX_LOAD:
		p.u64("table id")
	case MLOG_FILE_CREATE:
		p.u32("fsp flags")
		p.path("file name")
	case MLOG_FILE_DELETE:
		p.path("file name")
	case MLOG_FILE_RENAME:
		p.path("from")
		p.path("to")
	case MLOG_FILE_EXTEND:
		p.u64("offset")
		p.u64("size")
	default:
		p.fail("unsupported: %s body is not decoded, so the record length is unknown", MLogName(r.Type))
	}
}

// insert decodes page_cur_parse_insert_rec.
func (p *rp) insert(r *RedoRec) {
	ins := &InsertLog{}
	ins.CursorOff = int(p.u16("cursor rec offset"))
	ins.EndSegLen = int(p.comp("end seg len"))
	ins.HasHdr = ins.EndSegLen&1 != 0
	if ins.HasHdr {
		ins.InfoBits = p.u8("info and status bits")
		ins.OriginOffset = int(p.comp("origin offset"))
		ins.MismatchIndex = int(p.comp("mismatch index"))
	}
	n := ins.EndSegLen >> 1
	ins.DataOff = p.i
	ins.DataLen = n
	p.hexn("end segment", n)
	if p.err == nil {
		r.Ins = ins
	}
}

// updateVector decodes row_upd_index_parse.
func (p *rp) updateVector(u *UpdateLog) []UpdField {
	done := p.sub("update vector")
	defer done()
	u.InfoBits, u.HasInfo = p.u8("info bits"), true
	n := int(p.comp("n fields"))
	var out []UpdField
	for i := 0; i < n; i++ {
		f := p.sub(fmt.Sprintf("field %d", i))
		uf := UpdField{No: int(p.comp("field no"))}
		ln := p.comp("len")
		if ln != 0xFFFFFFFF {
			uf.Val = p.hexn("value", int(ln))
		} else {
			uf.Null = true
			p.add("value", 0, 0, "NULL")
		}
		f()
		if p.err != nil {
			return out
		}
		out = append(out, uf)
	}
	return out
}

// write and update attach a decoded body to the record, but only when the whole
// body parsed: a half-read body would replay as a wrong write.
func (p *rp) write(r *RedoRec, off int, val []byte) {
	if p.err == nil {
		r.Write = &WriteLog{Off: off, Val: val}
	}
}

func (p *rp) update(r *RedoRec, u *UpdateLog) {
	if p.err == nil {
		r.Upd = u
	}
}

// beBytes is the big-endian image of a value of the given width, which is how
// mlog_write_ulint puts it into a page.
func beBytes(v uint64, width int) []byte {
	b := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}

// index decodes mlog_parse_index: the physical field layout that the record was
// written with. Only INDEX_LOG_VERSION 1 (8.0.30 and newer) exists.
func (p *rp) index() *IndexDef {
	done := p.sub("index")
	defer done()
	if v := p.u8("index log version"); v != 1 && p.err == nil {
		p.fail("unsupported index log version %d", v)
		return nil
	}
	flag := p.u8("flag")
	isComp := flag&0x01 != 0
	isVersioned := flag&0x02 != 0
	isInstant := flag&0x04 != 0
	if !isComp && !isVersioned {
		// A REDUNDANT index logs no field information at all: n and n_uniq are
		// both 1 (parse_index_column_counts), and nothing follows the flag. The
		// change buffer tree is the one that turns up in a real log.
		p.add("row format", 0, 0, "REDUNDANT (no field information is logged)")
		return nil
	}
	n := int(p.u16("n fields"))
	if !isComp {
		p.fail("unsupported: REDUNDANT row format in redo")
		return nil
	}
	if isInstant {
		p.u16("n instant cols")
	}
	nUniq := int(p.u16("n uniq"))
	if p.err != nil {
		return nil
	}
	if n <= 0 || n > 1024 {
		p.fail("implausible field count %d in logged index", n)
		return nil
	}
	idx := &IndexDef{Name: "(from redo)", NUniqueInTree: nUniq, Cols: make([]Col, n)}
	fields := p.sub("fields")
	for i := 0; i < n; i++ {
		ln := int(p.u16(fmt.Sprintf("field %d len", i)))
		if p.err != nil {
			fields()
			return nil
		}
		c := Col{Name: fmt.Sprintf("field %d", i), Nullable: ln&0x8000 == 0}
		if l := ln & 0x7FFF; l != 0 && l != 0x7FFF {
			c.Fixed = l
		} else {
			c.MaxLen = l
		}
		idx.Cols[i] = c
	}
	fields()
	if nUniq != n && nUniq+1 < n {
		idx.Cols[nUniq].Name = "DB_TRX_ID"
		idx.Cols[nUniq+1].Name = "DB_ROLL_PTR"
	}
	if isVersioned {
		p.versionedFields(idx)
	}
	if p.err != nil {
		return nil
	}
	return idx
}

// versionedFields decodes parse_index_versioned_fields and reorders the columns
// into their physical order, mirroring mlog_parse_index_v1.
func (p *rp) versionedFields(idx *IndexDef) {
	done := p.sub("row versions")
	defer done()
	n := len(idx.Cols)
	phy := make([]int, n)
	for i := range phy {
		phy[i] = -1
	}
	used := map[int]bool{}
	nInst := int(p.u16("n instant fields"))
	for i := 0; i < nInst; i++ {
		g := p.sub(fmt.Sprintf("field %d", i))
		logical := int(p.u16("logical pos"))
		pos := int(p.u16("physical pos"))
		var added, dropped uint8
		if pos&0x8000 != 0 {
			pos &^= 0x8000
			added = p.u8("version added")
		}
		if pos&0x4000 != 0 {
			pos &^= 0x4000
			dropped = p.u8("version dropped")
		}
		g()
		if p.err != nil {
			return
		}
		if logical >= n || pos >= n {
			p.fail("logged row version refers to field %d/%d of %d", logical, pos, n)
			return
		}
		idx.Cols[logical].VersionAdded = added
		idx.Cols[logical].VersionDropped = dropped
		phy[logical] = pos
		used[pos] = true
	}
	shift := 0
	for i := 0; i < n; i++ {
		if phy[i] == -1 {
			pos := i + shift
			for used[pos] {
				pos++
			}
			if pos >= n {
				p.fail("cannot place field %d in the physical layout", i)
				return
			}
			phy[i], used[pos] = pos, true
		} else if idx.Cols[i].VersionAdded != 0 && idx.Cols[i].VersionDropped == 0 {
			shift--
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return phy[order[a]] < phy[order[b]] })
	cols := make([]Col, n)
	for i, o := range order {
		cols[i] = idx.Cols[o]
	}
	idx.Cols = cols
}
