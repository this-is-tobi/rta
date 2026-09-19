package tui

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// A tile's answer sets its row's height, which sets how many rows fit — and
// the refresh was the one mutation of that arithmetic that never re-windowed.
// Scrolled to the end of six tall rows, six short answers left the window on
// the last row alone above dead space, the header still saying there was
// more above, when two rows fit.
func TestATileThatShrinksPullsTheWindowBackToFillTheScreen(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	tiles := make([]config.Tile, 6)
	for i := range tiles {
		tiles[i] = config.Tile{ID: "tall.info"}
	}
	m := New(heightRegistry(t), config.Dashboard{Tiles: tiles}, nil)
	// At sixty cells the grid is one column, so six rows of tileHeight, and
	// the height leaves room for one of them — or for two at tileMinHeight.
	m = filled(t, m, 60, 30)
	m = filled(t, m, 60, 1+lipgloss.Height(m.dashFooter())+searchTileHeight+tileHeight+2)
	m.selected = 6
	m.clampScroll()
	if m.scroll != 5 {
		t.Fatalf("scroll = %d before the answers, want the last row", m.scroll)
	}
	for i := 1; i <= 6; i++ {
		next, _ := m.Update(tileMsg{id: "tall.info", idx: i, v: view.Text{Body: "one line"}})
		m = next.(Model)
	}
	if m.scroll != 4 {
		t.Errorf("scroll = %d after every row shrank, want 4 so both rows that fit are on screen", m.scroll)
	}
	if got := plain(m.View().Content); strings.Count(got, "╭") != 3 { // the search bar and two tiles
		t.Errorf("the frame does not show the two rows that fit:\n%s", got)
	}
}

// Every frame on the way to a settled dashboard is one the renderer paints as
// a difference against the frame before, and in the alt screen it is allowed
// to move whole lines instead of repainting them. A frame that is a different
// shape from what the model says it drew — a row taller than its content, a
// tile without its border, a frame taller than the terminal — is therefore
// not a cosmetic problem: it is what makes the renderer move the wrong lines.
// So each frame, from the first sized one through every arrival in every
// order, has to agree with the geometry tileAt hit-tests with.
//
// Not a golden of the frames: the same picture would rewrite itself on every
// header, footer or theme change and prove nothing more than this does.
func TestDashboardFirstFramesAreWholeFrames(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	for _, size := range layoutSizes {
		for _, order := range [][]int{{1, 2, 3, 4, 5}, {5, 4, 3, 2, 1}, {4, 1, 5, 2, 3}} {
			m := New(heightRegistry(t), dashFixture(), nil)
			next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m = next.(Model)
			assertWholeFrame(t, fmt.Sprintf("%s before any answer", sizeName("dashboard", size.w)), m, size.w, size.h)
			for n, i := range order {
				next, _ = m.Update(tileCmd(i, m.tiles[i], nil, "", nil, config.Connection{})())
				m = next.(Model)
				name := fmt.Sprintf("%s after %d of %v", sizeName("dashboard", size.w), n+1, order)
				assertWholeFrame(t, name, m, size.w, size.h)
			}
		}
	}
}

// dashFixture is the arrangement the frame sequence needs: rows that do not
// all end up the same height, so a result landing on one row moves every row
// below it. Across layoutSizes the grid is one, two or three columns, so the
// same five tiles fall into rows of different heights at each width.
func dashFixture() config.Dashboard {
	return config.Dashboard{Tiles: []config.Tile{
		{ID: "short.info"}, {ID: "mid.info"}, {ID: "short.info"},
		{ID: "tall.info"}, {ID: "short.info"},
	}}
}

