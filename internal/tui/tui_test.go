package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Without a TTY lipgloss falls back to the Ascii profile and drops every
// escape, which would make the highlight assertions vacuous.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI)
	os.Exit(m.Run())
}

func key(t *testing.T, m *Model, names ...string) {
	t.Helper()
	for _, n := range names {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(n)})
	}
}

func press(m *Model, k tea.KeyType) { m.Update(tea.KeyMsg{Type: k}) }

// TestBrowse drives the model the way the keyboard would: pick a table, walk
// into the B+tree, then open a page and select an annotated field.
func TestBrowse(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			dir := filepath.Join("..", "..", "test", "testdata", ver)
			m, err := New(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})

			labels := rowLabels(&m.tables)
			for _, want := range []string{"innolens", "#innodb_redo"} {
				if !hasLabel(labels, want) {
					t.Fatalf("left pane %v missing %q", labels, want)
				}
			}
			if hasLabel(labels, "types") {
				t.Fatalf("left pane %v opened a schema before it was expanded", labels)
			}
			expandAll(&m.tables)
			labels = rowLabels(&m.tables)
			for _, want := range []string{"types", "instant"} {
				if !hasLabel(labels, want) {
					t.Fatalf("expanded left pane %v missing %q", labels, want)
				}
			}
			m.tables.cur = tableIdx(&m.tables, "types")
			press(m, tea.KeyEnter)
			if m.focus != focusPages {
				t.Fatalf("enter on a table did not focus the page tree: %s", m.status)
			}
			if m.table.Name != "types" {
				t.Fatalf("table = %q", m.table.Name)
			}

			pageRows := rowLabels(&m.pages)
			for _, want := range []string{"PRIMARY", "idx_varchar", "Other pages"} {
				if !hasLabel(pageRows, want) {
					t.Fatalf("page tree %v missing %q", pageRows, want)
				}
			}
			root := m.pages.rows[1].n // root page of PRIMARY
			if !strings.HasPrefix(root.label, "page ") || !strings.Contains(root.label, "level 1") {
				t.Fatalf("PRIMARY root label = %q, want a level 1 page", root.label)
			}

			// Children are lazy: they appear only after expanding.
			if len(root.children) != 0 {
				t.Fatal("child pages loaded before expand")
			}
			m.pages.cur = 1
			press(m, tea.KeyRight)
			if len(root.children) == 0 {
				t.Fatal("expand did not load child pages")
			}
			for _, c := range root.children {
				if !strings.Contains(c.label, "level 0") {
					t.Fatalf("leaf label = %q", c.label)
				}
			}

			// Other pages is scanned on demand and holds no INDEX pages.
			m.pages.cur = indexOf(&m.pages, "Other pages")
			press(m, tea.KeyRight)
			other := m.pages.sel()
			if len(other.children) == 0 {
				t.Fatal("Other pages is empty")
			}
			for _, c := range other.children {
				if strings.Contains(c.label, " FIL_PAGE_INDEX") {
					t.Fatalf("INDEX page listed under Other pages: %q", c.label)
				}
			}

			// Enter on a leaf page opens the detail screen.
			m.pages.cur = 2
			press(m, tea.KeyEnter)
			if m.focus != focusDetail {
				t.Fatalf("enter on a page did not open the detail view: %s", m.status)
			}
			annRows := rowLabels(&m.ann)
			for _, want := range []string{"FIL header", "Records", "Page directory", "FIL trailer"} {
				if !hasLabel(annRows, want) {
					t.Fatalf("annotation tree %v missing %q", annRows, want)
				}
			}

			// Selecting FIL_PAGE_OFFSET highlights its bytes in the dump.
			m.ann.cur = indexOf(&m.ann, "FIL_PAGE_OFFSET")
			press(m, tea.KeyDown)
			press(m, tea.KeyUp)
			if got := m.ann.sel().label; got != "FIL_PAGE_OFFSET" {
				t.Fatalf("selection = %q", got)
			}
			dump := hexDump(m.page.Data, m.regions, nil, m.hexTop, 4, 4, 4)
			if !strings.Contains(dump, "\x1b[7") {
				t.Error("hex dump has no highlighted range")
			}

			// Esc unwinds detail -> pages -> tables.
			press(m, tea.KeyEsc)
			if m.focus != focusPages {
				t.Fatal("esc did not leave the detail view")
			}
			press(m, tea.KeyEsc)
			if m.focus != focusTables {
				t.Fatal("esc did not leave the page tree")
			}
		})
	}
}

// TestDeepPage checks that a page far into the file is reachable and renders.
func TestDeepPage(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), "")
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
	dump := hexDump(m.page.Data, m.regions, nil, 0, 3, 4, 4)
	if lines := strings.Split(dump, "\n"); len(lines) != 3 {
		t.Fatalf("hexDump returned %d lines", len(lines))
	}
	if !strings.Contains(strings.SplitN(dump, "\n", 2)[0], "0000") {
		t.Fatalf("hexDump = %q", dump)
	}
}

