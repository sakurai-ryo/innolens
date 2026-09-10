package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestReloadDiff writes to the fixture behind the open page and checks that the
// reload key finds exactly the bytes that changed.
func TestReloadDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "innolens"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "test", "testdata", "80", "innolens", "instant.ibd")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	ibd := filepath.Join(dir, "innolens", "instant.ibd")
	if err := os.WriteFile(ibd, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tables.cur = tableIdx(&m.tables, "instant")
	press(m, tea.KeyEnter)
	m.pages.cur = 1
	press(m, tea.KeyEnter)
	if m.focus != focusDetail {
		t.Fatalf("detail view not open: %s", m.status)
	}

	reload := func() { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) }
	reload()
	if m.diff != nil {
		t.Fatalf("unchanged file reported %d changed bytes", m.diff.n)
	}

	off := int64(m.page.No)*16384 + 100
	f, err := os.OpenFile(ibd, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{^b[off], ^b[off+1]}, off); err != nil {
		t.Fatal(err)
	}
	f.Close()

	reload()
	if m.diff == nil || m.diff.n != 2 {
		t.Fatalf("diff = %v", m.diff)
	}
	if !m.diff.at(100) || m.diff.at(102) {
		t.Fatal("diff marks the wrong bytes")
	}
	if !strings.Contains(m.status.delta, "2 bytes changed") {
		t.Fatalf("status = %s", m.status)
	}
	if got := rowLabels(&m.ann); !hasLabel(got, "changes") || !hasLabel(got, "@0064") {
		t.Fatalf("annotation tree %v has no changes section", got)
	}
	// The cursor lands on the change so the dump is already scrolled to it.
	if got := m.ann.sel().label; got != "@0064" {
		t.Fatalf("selection = %q", got)
	}
}
