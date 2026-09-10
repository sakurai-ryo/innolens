package tui

import "strings"

// filterTree keeps the schemas and tables the query names, matched
// case-insensitively. A dot splits the query into schema.table, either side
// allowed to be empty, so `sales.` is every table of a schema and `.log` is
// tables named log in any schema; a dotted query drops the redo log and the
// tablespace files, which have no schema to name. Without a dot the text is
// matched against both schema and table names, and a schema matching on its own
// name keeps all of its tables.
//
// Matched nodes are copied because the expanded flag differs between the
// filtered view and the full tree the filter is cleared back to.
func filterTree(root *node, q string) *node {
	schemaQ, tableQ, dotted := strings.Cut(strings.ToLower(q), ".")
	out := &node{}
	for _, c := range root.children {
		cp := *c
		switch {
		case dotted:
			// Both halves are named, so a leaf without a schema, like
			// #innodb_redo, drops out with no table to match against.
			if !matchLabel(c.label, schemaQ) {
				continue
			}
			cp.children = matchKids(c.children, tableQ)
		case matchLabel(c.label, schemaQ):
		default:
			cp.children = matchKids(c.children, schemaQ)
		}
		if len(cp.children) == 0 && !matchLabel(c.label, q) {
			continue
		}
		cp.expanded = len(cp.children) > 0
		out.children = append(out.children, &cp)
	}
	return out
}

func matchKids(kids []*node, q string) []*node {
	var out []*node
	for _, k := range kids {
		if matchLabel(k.label, q) {
			out = append(out, k)
		}
	}
	return out
}

func matchLabel(label, q string) bool {
	return strings.Contains(strings.ToLower(label), q)
}
