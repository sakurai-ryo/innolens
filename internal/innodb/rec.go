package innodb

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Compact record header (rem/rec.h).
const (
	REC_N_NEW_EXTRA_BYTES = 5
	REC_INFO_MIN_REC_FLAG = 0x10
	REC_INFO_DELETED_FLAG = 0x20
	REC_INFO_VERSION_FLAG = 0x40
	REC_INFO_INSTANT_FLAG = 0x80
	REC_STATUS_ORDINARY   = 0
	REC_STATUS_NODE_PTR   = 1
	REC_STATUS_INFIMUM    = 2
	REC_STATUS_SUPREMUM   = 3
	REC_NODE_PTR_SIZE     = 4
	FIELD_REF_SIZE        = 20
)

var statusNames = [...]string{"REC_STATUS_ORDINARY", "REC_STATUS_NODE_PTR", "REC_STATUS_INFIMUM", "REC_STATUS_SUPREMUM"}

// Col is one physical field of an index.
type Col struct {
	Name     string
	Fixed    int // fixed byte length; 0 means variable-length
	MaxLen   int // max byte length of a variable-length column; > 255 enables 2-byte lengths
	Nullable bool
	// Row versions (instant ADD/DROP COLUMN). 0 = present since table creation / never dropped.
	VersionAdded, VersionDropped uint8
	Default                      string                       // shown for columns added after the row's version
	Decode                       func([]byte, *[]Step) string // nil = hex; the slice records the decoding steps
}

func (c Col) presentIn(v uint8) bool {
	return c.VersionAdded <= v && (c.VersionDropped == 0 || c.VersionDropped > v)
}

// IndexDef describes the physical field layout of one B+tree.
type IndexDef struct {
	Name          string
	TableID       uint64 // se_private_id of the owning table
	ID            uint64
	RootPage      uint32
	Cols          []Col // physical order of leaf records
	NUniqueInTree int   // key fields stored in node pointer records
}

func (d *IndexDef) nullableIn(v uint8) int {
	n := 0
	for _, c := range d.Cols {
		if c.Nullable && c.presentIn(v) {
			n++
		}
	}
	return n
}

type FieldVal struct {
	Name     string
	Off, Len int // Len 0 with Off 0: value not stored (NULL or instant default)
	Null     bool
	Extern   bool
	Value    string

	// Where the record prefix encodes this field. NullOff 0 means the column is
	// not nullable; LenBytes 0 means no length is stored (fixed width, or NULL).
	NullOff, NullBit int
	LenOff, LenBytes int
	Var              bool // variable-length column, so it takes part in the length array

	dec func([]byte, *[]Step) string
}

type Rec struct {
	Off      int // record origin (start of data) within the page
	InfoBits uint8
	NOwned   uint8
	HeapNo   uint16
	Status   uint8
	Next     uint16 // relative offset to the next record origin
	Version  uint8
	Fields   []FieldVal
	Child    uint32 // node pointer child page
	Extra    int    // number of bytes before Off used by header, null bitmap and lengths
	End      int    // end of data
	Buf      []byte // page (or rebuilt page-sized buffer) the record was read from
}

func (r *Rec) Deleted() bool   { return r.InfoBits&REC_INFO_DELETED_FLAG != 0 }
func (r *Rec) MinRec() bool    { return r.InfoBits&REC_INFO_MIN_REC_FLAG != 0 }
func (r *Rec) Instant() bool   { return r.InfoBits&REC_INFO_INSTANT_FLAG != 0 }
func (r *Rec) Versioned() bool { return r.InfoBits&REC_INFO_VERSION_FLAG != 0 }

func (r *Rec) NextOff() int { return (r.Off + int(r.Next)) & 0xFFFF }

func (r *Rec) StatusName() string { return statusNames[r.Status&3] }

