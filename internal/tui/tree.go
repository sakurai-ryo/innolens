package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	tw := w - rw
	if l.cur < l.top {
		l.top = l.cur
	}
	// Rows wrap, so the screen holds a varying number of them: scroll down
	// until the cursor row is on it whole, or is the first one.
	sum := 0
	for i := min(l.cur, len(l.rows)-1); i >= l.top; i-- {
		if sum += l.height(i, tw); sum > h {
			l.top = min(i+1, l.cur)
			break
		}
	}
	var out []string
	for i := l.top; i < len(l.rows) && len(out) < h; i++ {
		out = append(out, l.render(i, tw, showOff, showSize, focused)...)
	}
	if len(out) > h {
		out = out[:h]
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// lines is how many screen lines the whole list takes at width w.
func (l *list) lines(w int) int {
	rw, _, _ := l.rightWidth(w)
	n := 0
	for i := range l.rows {
		n += l.height(i, w-rw)
	}
	return n
}

func (l *list) height(i, w int) int {
	_, body := l.rowLines(l.rows[i], w)
	return len(body)
}

// rowLines lays a row out at width w: the prefix of its first line (indent,
// fold mark, icon) and its text wrapped to fit beside it. Continuation lines
// are indented as far as the prefix. A pane too narrow to wrap in gets the
// text cut short on one line instead.
func (l *list) rowLines(r row, w int) (prefix string, body []string) {
	mark := " "
	if r.n.hasKids() {
		mark = ic.collapsed
		if r.n.expanded {
			mark = ic.expanded
		}
	}
	prefix = strings.Repeat("  ", r.depth) + mark + " "
	if r.n.icon != "" {
		prefix += r.n.icon + " "
	}
	text := r.n.text()
	bw := w - lipgloss.Width(prefix)
	if bw < 16 {
		return prefix, []string{truncate(text, bw)}
	}
	body = strings.Split(ansi.Wrap(text, bw, ""), "\n")
	// ansi.Wrap lets a line run one cell over when a word ends at the limit
	// and a hyphen or a lone dash follows; a line past the width would be
	// wrapped again by the pane and push everything below it down.
	for i, b := range body {
		body[i] = truncate(strings.TrimRight(b, " "), bw)
	}
	return prefix, body
}

// text is what a row says: the label, its value, then the note.
func (n *node) text() string {
	text := n.label
	if n.value != "" {
		text += ": " + oneLine(n.value)
	}
	if n.note != "" {
		text += "  " + n.note
	}
	return text
}

// render is the screen lines of one row: the offset/size column goes on the
// first, and the rest are padded to keep the column in place.
func (l *list) render(i, w int, showOff, showSize, focused bool) []string {
	r := l.rows[i]
	prefix, body := l.rowLines(r, w)
	// A prefix wider than the pane leaves no room for text: the body is
	// already empty, and the prefix itself is cut.
	prefix = truncate(prefix, w)
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	right := rightCol(r.n, showOff, showSize)
	blank := strings.Repeat(" ", lipgloss.Width(right))
	text := r.n.text()
	tagAt, noteAt := r.n.spans(text)
	var out []string
	pos := 0
	for j, b := range body {
		pre, rc := indent, blank
		if j == 0 {
			pre, rc = prefix, right
		}
		// Wrapping keeps each line a substring of the text, minus the space it
		// broke at, so the line's place in the text is found by searching on.
		// A line cut short is not one: it gets its own spans, so that a cut
		// never lands inside the ellipsis.
		start, tagAt, noteAt := pos, tagAt, noteAt
		if k := strings.Index(text[pos:], b); k >= 0 {
			start = pos + k
		} else {
			start = 0
			tagAt, noteAt = r.n.spans(b)
		}
		pos = start + len(b)
		pad := strings.Repeat(" ", max(0, w-lipgloss.Width(pre+b)))
		switch {
		case i == l.cur && focused:
			out = append(out, selStyle.Render(pre+b+pad+rc))
		case r.n.dim:
			out = append(out, dimStyle.Render(pre+b+pad+rc))
		case i == l.cur:
			out = append(out, restStyle.Render(pre+b+pad+rc))
		default:
			out = append(out, pre+colorize(b, start, r.n, tagAt, noteAt)+pad+dimStyle.Render(rc))
		}
	}
	return out
}

// spans is where the tag and the note of n sit in text: byte offsets, -1 when
// absent. The note is the tail, so the tag is looked for ahead of it.
func (n *node) spans(text string) (tagAt, noteAt int) {
	tagAt, noteAt = -1, -1
	if n.note != "" {
		noteAt = strings.LastIndex(text, n.note)
	}
	if n.tag != "" && n.color != "" {
		head := text
		if noteAt >= 0 {
			head = text[:noteAt]
		}
		tagAt = strings.Index(head, n.tag)
	}
	return tagAt, noteAt
}

// colorize paints the type token and dims the note in one line of a row,
// which starts at byte start of the row's text. Each line is styled on its
// own: a style left open at a line break would run into the pane beside it.
// The selected row skips this, as reverse video does not compose with colours
// nested inside the text.
func colorize(line string, start int, n *node, tagAt, noteAt int) string {
	end := start + len(line)
	cuts := []int{0, len(line)}
	for _, p := range []int{tagAt, tagAt + len(n.tag), noteAt} {
		if p > start && p < end {
			cuts = append(cuts, p-start)
		}
	}
	sort.Ints(cuts)
	var b strings.Builder
	for k := 0; k+1 < len(cuts); k++ {
		seg, at := line[cuts[k]:cuts[k+1]], start+cuts[k]
		switch {
		case noteAt >= 0 && at >= noteAt:
			b.WriteString(dimStyle.Render(seg))
		case tagAt >= 0 && at >= tagAt && at < tagAt+len(n.tag):
			b.WriteString(lipgloss.NewStyle().Foreground(n.color).Render(seg))
		default:
			b.WriteString(seg)
		}
	}
	return b.String()
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return strings.Repeat("…", max(w, 0))
	}
	return string([]rune(s)[:w-1]) + "…"
}
