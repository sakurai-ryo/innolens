package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// lockSet is what the last `l` worked out: the statement, the B+tree it
// scanned, and the record locks it would take. It stays in force until the
// next `l` or another table is opened, so that every page opened meanwhile
// shows the locks that fell on it.
type lockSet struct {
	stmt  innodb.LockStmt
	locks []innodb.Lock
}

func (ls *lockSet) String() string {
	if ls == nil {
		return ""
	}
	return "locks: " + ls.stmt.String()
}

// on is the locks that fell on one page. Page numbers are unique within the
// tablespace, so the index needs no checking.
func (ls *lockSet) on(no uint32) []innodb.Lock {
	var out []innodb.Lock
	if ls == nil {
		return nil
	}
	for _, l := range ls.locks {
		if l.PageNo == no {
			out = append(out, l)
		}
	}
	return out
}

// lockWizard is the `l` picker: the statement is put together one choice at a
// time in a popup, and only the key values are typed.
type lockWizard struct {
	picker
	step int
	st   innodb.LockStmt
	ix   *innodb.IndexDef
	cmp  string // the comparison picked: =, <, <=, >, >=, between
}

// lockChoice is one option row: Enter passes value to the step it belongs to.
type lockChoice struct {
	step  int
	value string
}

const (
	lockStepIso = iota
	lockStepOp
	lockStepIndex
	lockStepCmp
	lockStepValue
)

// startLock opens the picker over the page tree.
func (m *Model) startLock() {
	if m.space == nil || len(m.defs) == 0 {
		m.status.err = "no index to lock: this tablespace has no table definition"
		return
	}
	m.lockWiz = &lockWizard{ix: m.currentIndex()}
	m.lockStep()
}

// lockStep draws the options of the current step, with the choices made so
// far in the title.
func (m *Model) lockStep() {
	w := m.lockWiz
	root := &node{}
	add := func(label, note, value string) {
		root.children = append(root.children, &node{label: label, note: note, hkey: "lock statement",
			data: lockChoice{w.step, value}})
	}
	cur := 0
	switch w.step {
	case lockStepIso:
		add("REPEATABLE READ", "gap and next-key locks: what is read cannot gain rows either", "rr")
		add("READ COMMITTED", "record locks alone: only the rows read are held", "rc")
		if m.locks != nil {
			add("clear the locks shown", "take the marks off every page", "clear")
		}
	case lockStepOp:
		add("SELECT ... LOCK IN SHARE MODE", "S locks: others may read, none may write", "share")
		add("SELECT ... FOR UPDATE", "X locks: no one else may read for update or write", "x")
		add("UPDATE ... SET (a column no index covers)", "X locks, taken while the rows are read", "update")
		add("DELETE", "X locks, taken while the rows are read", "delete")
	case lockStepIndex:
		for i, ix := range w.ix.Table.Indexes {
			if ix == w.ix {
				cur = i
			}
			add(ix.Name+"  ("+ix.Cols[0].Name+")", "scan this B+tree; the predicate is on its first column", ix.Name)
		}
	case lockStepCmp:
		add("= key", "an exact match", "=")
		add("< key", "everything before the key", "<")
		add("<= key", "everything up to and including the key", "<=")
		add("> key", "everything after the key", ">")
		add(">= key", "the key and everything after it", ">=")
		add("between two keys", "a closed range, BETWEEN a AND b", "between")
	}
	w.list = newList(root)
	w.list.cur = cur
	w.title = "LOCK  " + w.progress()
}

// progress is the statement as far as it has been chosen.
func (w *lockWizard) progress() string {
	var parts []string
	if w.step > lockStepIso {
		if w.st.RC {
			parts = append(parts, "READ COMMITTED")
		} else {
			parts = append(parts, "REPEATABLE READ")
		}
	}
	if w.step > lockStepOp {
		parts = append(parts, w.st.Op)
	}
	if w.step > lockStepIndex {
		parts = append(parts, w.ix.Name+" ("+w.ix.Cols[0].Name+")")
	}
	if w.step > lockStepCmp {
		parts = append(parts, w.cmp)
	}
	if len(parts) == 0 {
		return "choose the isolation level"
	}
	return strings.Join(parts, " · ")
}

