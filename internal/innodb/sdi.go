package innodb

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

// sdiIndex is CLUST_IND_SDI (dict_sdi_create_idx_in_mem): type, id, then the
// system columns, then the lengths and the zlib-compressed JSON blob.
var sdiIndex = &IndexDef{
	Name: "CLUST_IND_SDI",
	Cols: []Col{
		{Name: "type", Fixed: 4, Decode: decUint},
		{Name: "id", Fixed: 8, Decode: decUint},
		{Name: "DB_TRX_ID", Fixed: 6, Decode: decUint},
		{Name: "DB_ROLL_PTR", Fixed: 7},
		{Name: "uncompressed_len", Fixed: 4, Decode: decUint},
		{Name: "compressed_len", Fixed: 4, Decode: decUint},
		{Name: "data", MaxLen: 1 << 31, Decode: func(b []byte, _ *[]Step) string { return fmt.Sprintf("zlib %d bytes", len(b)) }},
	},
	NUniqueInTree: 2,
}

const (
	SDITypeTable      = 1
	SDITypeTablespace = 2
)

type SDIRecord struct {
	Type uint32
	ID   uint64
	JSON []byte
}

// ReadSDI walks the SDI B+tree and returns the decompressed records.
func (s *Space) ReadSDI() ([]SDIRecord, error) {
	p0, err := s.Page(0)
	if err != nil {
		return nil, err
	}
	// A file whose page 0 was never flushed has no FSP header to read the root
	// page out of, so the bytes there would be someone else's.
	if p0.FIL.Type != FIL_PAGE_TYPE_FSP_HDR {
		return nil, fmt.Errorf("%s: page 0 is %s, not an FSP header: nothing of this tablespace has reached disk", s.Path, p0.TypeName())
	}
	no := p0.sdiRootPage()
	// A tablespace created since page 0 was last flushed still has the root page
	// number of the freshly created file, which is 0.
	if no == 0 {
		return nil, fmt.Errorf("%s: page 0 carries no SDI root page", s.Path)
	}
	sdiIndex.RootPage = no
	// Descend along the leftmost node pointers to the first leaf.
	for {
		p, err := s.Page(no)
		if err != nil {
			return nil, err
		}
		if p.FIL.Type != FIL_PAGE_SDI {
			return nil, fmt.Errorf("page %d is %s, expected FIL_PAGE_SDI", no, p.TypeName())
		}
		ip, err := p.ParseIndex(sdiIndex)
		if err != nil {
			return nil, err
		}
		if ip.Hdr.Level == 0 {
			break
		}
		ch := ip.Children()
		if len(ch) == 0 {
			return nil, fmt.Errorf("SDI page %d has no node pointers", no)
		}
		no = ch[0]
	}
	var out []SDIRecord
	for no != FIL_NULL {
		p, err := s.Page(no)
		if err != nil {
			return nil, err
		}
		ip, err := p.ParseIndex(sdiIndex)
		if err != nil {
			return nil, err
		}
		for _, r := range ip.UserRecs() {
			if r.Deleted() {
				continue
			}
			rec, err := sdiRecord(p.Data, r)
			if err != nil {
				return nil, fmt.Errorf("SDI page %d record @%d: %w", no, r.Off, err)
			}
			out = append(out, rec)
		}
		no = p.FIL.Next
	}
	return out, nil
}

func sdiRecord(b []byte, r *Rec) (SDIRecord, error) {
	f := r.Fields
	if len(f) != 7 {
		return SDIRecord{}, fmt.Errorf("unexpected field count %d", len(f))
	}
	data := f[6]
	if data.Extern {
		return SDIRecord{}, fmt.Errorf("SDI blob stored off-page (unsupported)")
	}
	zr, err := zlib.NewReader(bytes.NewReader(b[data.Off : data.Off+data.Len]))
	if err != nil {
		return SDIRecord{}, err
	}
	js, err := io.ReadAll(zr)
	if err != nil {
		return SDIRecord{}, err
	}
	return SDIRecord{
		Type: binary.BigEndian.Uint32(b[f[0].Off:]),
		ID:   binary.BigEndian.Uint64(b[f[1].Off:]),
		JSON: js,
	}, nil
}
