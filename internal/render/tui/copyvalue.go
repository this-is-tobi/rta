package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/internal/clipboard"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// copySpecs names, per capability, the one field of its result meant to be
// copied to the clipboard verbatim — a generated password, a token, a
// UUID — as opposed to the whole view `y` copies as indented JSON.
//
// There is no generic way to guess which cell of an arbitrary table or
// which pair of an arbitrary KeyValue is "the value": kv.list's own first
// column is a key *name*, never the secret, which is exactly why kv reaches
// its value a different way — kv.copy, a row action declared in
// capActionSpecs (dashboard.go) that re-reads the value from the store by
// key. gen has no store to re-read: a generated password exists only in the
// result already on screen, so its capabilities are declared here instead,
// against the value already in memory rather than by re-running anything.
//
// A capability must not appear both here and in capActionSpecs under the
// key "c" — capActionSpecs is checked first and would claim every "c"
// press, leaving an entry here dead code with a hint that never fires.
var copySpecs = map[string]copySpec{
	"gen.password": {column: "Password"},
	"gen.token":    {pair: "token"},
	"gen.uuid":     {column: "UUID"},
	// gen.overview's compact table (builtin/gen/sample.go) always names its
	// generated cell "Value", whether that is a password, a key or a UUID —
	// one column shared by every recipe, which is what lets one copySpec
	// cover a table mixing all three. It never has exactly one row (five
	// recipes, always), so every "c" against it reaches the picker, never
	// the direct single-value copy.
	"gen.overview": {column: "Value"},
}

type copySpec struct {
	column string // view.Table: header name of the cell to copy
	pair   string // view.KeyValue: Key of the pair to copy
}

// copyValue extracts the value spec names from v — the raw string, never
// whatever a renderer chose to display for it. ok is false when v is not
// the shape spec expects, the named column/pair is absent, or the field is
// one every renderer must mask (Redacted): copying it whole would undo
// exactly the protection that marking gives it on screen.
//
// A Table only qualifies with exactly one row. A multi-row result (e.g.
// gen.password --count 5) has no row-selection UI to choose among: that
// requires a capability with row actions declared in capActionSpecs, which
// a stateless generator has none of by construction — there is nothing to
// act on a generated value *with*. Without this restriction "copy" would
// silently always mean "copy the first row", with no way to reach the
// rest — that is what copyChoices and copypick.go's picker are for
// instead: asking, rather than guessing. One row has no such ambiguity: it
// is unambiguously the value the screen is showing.
func copyValue(spec copySpec, v view.View) (value string, ok bool) {
	switch vv := v.(type) {
	case view.Table:
		if spec.column == "" || len(vv.Rows) != 1 || vv.IsRedacted(spec.column) {
			return "", false
		}
		for i, c := range vv.Columns {
			if c.Name == spec.column && i < len(vv.Rows[0]) {
				return vv.Rows[0][i], true
			}
		}
	case view.KeyValue:
		if spec.pair == "" || vv.IsRedacted(spec.pair) {
			return "", false
		}
		for _, p := range vv.Pairs {
			if p.Key == spec.pair {
				return p.Value, true
			}
		}
	}
	return "", false
}

// copyChoices lists the values spec names across every row of v, in row
// order — for copypick.go's picker, which exists for exactly the case
// copyValue declines: more than one row, and so no single value to copy
// without asking which. ok is false under the same conditions copyValue
// refuses (not a Table, the column absent, or Redacted) — a spec that
// cannot name a value unambiguously with one row cannot name one
// per-row either, for the same reason.
//
// Unlike copyValue there is no row-count restriction here — a table with
// exactly one row would already have been handled by copyValue, so this is
// only ever reached with zero rows (values comes back empty, ok false) or
// several (every one of them offered).
func copyChoices(spec copySpec, v view.View) (values []string, ok bool) {
	tbl, isTable := v.(view.Table)
	if !isTable || spec.column == "" || tbl.IsRedacted(spec.column) {
		return nil, false
	}
	col := -1
	for i, c := range tbl.Columns {
		if c.Name == spec.column {
			col = i
			break
		}
	}
	if col == -1 {
		return nil, false
	}
	for _, row := range tbl.Rows {
		// Rows are allowed to be shorter than the column list (view.Table's
		// own contract); one that has nothing under this column offers
		// nothing to choose, rather than aborting every other row's chance.
		if col < len(row) {
			values = append(values, row[col])
		}
	}
	return values, len(values) > 0
}

// copyHint returns the footer hint copySpecs offers for v, if any — shared
// between resultView (a result on screen) and dashFooter (a tile's own,
// without opening it): "copy value" when exactly one value is addressable,
// "copy which value?" when there is more than one and the picker is what
// "c" opens instead, and ok false when there is nothing to copy at all.
func copyHint(specID string, v view.View) (hintItem, bool) {
	spec, ok := copySpecs[specID]
	if !ok {
		return hintItem{}, false
	}
	if _, has := copyValue(spec, v); has {
		return action("c", "copy value"), true
	}
	if _, has := copyChoices(spec, v); has {
		return action("c", "copy which value?"), true
	}
	return hintItem{}, false
}

// copyOrPick is what "c" does against v: copy the one addressable value
// immediately, or open the picker when spec names more than one. Shared by
// modeResult's "c" (a result on screen) and the dashboard's tile "c" (a
// tile's own view, without opening it) — the two places a copySpecs-named
// capability's output can be looked at. cap is threaded through only for
// the picker's own panel head — copyValue's direct-copy path needs
// nothing about identity beyond spec, which already named the field.
// returnTo is where the picker closes back to; it means nothing when
// copyValue resolves directly, since no mode change happens in that case.
func (m Model) copyOrPick(spec copySpec, cap plugin.Capability, v view.View, returnTo mode) (tea.Model, tea.Cmd) {
	if val, ok := copyValue(spec, v); ok {
		if verr := copyValueToClipboard(val); verr != nil {
			m.flash = "not copied: " + verr.Error()
		} else {
			m.flash = "copied value"
		}
		return m, nil
	}
	if _, found := copyChoices(spec, v); found {
		return m.startCopyPick(spec, cap, v, returnTo)
	}
	return m, nil
}

// copyValueToClipboard puts value on the system clipboard directly, through
// internal/clipboard — the same stdin-only subprocess mechanism kv.copy
// uses, never tea.SetClipboard's OSC 52 escape sequence.
//
// OSC 52 is what `y` (copy the whole view as JSON) uses, and that is the
// right tool there: a person pasting a data blob into an editor expects
// structure. It is the wrong tool here. json.MarshalIndent inserts a real
// newline between every field, and most places a generated secret gets
// pasted — a password field, a shell prompt, `kubectl create secret
// --from-literal` — read a newline as Enter: the paste submits, or the
// command runs, after whatever text preceded the break, and the rest of the
// value is silently gone. The same subprocess kv already proved out — value
// on stdin, never argv — sidesteps that entirely, along with OSC 52's own
// payload-length limits in tmux and older terminals: what lands on the
// clipboard is the exact bytes generated, one unbroken value, whatever its
// length or alphabet.
func copyValueToClipboard(value string) *view.Error {
	ok, failed, tried := clipboard.Copy([]byte(value))
	if ok {
		return nil
	}
	if len(failed) > 0 {
		return view.Errorf("tui.clipboard.failed",
			"no clipboard program would take the value: %s", strings.Join(failed, ", "))
	}
	return view.Errorf("tui.clipboard.missing",
		"no clipboard program on this machine — install one of: %s", strings.Join(tried, ", "))
}
