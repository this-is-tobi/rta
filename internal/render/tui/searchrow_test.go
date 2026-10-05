package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	if !strings.Contains(idle[1], "type to search") {
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
	if !strings.Contains(closed[1], "type to search") || !strings.HasPrefix(closed[2], "╭") {
		t.Errorf("leaving the search did not give its lines back:\n%s", strings.Join(closed, "\n"))
	}

	m = press(t, m, "down")
	if away := frameLines(m); !strings.Contains(away[1], "press / to search") {
		t.Errorf("with a tile selected the line does not say that a letter is its command: %q", away[1])
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

// The geometry the idle search line, the half-room rows and the glanced tables
// all lean on has to hold at every size, not at the three the tests above name:
// a frame never taller than the terminal, the footer kept where there is room
// for it, and no tile drawn with a border that does not close. Answers are taken
// once from the real tiles and delivered to a model at each size, so the sweep
// is of the layout and not of how fast sys.overview reads the machine.
func TestTheLandingDashboardHoldsTogetherAtEverySize(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	_, reg := realModel(t, 100, 40)
	seed := filled(t, New(reg, config.Dashboard{}, nil), 100, 40)
	answers := map[string]tileMsg{}
	for i, tl := range seed.tiles {
		if !tl.search {
			answers[tl.key()] = tileMsg{key: tl.key(), idx: i, v: seed.tileReturned(i), err: tl.err}
		}
	}
	closed := map[string]string{"╭": "╮", "│": "│", "╰": "╯"}
	for _, searching := range []bool{false, true} {
		for w := 40; w <= 200; w += 20 {
			for h := 8; h <= 60; h += 4 {
				m := New(reg, config.Dashboard{}, nil)
				next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m = next.(Model)
				for i, tl := range m.tiles {
					if msg, ok := answers[tl.key()]; ok {
						msg.idx = i
						next, _ = m.Update(msg)
						m = next.(Model)
					}
				}
				if searching {
					m = press(t, m, "/")
				}
				frame := frameLines(m)
				where := fmt.Sprintf("%dx%d searching=%v", w, h, searching)
				if len(frame) > h {
					t.Errorf("%s: the frame is %d lines", where, len(frame))
				}
				if h >= 9 && !strings.Contains(strings.Join(frame, "\n"), "quit") {
					t.Errorf("%s: the footer is gone", where)
				}
				for n, line := range frame {
					if line == "" {
						continue
					}
					first := string([]rune(line)[:1])
					want, boxed := closed[first]
					if !boxed {
						continue
					}
					runes := []rune(strings.TrimRight(line, " "))
					if last := string(runes[len(runes)-1]); last != want {
						t.Errorf("%s line %d opens with %s and ends with %s: %q", where, n, first, last, line)
						break
					}
				}
			}
		}
	}
}
