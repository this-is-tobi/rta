package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

// visibleRowCount admits one row even when it does not fit — a dashboard that
// draws nothing is worse than one whose tallest row is clipped — and the frame
// handed the renderer that row at its full height, so on a short terminal the
// footer was what fell off the bottom. The row is drawn at the room there is,
// with its hint and its bottom border.
func TestARowTallerThanTheScreenIsDrawnAtTheHeightThatFits(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "tall.info"}}}, nil)
	m = filled(t, m, 60, 16)
	content := plain(m.View().Content)
	if got := lipgloss.Height(content); got > 16 {
		t.Errorf("frame is %d lines on a 16-line terminal:\n%s", got, content)
	}
	lines := strings.Split(content, "\n")
	footer := lipgloss.Height(m.dashFooter())
	if above := lines[len(lines)-1-footer]; !strings.Contains(above, "╰") {
		t.Errorf("the line above the footer is not the tile's bottom border: %q", above)
	}
	if !strings.Contains(content, "enter for details") {
		t.Errorf("the clipped tile does not say it was clipped:\n%s", content)
	}
}

// filled sizes a model and delivers every tile's own result, the way the
// program does once the window size is known.
func filled(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(Model)
	for i, ti := range m.tiles {
		next, _ = m.Update(tileCmd(i, ti, nil, "", nil, config.Connection{})())
		m = next.(Model)
	}
	return m
}
