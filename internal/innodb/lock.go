package innodb

import (
	"fmt"
	"strings"
)

// LockStmt is a locking statement as typed into the `l` prompt:
//
//	[rc] share|x|update|delete <predicate>
//
// The predicate is `= v`, `< v`, `<= v`, `> v`, `>= v` or `a..b` on the first
// key column of the index it runs against. share is LOCK IN SHARE MODE and x is
// FOR UPDATE; update stands for an UPDATE of a column no index covers, delete
// for a DELETE. rc runs it under READ COMMITTED instead of REPEATABLE READ.
type LockStmt struct {
	Op string
	RC bool
	// Lo and Hi bound the key; "" leaves that side open. Eq is an exact match,
	// which the server searches for differently from the range Lo == Hi.
	Lo, Hi         string
	LoIncl, HiIncl bool
	Eq             bool
}

const lockUsage = "[rc] share|x|update|delete <= v | a..b | < v | <= v | > v | >= v>"

func ParseLockStmt(text string) (LockStmt, error) {
	var st LockStmt
	f := strings.Fields(text)
	if len(f) > 0 && f[0] == "rc" {
		st.RC = true
		f = f[1:]
	}
	if len(f) == 0 {
		return st, fmt.Errorf("lock: %s", lockUsage)
	}
	switch f[0] {
	case "share", "x", "update", "delete":
		st.Op = f[0]
	default:
		return st, fmt.Errorf("lock: %q is not share, x, update or delete", f[0])
	}
	f = f[1:]
	switch {
	case len(f) == 1 && strings.Contains(f[0], ".."):
		lo, hi, _ := strings.Cut(f[0], "..")
		if lo == "" || hi == "" {
			return st, fmt.Errorf("lock: a range needs both ends: a..b")
		}
		st.Lo, st.Hi, st.LoIncl, st.HiIncl = lo, hi, true, true
	case len(f) == 2 && f[0] == "=":
		st.Lo, st.Hi, st.LoIncl, st.HiIncl, st.Eq = f[1], f[1], true, true, true
	case len(f) == 2 && f[0] == "<":
		st.Hi = f[1]
	case len(f) == 2 && f[0] == "<=":
		st.Hi, st.HiIncl = f[1], true
	case len(f) == 2 && f[0] == ">":
		st.Lo = f[1]
	case len(f) == 2 && f[0] == ">=":
		st.Lo, st.LoIncl = f[1], true
	default:
		return st, fmt.Errorf("lock: cannot read the predicate: %s", lockUsage)
	}
	return st, nil
}

// String is the prompt text the statement parses back from.
func (st LockStmt) String() string {
	s := st.Op + " " + st.predicate(func(v string) string { return v }, "..")
	if st.RC {
		s = "rc " + s
	}
	return s
}

// SQL is the statement st stands for. test/testdata/locks.sh builds the same
// text in shell to record what the server locks for it.
func (st LockStmt) SQL(table, col string) string {
	where := col + " " + st.predicate(sqlLiteral, " AND ")
	if st.Lo != "" && st.Hi != "" && !st.Eq {
		where = col + " BETWEEN " + sqlLiteral(st.Lo) + " AND " + sqlLiteral(st.Hi)
	}
	switch st.Op {
	case "share":
		return "SELECT * FROM " + table + " WHERE " + where + " LOCK IN SHARE MODE"
	case "x":
		return "SELECT * FROM " + table + " WHERE " + where + " FOR UPDATE"
	case "update":
		return "UPDATE " + table + " SET c_tinyint = 0 WHERE " + where
	}
	return "DELETE FROM " + table + " WHERE " + where
}

func (st LockStmt) predicate(lit func(string) string, between string) string {
	switch {
	case st.Eq:
		return "= " + lit(st.Lo)
	case st.Lo != "" && st.Hi != "":
		return lit(st.Lo) + between + lit(st.Hi)
	case st.Lo != "" && st.LoIncl:
		return ">= " + lit(st.Lo)
	case st.Lo != "":
		return "> " + lit(st.Lo)
	case st.HiIncl:
		return "<= " + lit(st.Hi)
	}
	return "< " + lit(st.Hi)
}

// sqlLiteral quotes anything that is not a plain number.
func sqlLiteral(v string) string {
	if v != "" && strings.Trim(v, "0123456789") == "" {
		return v
	}
	return "'" + v + "'"
}