// parseRec decodes the record whose origin is at off. idx may be nil, in which
// case only the header is decoded.
func parseRec(b []byte, off int, idx *IndexDef) (*Rec, error) {
	if off < REC_N_NEW_EXTRA_BYTES || off >= PageSize-FIL_PAGE_DATA_END {
		return nil, fmt.Errorf("record offset %d out of page", off)
	}
	r := &Rec{Off: off, Buf: b}
	r.InfoBits = b[off-5] & 0xF0
	r.NOwned = b[off-5] & 0x0F
	hs := binary.BigEndian.Uint16(b[off-4:])
	r.HeapNo = hs >> 3
	r.Status = uint8(hs & 7)
	r.Next = binary.BigEndian.Uint16(b[off-2:])
	r.Extra = REC_N_NEW_EXTRA_BYTES
	r.End = off

	switch {
	case r.Status == REC_STATUS_INFIMUM || r.Status == REC_STATUS_SUPREMUM:
		r.Fields = []FieldVal{{Name: "value", Off: off, Len: 8, Value: fmt.Sprintf("%q", b[off:off+8])}}
		r.End = off + 8
		return r, nil
	case idx == nil || r.Instant():
		// Pre-8.0.29 instant ADD COLUMN layout is not decoded.
		return r, nil
	}

	nulls := off - REC_N_NEW_EXTRA_BYTES - 1 // byte holding null bit 0
	if r.Versioned() {
		r.Version = b[nulls]
		nulls--
		r.Extra++
	}
	cols := idx.Cols
	nNull := idx.nullableIn(r.Version)
	nodePtr := r.Status == REC_STATUS_NODE_PTR
	if nodePtr {
		cols = cols[:idx.NUniqueInTree]
		nNull = idx.nullableIn(0)
	}
	nullBytes := (nNull + 7) / 8
	lens := nulls - nullBytes // first length byte, read downwards
	r.Extra += nullBytes
	nullByte, nullBit := nulls, 0
	pos := off
	for _, c := range cols {
		if !nodePtr {
			if c.VersionDropped != 0 && c.VersionDropped <= r.Version {
				continue // dropped before this row was written: no data on disk
			}
			if c.VersionAdded > r.Version {
				r.Fields = append(r.Fields, FieldVal{Name: c.Name, Value: c.Default + " (instant default)"})
				continue
			}
		}
		f := FieldVal{Name: c.Name, Off: pos, Var: c.Fixed == 0, dec: c.Decode}
		if c.Nullable {
			if nullBit == 8 {
				nullByte--
				nullBit = 0
			}
			f.NullOff, f.NullBit = nullByte, nullBit
			isNull := b[nullByte]&(1<<nullBit) != 0
			nullBit++
			if isNull {
				f.Null, f.Off, f.Value = true, 0, "NULL"
				r.Fields = append(r.Fields, f)
				continue
			}
		}
		if c.Fixed > 0 {
			f.Len = c.Fixed
		} else {
			if lens < 0 {
				return nil, fmt.Errorf("record at %d: length bytes underflow", off)
			}
			l := int(b[lens])
			f.LenOff, f.LenBytes = lens, 1
			lens--
			r.Extra++
			if c.MaxLen > 255 && l&0x80 != 0 {
				l = (l&0x3F)<<8 | int(b[lens])
				f.Extern = b[lens+1]&0x40 != 0
				f.LenOff, f.LenBytes = lens, 2
				lens--
				r.Extra++
			}
			f.Len = l
		}
		if pos+f.Len > PageSize-FIL_PAGE_DATA_END {
			return nil, fmt.Errorf("record at %d: field %s runs past page end", off, c.Name)
		}
		data := b[pos : pos+f.Len]
		switch {
		case f.Extern:
			f.Value = externRef(data)
		case c.Decode != nil:
			f.Value = c.Decode(data, nil)
		default:
			f.Value = hex.EncodeToString(data)
		}
		pos += f.Len
		r.Fields = append(r.Fields, f)
	}
	if nodePtr {
		r.Child = binary.BigEndian.Uint32(b[pos:])
		r.Fields = append(r.Fields, FieldVal{Name: "child page", Off: pos, Len: REC_NODE_PTR_SIZE, Value: fmt.Sprint(r.Child)})
		pos += REC_NODE_PTR_SIZE
	}
	r.End = pos
	return r, nil
}

// externRef formats the trailing 20-byte off-page reference of a column.
func externRef(data []byte) string {
	if len(data) < FIELD_REF_SIZE {
		return hex.EncodeToString(data)
	}
	ref := data[len(data)-FIELD_REF_SIZE:]
	ln := binary.BigEndian.Uint64(ref[12:])
	return fmt.Sprintf("extern: prefix %d bytes, space_id %d page_no %d offset %d len %d (owner=%d inherited=%d)",
		len(data)-FIELD_REF_SIZE, binary.BigEndian.Uint32(ref), binary.BigEndian.Uint32(ref[4:]),
		binary.BigEndian.Uint32(ref[8:]), ln&0x0FFFFFFFFFFFFFFF, ref[12]>>7, (ref[12]>>6)&1)
}
