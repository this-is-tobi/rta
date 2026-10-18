package view

import (
	"slices"
	"strings"
)

// What pretty draws for a person and a program has no use for.
//
// `-o json`, yaml, csv, md and an agent over MCP read a view, and a view is
// written for the screen first: a key indented under the one it belongs to, a
// last line `…  2 more` saying a branch has entries it does not list. Both are
// layout, and both reached a program as data — `profile show -o json` had the
// key "  set:timeout", and `fs tree -o json` a child named "…" that is no
// entry of the directory. Taking them out where a view is encoded means no
// capability has to remember to, and the pretty and TUI renderers, which draw
// the view itself, keep them.

// CutLabel is the label of the node a tree draws in place of the entries it
// does not list, with the reason as its Detail: "3 more", "12 hidden (--all)",
// "unreadable: permission denied". The machine encodings have no node for it
// (Tree.WithoutCuts); a name that is really this one is shown quoted
// (glyph.Name), so that the label is never an entry's.
const CutLabel = "…"

// CutCode is the code of the warning that stands for a CutLabel node in the
// machine encodings, as the code of a table's warning says what it could not
// list.
const CutCode = "core.tree.cut"

// WithoutLayout is kv with the layout taken off every key and off the names of
// the masked ones: the whitespace a screen indents a key with. kv is not
// edited, and is returned as it is when no key has any.
func (kv KeyValue) WithoutLayout() KeyValue {
	if !slices.ContainsFunc(kv.Pairs, laidOut) && !slices.ContainsFunc(kv.Redacted, func(k string) bool { return k != strings.TrimSpace(k) }) {
		return kv
	}
	pairs := make([]Pair, len(kv.Pairs))
	for i, p := range kv.Pairs {
		p.Key = strings.TrimSpace(p.Key)
		pairs[i] = p
	}
	kv.Pairs = pairs
	if kv.Redacted != nil {
		masked := make([]string, len(kv.Redacted))
		for i, k := range kv.Redacted {
			masked[i] = strings.TrimSpace(k)
		}
		kv.Redacted = masked
	}
	return kv
}

func laidOut(p Pair) bool { return p.Key != strings.TrimSpace(p.Key) }

// WithoutCuts is t without its cut markers, and one warning for each, in the
// order they stand in the tree: the marker's Detail, behind the labels of the
// branches it is in ("project/docs/: 2 more"), so that a program told a tree
// is partial is told where. The warnings are the field a table carries what
// it could not list in, and are not Advisory for the same reason: a branch
// that stops short is a part of the answer that is missing.
//
// A marker is a leaf with the label CutLabel; one with children is a branch
// somebody labelled so and is kept. t is not edited, and is returned as it is
// when it has no marker.
func (t Tree) WithoutCuts() (Tree, []Error) {
	var cuts []Error
	roots := withoutCuts(t.Roots, "", &cuts)
	if len(cuts) == 0 {
		return t, nil
	}
	t.Roots = roots
	return t, cuts
}

func withoutCuts(nodes []Node, where string, cuts *[]Error) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Label == CutLabel && len(n.Children) == 0 {
			msg := n.Detail
			if where != "" {
				msg = where + ": " + msg
			}
			*cuts = append(*cuts, Error{Code: CutCode, Message: msg})
			continue
		}
		if len(n.Children) > 0 {
			n.Children = withoutCuts(n.Children, where+branch(n.Label), cuts)
		}
		out = append(out, n)
	}
	return out
}

// branch is a label as one step of the path to a node: a directory's already
// ends in a slash, and any other label is parted from the next by one.
func branch(label string) string {
	if strings.HasSuffix(label, "/") {
		return label
	}
	return label + "/"
}
