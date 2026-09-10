package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRedoBrowse walks the redo pane the way the keyboard would: open
// #innodb_redo, expand an mtr, expand a record, then jump to the page it wrote.
func TestRedoBrowse(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})

			m.tables.cur = tableIdx(&m.tables, "#innodb_redo")
			press(m, tea.KeyEnter)
			if m.focus != focusPages {
				t.Fatalf("enter on #innodb_redo did not focus the mtr tree: %s", m.status)
			}
			if !strings.Contains(m.status.info, "checkpoint lsn") {
				t.Fatalf("status = %q", m.status)
			}
			if len(m.pages.rows) < 2 {
				t.Fatalf("only %d mtrs", len(m.pages.rows))
			}
			for _, r := range m.pages.rows {
				if !strings.HasPrefix(r.n.label, "mtr lsn ") {
					t.Fatalf("unexpected top level node %q", r.n.label)
				}
			}

			// Every record must have parsed: an unknown body stops the scan.
			for _, mtr := range m.redo.mtrs {
				if mtr.Err != nil {
					t.Fatalf("mtr at lsn %d: %v", mtr.StartLSN, mtr.Err)
				}
			}

			// Find the INSERT of the row the fixture writes only into the redo.
			rec := findInsert(m, "redo-insert")
			if rec == nil {
				t.Fatal("no MLOG_REC_INSERT holding 'redo-insert' found")
			}
			fields := map[string]string{}
			for _, c := range rec.children {
				if c.label == "record image" {
					for _, f := range c.children {
						fields[f.label] = f.value
					}
				}
			}
			if got := fields["c_varchar"]; got != `"redo-insert"` {
				t.Fatalf("c_varchar = %q", got)
			}
			if fields["id"] == "" || fields["DB_TRX_ID"] == "" {
				t.Fatalf("record image is missing columns: %v", fields)
			}

			// The record header is annotated for every type.
			var hdr []string
			for _, c := range rec.children {
				hdr = append(hdr, c.label)
			}
			for _, want := range []string{"type", "space id", "page no", "index"} {
				if !hasLabel(hdr, want) {
					t.Fatalf("record fields %v missing %q", hdr, want)
				}
			}

			// Enter jumps to the page the record modified.
			jump := rec.data.(pageJump)
			press(m, tea.KeyEnter)
			if m.focus != focusDetail {
				t.Fatalf("enter on a redo record did not open a page: %s", m.status)
			}
			if m.page.No != jump.page {
				t.Fatalf("opened page %d, want %d", m.page.No, jump.page)
			}

			// That page detail carries a redo section listing the same record.
			i := indexOf(&m.ann, "redo")
			if i < 0 {
				t.Fatalf("page detail has no redo section: %v", rowLabels(&m.ann))
			}
			m.ann.cur = i
			press(m, tea.KeyRight)
			sec := m.ann.sel()
			if len(sec.children) == 0 {
				t.Fatal("redo section is empty")
			}
			var found bool
			for _, c := range sec.children {
				if strings.Contains(c.label, "MLOG_REC_INSERT") {
					found = true
				}
			}
			if !found {
				t.Fatalf("redo section does not list the insert: %v", childLabels(sec))
			}
		})
	}
}

// findInsert expands mtrs until it finds an MLOG_REC_INSERT whose rebuilt
// record image contains want, leaving the cursor on it.
func findInsert(m *Model, want string) *node {
	for i := 0; i < len(m.pages.rows); i++ {
		m.pages.cur = i
		if !strings.HasPrefix(m.pages.sel().label, "mtr lsn ") {
			continue
		}
		press(m, tea.KeyRight)
		for j := i + 1; j < len(m.pages.rows) && m.pages.rows[j].depth > 0; j++ {
			if !strings.HasPrefix(m.pages.rows[j].n.label, "MLOG_REC_INSERT") {
				continue
			}
			m.pages.cur = j
			press(m, tea.KeyRight)
			n := m.pages.sel()
			for _, c := range n.children {
				if c.label == "record image" && strings.Contains(childValues(c), want) {
					return n
				}
			}
			press(m, tea.KeyLeft)
		}
		m.pages.cur = i
		press(m, tea.KeyLeft)
	}
	return nil
}

func childLabels(n *node) []string {
	out := make([]string, len(n.children))
	for i, c := range n.children {
		out[i] = c.label
	}
	return out
}

func childValues(n *node) string {
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(c.value)
		b.WriteByte(' ')
	}
	return b.String()
}
