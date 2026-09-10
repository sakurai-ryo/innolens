package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// TestUndoJump walks a delete-marked row's DB_ROLL_PTR into the undo tablespace
// and checks that the undo record it lands on is decoded with column names.
func TestUndoJump(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)
			m.pages.cur = openLeaf(t, m)
			press(m, tea.KeyEnter)
			if m.focus != focusDetail {
				t.Fatalf("detail view not open: %s", m.status)
			}

			expandAll(&m.ann)
			i := rollPtrRow(m)
			if i < 0 {
				t.Fatal("no delete-marked row with an update DB_ROLL_PTR on this page")
			}
			m.ann.cur = i
			press(m, tea.KeyEnter)

			if m.status.err != "" {
				t.Fatalf("following DB_ROLL_PTR: %s", m.status.err)
			}
			if m.page.FIL.Type != innodb.FIL_PAGE_UNDO_LOG {
				t.Fatalf("landed on %s, want an undo log page", m.page.TypeName())
			}
			sel := m.ann.sel()
			if sel == nil || !strings.HasPrefix(sel.label, "record @") {
				t.Fatalf("the undo record is not selected: %v", sel)
			}
			if !strings.Contains(sel.value, "TRX_UNDO_DEL_MARK_REC") {
				t.Errorf("selected record = %q, want the delete-mark undo", sel.value)
			}
			expandAll(&m.ann)
			rows := rowLabels(&m.ann)
			for _, want := range []string{"undo page header", "key fields", "id"} {
				if !hasLabel(rows, want) {
					t.Errorf("undo annotation %v has no %q", rows, want)
				}
			}
			// Every row of an undo page has to reach a ? entry.
			checkRows(t, &m.ann)

			// An undo tablespace is also browsable on its own, with no table
			// definition to name the columns with.
			m.focus = focusTables
			m.tables.cur = tableIdx(&m.tables, "undo_")
			press(m, tea.KeyEnter)
			if m.status.err != "" {
				t.Fatalf("opening the undo tablespace: %s", m.status.err)
			}
			m.pages.cur = indexOf(&m.pages, "Other pages")
			m.pages.expand()
			i = indexOf(&m.pages, "page 3 ")
			if i < 0 {
				t.Fatalf("undo tablespace page list = %v", rowLabels(&m.pages))
			}
			m.pages.cur = i
			press(m, tea.KeyEnter)
			if m.focus != focusDetail || m.status.err != "" {
				t.Fatalf("opening an undo tablespace page: %s", m.status)
			}
		})
	}
}

// openLeaf is the row of the first leaf page of the clustered index.
func openLeaf(t *testing.T, m *Model) int {
	t.Helper()
	for i := 0; i < len(m.pages.rows); i++ {
		m.pages.cur = i
		m.pages.expand()
		if strings.Contains(m.pages.rows[i].n.label, "INDEX leaf") {
			return i
		}
	}
	t.Fatal("no leaf page in the page tree")
	return -1
}

// rollPtrRow is the first DB_ROLL_PTR row that points at an update undo record.
func rollPtrRow(m *Model) int {
	for i, r := range m.ann.rows {
		in, ok := r.n.data.(*innodb.Node)
		if !ok {
			continue
		}
		if rp, ok := in.Ref.(innodb.RollPtr); ok && !rp.Insert {
			return i
		}
	}
	return -1
}
