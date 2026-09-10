package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestVersionChain follows a row's DB_ROLL_PTR back through the undo log and
// checks that the older version is rebuilt, and that a read view picks out the
// version it would return.
func TestVersionChain(t *testing.T) {
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

			expandAll(&m.ann)
			i, chain := findChain(m)
			if chain == nil {
				t.Fatal("no record on this leaf page has an older version")
			}
			cur, prev := chain[0], chain[1]
			if !strings.HasPrefix(cur.label, "current") {
				t.Fatalf("the chain does not start at the record on the page: %q", cur.label)
			}
			if !strings.Contains(prev.label, "TRX_UNDO_") {
				t.Fatalf("the older version is not an undo record: %q", prev.label)
			}
			if cur.value == prev.value {
				t.Errorf("the two versions read the same: %q", cur.value)
			}
			if trxIDOf(cur.label) <= trxIDOf(prev.label) {
				t.Errorf("the older version has the newer trx_id: %q vs %q", cur.label, prev.label)
			}

			// Enter on a version opens the undo record that holds it.
			m.ann.cur = i + indexOfChild(chain, prev) + 1
			if got := m.ann.sel().label; got != prev.label {
				t.Fatalf("cursor is on %q, want %q", got, prev.label)
			}
			press(m, tea.KeyEnter)
			if m.status.err != "" {
				t.Fatalf("enter on a version: %s", m.status.err)
			}

			// A read view of the older transaction sees the older version.
			m.focus = focusTables
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)
			m.pages.cur = openLeaf(t, m)
			press(m, tea.KeyEnter)
			key(t, m, "v")
			key(t, m, strings.Split(strconv.FormatUint(trxIDOf(prev.label), 10), "")...)
			press(m, tea.KeyEnter)
			if m.status.err != "" {
				t.Fatalf("read view: %s", m.status.err)
			}
			if !strings.Contains(m.status.delta, "read view trx_id") {
				t.Fatalf("status does not show the read view: %s", m.status)
			}
			expandAll(&m.ann)
			_, chain = findChain(m)
			if chain == nil {
				t.Fatal("the version chain is gone after setting a read view")
			}
			var seen int
			for _, v := range chain {
				if strings.Contains(v.note, "sees this") {
					seen++
					if trxIDOf(v.label) > m.view.trxID {
						t.Errorf("the read view was pointed at %q", v.label)
					}
				}
			}
			if seen != 1 {
				t.Errorf("%d versions are marked as visible in %v", seen, childLabels(&node{children: chain}))
			}

			// A read view that is not a number is refused, not applied.
			key(t, m, "v", "x")
			press(m, tea.KeyEnter)
			if m.status.err == "" {
				t.Error("a non-numeric read view was accepted")
			}
		})
	}
}

// findChain expands the first version chain of the page that reaches an older
// version, and returns the row it hangs on together with the versions.
func findChain(m *Model) (int, []*node) {
	for i, r := range m.ann.rows {
		if r.n.label != "versions" {
			continue
		}
		m.ann.cur = i
		press(m, tea.KeyRight)
		if len(r.n.children) > 1 && strings.Contains(r.n.children[1].label, "TRX_UNDO_") {
			return i, r.n.children
		}
		press(m, tea.KeyLeft)
	}
	return -1, nil
}

// indexOfChild is how many rows below the versions row a version sits, counting
// the columns each one is expanded into.
func indexOfChild(chain []*node, want *node) int {
	n := 0
	for _, c := range chain {
		if c == want {
			return n
		}
		n++
		if c.expanded {
			n += len(c.children)
		}
	}
	return -1
}
