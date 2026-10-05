package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

func frameLines(m Model) []string {
	return strings.Split(plain(m.View().Content), "\n")
}

// The search box with room for three results sat on the landing screen at all
// times, and on an 80x24 terminal its four empty lines were what kept the tiles
// that matter off the first screen. Idle it is one line that says it is there;
// it opens to the box when it takes the keyboard and closes when it is left.
func TestAnIdleSearchIsOneLineAndOpensToTheBox(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "short.info"}}}, nil)
	m = filled(t, m, 80, 30)

	idle := frameLines(m)
	if !strings.Contains(idle[1], "press / to search") {
		t.Errorf("the line under the header does not say how to search: %q", idle[1])
	}
	if !strings.HasPrefix(idle[2], "╭") {
		t.Errorf("the first tile does not start right under the one-line search: %q", idle[2])
	}
	if strings.Contains(strings.Join(idle, "\n"), "⌕ search") {
		t.Errorf("the idle search draws its box:\n%s", strings.Join(idle, "\n"))
	}
	if got := m.tileAt(5, 1); got != 0 {
		t.Errorf("a click on the one-line search lands on tile %d, want the search (0)", got)
	}
	if got := m.tileAt(5, 2); got != 1 {
		t.Errorf("a click on the first tile's border lands on tile %d, want 1", got)
	}

	m = press(t, m, "/")
	open := frameLines(m)
	if !strings.Contains(open[1], "⌕ search") {
		t.Errorf("the box did not open when the search took the keyboard: %q", open[1])
	}
	if !strings.HasPrefix(open[1+searchTileHeight], "╭") {
		t.Errorf("the first tile is not under the open box (%d lines): %q", searchTileHeight, open[1+searchTileHeight])
	}
	if got := m.tileAt(5, 1+searchTileHeight); got != 1 {
		t.Errorf("a click under the open box lands on tile %d, want 1", got)
	}

	m = press(t, m, "esc")
	closed := frameLines(m)
	if !strings.Contains(closed[1], "press / to search") || !strings.HasPrefix(closed[2], "╭") {
		t.Errorf("leaving the search did not give its lines back:\n%s", strings.Join(closed, "\n"))
	}
}

// The first screen is the point of the dashboard, and a short terminal is where
// a newcomer is most likely to open it. On 80x24 the machine, the agents, the
// grants and the network are all there, in that order, and none of them is
// drawn short of the "… enter for details" line that says there is more.
func TestTheLandingDashboardShowsFourTilesOnEightyByTwentyFour(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m, _ := realModel(t, 80, 24)
	m = filled(t, m, 80, 24)
	frame := frameLines(m)
	if len(frame) > 24 {
		t.Fatalf("the frame is %d lines on a 24-line terminal", len(frame))
	}
	var titles []string
	for _, line := range frame {
		for _, field := range strings.Split(line, "╭─ ") {
			if name, _, ok := strings.Cut(field, " "); ok && strings.Contains(name, ".") {
				titles = append(titles, name)
			}
		}
	}
	want := []string{"sys.overview", "agent.overview", "grant.list", "net.overview"}
	if len(titles) < len(want) {
		t.Fatalf("the first screen shows %v, want at least %v:\n%s", titles, want, strings.Join(frame, "\n"))
	}
	for i, w := range want {
		if titles[i] != w {
			t.Errorf("tile %d on the first screen is %s, want %s (all: %v)", i+1, titles[i], w, titles)
		}
	}
}

// A row has always been allowed up to tileHeight lines, so on a terminal 24
// lines high the first row took eleven of the twenty that were left and the
// second did not fit. Two rows clipped at "… enter for details" show twice the
// tiles of one drawn whole, and the clipped line is where enter goes for the
// rest.
func TestAShortTerminalGivesEachOfTwoRowsHalfTheRoom(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	tiles := make([]config.Tile, 4)
	for i := range tiles {
		tiles[i] = config.Tile{ID: "tall.info"}
	}
	m := New(heightRegistry(t), config.Dashboard{Tiles: tiles}, nil)

	m = filled(t, m, 80, 24)
	short := plain(m.View().Content)
	if got := strings.Count(short, "╭"); got != 4 {
		t.Errorf("a 24-line terminal shows %d tiles of four tall ones, want both rows (4):\n%s", got, short)
	}
	if got := strings.Count(short, "… enter for details"); got != 4 {
		t.Errorf("%d tiles say there is more behind enter, want 4:\n%s", got, short)
	}

	m = filled(t, m, 80, 60)
	tall := plain(m.View().Content)
	for i, h := range m.rowHeights() {
		if h != tileHeight {
			t.Errorf("row %d is %d lines on a 60-line terminal, want the full %d", i, h, tileHeight)
		}
	}
	if got := strings.Count(tall, "╭"); got != 4 {
		t.Errorf("a 60-line terminal shows %d tiles, want 4:\n%s", got, tall)
	}
}
