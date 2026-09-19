package tui

import (
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// Bubble Tea renders the initial model before it delivers a window size, so
// this is the first frame of every run. It has to be nothing: a dashboard laid
// out at width zero is unframed (panel gives the body back below ten cells),
// unwrapped (cli.Render reads a non-positive width as no limit) and taller than
// any screen (dashRowsVisible renders every row when the height is unknown) —
// and the renderer diffs the next frame against whatever this one left behind.
//
// The tiles are filled first. With every tile still on "loading…" the unsized
// frame is small and innocuous; it is the filled one that carries the text
// that ends up inside a neighbour's border.
func TestUnsizedFrameIsEmpty(t *testing.T) {
	m := New(layoutRegistry(t), config.Dashboard{}, nil)
	for i, ti := range m.tiles {
		next, _ := m.Update(tileCmd(i, ti, nil, "", nil, config.Connection{})())
		m = next.(Model)
	}
	if got := m.View().Content; got != "" {
		t.Errorf("painted %d lines before the terminal size was known:\n%s",
			lipgloss.Height(got), got)
	}
}