// Lock is one record lock the statement takes, spelled the way
// performance_schema.data_locks would show it.
type Lock struct {
	Index  *IndexDef
	PageNo uint32
	HeapNo uint16
	RecOff int
	Mode   string // X, X,GAP, X,REC_NOT_GAP, or the S forms
	Key    string // first key column of the record; "supremum" for the pseudo-record
	Why    string
}

type lockKind int

const (
	lockOrdinary  lockKind = iota // next-key: the record and the gap before it
	lockGap                       // the gap before the record only
	lockRecNotGap                 // the record only
)

// SimulateLocks lists the record locks st takes when it scans idx, in the order
// InnoDB's row_search_mvcc would take them: one per index record read, plus the
// clustered index record behind every row a secondary index scan returns.
//
// Only an ascending scan on the first key column is modelled, and only the
// rules that scan reaches: an exact match on a unique key locks the record
// alone; under REPEATABLE READ every other record read gets a next-key lock,
// the record that stops an exact-match scan a gap lock, and the supremum of
// each page the scan runs off; a clustered index range bounded above locks the
// record past the bound only as far as the gap before it. READ COMMITTED locks
// the matching records alone. Locks another transaction already holds, and
// the implicit lock a row's own DB_TRX_ID stands for, are not on disk and are
// not modelled; update and delete therefore lock exactly as x does.
func (s *Space) SimulateLocks(t *Table, idx *IndexDef, st LockStmt) ([]Lock, error) {
	if t == nil || len(t.Indexes) == 0 || idx == nil {
		return nil, fmt.Errorf("no index definition to lock with")
	}
	m := &lockSim{s: s, t: t, idx: idx, st: st, clust: t.Indexes[0], base: "X"}
	if st.Op == "share" {
		m.base = "S"
	}
	return m.scan()
}

type lockSim struct {
	s     *Space
	t     *Table
	idx   *IndexDef
	clust *IndexDef
	st    LockStmt
	base  string
	out   []Lock
}

func (m *lockSim) scan() ([]Lock, error) {
	st := m.st
	clustered := m.idx == m.clust
	// row_search_mvcc's unique_search: an exact match on all columns of a
	// unique key, which the prompt can only give for a single-column one.
	uniqueSearch := st.Eq && m.idx.Unique && m.idx.NKey == 1
	_, leaf, pos, err := m.s.descend(m.idx, st.Lo, st.LoIncl)
	if err != nil {
		return nil, err
	}
	// PAGE_CUR_G: the cursor opens past every record equal to the bound.
	if st.Lo != "" && !st.LoIncl {
		for recs := leaf.UserRecs(); pos < len(recs) && compareKey(st.Lo, recKey(recs[pos])) == 0; pos++ {
		}
	}
	ge := st.Lo != "" && st.LoIncl
	first := true
	stopFound := false
	var prev *Rec
	for {
		recs := leaf.UserRecs()
		if pos >= len(recs) {
			if !st.RC {
				// The scan runs off the page. Once the last record read is past
				// the bound InnoDB knows there is nothing more to lock; otherwise
				// the supremum gets a next-key lock, which is the gap after the
				// last record, before it moves to the next page.
				if prev != nil && st.Hi != "" && m.pastHi(prev) {
					return m.out, nil
				}
				m.add(leaf, leaf.Recs[len(leaf.Recs)-1], lockOrdinary,
					"the scan ran off this page: the gap after its last record is locked before moving on")
			}
			if leaf.FIL.Next == FIL_NULL {
				return m.out, nil
			}
			if leaf, err = m.leaf(leaf.FIL.Next); err != nil {
				return m.out, err
			}
			pos = 0
			continue
		}
		r := recs[pos]
		key := recKey(r)
		if st.Eq && compareKey(st.Lo, key) != 0 {
			// ROW_SEL_EXACT: the first record that is not the key ends the
			// search, and the gap before it is where the key would go.
			if !st.RC {
				m.add(leaf, r, lockGap, "not the key: the gap before this record is where the key would be inserted")
			}
			return m.out, nil
		}
		inRange := st.Hi == "" || !m.pastHi(r)
		if st.RC {
			// No gap locks: a delete-marked or out-of-range record is locked and
			// released again before the statement moves on, so it shows nothing.
			if !inRange {
				return m.out, nil
			}
			if !r.Deleted() {
				m.add(leaf, r, lockRecNotGap, "READ COMMITTED locks the record alone: no gap, no phantom protection")
				if err := m.row(r); err != nil {
					return m.out, err
				}
			}
			if uniqueSearch {
				return m.out, nil
			}
			pos++
			prev = r
			continue
		}
		var kind lockKind
		var why string
		switch {
		case uniqueSearch && !r.Deleted():
			kind, why = lockRecNotGap, "an exact match on a unique key: no other row can have it, so the gap needs no lock"
		case clustered && ge && first && m.idx.NKey == 1 && compareKey(st.Lo, key) == 0:
			kind, why = lockRecNotGap, "the first record is the lower bound itself: nothing in range fits in the gap before it"
		case clustered && st.Hi != "":
			// row_compare_row_to_range: the clustered index knows the upper
			// bound, so the record past it is locked only as far as the gap
			// before it, and not at all once the bound itself has been seen.
			c := compareKey(st.Hi, key)
			switch {
			case c < 0 && stopFound:
				return m.out, nil
			case c < 0:
				m.add(leaf, r, lockGap, "past the upper bound: only the gap before this record is still in range")
				return m.out, nil
			case c == 0 && !st.HiIncl:
				stopFound = true
				kind, why = lockGap, "the upper bound is excluded: only the gap before this record is in range"
			case c == 0:
				// Only a whole key can be the last record in range; on a
				// composite key more may share its first column.
				stopFound = m.idx.NKey == 1
				kind, why = lockOrdinary, "next-key: the record and the gap before it; the bound itself, so the scan ends here"
			default:
				kind, why = lockOrdinary, "next-key: the record and the gap before it, so no row can appear in between"
			}
		default:
			kind, why = lockOrdinary, "next-key: the record and the gap before it, so no row can appear in between"
		}
		if r.Deleted() {
			why += "; delete-marked, so it is locked as read and then skipped"
		}
		m.add(leaf, r, kind, why)
		first = false
		prev = r
		if r.Deleted() {
			// A unique search on the clustered index knows no other record can
			// match; anywhere else the scan carries on to the next record.
			if clustered && uniqueSearch {
				return m.out, nil
			}
			pos++
			continue
		}
		if !inRange {
			return m.out, nil
		}
		if err := m.row(r); err != nil {
			return m.out, err
		}
		if uniqueSearch {
			return m.out, nil
		}
		pos++
	}
}

