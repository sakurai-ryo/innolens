package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestBaseline opens a datadir against another one and checks that a page
// detail comes up already diffed against the same page on the other side. The
// two fixtures are different servers, so every page differs somewhere.
func TestBaseline(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), filepath.Join("..", "..", "test", "testdata", "84"))
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tables.cur = tableIdx(&m.tables, "types")
	press(m, tea.KeyEnter)
	m.pages.cur = openLeaf(t, m)
	press(m, tea.KeyEnter)

	if m.diff == nil || m.diff.n == 0 {
		t.Fatal("the page was not diffed against the baseline")
	}
	if !strings.Contains(m.status.delta, "vs 84") {
		t.Errorf("status = %q, want it to name the baseline", m.status)
	}
	if indexOf(&m.ann, "changes") != 0 {
		t.Fatalf("the changes section does not head the annotation: %v", rowLabels(&m.ann))
	}

	// A datadir with no counterpart page is not an error, just no comparison.
	m2, err := New(filepath.Join("..", "..", "test", "testdata", "80"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m2.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m2.tables.cur = tableIdx(&m2.tables, "types")
	press(m2, tea.KeyEnter)
	m2.pages.cur = openLeaf(t, m2)
	press(m2, tea.KeyEnter)
	if m2.diff != nil || m2.status.err != "" {
		t.Fatalf("an empty baseline produced a diff or an error: %s", m2.status)
	}
}