// lockPick takes the choice made on the current step and moves to the next.
func (m *Model) lockPick(c lockChoice) {
	w := m.lockWiz
	if w == nil || c.step != w.step {
		return
	}
	switch c.step {
	case lockStepIso:
		if c.value == "clear" {
			m.locks, m.lockWiz, m.status.locks = nil, nil, ""
			m.status.notice = "locks cleared"
			return
		}
		w.st.RC = c.value == "rc"
	case lockStepOp:
		w.st.Op = c.value
	case lockStepIndex:
		for _, ix := range w.ix.Table.Indexes {
			if ix.Name == c.value {
				w.ix = ix
			}
		}
	case lockStepCmp:
		w.cmp = c.value
	}
	w.step++
	if w.step == lockStepValue {
		m.prompt = prompt{kind: promptLock}
		w.title = "LOCK  " + w.progress()
		return
	}
	m.lockStep()
}

// lockBack is esc: one step back, or out of the picker from the first.
func (m *Model) lockBack() {
	w := m.lockWiz
	if w.step == lockStepIso {
		m.lockWiz = nil
		return
	}
	if w.step == lockStepValue && w.st.Lo != "" && w.cmp == "between" {
		// The lower bound was typed: ask for it again rather than for the comparison.
		w.st.Lo = ""
		m.prompt = prompt{kind: promptLock}
		return
	}
	w.step--
	w.st.Lo, w.st.Hi = "", ""
	m.lockStep()
}

// lockValueLabel is what the value prompt asks for.
func (w *lockWizard) lockValueLabel() string {
	switch {
	case w.cmp != "between":
		return "key"
	case w.st.Lo == "":
		return "lower bound"
	}
	return "upper bound"
}

// lockValue takes a typed key. A BETWEEN needs two; the statement runs once it
// has what the comparison needs.
func (m *Model) lockValue(text string) {
	w := m.lockWiz
	text = strings.TrimSpace(text)
	if text == "" {
		m.status.err = "a key is needed"
		m.prompt = prompt{kind: promptLock}
		return
	}
	st := &w.st
	switch w.cmp {
	case "=":
		st.Lo, st.Hi, st.LoIncl, st.HiIncl, st.Eq = text, text, true, true, true
	case "<":
		st.Hi = text
	case "<=":
		st.Hi, st.HiIncl = text, true
	case ">":
		st.Lo = text
	case ">=":
		st.Lo, st.LoIncl = text, true
	case "between":
		if st.Lo == "" {
			st.Lo, st.LoIncl = text, true
			m.prompt = prompt{kind: promptLock}
			return
		}
		st.Hi, st.HiIncl = text, true
	}
	m.lockWiz = nil
	m.runLock(*st, w.ix)
}

// runLock simulates the statement on the index and replaces the page tree with
// the pages it locked, one row each.
func (m *Model) runLock(st innodb.LockStmt, ix *innodb.IndexDef) {
	locks, err := m.space.SimulateLocks(ix.Table, ix, st)
	m.locks = &lockSet{stmt: st, locks: locks}
	root := &node{}
	type pageKey struct {
		ix *innodb.IndexDef
		no uint32
	}
	var order []pageKey
	byPage := map[pageKey][]innodb.Lock{}
	for _, l := range locks {
		k := pageKey{l.Index, l.PageNo}
		if _, ok := byPage[k]; !ok {
			order = append(order, k)
		}
		byPage[k] = append(byPage[k], l)
	}
	for _, k := range order {
		root.children = append(root.children, lockPageNode(k.ix, k.no, byPage[k]))
	}
	if err != nil {
		root.children = append(root.children, errNode(err.Error()))
	}
	if len(locks) == 0 && err == nil {
		root.children = append(root.children, &node{label: "no record lock", hkey: "locks",
			note: "the scan read nothing it had to keep locked"})
	}
	m.pages = newList(root)
	m.pagesTitle = fmt.Sprintf("LOCKS %s IN %s", st, ix.Name)
	m.focus = focusPages
	m.status.locks = m.locks.String()
	iso := "REPEATABLE READ"
	if st.RC {
		iso = "READ COMMITTED"
	}
	m.status.info = st.SQL(ix.Table.Name, ix.Cols[0].Name) + "  under " + iso
}

