package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpWidth is the widest a line may be: the panel indents the bullets by four
// columns, so an 80 column terminal shows them whole.
const helpWidth = 76

func TestHelpEntries(t *testing.T) {
	for name, body := range help {
		lines := strings.Split(body, "\n")
		if len(lines) > helpLines {
			t.Errorf("%s: %d lines, want at most %d", name, len(lines), helpLines)
		}
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				t.Errorf("%s: empty line", name)
			}
			if lipgloss.Width(l) > helpWidth {
				t.Errorf("%s: line is %d columns: %q", name, lipgloss.Width(l), l)
			}
		}
	}
}

func TestEnumKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"17855 (FIL_PAGE_INDEX)", "FIL_PAGE_INDEX"},
		{"heap_no 2, status 0 (REC_STATUS_ORDINARY)", "REC_STATUS_ORDINARY"},
		{"FIL_NULL", ""},
		{"4", ""},
	} {
		if got := enumKey(c.in); got != c.want {
			t.Errorf("enumKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHelpPanel opens a page and checks what the panel shows for a field that
// has an entry, for one that does not, and that toggling it keeps the screen
// the same height.
func TestHelpPanel(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tables.cur = tableIdx(&m.tables, "types")
	press(m, tea.KeyEnter)
	m.pages.cur = 1
	press(m, tea.KeyEnter)
	if m.focus != focusDetail {
		t.Fatalf("detail view not open: %s", m.status)
	}

	// The enum path: FIL_PAGE_TYPE decodes to a constant the panel can key on.
	typ := findRow(&m.ann, "FIL_PAGE_TYPE")
	if got := enumKey(typ.value); got != "FIL_PAGE_INDEX" {
		t.Errorf("FIL_PAGE_TYPE value %q gives enum key %q", typ.value, got)
	}

	m.ann.cur = indexOf(&m.ann, "FIL_PAGE_LSN")
	name, body := m.ann.helpFor()
	if name != "FIL_PAGE_LSN" || body == "" {
		t.Fatalf("help for FIL_PAGE_LSN = %q, %q", name, body)
	}

	// A record has no entry of its own, so the panel falls back to the section.
	m.ann.cur = indexOf(&m.ann, "record @")
	if m.ann.cur < 0 {
		t.Fatal("no record row in the annotation tree")
	}
	if name, _ := m.ann.helpFor(); name != "Records" {
		t.Fatalf("record row fell back to %q, want Records", name)
	}

	off := strings.Count(m.View(), "\n")
	key(t, m, "?")
	if !m.help {
		t.Fatal("? did not open the help panel")
	}
	if got := len(m.helpPanel()); got != helpLines {
		t.Fatalf("panel is %d lines, want %d", got, helpLines)
	}
	if on := strings.Count(m.View(), "\n"); on != off {
		t.Fatalf("screen is %d lines with the panel, %d without", on+1, off+1)
	}
	if !strings.Contains(m.View(), "Records") {
		t.Error("the panel is not on screen")
	}
	key(t, m, "?")
	if m.help {
		t.Fatal("? did not close the help panel")
	}
}

// TestHelpCoverage walks the rows the browser can show and checks that each one
// reaches an entry, its own or an ancestor's. Pages and redo records are opened
// one per kind: what is being checked is the vocabulary, not every row.
func TestHelpCoverage(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			expandAll(&m.tables)
			checkRows(t, &m.tables)

			for _, tbl := range []string{"types", "instant"} {
				m.focus = focusTables
				m.tables.cur = tableIdx(&m.tables, tbl)
				press(m, tea.KeyEnter)
				expandAll(&m.pages)
				checkRows(t, &m.pages)
				seen := map[string]bool{}
				for i := range m.pages.rows {
					n := m.pages.rows[i].n
					if _, ok := n.data.(pageRef); !ok || seen[n.tag] {
						continue
					}
					seen[n.tag] = true
					m.pages.cur = i
					press(m, tea.KeyEnter)
					expandAll(&m.ann)
					checkRows(t, &m.ann)
					m.focus = focusPages
				}
			}

			m.focus = focusTables
			m.tables.cur = tableIdx(&m.tables, "#innodb_redo")
			press(m, tea.KeyEnter)
			seen := map[string]bool{}
			for i := 0; i < len(m.pages.rows); i++ {
				r := m.pages.rows[i]
				if r.depth > 1 || (r.depth == 1 && seen[r.n.tag]) {
					continue
				}
				seen[r.n.tag] = true
				m.pages.cur = i
				m.pages.expand()
			}
			checkRows(t, &m.pages)
		})
	}
}

// expandAll opens every node, including the ones that load their children the
// first time they are expanded.
func expandAll(l *list) {
	for i := 0; i < len(l.rows); i++ {
		l.cur = i
		l.expand()
	}
	l.cur = 0
}

func checkRows(t *testing.T, l *list) {
	t.Helper()
	seen := map[string]bool{}
	for i := range l.rows {
		l.cur = i
		name, _ := l.helpFor()
		if name == "" && !seen[l.rows[i].n.label] {
			seen[l.rows[i].n.label] = true
			t.Errorf("no description for %q (depth %d)", l.rows[i].n.label, l.rows[i].depth)
		}
	}
	l.cur = 0
}
