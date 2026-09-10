package innodb

import (
	"path/filepath"
	"strings"
	"testing"
)

func openFixtureRedo(t *testing.T, ver string) (*Redo, *LogStream) {
	t.Helper()
	r, err := OpenRedo(filepath.Join("..", "..", "test", "testdata", ver, "#innodb_redo"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s, err := r.Scan()
	if err != nil {
		t.Fatal(err)
	}
	return r, s
}

// TestRedoScan checks the file headers, the checkpoint choice and that every
// record in the valid range decodes: a wrong body length desynchronises the
// stream immediately, so a clean walk to the end is the real assertion here.
func TestRedoScan(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			r, s := openFixtureRedo(t, ver)

			for _, f := range r.Files {
				if !f.HdrChecksumOK {
					t.Errorf("%s: file header checksum mismatch", f.Name)
				}
				if f.Format < logFormatMin {
					t.Errorf("%s: format %d", f.Name, f.Format)
				}
				if !strings.HasPrefix(f.Creator, "MySQL ") {
					t.Errorf("%s: creator %q", f.Name, f.Creator)
				}
			}
			if r.CheckpointFile == nil || !r.CheckpointFile.contains(r.CheckpointLSN) {
				t.Fatalf("checkpoint lsn %d is not inside %v", r.CheckpointLSN, r.CheckpointFile)
			}
			// The fixture is a killed server, so the log ends mid-block.
			if !strings.Contains(s.Stop, "partial") {
				t.Errorf("scan stopped with %q, expected a partial last block", s.Stop)
			}
			if s.StartLSN > r.CheckpointLSN || s.EndLSN <= r.CheckpointLSN {
				t.Fatalf("stream lsn %d..%d does not cover checkpoint %d", s.StartLSN, s.EndLSN, r.CheckpointLSN)
			}
			if s.LSN(0) != s.StartLSN {
				t.Errorf("LSN(0) = %d, want %d", s.LSN(0), s.StartLSN)
			}

			mtrs := s.MTRs()
			seen := map[string]int{}
			for _, m := range mtrs {
				if m.Err != nil {
					t.Fatalf("mtr at lsn %d (offset %d): %v", m.StartLSN, m.Off, m.Err)
				}
				last := m.Recs[len(m.Recs)-1]
				if len(m.Recs) > 1 && last.Type != MLOG_MULTI_REC_END {
					t.Fatalf("mtr at lsn %d ends with %s", m.StartLSN, last.TypeName())
				}
				for _, rec := range m.Recs {
					seen[rec.TypeName()]++
					if rec.LSN < s.StartLSN || rec.LSN >= s.EndLSN {
						t.Fatalf("record lsn %d outside %d..%d", rec.LSN, s.StartLSN, s.EndLSN)
					}
				}
			}
			// The DML the fixture runs after the ibd snapshot (test/testdata/gen.sh).
			for _, want := range []string{
				"MLOG_REC_INSERT", "MLOG_REC_UPDATE_IN_PLACE", "MLOG_REC_DELETE",
				"MLOG_COMP_PAGE_CREATE", "MLOG_WRITE_STRING", "MLOG_MULTI_REC_END",
				"MLOG_1BYTE", "MLOG_2BYTES", "MLOG_4BYTES", "MLOG_8BYTES",
			} {
				if seen[want] == 0 {
					t.Errorf("no %s in the redo stream: %v", want, seen)
				}
			}
		})
	}
}

// TestRedoAnnotate checks that the field tree of a record stays inside it.
func TestRedoAnnotate(t *testing.T) {
	_, s := openFixtureRedo(t, "84")
	for _, m := range s.MTRs() {
		for _, rec := range m.Recs {
			n := rec.Annotate(s.Buf)
			if len(n.Children) == 0 || n.Children[0].Name != "type" {
				t.Fatalf("%s: first field is %v", rec.TypeName(), n.Children)
			}
			checkRange(t, rec, n)
		}
	}
}

func checkRange(t *testing.T, rec *RedoRec, n *Node) {
	t.Helper()
	if n.Len > 0 && (n.Off < 0 || n.Off+n.Len > rec.Len) {
		t.Fatalf("%s: field %q at %d+%d is outside the %d byte record",
			rec.TypeName(), n.Name, n.Off, n.Len, rec.Len)
	}
	for _, c := range n.Children {
		checkRange(t, rec, c)
	}
}

// TestRedoRecordImage rebuilds the row that the fixture inserts only into the
// redo log and decodes its columns through the table's SDI.
func TestRedoRecordImage(t *testing.T) {
	for _, ver := range []string{"80", "84"} {
		t.Run(ver, func(t *testing.T) {
			_, s := openFixtureRedo(t, ver)
			sp, err := Open(filepath.Join("..", "..", "test", "testdata", ver, "innolens", "types.ibd"))
			if err != nil {
				t.Fatal(err)
			}
			defer sp.Close()
			tbl, err := sp.ReadTable()
			if err != nil {
				t.Fatal(err)
			}

			want := map[string]string{
				"redo-insert":   "2001",
				"redo-rollback": "2002",
			}
			got, sec := map[string]string{}, map[string]int{}
			for _, m := range s.MTRs() {
				for _, rec := range m.Recs {
					if rec.Ins == nil || rec.SpaceID != sp.ID {
						continue
					}
					img := decodeImage(t, s, sp, tbl, rec)
					if img == nil {
						continue
					}
					fields := map[string]string{}
					for _, f := range img.Fields {
						fields[f.Name] = f.Value
					}
					for k := range want {
						if fields["c_varchar"] != `"`+k+`"` {
							continue
						}
						if _, clustered := fields["DB_TRX_ID"]; !clustered {
							sec[k]++ // idx_varchar carries only (c_varchar, id)
							continue
						}
						got[k] = fields["id"]
						if fields["c_tinyint"] != "NULL" || fields["DB_ROLL_PTR"] == "" {
							t.Errorf("%s decoded as %v", k, fields)
						}
					}
				}
			}
			for k, id := range want {
				if got[k] != id {
					t.Errorf("row %s: clustered id = %q, want %q", k, got[k], id)
				}
				if sec[k] == 0 {
					t.Errorf("row %s: no secondary index image decoded", k)
				}
			}
		})
	}
}

func decodeImage(t *testing.T, s *LogStream, sp *Space, tbl *Table, rec *RedoRec) *Rec {
	t.Helper()
	p, err := sp.Page(rec.PageNo)
	if err != nil {
		return nil
	}
	ip, err := p.ParseIndex(nil)
	if err != nil {
		return nil
	}
	idx := tbl.Index(ip.Hdr.IndexID)
	if idx == nil {
		return nil
	}
	// Records written before the .ibd snapshot may no longer match the record
	// their prefix is shared with, and then cannot be rebuilt.
	img, err := rec.RecordImage(s.Buf, p, idx)
	if err != nil {
		return nil
	}
	return img
}