// lockPageNode is one page the statement locked something on. Enter opens it
// with the first locked record selected.
func lockPageNode(ix *innodb.IndexDef, no uint32, locks []innodb.Lock) *node {
	counts := map[string]int{}
	var modes []string
	for _, l := range locks {
		if counts[l.Mode] == 0 {
			modes = append(modes, l.Mode)
		}
		counts[l.Mode]++
	}
	sort.Strings(modes)
	parts := make([]string, len(modes))
	for i, mode := range modes {
		parts[i] = fmt.Sprintf("%d × %s", counts[mode], mode)
	}
	tag := indexTag(0)
	return &node{label: fmt.Sprintf("page %d  %s  %s  locks %d", no, tag, ix.Name, len(locks)),
		icon: ic.page, tag: tag, color: pageColor(innodb.FIL_PAGE_INDEX, 0), hkey: "locks",
		note: strings.Join(parts, "  "), data: recRef{no: no, off: locks[0].RecOff}}
}

// addLocks marks every record of the page the statement locked and adds a
// `locks` section after the records, one row per lock, that selects the record
// on Enter.
func (m *Model) addLocks(tree *node, p *innodb.Page) {
	if m.locks == nil || m.space == nil || m.pageSpace != m.space.ID {
		return
	}
	locks := m.locks.on(p.No)
	if len(locks) == 0 {
		return
	}
	at := -1
	byOff := map[int]*node{}
	for i, sec := range tree.children {
		if sec.label != "Records" {
			continue
		}
		at = i
		for _, rec := range sec.children {
			var off int
			if _, err := fmt.Sscanf(rec.label, "record @%x", &off); err == nil {
				byOff[off] = rec
			}
		}
	}
	if at < 0 {
		return
	}
	sect := &node{label: "locks", hkey: "locks"}
	for _, l := range locks {
		row := &node{label: l.Mode, tag: l.Mode, color: lockColor(l.Mode), icon: ic.lock, hkey: "lock",
			value: fmt.Sprintf("heap_no %d  %s", l.HeapNo, lockCovers(l)), note: l.Why}
		if rec := byOff[l.RecOff]; rec != nil {
			row.data, row.off, row.hasOff, row.size = rec.data, rec.off, rec.hasOff, rec.size
			// The mode alone: the pane is narrow, and the locks section has the rest.
			rec.note = strings.TrimSpace(rec.note + "  " + ic.lock + " " + l.Mode)
		}
		sect.children = append(sect.children, row)
	}
	tree.children = append(tree.children[:at+1], append([]*node{sect}, tree.children[at+1:]...)...)
}

// lockCovers says what part of the key space the lock holds, in the words the
// mode implies.
func lockCovers(l innodb.Lock) string {
	switch {
	case l.Key == "supremum":
		return "the gap after the last record"
	case strings.HasSuffix(l.Mode, ",GAP"):
		return "the gap before key " + l.Key
	case strings.HasSuffix(l.Mode, ",REC_NOT_GAP"):
		return "key " + l.Key + " only"
	}
	return "key " + l.Key + " and the gap before it"
}

func lockColor(mode string) lipgloss.Color {
	if strings.HasPrefix(mode, "S") {
		return colIndex
	}
	return colDanger
}
