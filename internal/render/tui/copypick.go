package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The copy picker: when a capability's Copy names a value that exists in more than
// one row of a result — gen.password --count 5, or gen.overview's five
// recipes side by side — "c" opens this instead of doing nothing. There is
// no row-selection UI to point at one of the several another way: that
// needs a capability with row actions declared in capActionSpecs
// (dashboard.go), which a stateless generator has none of by construction —
// there is nothing to act on a generated value *with* besides copying it,
// which is exactly the gap this closes. One huh.Select naming every value
// verbatim; Enter copies whichever is highlighted, unprefixed and exact —
// the "N: " before it is a label for finding it on screen, never part of
// what lands on the clipboard.
//
// Reachable from two places: a result already open (modeResult) and a
// tile's own preview on the dashboard, without opening it first — the
// values are the same either way (m.result.raw and a tile's own view are
// both just a view.View), so one picker serves both. What differs is where
// "done" goes back to, which is why the capability and the return mode
// travel with the form rather than being read off m.current/m.origin: a
// tile does not set m.current at all, and m.origin already means something
// else (where the *result itself* was opened from, not where this picker
// was opened from).

// startCopyPick opens the picker for spec against v — m.result.raw from
// modeResult, or a tile's own view as its capability returned it from the
// dashboard. cap is whichever capability v belongs to, for the panel head.
func (m Model) startCopyPick(spec copySpec, cap plugin.Capability, v view.View, returnTo mode) (tea.Model, tea.Cmd) {
	values, ok := copyChoices(spec, v)
	if !ok {
		return m, nil
	}
	options := make([]pickOption, len(values))
	for i, value := range values {
		// values come from the view as its capability returned it, so the
		// clipboard gets the value; the label is drawn, so it is cleaned.
		options[i] = pickOption{label: fmt.Sprintf("%d: %s", i+1, textclean.Terminal(value)), value: value}
	}
	return m.openPick(newPickForm(pickCopy, "copy which value?", "", options, cap, returnTo))
}
