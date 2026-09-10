package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// node is one entry of either pane's tree. Children may be produced lazily by
// load, which runs once the first time the node is expanded.
//
// label stays plain text: icon, tag and note are applied at render time so that
// callers can keep matching on the label. tag must be a substring of label; it
// is the token that gets coloured with color.
type node struct {
	label    string
	value    string
	icon     string
	tag      string
	hkey     string // key for the ? panel when the row has no annotation or tag
	note     string
	color    lipgloss.Color
	off      int  // byte offset for the right column
	hasOff   bool // off is meaningful; the node covers bytes
	size     int  // bytes the node accounts for; 0 = nothing to show
	dim      bool
	expanded bool
	loaded   bool
	load     func(*node)
	children []*node
	data     any
}

func (n *node) hasKids() bool { return len(n.children) > 0 || (n.load != nil && !n.loaded) }

// stripOffs clears the offsets of a subtree, including the children a lazy load
// will produce. Offsets of a redo record count from the start of that record,
// so once the subtree is spliced into a page they no longer name page bytes.
func stripOffs(n *node) *node {
	n.hasOff = false
	if load := n.load; load != nil {
		n.load = func(n *node) {
			load(n)
			for _, c := range n.children {
				stripOffs(c)
			}
		}
	}
	for _, c := range n.children {
		stripOffs(c)
	}
	return n
}

func (n *node) expand() {
	if n.load != nil && !n.loaded {
		n.loaded = true
		n.load(n)
	}
	n.expanded = n.hasKids()
}

type row struct {
	n     *node
	depth int
}

func flatten(n *node, depth int, out *[]row) {
	for _, c := range n.children {
		*out = append(*out, row{c, depth})
		if c.expanded {
			flatten(c, depth+1, out)
		}
	}
}

// list is a scrollable cursor over a tree.
type list struct {
	root     *node
	cur, top int
	rows     []row
	sizes    bool // draw the offset/size column on the right
}

func newList(root *node) list {
	l := list{root: root}
	l.refresh()
	return l
}

func (l *list) refresh() {
	l.rows = l.rows[:0]
	flatten(l.root, 0, &l.rows)
	if l.cur >= len(l.rows) {
		l.cur = len(l.rows) - 1
	}
	if l.cur < 0 {
		l.cur = 0
	}
}

func (l *list) sel() *node {
	if l.cur < len(l.rows) {
		return l.rows[l.cur].n
	}
	return nil
}

func (l *list) move(d int) {
	l.cur += d
	if l.cur < 0 {
		l.cur = 0
	}
	if l.cur >= len(l.rows) {
		l.cur = len(l.rows) - 1
	}
}

func (l *list) expand() {
	n := l.sel()
	if n == nil {
		return
	}
	n.expand()
	l.refresh()
}

// collapse folds the selected node, or jumps to its parent when already folded.
func (l *list) collapse() {
	n := l.sel()
	if n == nil {
		return
	}
	if n.expanded {
		n.expanded = false
		l.refresh()
		return
	}
	depth := l.rows[l.cur].depth
	for i := l.cur - 1; i >= 0; i-- {
		if l.rows[i].depth < depth {
			l.cur = i
			return
		}
	}
}

// rightWidth is the space the offset/size column needs. When the pane is narrow
// the offset goes first, then the size.
func (l *list) rightWidth(w int) (rw int, off, size bool) {
	switch {
	case !l.sizes:
		return 0, false, false
	case w >= 46:
		return 16, true, true
	case w >= 34:
		return 10, false, true
	}
	return 0, false, false
}

func rightCol(n *node, off, size bool) string {
	var b strings.Builder
	if off {
		if n.hasOff {
			fmt.Fprintf(&b, "@%04x", n.off)
		} else {
			b.WriteString("     ")
		}
		b.WriteByte(' ')
	}
	if size {
		if n.size > 0 {
			fmt.Fprintf(&b, "%6d B", n.size)
		} else {
			b.WriteString("        ")
		}
	}
	return b.String()
}

func (l *list) view(w, h int, focused bool) string {
	if h < 1 {
		return ""
	}
	rw, showOff, showSize := l.rightWidth(w)
	if l.cur < l.top {
		l.top = l.cur
	}
	if l.cur >= l.top+h {
		l.top = l.cur - h + 1
	}
	var b strings.Builder
	for i := l.top; i < l.top+h; i++ {
		if i > l.top {
			b.WriteByte('\n')
		}
		if i >= len(l.rows) {
			continue
		}
		r := l.rows[i]
		mark := " "
		if r.n.hasKids() {
			mark = ic.collapsed
			if r.n.expanded {
				mark = ic.expanded
			}
		}
		text := strings.Repeat("  ", r.depth) + mark + " "
		if r.n.icon != "" {
			text += r.n.icon + " "
		}
		text += r.n.label
		if r.n.value != "" {
			text += ": " + oneLine(r.n.value)
		}
		if r.n.note != "" {
			text += "  " + r.n.note
		}
		text = truncate(text, w-rw)
		pad, right := "", ""
		if rw > 0 {
			pad = strings.Repeat(" ", max(0, w-rw-lipgloss.Width(text)))
			right = rightCol(r.n, showOff, showSize)
		}
		switch {
		case i == l.cur && focused:
			b.WriteString(selStyle.Render(text + pad + right))
		case r.n.dim:
			b.WriteString(dimStyle.Render(text + pad + right))
		case i == l.cur:
			b.WriteString(restStyle.Render(text + pad + right))
		default:
			b.WriteString(colorize(text, r.n) + pad + dimStyle.Render(right))
		}
	}
	return b.String()
}

// colorize paints the type token and dims the trailing note. The selected row
// skips it: reverse video does not compose with colours nested inside the text.
func colorize(text string, n *node) string {
	if n.note != "" {
		if i := strings.LastIndex(text, n.note); i >= 0 {
			text = text[:i] + dimStyle.Render(n.note)
		}
	}
	if n.tag != "" && n.color != "" {
		if i := strings.Index(text, n.tag); i >= 0 {
			text = text[:i] + lipgloss.NewStyle().Foreground(n.color).Render(n.tag) + text[i+len(n.tag):]
		}
	}
	return text
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}

func truncate(s string, w int) string {
	if w <= 1 || lipgloss.Width(s) <= w {
		return s
	}
	return string([]rune(s)[:w-1]) + "…"
}
