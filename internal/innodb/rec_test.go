package innodb

import "testing"

// TestParseRecShortHeader feeds records whose header would reach below the
// buffer, as a corrupt next pointer or redo offset can; they must be errors.
func TestParseRecShortHeader(t *testing.T) {
	cases := []struct {
		name string
		off  int
		info byte
		idx  *IndexDef
	}{
		{"row version", 5, REC_INFO_VERSION_FLAG, &IndexDef{Cols: []Col{{Name: "a", Fixed: 4}}}},
		{"null bitmap", 5, 0, &IndexDef{Cols: []Col{{Name: "a", Fixed: 4, Nullable: true}}}},
		{"2-byte length", 6, 0, &IndexDef{Cols: []Col{{Name: "a", MaxLen: 1000}}}},
		{"node pointer", 5, 0, &IndexDef{NUniqueInTree: 3, Cols: []Col{{Name: "a", Fixed: 4}}}},
	}
	for _, c := range cases {
		b := make([]byte, PageSize)
		b[c.off-5] = c.info
		if c.name == "node pointer" {
			b[c.off-3] = REC_STATUS_NODE_PTR
		}
		if c.off == 6 {
			b[0] = 0x80 // a long length in the first length byte
		}
		if _, err := parseRec(b, c.off, c.idx); err == nil {
			t.Errorf("%s: parsed without error", c.name)
		}
	}
}
