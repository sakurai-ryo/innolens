package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// TestInsertKey simulates an insert from the page tree: the picker, the key,
// then the rows that say where the record goes, and how the locks of the
// last `l` get in its way.
func TestInsertKey(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)

			key(t, m, "i")
			if m.findWiz == nil || !m.findWiz.insert || m.findWiz.title != "INSERT  choose the index" {
				t.Fatalf("i did not open the index picker: %+v", m.findWiz)
			}
			press(m, tea.KeyEnter)
			key(t, m, "1", "2", "5", "0")
			press(m, tea.KeyEnter)
			if m.status.err != "" || m.pagesTitle != "INSERT 1250 INTO PRIMARY" {
				t.Fatalf("insert: err %q, title %q", m.status.err, m.pagesTitle)
			}
			rows := rowLabels(&m.pages)
			for _, want := range []string{"level 1", "level 0", "page directory", "insert pattern"} {
				if !hasLabel(rows, want) {
					t.Errorf("no %q row in %v", want, rows)
				}
			}
			if !hasLabel(rows, "takes the heap") && !hasLabel(rows, "reuses the free list") {
				t.Errorf("no row says where the bytes come from: %v", rows)
			}
			var leaf *node
			for _, r := range m.pages.rows {
				if strings.HasPrefix(r.n.label, "level 0") {
					leaf = r.n
				}
			}
			if leaf == nil || !strings.Contains(leaf.note, "between @") || !strings.Contains(leaf.note, "key 1249") {
				t.Fatalf("leaf row = %+v", leaf)
			}
			if strings.Contains(m.View(), "panic") || m.View() == "" {
				t.Error("the insert pane does not render")
			}

			// Esc puts the page tree back.
			press(m, tea.KeyEsc)
			if m.pagesTitle != "PAGES" || !hasLabel(rowLabels(&m.pages), "PRIMARY") {
				t.Fatalf("esc: title %q, rows %v", m.pagesTitle, rowLabels(&m.pages))
			}

			// A key that is there is a duplicate.
			key(t, m, "i")
			press(m, tea.KeyEnter)
			key(t, m, "1", "2", "3", "4")
			press(m, tea.KeyEnter)
			if rows := rowLabels(&m.pages); !hasLabel(rows, "duplicate key") {
				t.Errorf("1234 gave %v", rows)
			}
			press(m, tea.KeyEsc)

			// A locking read of the missing key leaves a gap lock on the record
			// after it, which is what the insert would wait for.
			st, _ := innodb.ParseLockStmt("x = 1250")
			locks, err := m.space.SimulateLocks(m.table, m.table.Indexes[0], st)
			if err != nil {
				t.Fatal(err)
			}
			m.locks = &lockSet{stmt: st, locks: locks}
			key(t, m, "i")
			press(m, tea.KeyEnter)
			key(t, m, "1", "2", "5", "0")
			press(m, tea.KeyEnter)
			if rows := rowLabels(&m.pages); !hasLabel(rows, "waits for a lock") {
				t.Errorf("the insert does not wait for the gap lock: %v", rows)
			}
		})
	}
}
