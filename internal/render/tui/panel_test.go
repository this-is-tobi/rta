package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// assertExact verifies the panel contract: every line exactly width cells,
// total height exact when fixed. Grid layout and mouse hit-testing depend on
// this being true for arbitrary content.
func assertExact(t *testing.T, p string, width, height int) {
	t.Helper()
	lines := strings.Split(p, "\n")
	if height > 0 && len(lines) != height {
		t.Errorf("panel height = %d, want %d", len(lines), height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("line %d is %d cells, want %d: %q", i, got, width, line)
		}
	}
}

func TestPanelExactGeometry(t *testing.T) {
	long := strings.Repeat("x", 300)
	tests := []struct {
		name               string
		title, right, body string
		width, height      int
	}{
		{"plain", "title", "", "body", 40, 6},
		{"right segment", "title", "12ms", "body", 40, 6},
		{"overflowing body line", "title", "", long + "\n" + long, 40, 8},
		{"overflowing title", long, "12ms", "body", 40, 4},
		{"more lines than height", "t", "", strings.Repeat("l\n", 50), 30, 5},
		{"fewer lines than height", "t", "", "one", 30, 10},
		{"narrow", "title", "right", "body", 12, 4},
		{"styled content", "t", "", lipgloss.NewStyle().Bold(true).Render(long), 40, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := panel(panelHead{Title: tt.title, Right: tt.right}, tt.body, tt.width, tt.height, false)
			assertExact(t, p, tt.width, tt.height)
		})
	}
}

func TestPanelEmbedsTitleAndRight(t *testing.T) {
	p := panel(panelHead{Title: "sys.mem", Note: "memory in use", Right: "12ms"}, "body", 60, 5, false)
	first := strings.Split(p, "\n")[0]
	for _, want := range []string{"sys.mem", "memory in use", "12ms"} {
		if !strings.Contains(first, want) {
			t.Errorf("top border missing %q: %q", want, first)
		}
	}
}

// TestTilesOnSameRowAreUniform is the regression the panel exists for:
// content that wraps or overflows must never change a tile's footprint, so
// neighbors on one row always align.
func TestTilesOnSameRowAreUniform(t *testing.T) {
	longLine := strings.Repeat("0123456789abcdef:", 20) // unbreakable overflow
	overflowing := tile{
		cap:  plugin.Capability{ID: "x.long"},
		view: view.KeyValue{Pairs: []view.Pair{{Key: "addr", Value: longLine}, {Key: "k", Value: "v"}}},
	}
	tiny := tile{
		cap:  plugin.Capability{ID: "x.tiny"},
		view: view.Text{Body: "ok"},
	}
	const w = 49
	for _, ti := range []tile{overflowing, tiny} {
		p := renderTile(ti, w, tileHeight, false)
		assertExact(t, p, w, tileHeight)
	}
}

// fgBefore returns the foreground colour a rendered string paints text with,
// as the "38;2;r;g;b" a terminal actually receives.
//
// The colour and not the SGR sequence, because the first version of this
// compared sequences and could not fail: the title carries a reset left over
// from the border segment before it and the body key does not, so two
// identical oranges compared unequal, and painting the title with the content
// colour still passed. A bold style and a plain one carry the same colour
// under different prefixes for the same reason.
func fgBefore(t *testing.T, rendered, text string) string {
	t.Helper()
	m := regexp.MustCompile(`\x1b\[([0-9;]*)m` + regexp.QuoteMeta(text)).FindStringSubmatch(rendered)
	if m == nil {
		t.Fatalf("%q is not styled anywhere in %q", text, rendered)
	}
	fg := regexp.MustCompile(`38;2;\d+;\d+;\d+`).FindString(m[1])
	if fg == "" {
		t.Fatalf("%q is painted no colour at all (SGR %q)", text, m[1])
	}
	return fg
}

// A tile drew its own name in the same Primary its contents use for their
// keys, so `sys.mem` in the border and `used` in the body were the identical
// orange and the eye had nothing to separate the pane from what was in it.
//
// The fix is not a nicer colour, it is that panel() paints its own title:
// four call sites each rendered their own, and each independently reached for
// the content colour. Reverting either half — the Label colour or panel
// owning the title — fails here.
func TestAPanelTitleIsNotTheColourOfItsContents(t *testing.T) {
	p := renderTile(tile{
		cap:  plugin.Capability{ID: "sys.mem"},
		view: view.KeyValue{Pairs: []view.Pair{{Key: "used", Value: "4.2 GB"}}},
	}, 40, 6, false)

	title, key := fgBefore(t, p, "sys.mem"), fgBefore(t, p, "used")
	if title == key {
		t.Errorf("the tile name and its contents are both %q", title)
	}
}

// The one thing the colour must not do is impersonate a status. Tile bodies
// are full of Good/Warn/Bad, and a title that reads as one of them would be
// worse than a title that reads as a key.
func TestAPanelTitleIsNotTheColourOfAnyStatus(t *testing.T) {
	p := renderTile(tile{
		cap:  plugin.Capability{ID: "sys.mem"},
		view: view.KeyValue{Pairs: []view.Pair{{Key: "used", Value: "4.2 GB"}}},
	}, 40, 6, false)
	title := fgBefore(t, p, "sys.mem")

	for name, style := range map[string]lipgloss.Style{
		"Good": theme.GoodText, "Warn": theme.WarnText, "Bad": theme.BadText,
		"Accent": theme.AccentTxt, "Muted": theme.Subtle, "Faint": theme.Faded,
	} {
		if title == fgBefore(t, style.Render("x"), "x") {
			t.Errorf("the panel title is %s, which a tile body already uses", name)
		}
	}
}
