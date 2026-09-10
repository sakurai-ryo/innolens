package innodb

import (
	"encoding/hex"
	"strings"
	"testing"
)

var versions = []string{"80", "84"}

func openTable(t *testing.T, ver, name string) (*Space, *Table) {
	t.Helper()
	s, err := Open("../../test/testdata/" + ver + "/innolens/" + name + ".ibd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	tbl, err := s.ReadTable()
	if err != nil {
		t.Fatal(err)
	}
	return s, tbl
}

func fields(r *Rec) map[string]string {
	m := map[string]string{}
	for _, f := range r.Fields {
		m[f.Name] = f.Value
	}
	return m
}

// leaves walks the B+tree and returns every leaf page in key order.
func leaves(t *testing.T, s *Space, idx *IndexDef) []*IndexPage {
	t.Helper()
	var out []*IndexPage
	var walk func(no uint32)
	walk = func(no uint32) {
		p, err := s.Page(no)
		if err != nil {
			t.Fatal(err)
		}
		ip, err := p.ParseIndex(idx)
		if err != nil {
			t.Fatalf("page %d: %v", no, err)
		}
		if ip.Hdr.IndexID != idx.ID {
			t.Fatalf("page %d: index id %d, want %d", no, ip.Hdr.IndexID, idx.ID)
		}
		if ip.Hdr.Level == 0 {
			out = append(out, ip)
			return
		}
		for _, c := range ip.Children() {
			walk(c)
		}
	}
	walk(idx.RootPage)
	return out
}

func TestTypesTable(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "types")
			if tbl.Name != "types" || tbl.Schema != "innolens" || len(tbl.Indexes) != 2 {
				t.Fatalf("table %+v", tbl)
			}
			for no := uint32(0); no < s.NPages; no++ {
				p, err := s.Page(no)
				if err != nil {
					t.Fatal(err)
				}
				if !p.ChecksumOK {
					t.Errorf("page %d: checksum mismatch", no)
				}
				if p.FIL.Offset != no && !isZero(p.Data) {
					t.Errorf("page %d: FIL_PAGE_OFFSET %d", no, p.FIL.Offset)
				}
				if _, err := p.Annotate(tbl.Index(p.indexHeader().IndexID)); err != nil {
					t.Errorf("page %d: annotate: %v", no, err)
				}
			}

			pk := tbl.Indexes[0]
			root, _ := s.Page(pk.RootPage)
			rip, err := root.ParseIndex(pk)
			if err != nil || rip.Hdr.Level != 1 {
				t.Fatalf("root level %d err %v", rip.Hdr.Level, err)
			}
			nrecs, deleted, free := 0, 0, 0
			var first, tenth *Rec
			for _, lp := range leaves(t, s, pk) {
				nrecs += int(lp.Hdr.NRecs)
				free += len(lp.Free)
				for _, r := range lp.UserRecs() {
					if r.Deleted() {
						deleted++
					}
					switch fields(r)["id"] {
					case "1":
						first = r
					case "10":
						tenth = r
					}
				}
			}
			// 2000 rows: id%50==0 purged onto PAGE_FREE, id%50==25 delete-marked.
			// Page splits also leave moved records on PAGE_FREE, so free is a lower bound.
			if nrecs != 1960 || deleted != 40 || free < 40 {
				t.Errorf("nrecs %d deleted %d free %d", nrecs, deleted, free)
			}
			want := map[string]string{
				"id": "1", "c_tinyint": "-63", "c_smallint": "-2997", "c_mediumint": "100", "c_int": "-999000",
				"c_bigint": "100000", "c_char": `"c1      "`, "c_varchar": `"varchar-1-x"`, "c_latin1": `"café1"`,
				"c_ascii": `"ascii1"`, "c_binary": "c4ca4238a0b923820dcc509a6f75849b", "c_sjis": "varchar(16): 93fa967b8cea31",
				"c_datetime": "2026-01-01 00:01:00", "c_timestamp": "2026-01-01 00:00:01 UTC", "c_date": "2026-01-02",
				"c_decimal": "0.14", "c_float": "float: abaaaa3e",
			}
			got := fields(first)
			for k, v := range want {
				if got[k] != v {
					t.Errorf("row 1 %s = %q, want %q", k, got[k], v)
				}
			}
			if !strings.HasPrefix(got["c_text"], "extern:") || !strings.Contains(got["c_text"], "len 20000") {
				t.Errorf("row 1 c_text = %q", got["c_text"])
			}
			if fields(tenth)["c_timestamp"] != "NULL" {
				t.Errorf("row 10 c_timestamp = %q", fields(tenth)["c_timestamp"])
			}

			sec := tbl.Indexes[1]
			if sec.Name != "idx_varchar" || sec.NUniqueInTree != 2 {
				t.Fatalf("secondary %+v", sec)
			}
			var n int
			for _, lp := range leaves(t, s, sec) {
				for _, r := range lp.UserRecs() {
					n++
					if f := fields(r); f["c_varchar"] == `"varchar-1-x"` && f["id"] != "1" {
						t.Errorf("secondary row: %v", f)
					}
				}
			}
			if n != 1960 {
				t.Errorf("secondary rows %d", n)
			}
		})
	}
}

