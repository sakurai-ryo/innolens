package innodb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
)

const PageSize = 16384

// FIL header offsets (fil0types.h).
const (
	FIL_PAGE_SPACE_OR_CHKSUM = 0
	FIL_PAGE_OFFSET          = 4
	FIL_PAGE_PREV            = 8
	FIL_PAGE_NEXT            = 12
	FIL_PAGE_LSN             = 16
	FIL_PAGE_TYPE            = 24
	FIL_PAGE_FILE_FLUSH_LSN  = 26
	FIL_PAGE_SPACE_ID        = 34
	FIL_PAGE_DATA            = 38
	FIL_PAGE_DATA_END        = 8
	FIL_NULL                 = 0xFFFFFFFF
)

// Page types (fil0fil.h).
const (
	FIL_PAGE_INDEX          = 17855
	FIL_PAGE_RTREE          = 17854
	FIL_PAGE_SDI            = 17853
	FIL_PAGE_TYPE_ALLOCATED = 0
	FIL_PAGE_UNDO_LOG       = 2
	FIL_PAGE_INODE          = 3
	FIL_PAGE_IBUF_FREE_LIST = 4
	FIL_PAGE_IBUF_BITMAP    = 5
	FIL_PAGE_TYPE_SYS       = 6
	FIL_PAGE_TYPE_TRX_SYS   = 7
	FIL_PAGE_TYPE_FSP_HDR   = 8
	FIL_PAGE_TYPE_XDES      = 9
	FIL_PAGE_TYPE_BLOB      = 10
	FIL_PAGE_SDI_BLOB       = 18
	FIL_PAGE_TYPE_LOB_INDEX = 22
	FIL_PAGE_TYPE_LOB_DATA  = 23
	FIL_PAGE_TYPE_LOB_FIRST = 24
)

var pageTypeNames = map[uint16]string{
	FIL_PAGE_INDEX: "FIL_PAGE_INDEX", FIL_PAGE_RTREE: "FIL_PAGE_RTREE", FIL_PAGE_SDI: "FIL_PAGE_SDI",
	0: "FIL_PAGE_TYPE_ALLOCATED", 1: "FIL_PAGE_TYPE_UNUSED", 2: "FIL_PAGE_UNDO_LOG", 3: "FIL_PAGE_INODE",
	4: "FIL_PAGE_IBUF_FREE_LIST", 5: "FIL_PAGE_IBUF_BITMAP", 6: "FIL_PAGE_TYPE_SYS", 7: "FIL_PAGE_TYPE_TRX_SYS",
	8: "FIL_PAGE_TYPE_FSP_HDR", 9: "FIL_PAGE_TYPE_XDES", 10: "FIL_PAGE_TYPE_BLOB", 11: "FIL_PAGE_TYPE_ZBLOB",
	12: "FIL_PAGE_TYPE_ZBLOB2", 13: "FIL_PAGE_TYPE_UNKNOWN", 14: "FIL_PAGE_COMPRESSED", 15: "FIL_PAGE_ENCRYPTED",
	16: "FIL_PAGE_COMPRESSED_AND_ENCRYPTED", 17: "FIL_PAGE_ENCRYPTED_RTREE", 18: "FIL_PAGE_SDI_BLOB",
	19: "FIL_PAGE_SDI_ZBLOB", 20: "FIL_PAGE_TYPE_LEGACY_DBLWR", 21: "FIL_PAGE_TYPE_RSEG_ARRAY",
	22: "FIL_PAGE_TYPE_LOB_INDEX", 23: "FIL_PAGE_TYPE_LOB_DATA", 24: "FIL_PAGE_TYPE_LOB_FIRST",
	25: "FIL_PAGE_TYPE_ZLOB_FIRST", 26: "FIL_PAGE_TYPE_ZLOB_DATA", 27: "FIL_PAGE_TYPE_ZLOB_INDEX",
	28: "FIL_PAGE_TYPE_ZLOB_FRAG", 29: "FIL_PAGE_TYPE_ZLOB_FRAG_ENTRY",
}

func PageTypeName(t uint16) string {
	if s, ok := pageTypeNames[t]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", t)
}

// Space is a file-per-table tablespace opened read-only.
type Space struct {
	f      *os.File
	Path   string
	NPages uint32
	ID     uint32
	Flags  uint32
}

