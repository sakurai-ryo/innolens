package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sakurai-ryo/innolens/internal/innodb"
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

			// f lists the indexes with the clustered one selected; enter asks for the key.
			key(t, m, "f")
			if m.findWiz == nil || m.pagesTitle != "FIND  choose the index" {
				t.Fatalf("f did not open the index picker: %q", m.pagesTitle)
			}
			if rows := rowLabels(&m.pages); len(rows) != 2 || !strings.HasPrefix(rows[0], "PRIMARY") || !strings.HasPrefix(rows[1], "idx_varchar") {
				t.Fatalf("index picker = %v", rows)
			}
			press(m, tea.KeyEnter)
			press(m, tea.KeyEnter)
			if m.prompt.kind != promptFind || m.status.err == "" {
				t.Fatalf("enter with no key: prompt %+v, err %q", m.prompt, m.status.err)
			}
			key(t, m, "1", "2", "3", "4")
			if m.prompt.kind != promptFind || m.prompt.text != "1234" {
				t.Fatalf("find prompt = %+v", m.prompt)
			}
			before := rowLabels(&m.pages)
			press(m, tea.KeyLeft)
			press(m, tea.KeyRight)
			if got := rowLabels(&m.pages); strings.Join(got, "\n") != strings.Join(before, "\n") {
				t.Fatalf("arrows typed into the find prompt changed the tree:\n%v\n%v", before, got)
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

			// Esc from the key goes back to the index list, and from there out.
			key(t, m, "f")
			press(m, tea.KeyEnter)
			press(m, tea.KeyEsc)
			if m.findWiz == nil || m.prompt.kind != promptNone || m.pagesTitle != "FIND  choose the index" {
				t.Fatalf("esc from the key: wiz %v, prompt %+v, title %q", m.findWiz, m.prompt, m.pagesTitle)
			}
			press(m, tea.KeyEsc)
			if m.findWiz != nil || m.pagesTitle != "PAGES" || !hasLabel(rowLabels(&m.pages), "PRIMARY") {
				t.Fatalf("esc from the list: wiz %v, title %q, rows %v", m.findWiz, m.pagesTitle, rowLabels(&m.pages))
			}

			// A key that is not in the tree still shows how far the search got.
			key(t, m, "f")
			press(m, tea.KeyEnter)
			key(t, m, "1", "2", "5", "0")
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

// TestClusterLink follows the link a secondary index leaf record has to the
// clustered index: enter on it runs the PK lookup and lands on the row.
func TestClusterLink(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)
			openSecondaryLeaf(t, m)
			expandAll(&m.ann)

			link := findRow(&m.ann, "clustered index")
			ref, ok := link.data.(clustRef)
			if !ok || ref.ix.Name != "PRIMARY" {
				t.Fatalf("the link does not point at the clustered index: %q %v", link.value, link.data)
			}
			if !strings.Contains(link.value, "PRIMARY  id="+ref.key) {
				t.Fatalf("link row = %q, key %q", link.value, ref.key)
			}

			// A freed record keeps its key, so the free list is linked as well.
			for _, sec := range m.ann.root.children {
				if sec.label != "PAGE_FREE list" || len(sec.children) == 0 {
					continue
				}
				linked := 0
				for _, rec := range sec.children {
					for _, c := range rec.children {
						if c.label == "clustered index" {
							linked++
						}
					}
				}
				if linked != len(sec.children) {
					t.Errorf("%d of %d freed records are linked", linked, len(sec.children))
				}
			}

			m.ann.cur = indexOf(&m.ann, "clustered index")
			press(m, tea.KeyEnter)
			if m.status.err != "" {
				t.Fatalf("enter on the link: %s", m.status)
			}
			if want := "FIND " + ref.key + " IN PRIMARY"; !strings.Contains(m.pagesTitle, want) {
				t.Fatalf("pane title = %q, want %q", m.pagesTitle, want)
			}
			leaf := m.pages.rows[len(m.pages.rows)-1].n
			if _, ok := leaf.data.(recRef); !ok {
				t.Fatalf("the descent did not reach the record: %q %v", leaf.label, leaf.note)
			}
			m.pages.cur = len(m.pages.rows) - 1
			press(m, tea.KeyEnter)
			if id := findRow(&m.ann, "record @").children; len(id) == 0 {
				t.Fatal("the record has no columns")
			}
			if got := field(m.ann.sel().data.(*innodb.Node), "id"); got == nil || got.Value != ref.key {
				t.Fatalf("landed on id %v, want %s", got, ref.key)
			}
		})
	}
}

// openSecondaryLeaf opens the first leaf page of the idx_varchar B+tree.
func openSecondaryLeaf(t *testing.T, m *Model) {
	t.Helper()
	i := indexOf(&m.pages, "idx_varchar")
	if i < 0 {
		t.Fatalf("no idx_varchar in the page tree: %v", rowLabels(&m.pages))
	}
	for ; i < len(m.pages.rows); i++ {
		m.pages.cur = i
		m.pages.expand()
		if strings.Contains(m.pages.rows[i].n.label, "INDEX leaf") {
			press(m, tea.KeyEnter)
			if m.focus != focusDetail {
				t.Fatalf("the leaf page did not open: %s", m.status)
			}
			return
		}
	}
	t.Fatal("no leaf page under idx_varchar")
}
