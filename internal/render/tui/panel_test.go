package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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
			p := panel(tt.title, tt.right, tt.body, tt.width, tt.height, false)
			assertExact(t, p, tt.width, tt.height)
		})
	}
}

func TestPanelEmbedsTitleAndRight(t *testing.T) {
	p := panel("sys.mem", "12ms", "body", 50, 5, false)
	first := strings.Split(p, "\n")[0]
	if !strings.Contains(first, "sys.mem") || !strings.Contains(first, "12ms") {
		t.Errorf("top border missing title/right: %q", first)
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
