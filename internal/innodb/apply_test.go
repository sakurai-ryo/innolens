package innodb

import (
	"testing"
)

// TestApplyUpdateInPlace replays the UPDATE the fixture runs after the .ibd
// files were copied. The row on disk still holds the old value, so applying the
// record has to produce the new one.
func TestApplyUpdateInPlace(t *testing.T) {
	for _, ver := range versions {
		t.Run(ver, func(t *testing.T) {
			_, s := openFixtureRedo(t, ver)
			space, tbl := openTable(t, ver, "types")

			var rec *RedoRec
			for _, m := range s.MTRs() {
				for _, r := range m.Recs {
					if r.Type == MLOG_REC_UPDATE_IN_PLACE && r.SpaceID == space.ID {
						rec = r
					}
				}
			}
			if rec == nil {
				t.Fatal("no MLOG_REC_UPDATE_IN_PLACE for the types tablespace in the redo log")
			}
			p, err := space.Page(rec.PageNo)
			if err != nil {
				t.Fatal(err)
			}
			idx := tbl.Indexes[0]
			before, err := parseRec(p.Data, rec.Upd.RecOff, idx)
			if err != nil {
				t.Fatal(err)
			}
			if got := fields(before)["c_tinyint"]; got == "100" {
				t.Skip("the page already holds the updated row")
			}

			out, note, err := rec.Apply(p, idx)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if out == p {
				t.Fatalf("the record was not applied: %s", note)
			}
			if !out.ChecksumOK {
				t.Error("the replayed page does not checksum")
			}
			if out.FIL.LSN != rec.EndLSN {
				t.Errorf("page lsn = %d, want the record end %d", out.FIL.LSN, rec.EndLSN)
			}
			after, err := parseRec(out.Data, rec.Upd.RecOff, idx)
			if err != nil {
				t.Fatal(err)
			}
			if got := fields(after)["c_tinyint"]; got != "100" {
				t.Errorf("c_tinyint = %q after the replay, want 100", got)
			}
			if fields(after)["id"] != fields(before)["id"] {
				t.Error("the replay changed the primary key of the record")
			}

			// A page that already carries the change is left alone, which is
			// what stops recovery applying a record twice.
			same, note, err := rec.Apply(out, idx)
			if err != nil {
				t.Fatal(err)
			}
			if same != out {
				t.Errorf("the record was applied to a newer page: %s", note)
			}
		})
	}
}

// TestApplyWrite replays every byte-writing record of the log against its page
// and checks the write lands inside the page and moves its LSN forward.
func TestApplyWrite(t *testing.T) {
	_, s := openFixtureRedo(t, "80")
	space, _ := openTable(t, "80", "types")
	n := 0
	for _, m := range s.MTRs() {
		for _, r := range m.Recs {
			if r.Write == nil || r.SpaceID != space.ID || r.PageNo >= space.NPages {
				continue
			}
			p, err := space.Page(r.PageNo)
			if err != nil {
				t.Fatal(err)
			}
			out, note, err := r.Apply(p, nil)
			if err != nil {
				t.Fatalf("%s at lsn %d: %v", r.TypeName(), r.LSN, err)
			}
			if out == p {
				continue // the page on disk is already newer than the record
			}
			n++
			if out.FIL.LSN < p.FIL.LSN {
				t.Errorf("%s: page lsn went backwards (%s)", r.TypeName(), note)
			}
		}
	}
	if n == 0 {
		t.Skip("every byte-writing record was already on disk")
	}
}
