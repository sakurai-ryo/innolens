package innodb

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Node is one entry of the annotation tree shown next to the hex dump.
// Off/Len is the byte range inside the page; Len == 0 means a grouping node.
type Node struct {
	Name     string
	Off, Len int
	Value    string
	// Bytes is what the node accounts for when it is not one contiguous range,
	// such as the records scattered over a page.
	Bytes int
	// Ref is what Enter on this row opens, when the field points somewhere.
	Ref      any
	Children []*Node
}

// Size is the number of bytes a node accounts for: its own range, the total it
// was given, or the sum of its children when it is only a grouping node.
func (n *Node) Size() int {
	switch {
	case n.Len > 0:
		return n.Len
	case n.Bytes != 0:
		return n.Bytes
	}
	total := 0
	for _, c := range n.Children {
		total += c.Size()
	}
	return total
}

// Start is the lowest offset the node covers, or -1 when it covers no bytes.
func (n *Node) Start() int {
	if n.Len > 0 {
		return n.Off
	}
	best := -1
	for _, c := range n.Children {
		if s := c.Start(); s >= 0 && (best < 0 || s < best) {
			best = s
		}
	}
	return best
}

func (n *Node) Add(name string, off, ln int, value string) *Node {
	c := &Node{Name: name, Off: off, Len: ln, Value: value}
	n.Children = append(n.Children, c)
	return c
}

func (n *Node) Group(name string) *Node { return n.Add(name, 0, 0, "") }

// Step is one stage of turning stored bytes into a value. Decoders record steps
// only when the caller passes a non-nil slice, so the plain decode path does no
// extra formatting.
type Step struct{ Name, Value string }

// reader decodes big-endian fields from a page while recording them as Nodes.
type reader struct {
	b []byte
	n *Node
}

func (r reader) u8(name string, off int) uint8 {
	v := r.b[off]
	r.n.Add(name, off, 1, fmt.Sprint(v))
	return v
}

func (r reader) u16(name string, off int) uint16 {
	v := binary.BigEndian.Uint16(r.b[off:])
	r.n.Add(name, off, 2, fmt.Sprint(v))
	return v
}

func (r reader) u32(name string, off int) uint32 {
	v := binary.BigEndian.Uint32(r.b[off:])
	r.n.Add(name, off, 4, fmt.Sprint(v))
	return v
}

func (r reader) u64(name string, off int) uint64 {
	v := binary.BigEndian.Uint64(r.b[off:])
	r.n.Add(name, off, 8, fmt.Sprint(v))
	return v
}

func (r reader) hex(name string, off, ln int) []byte {
	v := r.b[off : off+ln]
	r.n.Add(name, off, ln, hex.EncodeToString(v))
	return v
}

// note replaces the value of the most recently added field.
func (r reader) note(v string) { r.n.Children[len(r.n.Children)-1].Value = v }

// fsegHeader decodes a 10-byte FSEG_HEADER (space, page_no, offset of the inode).
func (r reader) fsegHeader(name string, off int) {
	g := r.n.Add(name, off, 10, "")
	sub := reader{r.b, g}
	sub.u32("FSEG_HDR_SPACE", off)
	sub.u32("FSEG_HDR_PAGE_NO", off+4)
	sub.u16("FSEG_HDR_OFFSET", off+8)
}

// flstBaseNode decodes a 16-byte file list base node (len, first addr, last addr).
func (r reader) flstBaseNode(name string, off int) {
	g := r.n.Add(name, off, 16, "")
	sub := reader{r.b, g}
	sub.u32("FLST_LEN", off)
	sub.filAddr("FLST_FIRST", off+4)
	sub.filAddr("FLST_LAST", off+10)
}

// flstNode decodes a 12-byte file list node (prev addr, next addr).
func (r reader) flstNode(name string, off int) {
	g := r.n.Add(name, off, FLST_NODE_SIZE, "")
	sub := reader{r.b, g}
	sub.filAddr("FLST_PREV", off)
	sub.filAddr("FLST_NEXT", off+6)
}

func (r reader) filAddr(name string, off int) {
	page := binary.BigEndian.Uint32(r.b[off:])
	boff := binary.BigEndian.Uint16(r.b[off+4:])
	v := fmt.Sprintf("page %d offset %d", page, boff)
	if page == 0xFFFFFFFF {
		v = "FIL_NULL"
	}
	r.n.Add(name, off, 6, v)
}
