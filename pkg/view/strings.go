package view

// MapStrings returns v with f applied to every string a renderer will show.
//
// It exists so that "walk every displayable string in a view" is written once.
// There are at least two consumers with different ideas of what to do with
// those strings — a terminal renderer neutralising escape sequences, an MCP
// bridge neutralising what a model would read as instructions — and the walk
// is the part that has to be exhaustive. A traversal per consumer means the
// second one misses Chart.Unit, and misses it silently, because a view with no
// chart in it tests identically either way.
//
// Nothing is exempt, and two things used to be. Both exemptions rested on the
// same premise — that the value is an identifier constrained where it is
// produced — and that premise died when plugins arrived:
//
//   - Error.Code was exempt as "the stable identifier callers branch on,
//     whose shape Validate already constrains at the producer". Validate
//     constrains *declared* text at registration. An error code is
//     per-call output, and wire.ErrorFromProto copies it off the wire
//     verbatim, so a plugin's code is neither declared nor validated and went
//     straight to a model.
//
//   - Section.ID was exempt as "addressed by scripts and agents and never
//     printed". It is emitted in JSON now, and it arrives from the wire the
//     same way.
//
// Mapping them costs nothing a legitimate value would notice: textclean
// strips control characters, bidi overrides and invisible characters, and an
// identifier that is genuinely an identifier passes through byte for byte. A
// value the cleaner does change was never one a caller could safely branch on.
//
//   - Table.Redacted and KeyValue.Redacted name columns and keys, and ARE
//     mapped, precisely because they are matched against Column.Name and
//     Pair.Key. This comment used to say the opposite — that they were
//     "covered by mapping the names they refer to, and only by that" — which
//     is the argument for mapping them, applied backwards. Mapping one side
//     and not the other is exactly what turns a mask off.
//
//     Reproduced before the fix: a key containing a zero-width space, marked
//     Redacted, went through internal/mcp.viewResult — which maps with
//     textclean.Model and then calls Redact. The map stripped the space from
//     Pair.Key, Redacted still held the original, IsRedacted no longer
//     matched, and the secret reached the model in plaintext. Mapping both
//     sides with the same pure f keeps them equal by construction, which
//     also makes the order of map-then-redact irrelevant rather than
//     load-bearing.
//
// f must be pure. It is called once per string and may be called on strings
// that turn out not to need it.
//
// The result shares everything f left alone: a view f does not change is
// returned as itself rather than as a copy, so a host re-rendering the same
// view on every keystroke does not allocate one per frame.
func MapStrings(v View, f func(string) string) View {
	out, _ := mapStrings(v, f)
	return out
}

// mapStrings is MapStrings with the answer to "did anything change".
//
// The nested cases need that answer and cannot get it by comparing: Table,
// Tree, Chart, KeyValue and Sections all hold slices, so == on an interface
// holding one of them is a runtime panic rather than a compile error. That
// surfaced as a nil-pointer panic inside a section carrying a table, which is
// every composed detail page in the catalogue.
func mapStrings(v View, f func(string) string) (View, bool) {
	switch t := v.(type) {
	case Text:
		if s := f(t.Body); s != t.Body {
			t.Body = s
			return t, true
		}
	case KeyValue:
		pairs, pc := mapPairs(t.Pairs, f)
		red, rc := mapNames(t.Redacted, f)
		if pc || rc {
			t.Pairs, t.Redacted = pairs, red
			return t, true
		}
	case Table:
		cols, cc := mapColumns(t.Columns, f)
		rows, rc := mapRows(t.Rows, f)
		// Page.Next is as much plugin-controlled text as a cell is: it round
		// trips through the caller and reaches a model in both the text and
		// the structured halves of an MCP result. It was the one string on a
		// Table this function did not visit.
		page, pc := t.Page, false
		if t.Page != nil {
			if next := f(t.Page.Next); next != t.Page.Next {
				page, pc = &Cursor{Next: next}, true
			}
		}
		red, dc := mapNames(t.Redacted, f)
		if cc || rc || pc || dc {
			t.Columns, t.Rows, t.Page, t.Redacted = cols, rows, page, red
			return t, true
		}
	case Chart:
		series, sc := mapSeries(t.Series, f)
		unit := f(t.Unit)
		if sc || unit != t.Unit {
			t.Series, t.Unit = series, unit
			return t, true
		}
	case Tree:
		if roots, ok := mapNodes(t.Roots, f); ok {
			t.Roots = roots
			return t, true
		}
	case Sections:
		items, ic := mapSections(t.Items, f)
		warns, wc := mapErrors(t.Warnings, f)
		if ic || wc {
			t.Items, t.Warnings = items, warns
			return t, true
		}
	case *Error:
		if t == nil {
			return v, false
		}
		if e := mapError(*t, f); e != *t {
			return &e, true
		}
	}
	// Unchanged, a nil view, or a type outside the union — the interface is
	// closed by an unexported method, so the last cannot happen from outside
	// this package.
	return v, false
}

