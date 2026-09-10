package innodb

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Apply replays one redo record onto a copy of the page it addresses, the way
// recovery does: a record the page has already seen is skipped, and what is
// written moves the page LSN forward. The page passed in is never modified.
//
// Replayed are the types that write bytes at an offset and the ones that change
// a record where it lies. The types that add or remove a record, or build a
// page from nothing, are reported as not replayed and leave the page alone;
// rebuilding the record heap is a different job from writing bytes.
func (r *RedoRec) Apply(p *Page, idx *IndexDef) (*Page, string, error) {
	if !r.HasPage() {
		return p, "not replayed: the record does not address a page", nil
	}
	if r.SpaceID != p.FIL.SpaceID || r.PageNo != p.No {
		return p, "", fmt.Errorf("record writes space %d page %d, not space %d page %d",
			r.SpaceID, r.PageNo, p.FIL.SpaceID, p.No)
	}
	if r.EndLSN != 0 && p.FIL.LSN >= r.EndLSN {
		return p, fmt.Sprintf("skipped: page lsn %d is not older than the record end %d", p.FIL.LSN, r.EndLSN), nil
	}
	b := append([]byte(nil), p.Data...)
	note, applied, err := r.applyTo(b, idx)
	if err != nil {
		return p, "", err
	}
	if !applied {
		return p, note, nil
	}
	if r.EndLSN != 0 {
		binary.BigEndian.PutUint64(b[FIL_PAGE_LSN:], r.EndLSN)
		binary.BigEndian.PutUint32(b[PageSize-4:], uint32(r.EndLSN))
	}
	// A server recomputes the checksum when it flushes the page, long after the
	// record was applied. Doing it here keeps a replayed page from reading as
	// corrupt in the annotation next to it.
	sum := crc32c(b)
	binary.BigEndian.PutUint32(b[FIL_PAGE_SPACE_OR_CHKSUM:], sum)
	binary.BigEndian.PutUint32(b[PageSize-8:], sum)
	return newPage(p.No, b), note, nil
}

func (r *RedoRec) applyTo(b []byte, idx *IndexDef) (note string, applied bool, err error) {
	switch {
	case r.Write != nil:
		w := r.Write
		if w.Off < 0 || w.Off+len(w.Val) > PageSize {
			return "", false, fmt.Errorf("write of %d bytes at @%04x runs past the page", len(w.Val), w.Off)
		}
		copy(b[w.Off:], w.Val)
		return fmt.Sprintf("wrote %d bytes at @%04x", len(w.Val), w.Off), true, nil
	case r.Upd != nil:
		return r.applyUpdate(b, idx)
	}
	return "not replayed: " + r.TypeName() + " changes which records the page holds", false, nil
}

// applyUpdate is btr_cur_upd_rec_in_place: the delete mark and the info bits
// sit in the record header, the system columns and the updated columns are
// written over the field they replace.
func (r *RedoRec) applyUpdate(b []byte, idx *IndexDef) (string, bool, error) {
	u := r.Upd
	if u.RecOff < REC_N_NEW_EXTRA_BYTES || u.RecOff >= PageSize-FIL_PAGE_DATA_END {
		return "", false, fmt.Errorf("record offset %d is out of the page", u.RecOff)
	}
	var rec *Rec
	if u.SysPos >= 0 || len(u.Fields) > 0 {
		if idx == nil {
			return "", false, fmt.Errorf("no index definition to find the fields of the record with")
		}
		var err error
		if rec, err = parseRec(b, u.RecOff, idx); err != nil {
			return "", false, fmt.Errorf("record being updated: %w", err)
		}
	}
	var did []string
	if u.HasInfo {
		b[u.RecOff-5] = b[u.RecOff-5]&^0xF0 | u.InfoBits&0xF0
		did = append(did, fmt.Sprintf("info bits %#02x", u.InfoBits&0xF0))
	}
	if u.DelMark >= 0 {
		b[u.RecOff-5] &^= REC_INFO_DELETED_FLAG
		if u.DelMark != 0 {
			b[u.RecOff-5] |= REC_INFO_DELETED_FLAG
		}
		did = append(did, fmt.Sprintf("delete mark %d", u.DelMark))
	}
	if u.SysPos >= 0 {
		if err := writeField(b, rec, u.SysPos, beBytes(u.TrxID, 6)); err != nil {
			return "", false, fmt.Errorf("DB_TRX_ID: %w", err)
		}
		if err := writeField(b, rec, u.SysPos+1, u.Roll); err != nil {
			return "", false, fmt.Errorf("DB_ROLL_PTR: %w", err)
		}
		did = append(did, fmt.Sprintf("DB_TRX_ID %d", u.TrxID))
	}
	for _, f := range u.Fields {
		if f.Null {
			return "", false, fmt.Errorf("field %d is set to NULL, which changes the record length", f.No)
		}
		if err := writeField(b, rec, f.No, f.Val); err != nil {
			return "", false, fmt.Errorf("field %d: %w", f.No, err)
		}
		did = append(did, fmt.Sprintf("%s %d bytes", colName(idx, f.No), len(f.Val)))
	}
	if len(did) == 0 {
		did = append(did, "nothing to write")
	}
	return fmt.Sprintf("record @%04x: %s", u.RecOff, strings.Join(did, ", ")), true, nil
}

// writeField overwrites one field of a record in place. An update logged as
// in-place never changes a length, so a value of a different size means the
// record on the page is not the one the log was written against.
//
// Fields are named by their position in the index. A record written before an
// instant DROP COLUMN stores fewer of them than the index defines, and those
// records are left alone rather than written at a guessed offset.
func writeField(b []byte, rec *Rec, no int, val []byte) error {
	if rec == nil || no < 0 || no >= len(rec.Fields) {
		return fmt.Errorf("the record has %d fields", len(rec.Fields))
	}
	f := rec.Fields[no]
	if f.Len != len(val) || f.Off == 0 {
		return fmt.Errorf("stores %d bytes, the log writes %d", f.Len, len(val))
	}
	copy(b[f.Off:], val)
	return nil
}