// pastHi says whether the record is beyond the upper bound.
func (m *lockSim) pastHi(r *Rec) bool {
	c := compareKey(m.st.Hi, recKey(r))
	return c < 0 || (c == 0 && !m.st.HiIncl)
}

// row is what happens once a record is returned as a row: a secondary index
// scan reads the clustered index record it points at and locks that too. An
// UPDATE or DELETE then modifies the row under the lock it already holds; the
// secondary index entries it delete-marks get only an implicit lock, so no
// further explicit lock appears.
func (m *lockSim) row(r *Rec) error {
	if m.idx == m.clust {
		return nil
	}
	key := fieldValue(r, m.clust.Cols[0].Name)
	_, cl, pos, err := m.s.descend(m.clust, key, false)
	if err != nil {
		return err
	}
	recs := cl.UserRecs()
	if pos >= len(recs) || compareKey(key, recKey(recs[pos])) != 0 {
		return fmt.Errorf("%s has no record with %s = %s, which %s points at", m.clust.Name, m.clust.Cols[0].Name, key, m.idx.Name)
	}
	m.add(cl, recs[pos], lockRecNotGap, "the row behind the secondary index record: a secondary index scan locks it too")
	return nil
}

func (m *lockSim) leaf(no uint32) (*IndexPage, error) {
	p, err := m.s.Page(no)
	if err != nil {
		return nil, err
	}
	ip, err := p.ParseIndex(m.idx)
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", no, err)
	}
	return ip, nil
}

func (m *lockSim) add(ip *IndexPage, r *Rec, kind lockKind, why string) {
	mode := m.base
	switch kind {
	case lockGap:
		mode += ",GAP"
	case lockRecNotGap:
		mode += ",REC_NOT_GAP"
	}
	key := recKey(r)
	if r.Status == REC_STATUS_SUPREMUM {
		key = "supremum"
	}
	m.out = append(m.out, Lock{Index: ip.Idx, PageNo: ip.No, HeapNo: r.HeapNo, RecOff: r.Off,
		Mode: mode, Key: key, Why: why})
}

// fieldValue is the decoded value of the named column of a record, unquoted.
func fieldValue(r *Rec, name string) string {
	for _, f := range r.Fields {
		if f.Name == name {
			return strings.Trim(f.Value, `"`)
		}
	}
	return ""
}