func Open(path string) (*Space, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	s := &Space{f: f, Path: path, NPages: uint32(st.Size() / PageSize)}
	p0, err := s.Page(0)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.ID = binary.BigEndian.Uint32(p0.Data[FIL_PAGE_DATA+FSP_SPACE_ID:])
	s.Flags = binary.BigEndian.Uint32(p0.Data[FIL_PAGE_DATA+FSP_SPACE_FLAGS:])
	if err := checkFlags(s.Flags); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// FSP_SPACE_FLAGS bit positions (fsp0types.h).
const (
	fspFlagsPosZipSsize   = 1
	fspFlagsPosPageSsize  = 6
	fspFlagsPosEncryption = 13
)

func checkFlags(flags uint32) error {
	if (flags>>fspFlagsPosZipSsize)&0xF != 0 {
		return fmt.Errorf("unsupported: compressed tablespace")
	}
	if (flags>>fspFlagsPosPageSsize)&0xF != 0 {
		return fmt.Errorf("unsupported: page size is not 16KB")
	}
	if (flags>>fspFlagsPosEncryption)&1 != 0 {
		return fmt.Errorf("unsupported: encrypted tablespace")
	}
	return nil
}

func (s *Space) Close() error { return s.f.Close() }

func (s *Space) Page(no uint32) (*Page, error) {
	if no >= s.NPages {
		return nil, fmt.Errorf("page %d out of range (%d pages)", no, s.NPages)
	}
	b := make([]byte, PageSize)
	if _, err := s.f.ReadAt(b, int64(no)*PageSize); err != nil {
		return nil, err
	}
	return newPage(no, b), nil
}

type FILHeader struct {
	Checksum   uint32
	Offset     uint32
	Prev, Next uint32
	LSN        uint64
	Type       uint16
	FlushLSN   uint64
	SpaceID    uint32
}

type Page struct {
	No   uint32
	Data []byte
	FIL  FILHeader
	// ChecksumOK is true when the crc32c matches or the page is all zeros.
	ChecksumOK   bool
	ChecksumCalc uint32
}

func newPage(no uint32, b []byte) *Page {
	p := &Page{No: no, Data: b}
	p.FIL = FILHeader{
		Checksum: binary.BigEndian.Uint32(b[FIL_PAGE_SPACE_OR_CHKSUM:]),
		Offset:   binary.BigEndian.Uint32(b[FIL_PAGE_OFFSET:]),
		Prev:     binary.BigEndian.Uint32(b[FIL_PAGE_PREV:]),
		Next:     binary.BigEndian.Uint32(b[FIL_PAGE_NEXT:]),
		LSN:      binary.BigEndian.Uint64(b[FIL_PAGE_LSN:]),
		Type:     binary.BigEndian.Uint16(b[FIL_PAGE_TYPE:]),
		FlushLSN: binary.BigEndian.Uint64(b[FIL_PAGE_FILE_FLUSH_LSN:]),
		SpaceID:  binary.BigEndian.Uint32(b[FIL_PAGE_SPACE_ID:]),
	}
	p.ChecksumCalc = crc32c(b)
	p.ChecksumOK = p.ChecksumCalc == p.FIL.Checksum || isZero(b)
	return p
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// crc32c is buf_calc_page_crc32: crc of [4,26) xor crc of [38, PageSize-8).
func crc32c(b []byte) uint32 {
	c1 := crc32.Checksum(b[FIL_PAGE_OFFSET:FIL_PAGE_FILE_FLUSH_LSN], castagnoli)
	c2 := crc32.Checksum(b[FIL_PAGE_DATA:PageSize-FIL_PAGE_DATA_END], castagnoli)
	return c1 ^ c2
}

func isZero(b []byte) bool { return bytes.IndexFunc(b, func(r rune) bool { return r != 0 }) < 0 }

func (p *Page) TypeName() string { return PageTypeName(p.FIL.Type) }

func (p *Page) annotateFILHeader(root *Node) {
	h := root.Group("FIL header")
	r := reader{p.Data, h}
	r.u32("FIL_PAGE_SPACE_OR_CHKSUM", FIL_PAGE_SPACE_OR_CHKSUM)
	switch {
	case isZero(p.Data):
		r.note("0 (empty page)")
	case p.ChecksumOK:
		r.note(fmt.Sprintf("%#08x (crc32c ok)", p.FIL.Checksum))
	default:
		r.note(fmt.Sprintf("%#08x (MISMATCH: computed %#08x)", p.FIL.Checksum, p.ChecksumCalc))
	}
	r.u32("FIL_PAGE_OFFSET", FIL_PAGE_OFFSET)
	r.u32("FIL_PAGE_PREV", FIL_PAGE_PREV)
	if p.FIL.Prev == FIL_NULL {
		r.note("FIL_NULL")
	}
	r.u32("FIL_PAGE_NEXT", FIL_PAGE_NEXT)
	if p.FIL.Next == FIL_NULL {
		r.note("FIL_NULL")
	}
	r.u64("FIL_PAGE_LSN", FIL_PAGE_LSN)
	r.u16("FIL_PAGE_TYPE", FIL_PAGE_TYPE)
	r.note(fmt.Sprintf("%d (%s)", p.FIL.Type, p.TypeName()))
	r.u64("FIL_PAGE_FILE_FLUSH_LSN", FIL_PAGE_FILE_FLUSH_LSN)
	r.u32("FIL_PAGE_SPACE_ID", FIL_PAGE_SPACE_ID)
}

func (p *Page) annotateFILTrailer(root *Node) {
	r := reader{p.Data, root.Group("FIL trailer")}
	r.u32("FIL_PAGE_END_CHKSUM", PageSize-8)
	r.u32("FIL_PAGE_END_LSN_LOW32", PageSize-4)
}

// FSP header offsets relative to FIL_PAGE_DATA (fsp0fsp.h).
const (
	FSP_SPACE_ID        = 0
	FSP_NOT_USED        = 4
	FSP_SIZE            = 8
	FSP_FREE_LIMIT      = 12
	FSP_SPACE_FLAGS     = 16
	FSP_FRAG_N_USED     = 20
	FSP_FREE            = 24
	FSP_FREE_FRAG       = 40
	FSP_FULL_FRAG       = 56
	FSP_SEG_ID          = 72
	FSP_SEG_INODES_FULL = 80
	FSP_SEG_INODES_FREE = 96
	FSP_HEADER_SIZE     = 112
	// SDI header lives after the 256 XDES entries (40 bytes each) and the
	// reserved encryption info (115 bytes): fsp_header_get_sdi_offset.
	fspSDIOffset = FIL_PAGE_DATA + FSP_HEADER_SIZE + 40*256 + 115
)

func (p *Page) annotateFSP(root *Node) {
	g := root.Group("FSP header")
	r := reader{p.Data, g}
	base := FIL_PAGE_DATA
	r.u32("FSP_SPACE_ID", base+FSP_SPACE_ID)
	r.u32("FSP_NOT_USED", base+FSP_NOT_USED)
	r.u32("FSP_SIZE", base+FSP_SIZE)
	r.u32("FSP_FREE_LIMIT", base+FSP_FREE_LIMIT)
	flags := r.u32("FSP_SPACE_FLAGS", base+FSP_SPACE_FLAGS)
	r.note(fmt.Sprintf("%#x (post_antelope=%d zip_ssize=%d atomic_blobs=%d page_ssize=%d data_dir=%d shared=%d temporary=%d encryption=%d sdi=%d)",
		flags, flags&1, (flags>>1)&0xF, (flags>>5)&1, (flags>>6)&0xF, (flags>>10)&1, (flags>>11)&1, (flags>>12)&1, (flags>>13)&1, (flags>>14)&1))
	r.u32("FSP_FRAG_N_USED", base+FSP_FRAG_N_USED)
	r.flstBaseNode("FSP_FREE", base+FSP_FREE)
	r.flstBaseNode("FSP_FREE_FRAG", base+FSP_FREE_FRAG)
	r.flstBaseNode("FSP_FULL_FRAG", base+FSP_FULL_FRAG)
	r.u64("FSP_SEG_ID", base+FSP_SEG_ID)
	r.flstBaseNode("FSP_SEG_INODES_FULL", base+FSP_SEG_INODES_FULL)
	r.flstBaseNode("FSP_SEG_INODES_FREE", base+FSP_SEG_INODES_FREE)
	s := root.Group("SDI header")
	r = reader{p.Data, s}
	r.u32("FSP_SDI_VERSION", fspSDIOffset)
	r.u32("FSP_SDI_ROOT_PAGE_NO", fspSDIOffset+4)
}

func (p *Page) sdiRootPage() uint32 {
	return binary.BigEndian.Uint32(p.Data[fspSDIOffset+4:])
}