func TestInstantTable(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, tbl := openTable(t, ver, "instant")
			pk := tbl.Indexes[0]
			var names []string
			for _, c := range pk.Cols {
				names = append(names, c.Name)
			}
			if got := strings.Join(names, ","); got != "id,DB_TRX_ID,DB_ROLL_PTR,!hidden!_dropped_v2_p3_a,b,c" {
				t.Fatalf("physical order %s", got)
			}
			rows := map[string]map[string]string{}
			vers := map[string]uint8{}
			for _, lp := range leaves(t, s, pk) {
				for _, r := range lp.UserRecs() {
					rows[fields(r)["id"]] = fields(r)
					vers[fields(r)["id"]] = r.Version
				}
			}
			check := func(id, col, want string) {
				if got := rows[id][col]; got != want {
					t.Errorf("id %s %s = %q, want %q", id, col, got, want)
				}
			}
			check("1", "!hidden!_dropped_v2_p3_a", "1")
			check("1", "b", `"v0"`)
			check("1", "c", `"42" (instant default)`)
			check("3", "!hidden!_dropped_v2_p3_a", "3")
			check("3", "c", "3")
			check("4", "b", `"v2"`)
			check("4", "c", "4")
			if _, ok := rows["4"]["!hidden!_dropped_v2_p3_a"]; ok {
				t.Errorf("id 4 still has the dropped column: %v", rows["4"])
			}
			if vers["1"] != 0 || vers["3"] != 1 || vers["4"] != 2 {
				t.Errorf("versions %v", vers)
			}
		})
	}
}

func TestUnsupportedAndErrors(t *testing.T) {
	if err := checkFlags(1 << fspFlagsPosEncryption); err == nil {
		t.Error("encryption flag accepted")
	}
	if err := checkFlags(3 << fspFlagsPosZipSsize); err == nil {
		t.Error("zip ssize accepted")
	}
	if _, err := Open("../../test/testdata/80/innolens/redo_new.ibd"); err != nil {
		t.Errorf("redo_new.ibd (phase-2 kill, only page 0 written): %v", err)
	}
}

