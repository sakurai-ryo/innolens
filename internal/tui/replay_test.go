package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestReplay walks from a redo record to the page it modified and applies it
// with n. The fixture writes its DML after copying the .ibd files, so the page
// on disk still holds the old row and the replay has to produce the new one.
func TestReplay(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			m, err := New(filepath.Join("..", "..", "test", "testdata", ver), "")
			if err != nil {
				t.Fatal(err)
			}
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.tables.cur = tableIdx(&m.tables, "#innodb_redo")
			press(m, tea.KeyEnter)

			rec := findRecord(m, "MLOG_REC_UPDATE_IN_PLACE")
			if rec == nil {
				t.Fatal("no MLOG_REC_UPDATE_IN_PLACE in the redo tree")
			}
			press(m, tea.KeyEnter)
			if m.focus != focusDetail {
				t.Fatalf("enter on the redo record did not open its page: %s", m.status)
			}
			before := append([]byte(nil), m.page.Data...)

			key(t, m, "n")
			if m.status.err != "" {
				t.Fatalf("replay: %s", m.status.err)
			}
			if m.rep == nil || m.rep.pos != 1 {
				t.Fatalf("replay did not step: %+v", m.rep)
			}
			if !strings.Contains(m.status.info, "replay 1/") {
				t.Fatalf("status = %q", m.status)
			}
			if m.diff == nil || m.diff.n == 0 {
				t.Fatal("the step changed no byte of the page")
			}
			if m.page.FIL.LSN <= m.rep.recs[0].LSN-uint64(m.rep.recs[0].Len) {
				t.Errorf("page lsn %d did not move to the record", m.page.FIL.LSN)
			}
			if !m.page.ChecksumOK {
				t.Error("the replayed page does not checksum")
			}

			// The annotation lists the records with the applied one marked, and
			// the changes section names the bytes the step wrote.
			for _, want := range []string{"replay", "changes"} {
				if indexOf(&m.ann, want) < 0 {
					t.Fatalf("annotation %v has no %q section", rowLabels(&m.ann), want)
				}
			}
			m.ann.cur = indexOf(&m.ann, "replay")
			if got := m.ann.sel().children[0].note; !strings.HasPrefix(got, "applied") {
				t.Errorf("first record of the replay section is noted %q", got)
			}

			if m.View() == "" {
				t.Error("the replayed page does not render")
			}

			// p takes the step back, which puts the page bytes back as they are
			// on disk.
			key(t, m, "p")
			if m.rep.pos != 0 {
				t.Fatalf("p did not step back: %+v", m.rep)
			}
			if string(m.page.Data) != string(before) {
				t.Error("stepping back did not restore the page as it is on disk")
			}
			// Nothing was written: the file still reads the way it did.
			p, err := m.pageOpen()
			if err != nil {
				t.Fatal(err)
			}
			if string(p.Data) != string(before) {
				t.Fatal("the replay wrote to the tablespace file")
			}
		})
	}
}

// TestScenario opens the two snapshots scripts/scenario.sh records: the page on
// disk still holds the row as it was, the redo log holds the UPDATE, and
// replaying it produces the new value. Set INNOLENS_SCENARIO to the output
// directory of a run to check it; the recording needs Docker, so CI skips this.
func TestScenario(t *testing.T) {
	dir := os.Getenv("INNOLENS_SCENARIO")
	if dir == "" {
		t.Skip("set INNOLENS_SCENARIO to the output of scripts/scenario.sh")
	}
	m, err := New(filepath.Join(dir, "after"), filepath.Join(dir, "before"))
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tables.cur = tableIdx(&m.tables, "accounts")
	press(m, tea.KeyEnter)
	m.pages.cur = openLeaf(t, m)
	press(m, tea.KeyEnter)
	if m.status.err != "" {
		t.Fatalf("opening the leaf page: %s", m.status)
	}
	if got := recordValue(m, "balance"); got != "1000" {
		t.Fatalf("balance on disk = %q, want the row as it was before the step", got)
	}

	key(t, m, "n")
	if m.status.err != "" {
		t.Fatalf("replay: %s", m.status.err)
	}
	for m.rep.pos < len(m.rep.recs) && recordValue(m, "balance") != "900" {
		key(t, m, "n")
	}
	if got := recordValue(m, "balance"); got != "900" {
		t.Errorf("balance after replaying %d records = %q, want 900", m.rep.pos, got)
	}
}

// recordValue is the value of a column of the first user record of the page.
func recordValue(m *Model, col string) string {
	expandAll(&m.ann)
	for i, r := range m.ann.rows {
		if !strings.HasPrefix(r.n.label, "record @") {
			continue
		}
		for _, c := range m.ann.rows[i+1:] {
			if c.n.label == col {
				return c.n.value
			}
		}
	}
	return ""
}

// findRecord expands mtrs until a record of the given type turns up in a
// tablespace of this datadir, leaving the cursor on it. The log also holds
// records for the dictionary and the temporary tablespaces, which are not
// files innolens can open.
func findRecord(m *Model, typ string) *node {
	for i := 0; i < len(m.pages.rows); i++ {
		m.pages.cur = i
		if !strings.HasPrefix(m.pages.sel().label, "mtr lsn ") {
			continue
		}
		press(m, tea.KeyRight)
		for j := i + 1; j < len(m.pages.rows) && m.pages.rows[j].depth > 0; j++ {
			n := m.pages.rows[j].n
			if !strings.HasPrefix(n.label, typ) {
				continue
			}
			if jump, ok := n.data.(pageJump); !ok || m.dir.spaces[jump.space] == "" {
				continue
			}
			m.pages.cur = j
			return n
		}
		m.pages.cur = i
		press(m, tea.KeyLeft)
	}
	return nil
}
