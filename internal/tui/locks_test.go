package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pick presses Enter on the picker row whose label starts with prefix.
func pick(t *testing.T, m *Model, prefix string) {
	t.Helper()
	i := indexOf(m.cur(), prefix)
	if i < 0 {
		t.Fatalf("no row %q in %v", prefix, rowLabels(m.cur()))
	}
	m.cur().cur = i
	press(m, tea.KeyEnter)
}

// TestLocks builds a locking statement with the `l` picker and follows what it
// draws: one row per page locked, and on the page the marked records and the
// `locks` section, whose rows select the record they stand for.
func TestLocks(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)
			tree := rowLabels(&m.pages)

			// The cursor is on the secondary index, which the index step offers first.
			m.pages.cur = indexOf(&m.pages, "idx_varchar")
			key(t, m, "l")
			if m.lockWiz == nil || !strings.HasPrefix(m.lockWiz.title, "LOCK ") {
				t.Fatalf("l did not open the picker: %+v", m.lockWiz)
			}
			// The popup floats over the page tree, which stays as it was.
			if v := m.View(); !strings.Contains(v, "choose the isolation level") || !strings.Contains(v, "REPEATABLE READ") {
				t.Errorf("the picker is not drawn")
			}
			if strings.Join(rowLabels(&m.pages), "|") != strings.Join(tree, "|") {
				t.Errorf("the picker replaced the page tree: %v", rowLabels(&m.pages))
			}
			pick(t, m, "REPEATABLE READ")
			pick(t, m, "SELECT ... FOR UPDATE")
			if sel := m.cur().sel(); !strings.HasPrefix(sel.label, "idx_varchar") {
				t.Errorf("the index step starts on %q, want the tree under the cursor", sel.label)
			}
			pick(t, m, "idx_varchar")
			pick(t, m, "= key")
			if m.prompt.kind != promptLock {
				t.Fatalf("the comparison did not open the key prompt: %+v", m.prompt)
			}
			if v := m.View(); !strings.Contains(v, "key: ") || !strings.Contains(v, "REPEATABLE READ · x · idx_varchar (c_varchar) · =") {
				t.Errorf("the value prompt does not show what was chosen")
			}
			for _, r := range "varchar-101-x" {
				key(t, m, string(r))
			}
			press(m, tea.KeyEnter)
			if m.status.err != "" {
				t.Fatalf("lock: %s", m.status.err)
			}
			if !strings.HasPrefix(m.pagesTitle, "LOCKS x = varchar-101-x IN idx_varchar") {
				t.Fatalf("pane title = %q", m.pagesTitle)
			}
			if !strings.Contains(m.status.info, "SELECT * FROM types WHERE c_varchar = 'varchar-101-x' FOR UPDATE") {
				t.Errorf("status does not say the SQL: %q", m.status.info)
			}
			rows := rowLabels(&m.pages)
			if len(rows) != 2 || !strings.Contains(rows[0], "idx_varchar") || !strings.Contains(rows[1], "PRIMARY") {
				t.Fatalf("pages = %v, want one idx_varchar page then one PRIMARY page", rows)
			}
			if note := m.pages.rows[0].n.note; !strings.Contains(note, "1 × X,GAP") || !strings.Contains(note, "1 × X") {
				t.Errorf("secondary page note = %q, want a next-key and a gap lock", note)
			}

			// Enter on the PRIMARY row opens the page with the locked record selected.
			m.pages.cur = 1
			press(m, tea.KeyEnter)
			if m.focus != focusDetail || m.status.err != "" {
				t.Fatalf("enter on the page row: %s", m.status)
			}
			sel := m.ann.sel()
			if sel == nil || !strings.HasPrefix(sel.label, "record @") {
				t.Fatalf("the record is not selected: %v", sel)
			}
			if !strings.Contains(sel.note, "X,REC_NOT_GAP") {
				t.Errorf("record note = %q, want the lock on it", sel.note)
			}
			if v := m.View(); !strings.Contains(v, "X,REC_NOT_GAP") {
				t.Errorf("the page view does not show the lock")
			}
			labels := rowLabels(&m.ann)
			if !hasLabel(labels, "locks") {
				t.Fatalf("no locks section: %v", labels)
			}
			if !strings.Contains(m.status.String(), "locks: x = varchar-101-x") {
				t.Errorf("status bar = %q, want the lock statement", m.status)
			}

			// A lock row selects its record on enter.
			i := indexOf(&m.ann, "X,REC_NOT_GAP")
			if i < 0 {
				t.Fatalf("no lock row: %v", labels)
			}
			m.ann.cur = i
			press(m, tea.KeyEnter)
			if sel := m.ann.sel(); sel == nil || !strings.HasPrefix(sel.label, "record @") {
				t.Errorf("enter on the lock row selected %v", sel)
			}

			// Esc from the list restores the page tree.
			m.focus = focusPages
			press(m, tea.KeyEsc)
			if strings.HasPrefix(m.pagesTitle, "LOCKS ") || !hasLabel(rowLabels(&m.pages), "PRIMARY") {
				t.Fatalf("esc did not restore the page tree: %q %v", m.pagesTitle, rowLabels(&m.pages))
			}

			// A BETWEEN under READ COMMITTED asks for two keys; esc from the second
			// asks for the first again, esc from the first step leaves the picker
			// with the tree as it was.
			key(t, m, "l")
			pick(t, m, "READ COMMITTED")
			pick(t, m, "UPDATE")
			pick(t, m, "PRIMARY")
			pick(t, m, "between")
			key(t, m, "1", "0")
			press(m, tea.KeyEnter)
			if m.prompt.kind != promptLock || m.lockWiz.lockValueLabel() != "upper bound" {
				t.Fatalf("after the lower bound: prompt %+v, asks for %q", m.prompt, m.lockWiz.lockValueLabel())
			}
			press(m, tea.KeyEsc)
			if m.prompt.kind != promptLock || m.lockWiz.lockValueLabel() != "lower bound" {
				t.Fatalf("esc from the upper bound: prompt %+v, asks for %q", m.prompt, m.lockWiz.lockValueLabel())
			}
			key(t, m, "1", "0")
			press(m, tea.KeyEnter)
			key(t, m, "1", "3")
			press(m, tea.KeyEnter)
			if m.status.err != "" || !strings.HasPrefix(m.pagesTitle, "LOCKS rc update 10..13 IN PRIMARY") {
				t.Fatalf("between: %q %s", m.pagesTitle, m.status.err)
			}
			if note := m.pages.rows[0].n.note; note != "4 × X,REC_NOT_GAP" {
				t.Errorf("rc update 10..13 note = %q", note)
			}
			press(m, tea.KeyEsc)
			key(t, m, "l")
			pick(t, m, "clear the locks shown")
			if m.locks != nil || m.lockWiz != nil || m.status.locks != "" {
				t.Errorf("clearing left %v %v %q", m.locks, m.lockWiz, m.status.locks)
			}
			key(t, m, "l")
			press(m, tea.KeyEsc)
			if m.lockWiz != nil || strings.Join(rowLabels(&m.pages), "|") != strings.Join(tree, "|") {
				t.Errorf("esc from the first step: %v", rowLabels(&m.pages))
			}
		})
	}
}
