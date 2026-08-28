package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// What the panes look like, measured rather than eyeballed.

// shortCatalogue is a registry whose ids and permissions are both narrower
// than the headings above them — the case that shows a heading being cut to
// fit a column measured only from data.
func shortCatalogue(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	if err := reg.Register(plugin.Plugin{Name: "ab", Summary: "s", Capabilities: []plugin.Capability{
		{ID: "ab.c", Summary: "one", Safety: plugin.Read, Run: run},
		{ID: "ab.d", Summary: "two", Safety: plugin.Write, Run: run},
	}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// A column is never narrower than its own heading.
//
// The widths were measured from the capabilities alone, so a catalogue of
// four-cell ids drew `CAPABI…` and a catalogue with nothing destructive in it
// drew `PERM…`. A heading that has been cut to fit its column is a heading
// that is no longer telling you what the column is.
func TestAHeadingIsNeverCutToFitItsColumn(t *testing.T) {
	m := New(shortCatalogue(t), config.Dashboard{}, nil)
	m.width, m.height = 100, 30
	for _, width := range []int{120, 100, 80} {
		head := plain(m.browseHeader(width))
		for _, want := range []string{headID, headPermission} {
			if !strings.Contains(head, want) {
				t.Errorf("at %d columns the heading is %q, missing %q", width, strings.TrimSpace(head), want)
			}
		}
	}
}

// The count sits inside the table it counts.
func TestTheCatalogueCountStaysInsideTheTable(t *testing.T) {
	m := New(shortCatalogue(t), config.Dashboard{}, nil)
	m.width, m.height = 100, 30
	for _, width := range []int{120, 100, 80} {
		if got := lipgloss.Width(plain(m.browseHeader(width))); got > width-2 {
			t.Errorf("at %d columns the header line is %d wide, past the table's own edge", width, got)
		}
	}
}

// An empty pane's prose starts where every other line does.
//
// lipgloss pads a multi-line block to its widest line, so a styled string that
// *ends* in "\n\n" carries two lines of padding with it and whatever is
// concatenated after it starts at that column instead of at the margin. The
// first screen a new operator sees had its second paragraph beginning 29 cells
// in, which reads as a rendering fault rather than as a sentence.
func TestAnEmptyPanesProseStartsAtTheMargin(t *testing.T) {
	for _, tc := range []struct {
		name string
		open string
		cfg  config.Config
	}{
		{"no environments", "", config.Config{}},
		{"no plugins in one", "empty", config.Config{Profiles: map[string]config.Profile{
			"empty": {Note: "nothing in it yet"},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := profileModel(t, tc.cfg)
			m.width, m.height = 100, 24
			m.profileOpen = tc.open
			body := m.profilesView()
			if tc.open != "" {
				m.profiles = m.profileRows()
				body = m.connsView()
			}
			for _, line := range strings.Split(plain(body), "\n") {
				trimmed := strings.TrimRight(line, " │")
				text := strings.TrimLeft(trimmed, " │")
				if text == "" || !strings.HasPrefix(trimmed, "│") {
					continue
				}
				lead := lipgloss.Width(trimmed) - lipgloss.Width(text)
				if lead > 6 {
					t.Errorf("a body line starts %d cells in, which is not the margin: %q", lead, trimmed)
				}
			}
		})
	}
}

// A styled block that ends in newlines pads them — the mechanism behind the
// test above, pinned on its own so the fix cannot be undone by somebody
// re-inlining the newline.
func TestAStyledBlockDoesNotCarryPaddingIntoWhatFollows(t *testing.T) {
	styled := theme.Subtle.Render("  No environments configured.") + "\n\n"
	lines := strings.Split(styled+"next", "\n")
	if got := lipgloss.Width(lines[len(lines)-1]); got != lipgloss.Width("next") {
		t.Errorf("the line after a styled block starts %d cells in", got-lipgloss.Width("next"))
	}
}

// A confirmation cannot push the layout past the terminal.
//
// The flash used to be appended after fitHintBar had already packed the bar to
// the full width, so a long one ran off the edge — and the dashboard budgets
// its tile rows from lipgloss.Height(dashFooter()), which then reported one
// line while the terminal drew two, stealing a row the grid had already given
// to a tile and scrolling the header off the top. layout_test's sweep could
// not see it: nothing there ever sets m.flash.
func TestALongFlashDoesNotBreakTheLayout(t *testing.T) {
	const long = "saved profile proj1-staging — covers pg, s3 and vault, and the switch lapses in 8h"
	for _, size := range layoutSizes {
		m := sized(t, size.w, size.h)
		m.flash = long
		assertFits(t, sizeName("dashboard+flash", size.w), frame(t, m), size.w)

		m.mode = modeBrowse
		assertFits(t, sizeName("browse+flash", size.w), frame(t, m), size.w)

		// And the grid's own budget agrees with what is drawn.
		withFlash := lipgloss.Height(m.dashFooter())
		m.flash = ""
		if plain := lipgloss.Height(m.dashFooter()); withFlash < plain {
			t.Errorf("at %d columns the footer measures %d lines with a flash and %d without",
				size.w, withFlash, plain)
		}
	}
}
