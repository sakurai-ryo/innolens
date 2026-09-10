package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFindKey searches the clustered index for a key and follows the descent it
// draws: one row per level, the last one opening the page with the record on it.
func TestFindKey(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)

			key(t, m, "f", "1", "2", "3", "4")
			if m.prompt.kind != promptFind || m.prompt.text != "1234" {
				t.Fatalf("find prompt = %+v", m.prompt)
			}
			press(m, tea.KeyEnter)
			if m.prompt.kind != promptNone {
				t.Fatal("enter did not close the find prompt")
			}
			if m.status.err != "" {
				t.Fatalf("find: %s", m.status.err)
			}
			if !strings.Contains(m.pagesTitle, "FIND 1234 IN PRIMARY") {
				t.Fatalf("pane title = %q", m.pagesTitle)
			}
			rows := rowLabels(&m.pages)
			if len(rows) < 2 {
				t.Fatalf("descent = %v, want a root and a leaf", rows)
			}
			if !strings.HasPrefix(rows[0], "level 1") || !strings.HasPrefix(rows[len(rows)-1], "level 0") {
				t.Fatalf("descent %v does not run from the root to a leaf", rows)
			}
			if note := m.pages.rows[0].n.note; !strings.Contains(note, "→ page") {
				t.Errorf("the root row does not say which child it took: %q", note)
			}

			// Enter on the leaf opens its page with the record selected.
			m.pages.cur = len(m.pages.rows) - 1
			leaf := m.pages.sel()
			ref, ok := leaf.data.(recRef)
			if !ok {
				t.Fatalf("the leaf row does not point at a record: %q %v", leaf.label, leaf.note)
			}
			press(m, tea.KeyEnter)
			if m.focus != focusDetail || m.status.err != "" {
				t.Fatalf("enter on the leaf row: %s", m.status)
			}
			if m.page.No != ref.no {
				t.Fatalf("opened page %d, want %d", m.page.No, ref.no)
			}
			sel := m.ann.sel()
			if sel == nil || !strings.HasPrefix(sel.label, "record @") {
				t.Fatalf("the record is not selected: %v", sel)
			}
			var id string
			for _, c := range sel.children {
				if c.label == "id" {
					id = c.value
				}
			}
			if id != "1234" {
				t.Errorf("the selected record has id %q, want 1234", id)
			}

			// Esc leaves the search and puts the page tree back.
			m.focus = focusPages
			press(m, tea.KeyEsc)
			if m.focus != focusPages || m.pagesTitle != "PAGES" {
				t.Fatalf("esc left the search: focus %d, title %q", m.focus, m.pagesTitle)
			}
			if !hasLabel(rowLabels(&m.pages), "PRIMARY") {
				t.Fatalf("the page tree was not restored: %v", rowLabels(&m.pages))
			}

			// A key that is not in the tree still shows how far the search got.
			key(t, m, "f", "1", "2", "5", "0")
			press(m, tea.KeyEnter)
			rows = rowLabels(&m.pages)
			if !hasLabel(rows, "no record with key") {
				t.Fatalf("a purged key gave %v", rows)
			}
			if strings.Contains(m.View(), "panic") || m.View() == "" {
				t.Error("the search pane does not render")
			}
		})
	}
}