func rowLabels(l *list) []string {
	out := make([]string, len(l.rows))
	for i, r := range l.rows {
		out[i] = r.n.label
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, s := range labels {
		if s == want || strings.HasPrefix(s, want) {
			return true
		}
	}
	return false
}

func indexOf(l *list, prefix string) int {
	for i, r := range l.rows {
		if strings.HasPrefix(r.n.label, prefix) {
			return i
		}
	}
	return -1
}

func findRow(l *list, prefix string) *node { return l.rows[indexOf(l, prefix)].n }

// redo_new.ibd was created during the redo phase and killed before its SDI
// reached disk. The table is unreadable, the pages are not.
func TestOpenTableWithoutSDI(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tables.cur = tableIdx(&m.tables, "redo_new")
	press(m, tea.KeyEnter)
	if m.focus != focusPages {
		t.Fatalf("enter did not focus the page tree: %s", m.status)
	}
	if m.table != nil {
		t.Fatalf("table = %v, want nil", m.table)
	}
	if !hasLabel(rowLabels(&m.pages), "Other pages") {
		t.Fatalf("page tree %v has no pages", rowLabels(&m.pages))
	}
	if !strings.Contains(m.status.err, "SDI") {
		t.Fatalf("status does not say why the table is missing: %s", m.status)
	}
}

// tableIdx finds a row in the left pane, which starts with every schema folded.
func tableIdx(l *list, prefix string) int {
	if i := indexOf(l, prefix); i >= 0 {
		return i
	}
	expandAll(l)
	return indexOf(l, prefix)
}

// TestFilter types into the datadir filter and checks that it narrows the tree
// to the matches, keeps their schema, and restores the full tree on esc.
func TestFilter(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})

	key(t, m, "/", "i", "n", "s", "t")
	labels := rowLabels(&m.tables)
	for _, want := range []string{"innolens", "instant"} {
		if !hasLabel(labels, want) {
			t.Fatalf("filtered left pane %v missing %q", labels, want)
		}
	}
	if hasLabel(labels, "types") {
		t.Fatalf("filtered left pane %v kept a table that does not match", labels)
	}
	if !strings.Contains(m.datadirTitle(), "/inst") {
		t.Fatalf("heading = %q, want the query in it", m.datadirTitle())
	}

	// A schema matching on its own name keeps every table under it.
	press(m, tea.KeyEsc)
	key(t, m, "/", "i", "n", "n", "o")
	labels = rowLabels(&m.tables)
	for _, want := range []string{"instant", "types"} {
		if !hasLabel(labels, want) {
			t.Fatalf("schema match %v missing %q", labels, want)
		}
	}

	// A dot splits the query: the schema half, the table half, or both.
	press(m, tea.KeyEsc)
	key(t, m, "/", "i", "n", "n", "o", ".", "t", "y", "p")
	labels = rowLabels(&m.tables)
	if !hasLabel(labels, "types") || hasLabel(labels, "instant") {
		t.Fatalf("schema.table match %v is not narrowed to the table half", labels)
	}
	press(m, tea.KeyEsc)
	key(t, m, "/", ".", "t", "y", "p")
	labels = rowLabels(&m.tables)
	if !hasLabel(labels, "types") || hasLabel(labels, "#innodb_redo") {
		t.Fatalf(".table match %v kept an entry without a schema", labels)
	}
	press(m, tea.KeyEsc)
	key(t, m, "/", "i", "n", "n", "o", ".")
	labels = rowLabels(&m.tables)
	for _, want := range []string{"instant", "types"} {
		if !hasLabel(labels, want) {
			t.Fatalf("schema. match %v missing %q", labels, want)
		}
	}

	press(m, tea.KeyEsc)
	if m.prompt.kind != promptNone || m.query != "" {
		t.Fatalf("esc left the filter open: %q", m.query)
	}
	labels = rowLabels(&m.tables)
	if !hasLabel(labels, "#innodb_redo") || hasLabel(labels, "instant") {
		t.Fatalf("left pane %v was not restored to the folded tree", labels)
	}
}

// TestNoticeDialog checks the notice floats over the frame as a centred box
// without pushing any of it out of the terminal.
func TestNoticeDialog(t *testing.T) {
	m, err := New(filepath.Join("..", "..", "test", "testdata", "80"), "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	plain := strings.Split(m.View(), "\n")
	m.status.notice = "read view trx_id 42 marks 3 version chain(s) on this page"
	got := strings.Split(m.View(), "\n")
	if len(got) != len(plain) {
		t.Fatalf("the dialog resized the frame: %d lines, want %d", len(got), len(plain))
	}
	var boxed []string
	for _, l := range got {
		if strings.ContainsAny(l, "╭╰") || strings.Contains(l, "trx_id 42") {
			boxed = append(boxed, l)
		}
	}
	if len(boxed) != 3 {
		t.Fatalf("dialog is %d lines, want a top, a body and a bottom:\n%s", len(boxed), m.View())
	}
	for _, l := range boxed {
		if !strings.HasPrefix(l, "  ") {
			t.Errorf("dialog is not centred: %q", l)
		}
	}
	t.Log("\n" + m.View())
}