func TestDecoders(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{decDecimal(10, 2)([]byte{0x80, 0x00, 0x00, 0x00, 0x00}, nil), "0.00"},
		{decDecimal(10, 2)([]byte{0x80, 0x00, 0x04, 0xd2, 0x2d}, nil), "1234.45"},
		{decDecimal(10, 2)([]byte{0x7f, 0xff, 0xfb, 0x2d, 0xd2}, nil), "-1234.45"},
		{decSint([]byte{0x80, 0x00, 0x00, 0x00}, nil), "0"},
		{decSint([]byte{0x7f, 0xff, 0xff, 0xff}, nil), "-1"},
		{decSint([]byte{0x7f}, nil), "-1"},
		{decUint([]byte{0xff, 0xff}, nil), "65535"},
		{decDate([]byte{0x8f, 0xd4, 0x21}, nil), "2026-01-01"},
		{decDatetime(3)([]byte{0x99, 0xb8, 0xc2, 0x00, 0x00, 0x04, 0xce}, nil), "2026-01-01 00:00:00.123"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

// leafPage walks down the PRIMARY tree of the types fixture to a leaf.
func leafPage(t *testing.T, ver string) *IndexPage {
	t.Helper()
	s, tb := openTable(t, ver, "types")
	ix := tb.Indexes[0]
	for no := ix.RootPage; ; {
		p, err := s.Page(no)
		if err != nil {
			t.Fatal(err)
		}
		ip, err := p.ParseIndex(ix)
		if err != nil {
			t.Fatal(err)
		}
		if ip.Hdr.Level == 0 {
			return ip
		}
		no = ip.Children()[0]
	}
}

// TestRecordLayout checks the prefix positions parseRec records: a null bit has
// to sit where the bitmap says, and a length has to be readable from the bytes
// the field points at.
func TestRecordLayout(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			ip := leafPage(t, ver)
			for _, rec := range ip.UserRecs() {
				lo, hi := rec.Off-rec.Extra, rec.Off-REC_N_NEW_EXTRA_BYTES
				for _, f := range rec.Fields {
					if f.NullOff != 0 {
						if f.NullOff < lo || f.NullOff >= hi {
							t.Errorf("%s: null byte @%d outside the prefix [%d,%d)", f.Name, f.NullOff, lo, hi)
						}
						if got := rec.Buf[f.NullOff]&(1<<f.NullBit) != 0; got != f.Null {
							t.Errorf("%s: null bit %d of %#02x says %v, field says %v", f.Name, f.NullBit, rec.Buf[f.NullOff], got, f.Null)
						}
					}
					if f.Var && !f.Null && f.LenBytes == 0 {
						t.Errorf("%s: variable-length column stores no length", f.Name)
					}
					if f.LenBytes == 0 {
						continue
					}
					if f.LenOff < lo || f.LenOff+f.LenBytes > hi {
						t.Errorf("%s: length bytes @%d outside the prefix [%d,%d)", f.Name, f.LenOff, lo, hi)
					}
					l := int(rec.Buf[f.LenOff])
					if f.LenBytes == 2 {
						l = (int(rec.Buf[f.LenOff+1])&0x3F)<<8 | l
					}
					if l != f.Len {
						t.Errorf("%s: length bytes say %d, field is %d bytes", f.Name, l, f.Len)
					}
				}
			}
		})
	}
}

// TestDecodeSteps pins the steps to the decoding: the last step is the value the
// decoder returns, so the explanation cannot drift from what it explains.
func TestDecodeSteps(t *testing.T) {
	cases := []struct {
		name string
		dec  func([]byte, *[]Step) string
		b    []byte
	}{
		{"signed int", decSint, []byte{0x80, 0x00, 0x00, 0x01}},
		{"unsigned int", decUint, []byte{0x00, 0x00, 0x00, 0x02}},
		{"date", decDate, []byte{0x8f, 0xd4, 0x21}},
		{"datetime", decDatetime(3), []byte{0x99, 0xb8, 0xc2, 0x00, 0x00, 0x04, 0xce}},
		{"timestamp", decTimestamp(0), []byte{0x69, 0x55, 0xb9, 0x01}},
		{"decimal", decDecimal(10, 2), []byte{0x80, 0x00, 0x04, 0xd2, 0x2d}},
	}
	for _, c := range cases {
		var steps []Step
		want := c.dec(c.b, &steps)
		if len(steps) < 2 {
			t.Errorf("%s: recorded %d steps", c.name, len(steps))
			continue
		}
		if steps[0].Name != "raw" || steps[0].Value != hex.EncodeToString(c.b) {
			t.Errorf("%s: first step = %+v, want the raw bytes", c.name, steps[0])
		}
		if last := steps[len(steps)-1]; last.Name != "value" || last.Value != want {
			t.Errorf("%s: last step = %+v, want value %q", c.name, last, want)
		}
		if got := c.dec(c.b, nil); got != want {
			t.Errorf("%s: decoding without steps returned %q, want %q", c.name, got, want)
		}
	}
}

// TestPageUsage checks the 16KB budget adds up on every index page of the
// fixtures. That is what pins the PAGE_HEAP_TOP and page directory arithmetic:
// anything left over would land in "free" and show up as a wrong total.
func TestPageUsage(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			s, _ := openTable(t, ver, "types")
			for no := uint32(0); no < s.NPages; no++ {
				p, err := s.Page(no)
				if err != nil {
					t.Fatal(err)
				}
				if !p.IsIndex() {
					continue
				}
				ip, err := p.ParseIndex(nil)
				if err != nil {
					continue
				}
				total := 0
				for _, c := range ip.usage().Children {
					if c.Size() < 0 {
						t.Errorf("page %d: %s is %d bytes", no, c.Name, c.Size())
					}
					total += c.Size()
				}
				if total != PageSize {
					t.Errorf("page %d: usage totals %d bytes, want %d", no, total, PageSize)
				}
			}
		})
	}
}