// assertWholeFrame checks a frame against the geometry the model says it drew:
// header, search bar, each visible row at its drawn height, footer, inside
// the terminal in both directions. The row heights are the ones tileAt walks,
// so a frame that disagrees with them is a frame whose clicks land on the
// wrong tile — and a frame whose rows the renderer is entitled to shift
// instead of repaint.
func assertWholeFrame(t *testing.T, name string, m Model, w, h int) {
	t.Helper()
	content := m.View().Content
	assertFits(t, name, content, w)
	if got := lipgloss.Height(content); got > h {
		t.Errorf("%s: %d lines tall, terminal is %d", name, got, h)
	}
	lines := strings.Split(plain(content), "\n")
	rows := m.tileRows()
	first, last := m.rowWindow(len(rows))
	heights := m.drawnHeights(first, last)
	want := 1 + searchTileHeight + lipgloss.Height(m.dashFooter())
	for _, rh := range heights {
		want += rh
	}
	if len(lines) != want {
		t.Fatalf("%s: frame is %d lines, the geometry says %d:\n%s", name, len(lines), want, strings.Join(lines, "\n"))
	}
	y := 1 + searchTileHeight
	for r := first; r < last; r++ {
		top, bottom := lines[y], lines[y+heights[r-first]-1]
		if n := strings.Count(top, "╭"); n != len(rows[r]) {
			t.Errorf("%s: row %d opens %d panels, holds %d tiles: %q", name, r, n, len(rows[r]), top)
		}
		if n := strings.Count(bottom, "╰"); n != len(rows[r]) {
			t.Errorf("%s: row %d closes %d panels, holds %d tiles: %q", name, r, n, len(rows[r]), bottom)
		}
		// The title on the border is what says whose frame this is. A tile's
		// body inside a neighbour's box is exactly this assertion failing.
		for _, i := range rows[r] {
			if !strings.Contains(top, m.tiles[i].cap.ID) {
				t.Errorf("%s: row %d's border does not name %s: %q", name, r, m.tiles[i].cap.ID, top)
			}
		}
		y += heights[r-first]
	}
}

// sentinel is wider than a tile's inner width in a three-column grid and
// narrower than the terminal, so a correctly drawn tile always hard-breaks it
// (cli.wrap) or truncates it (panel) and it can never reach the terminal
// whole, while a body rendered without any width — which is what panel hands
// back below ten cells, and what the dashboard asked of it on every unsized
// frame — carries it whole and inside the renderer's own clip. The grid has
// to stay three tiles wide for that: a row's last tile takes the remaining
// width, and a tile alone on its row is as wide as the terminal.
const sentinel = "ZZ0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij"

// answeringRegistry is one previewable capability per name, each answering
// with a token nothing else on the screen renders, beside the sentinel.
func answeringRegistry(t *testing.T, names ...string) *registry.Registry {
	t.Helper()
	reg := registry.New()
	for _, name := range names {
		err := reg.Register(plugin.Plugin{
			Name: name, Summary: name + " fixture",
			Capabilities: []plugin.Capability{{
				ID: name + ".overview", Summary: "what " + name + " sees", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.KeyValue{Pairs: []view.Pair{
						{Key: "who", Value: "ANSWER-" + name},
						{Key: "blob", Value: sentinel},
					}}, nil
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// The real program, real renderer, real size: whatever order the answers land
// in, no tile body ever reaches the terminal unclipped. This is the one
// screen-independent property the output stream can prove — where a line
// landed needs a terminal emulator, which is what the frame geometry test is
// for — so it is a guard on the program rather than a picture of it.
//
// One predicate over one read of the stream: teatest's output is consumed by
// whoever reads it first, so the sentinel is checked against every byte the
// program wrote, not against whatever an earlier wait left behind. The wait
// is on the answers, not on the titles, because an unchanged line is not a
// diff and would never be re-emitted.
func TestDashboardTilesArriveWithoutEscapingTheirPanels(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	names := []string{"alpha", "beta", "gamma"} // one full row at 120 cells, see sentinel
	tm := teatest.NewTestModel(t, New(answeringRegistry(t, names...), config.Dashboard{}, nil),
		teatest.WithInitialTermSize(120, 36))
	var escaped string
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		if i := bytes.Index(bts, []byte(sentinel)); i >= 0 {
			escaped = plain(string(bts[max(i-300, 0):min(i+len(sentinel)+100, len(bts))]))
			return true
		}
		for _, name := range names {
			if !bytes.Contains(bts, []byte("ANSWER-"+name)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(framePatience))
	if escaped != "" {
		t.Errorf("a tile body reached the terminal unclipped, around:\n%q", escaped)
	}
	quit(t, tm)
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
