package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Without colour the focused tile is the one drawn in heavy lines. The border's
// primary colour was the only thing that said which tile the arrow keys had
// selected, so under NO_COLOR or TERM=dumb the selection could not be seen.
func TestTheFocusedTileIsMarkedInGlyphsWhenTheTerminalShowsNoColour(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m.selected = 1

	heavy := func(m Model) int {
		return strings.Count(plain(m.dashboardView()), "┏")
	}
	if got := heavy(m); got != 0 {
		t.Fatalf("a coloured terminal drew %d heavy corners, want none: colour already says it", got)
	}
	for _, p := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.NoTTY} {
		next, _ := m.Update(tea.ColorProfileMsg{Profile: p})
		if got := heavy(next.(Model)); got != 1 {
			t.Errorf("profile %v: %d heavy top-left corners on the dashboard, want exactly the selected tile's", p, got)
		}
	}
	next, _ := m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if got := heavy(next.(Model)); got != 0 {
		t.Errorf("a truecolor profile drew %d heavy corners, want none", got)
	}
}

// The heavy frame is the same size as the rounded one: the grid's arithmetic
// and the mouse's hit-testing count on every panel being exactly width by
// height cells.
func TestAHeavyPanelIsTheSameSizeAsARoundedOne(t *testing.T) {
	head := panelHead{Title: "kube.overview", Note: "prod", Right: "3s", Heavy: true}
	rounded := panel(head, "one\ntwo", 40, 6, false)
	focused := panel(head, "one\ntwo", 40, 6, true)
	if w, h := ansi.StringWidth(strings.Split(focused, "\n")[0]), strings.Count(focused, "\n")+1; w != 40 || h != 6 {
		t.Errorf("focused panel is %dx%d, want 40x6", w, h)
	}
	if got, want := strings.Count(rounded, "\n"), strings.Count(focused, "\n"); got != want {
		t.Errorf("rounded panel is %d lines tall, focused %d", got+1, want+1)
	}
	if !strings.HasPrefix(plain(focused), "┏━ kube.overview") || !strings.HasPrefix(plain(rounded), "╭─ kube.overview") {
		t.Errorf("unexpected frames:\n%s\n%s", plain(focused), plain(rounded))
	}
}
