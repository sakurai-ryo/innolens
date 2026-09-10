package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sakurai-ryo/innolens/internal/innodb"
)

// Hues carry meaning rather than decoration, and the same hue means the same
// thing in the page tree, the redo tree and the hex dump: green is user data,
// blue is structure, yellow is the directory and updates, red is deletion and
// corruption, purple is metadata, grey is space nothing lives in.
const (
	colData    = lipgloss.Color("#9ece6a")
	colDataAlt = lipgloss.Color("#7a9e52")
	colStruct  = lipgloss.Color("#7aa2f7")
	colIndex   = lipgloss.Color("#e0af68")
	colDanger  = lipgloss.Color("#f7768e")
	colMeta    = lipgloss.Color("#bb9af7")
	colMuted   = lipgloss.Color("#565f89")
	colVoid    = lipgloss.Color("#414868")
	colText    = lipgloss.Color("#a9b1d6")
	colAccent  = lipgloss.Color("#7dcfff")
)

var (
	dimStyle    = lipgloss.NewStyle().Foreground(colMuted)
	selStyle    = lipgloss.NewStyle().Reverse(true)
	restStyle   = lipgloss.NewStyle().Foreground(colAccent)
	headStyle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	offStyle    = lipgloss.NewStyle().Foreground(colMuted)
	dangerStyle = lipgloss.NewStyle().Foreground(colDanger)
)

// Region ids paint every byte of a page. Records alternate between two shades
// so that record boundaries are visible in the dump without any extra markup.
const (
	regNone uint8 = iota
	regStruct
	regRecs
	regRecsAlt
	regDir
	regMeta
	regFree
)

var regionStyle = [...]lipgloss.Style{
	regNone:    lipgloss.NewStyle().Foreground(colVoid),
	regStruct:  lipgloss.NewStyle().Foreground(colStruct),
	regRecs:    lipgloss.NewStyle().Foreground(colData),
	regRecsAlt: lipgloss.NewStyle().Foreground(colDataAlt),
	regDir:     lipgloss.NewStyle().Foreground(colIndex),
	regMeta:    lipgloss.NewStyle().Foreground(colMeta),
	regFree:    lipgloss.NewStyle().Foreground(colMuted),
}

// sectionRegion maps the top level of the annotation tree onto regions. Names
// not listed here leave their bytes unpainted.
var sectionRegion = map[string]uint8{
	"FIL header":          regStruct,
	"INDEX header":        regStruct,
	"FSEG header":         regStruct,
	"FSP header":          regStruct,
	"Records":             regRecs,
	"PAGE_FREE list":      regFree,
	"Page directory":      regDir,
	"undo page header":    regStruct,
	"undo segment header": regStruct,
	"undo log header":     regStruct,
	"SDI header":          regMeta,
	"SDI JSON":            regMeta,
	"FIL trailer":         regMeta,
}

// sectionColor is the colour of a section row in the annotation tree, matching
// the bytes it covers in the dump next to it.
func sectionColor(name string) lipgloss.Color {
	id, ok := sectionRegion[name]
	if !ok {
		return ""
	}
	return regionStyle[id].GetForeground().(lipgloss.Color)
}

// recPrefix names the record children that sit before the origin: they say
// where the columns are rather than holding one, so they read as structure.
var recPrefix = map[string]bool{"var lengths": true, "null bitmap": true, "header": true}

// hexRegions walks the annotation tree and records which region every byte of
// the page belongs to.
func hexRegions(root *innodb.Node) []uint8 {
	regs := make([]uint8, innodb.PageSize)
	for _, sec := range root.Children {
		id, ok := sectionRegion[sec.Name]
		if !ok {
			continue
		}
		if id == regRecs {
			for i, rec := range sec.Children {
				paint(regs, rec, id+uint8(i%2))
				for _, c := range rec.Children {
					if recPrefix[c.Name] {
						paint(regs, c, regStruct)
					}
				}
			}
			continue
		}
		paint(regs, sec, id)
	}
	return regs
}

func paint(regs []uint8, n *innodb.Node, id uint8) {
	for i := n.Off; i < n.Off+n.Len && i < len(regs); i++ {
		regs[i] = id
	}
	for _, c := range n.Children {
		paint(regs, c, id)
	}
}

// indexTag names an INDEX page by its role in the tree: leaf pages hold the
// rows, internal pages only route to them. level < 0 means unknown.
func indexTag(level int) string {
	switch {
	case level == 0:
		return "INDEX leaf"
	case level > 0:
		return "INDEX node"
	}
	return "INDEX"
}

// pageTagOf is the type token and colour of a whole page, reading the B+tree
// level so that leaf and internal INDEX pages read differently.
func pageTagOf(p *innodb.Page) (string, lipgloss.Color) {
	if p.FIL.Type == innodb.FIL_PAGE_INDEX {
		level := -1
		if ip, err := p.ParseIndex(nil); err == nil {
			level = int(ip.Hdr.Level)
		}
		return indexTag(level), pageColor(p.FIL.Type, level)
	}
	return p.TypeName(), pageColor(p.FIL.Type, -1)
}

// pageColor colours a page by type, splitting INDEX by level because that is
// the distinction you navigate by. level < 0 means unknown.
func pageColor(typ uint16, level int) lipgloss.Color {
	switch typ {
	case innodb.FIL_PAGE_INDEX:
		if level == 0 {
			return colData
		}
		return colStruct
	case innodb.FIL_PAGE_SDI, innodb.FIL_PAGE_SDI_BLOB:
		return colMeta
	case innodb.FIL_PAGE_UNDO_LOG:
		return colIndex
	case innodb.FIL_PAGE_TYPE_ALLOCATED:
		return colVoid
	}
	return colMuted
}

// redoColor folds the 60-odd MLOG types into the five categories worth telling
// apart while reading a log: what inserted, deleted, updated or created a page.
// Undo, file, zip and raw-write records stay grey; they are noise for that.
func redoColor(t uint8) lipgloss.Color {
	n := innodb.MLogName(t)
	switch {
	case strings.HasPrefix(n, "MLOG_UNDO"), strings.HasPrefix(n, "MLOG_FILE"), strings.HasPrefix(n, "MLOG_ZIP"):
		return colMuted
	case strings.Contains(n, "DELETE"):
		return colDanger
	case strings.Contains(n, "INSERT"):
		return colData
	case strings.Contains(n, "UPDATE"):
		return colIndex
	case strings.Contains(n, "PAGE_CREATE"), strings.Contains(n, "REORGANIZE"), strings.Contains(n, "INIT_FILE_PAGE"):
		return colStruct
	}
	return colMuted
}

// iconSet is the glyph table. The Nerd Font set is the default; INNOLENS_ICONS=0
// falls back to plain Unicode for terminals without a patched font.
type iconSet struct {
	collapsed, expanded            string
	schema, table, system, redo    string
	page, index, record, mtr, warn string
	delta, undo                    string
}

var nerdIcons = iconSet{
	collapsed: "\u25b8", expanded: "\u25be",
	schema: "\uf07b", table: "\uf1c0", system: "\uf15b", redo: "\uf1da",
	page: "\uf15c", index: "\uf0e8", record: "\uf0ca", mtr: "\uf0e7", warn: "\uf071",
	delta: "\uf0ec", undo: "\uf0e2",
}

var plainIcons = iconSet{collapsed: "▸", expanded: "▾", warn: "!", delta: "Δ", undo: "↺"}

var ic = pickIcons()

func pickIcons() iconSet {
	if os.Getenv("INNOLENS_ICONS") == "0" {
		return plainIcons
	}
	return nerdIcons
}