func mapPairs(in []Pair, f func(string) string) ([]Pair, bool) {
	changed := false
	out := make([]Pair, len(in))
	for i, p := range in {
		out[i] = Pair{Key: f(p.Key), Value: f(p.Value)}
		if out[i] != p {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapColumns(in []Column, f func(string) string) ([]Column, bool) {
	changed := false
	out := make([]Column, len(in))
	for i, c := range in {
		out[i] = Column{Name: f(c.Name), Kind: c.Kind}
		if out[i] != c {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapRows(in [][]string, f func(string) string) ([][]string, bool) {
	changed := false
	out := make([][]string, len(in))
	for i, row := range in {
		cells := make([]string, len(row))
		rowChanged := false
		for j, cell := range row {
			cells[j] = f(cell)
			if cells[j] != cell {
				rowChanged = true
			}
		}
		if rowChanged {
			out[i], changed = cells, true
			continue
		}
		out[i] = row
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapSeries(in []Series, f func(string) string) ([]Series, bool) {
	changed := false
	out := make([]Series, len(in))
	for i, s := range in {
		out[i] = Series{Name: f(s.Name), Points: s.Points}
		if out[i].Name != s.Name {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapNodes(in []Node, f func(string) string) ([]Node, bool) {
	if len(in) == 0 {
		return in, false
	}
	changed := false
	out := make([]Node, len(in))
	for i, n := range in {
		kids, kc := mapNodes(n.Children, f)
		out[i] = Node{Label: f(n.Label), Detail: f(n.Detail), Children: kids}
		if kc || out[i].Label != n.Label || out[i].Detail != n.Detail {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapSections(in []Section, f func(string) string) ([]Section, bool) {
	if len(in) == 0 {
		return in, false
	}
	changed := false
	out := make([]Section, len(in))
	for i, s := range in {
		inner, ic := mapStrings(s.View, f)
		out[i] = Section{ID: f(s.ID), Title: f(s.Title), View: inner}
		if ic || out[i].Title != s.Title || out[i].ID != s.ID {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapErrors(in []Error, f func(string) string) ([]Error, bool) {
	if len(in) == 0 {
		return in, false
	}
	changed := false
	out := make([]Error, len(in))
	for i, e := range in {
		out[i] = mapError(e, f)
		if out[i] != e {
			changed = true
		}
	}
	if !changed {
		return in, false
	}
	return out, true
}

func mapError(e Error, f func(string) string) Error {
	e.Code, e.Message, e.Hint = f(e.Code), f(e.Message), f(e.Hint)
	return e
}

// MapErrorStrings is MapStrings for the pointer form the error surfaces carry.
// An error f leaves alone keeps its identity, so a caller comparing pointers
// still sees the error it was given.
func MapErrorStrings(e *Error, f func(string) string) *Error {
	if e == nil {
		return nil
	}
	c := mapError(*e, f)
	if c == *e {
		return e
	}
	return &c
}

// mapNames maps a list of names that must stay equal to names elsewhere in
// the same view — Redacted against Column.Name and Pair.Key. Shared so the
// two cases cannot drift from each other.
func mapNames(in []string, f func(string) string) ([]string, bool) {
	changed := false
	out := in
	for i, s := range in {
		mapped := f(s)
		if mapped == s {
			continue
		}
		if !changed {
			out = make([]string, len(in))
			copy(out, in)
			changed = true
		}
		out[i] = mapped
	}
	return out, changed
}
