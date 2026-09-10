package innodb

import (
	"encoding/binary"
	"fmt"
	"os"
)

// SpaceID reads the tablespace id from page 0 without keeping the file open.
// It is used to map the space_id of a redo record back to an .ibd file.
func SpaceID(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b := make([]byte, FIL_PAGE_DATA+FSP_SPACE_ID+4)
	if _, err := f.ReadAt(b, 0); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[FIL_PAGE_DATA+FSP_SPACE_ID:]), nil
}

// RecordImage rebuilds the record image carried by MLOG_REC_INSERT and decodes
// its columns with idx.
//
// The log stores only the tail of the record: the first MismatchIndex bytes are
// shared with the record it is inserted after, so page must be the target page
// to recover them. When the log omits the header fields entirely, they are
// taken from that same record.
func (r *RedoRec) RecordImage(buf []byte, page *Page, idx *IndexDef) (*Rec, error) {
	ins := r.Ins
	if ins == nil {
		return nil, fmt.Errorf("%s carries no record image", r.TypeName())
	}
	if idx == nil {
		return nil, fmt.Errorf("no index definition to decode the record with")
	}
	if ins.DataOff+ins.DataLen > len(buf) {
		return nil, fmt.Errorf("record image runs past the end of the log stream")
	}
	data := buf[ins.DataOff : ins.DataOff+ins.DataLen]
	infoBits, origin, mismatch := ins.InfoBits, ins.OriginOffset, ins.MismatchIndex

	var prefix []byte
	if !ins.HasHdr || mismatch > 0 {
		if page == nil {
			return nil, fmt.Errorf("page %d of space %d is needed to rebuild the record", r.PageNo, r.SpaceID)
		}
		cur, err := parseRec(page.Data, ins.CursorOff, idx)
		if err != nil {
			return nil, fmt.Errorf("record it is inserted after: %w", err)
		}
		if !ins.HasHdr {
			infoBits = cur.InfoBits | cur.Status
			origin = cur.Extra
			mismatch = cur.Extra + (cur.End - cur.Off) - ins.DataLen
		}
		start := ins.CursorOff - cur.Extra
		if mismatch < 0 || start < 0 || start+mismatch > len(page.Data) {
			return nil, fmt.Errorf("mismatch index %d does not fit the record at %d", mismatch, ins.CursorOff)
		}
		prefix = page.Data[start : start+mismatch]
	}
	if origin < REC_N_NEW_EXTRA_BYTES || mismatch+ins.DataLen > PageSize-FIL_PAGE_DATA_END {
		return nil, fmt.Errorf("rebuilt record does not fit a page (origin %d, len %d)", origin, mismatch+ins.DataLen)
	}
	// parseRec indexes with page offsets, so rebuild inside a page-sized buffer.
	img := make([]byte, PageSize)
	copy(img, prefix)
	copy(img[len(prefix):], data)
	img[origin-5] = img[origin-5]&^0xF0 | infoBits&0xF0
	img[origin-3] = img[origin-3]&^0x07 | infoBits&0x07
	return parseRec(img, origin, idx)
}
