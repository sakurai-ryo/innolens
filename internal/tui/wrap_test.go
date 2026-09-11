package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// TestRowsFitWidth lays rows out at every width from too narrow to wrap in
// up to wide enough not to, and checks each line is exactly as wide as the
// pane, is whole UTF-8, and that height agrees with what render draws.
func TestRowsFitWidth(t *testing.T) {
	root := &node{children: []*node{
		{label: "record image", value: "0x00 0x01 0x02", tag: "record image", color: "1", icon: ic.page,
			note: "a note long enough to wrap around more than once on a narrow pane"},
		{label: "hello abcd- z", tag: "abcd-", color: "2", note: "abcdefghij -1 and on"},
		{label: "x", value: strings.Repeat("ab", 40), note: "n"},
		{label: "deep", icon: ic.page, children: []*node{{label: "deeper", children: []*node{{label: "deepest", icon: ic.lock,
			note: "the prefix of this row can eat a narrow pane whole"}}}}},
		{label: "dim", dim: true, note: strings.Repeat("dim ", 20)},
	}}
	l := newList(root)
	l.rows[3].n.expand()
	l.refresh()
	l.rows[4].n.expand()
	l.refresh()
	for w := 1; w <= 60; w++ {
		for i := range l.rows {
			for _, focused := range []bool{true, false} {
				l.cur = i
				lines := l.render(i, w, false, false, focused)
				if h := l.height(i, w); h != len(lines) {
					t.Errorf("w=%d row %d: height %d, drawn %d", w, i, h, len(lines))
				}
				for _, line := range lines {
					if !utf8.ValidString(line) {
						t.Errorf("w=%d row %d: invalid UTF-8 in %q", w, i, line)
					}
					if got := lipgloss.Width(line); got != w {
						t.Errorf("w=%d row %d: line is %d wide: %q", w, i, got, line)
					}
				}
			}
		}
		// The pane draws exactly h lines whatever the rows need.
		if got := strings.Count(l.view(w, 7, true), "\n"); got != 6 {
			t.Errorf("w=%d: view has %d lines, want 7", w, got+1)
		}
	}
}
