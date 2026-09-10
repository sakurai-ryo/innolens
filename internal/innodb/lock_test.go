package innodb

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestParseLockStmt round-trips the prompt grammar and the SQL it stands for.
func TestParseLockStmt(t *testing.T) {
	for _, c := range []struct{ in, sql string }{
		{"x = 100", "SELECT * FROM t WHERE id = 100 FOR UPDATE"},
		{"share = abc", "SELECT * FROM t WHERE id = 'abc' LOCK IN SHARE MODE"},
		{"rc update 10..13", "UPDATE t SET c_tinyint = 0 WHERE id BETWEEN 10 AND 13"},
		{"delete < 3", "DELETE FROM t WHERE id < 3"},
		{"x >= 1997", "SELECT * FROM t WHERE id >= 1997 FOR UPDATE"},
		{"x <= 3", "SELECT * FROM t WHERE id <= 3 FOR UPDATE"},
		{"rc x > v-1", "SELECT * FROM t WHERE id > 'v-1' FOR UPDATE"},
	} {
		st, err := ParseLockStmt(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if st.String() != c.in {
			t.Errorf("%q parses to %q", c.in, st)
		}
		if got := st.SQL("t", "id"); got != c.sql {
			t.Errorf("%q: SQL = %q, want %q", c.in, got, c.sql)
		}
	}
	for _, bad := range []string{"", "rc", "lock = 1", "x", "x = ", "x 1..", "x == 1", "x between 1 2"} {
		if _, err := ParseLockStmt(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// lockCase is one recording made by test/testdata/locks.sh: the prompt, the
// SQL it ran, and the record locks performance_schema.data_locks showed.
type lockCase struct {
	index, prompt, sql string
	locks              map[string]string // "index page heap mode" -> LOCK_DATA
}

func readLockCase(t *testing.T, path string) lockCase {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := lockCase{locks: map[string]string{}}
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan(); n++ {
		line := sc.Text()
		cols := strings.Split(line, "\t")
		switch {
		case n == 0:
			c.index, c.prompt = strings.TrimPrefix(cols[0], "# "), cols[1]
		case n == 1:
			c.sql = strings.TrimPrefix(line, "# ")
		case cols[0] == "RECORD":
			c.locks[strings.Join([]string{cols[1], cols[4], cols[5], cols[2]}, " ")] = cols[6]
		}
	}
	return c
}

// TestSimulateLocks checks the simulator against what a real server locked for
// the same statement on the same table. The recording also names the key at
// every locked heap_no, which is how a fixture that no longer matches the
// recorded table is told apart from a wrong rule.
func TestSimulateLocks(t *testing.T) {
	for _, ver := range versions {
		files, _ := filepath.Glob(filepath.Join("..", "..", "test", "testdata", ver, "locks", "*.tsv"))
		if len(files) == 0 {
			t.Fatalf("%s: no lock recordings; run test/testdata/locks.sh", ver)
		}
		s, tbl := openTable(t, ver, "types")
		for _, path := range files {
			c := readLockCase(t, path)
			t.Run(ver+"/"+strings.TrimSuffix(filepath.Base(path), ".tsv"), func(t *testing.T) {
				st, err := ParseLockStmt(c.prompt)
				if err != nil {
					t.Fatal(err)
				}
				var idx *IndexDef
				for _, ix := range tbl.Indexes {
					if ix.Name == c.index {
						idx = ix
					}
				}
				if idx == nil {
					t.Fatalf("no index %s", c.index)
				}
				if got := st.SQL("types", idx.Cols[0].Name); got != c.sql {
					t.Errorf("the recording ran %q, the prompt stands for %q", c.sql, got)
				}
				locks, err := s.SimulateLocks(tbl, idx, st)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, l := range locks {
					got[fmt.Sprintf("%s %d %d %s", l.Index.Name, l.PageNo, l.HeapNo, l.Mode)] = l.Key
				}
				for k, data := range c.locks {
					key, ok := got[k]
					if !ok {
						t.Errorf("missing %s (%s)", k, data)
						continue
					}
					// LOCK_DATA is the key columns, quoted and comma separated.
					want := strings.Trim(strings.SplitN(data, ", ", 2)[0], "'")
					if data == "supremum pseudo-record" {
						want = "supremum"
					}
					if key != want {
						t.Errorf("%s: the fixture has key %s there, the server locked %s", k, key, want)
					}
				}
				var extra []string
				for k := range got {
					if _, ok := c.locks[k]; !ok {
						extra = append(extra, k+" ("+got[k]+")")
					}
				}
				sort.Strings(extra)
				for _, e := range extra {
					t.Errorf("extra %s", e)
				}
			})
		}
	}
}
