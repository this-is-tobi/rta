package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A result taller than its pane told the person nothing about where in it they
// were, and had no key for either end: the border carried the time and nothing
// else, and home and end did nothing while ctrl+d, space and the page keys
// scrolled. These tests hold the position mark in the border, the two keys, the
// bar that teaches the paging, and the one thing all three rest on: that the
// last line is on screen when the pane says it is at the bottom.

// longModel is a model on a result of n lines, one capability with no actions
// of its own, so the result is a leaf the way fs.tree opened from the catalogue
// is.
func longModel(t *testing.T, width, height, n int) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	var body strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	sized, _ := New(testRegistry(t), config.Dashboard{}, nil).Update(tea.WindowSizeMsg{Width: width, Height: height})
	shown, _ := sized.(Model).Update(resultMsg{
		cap:  plugin.Capability{ID: "demo.long", Summary: "a long answer", Safety: plugin.Read},
		view: view.Text{Body: body.String()},
	})
	return shown.(Model)
}

// send delivers one key by the name the rest of this package's tests use.
func send(t *testing.T, m Model, key string) Model {
	t.Helper()
	out, _ := m.Update(keyMsg(key))
	return out.(Model)
}

func topBorderOf(m Model) string {
	return strings.SplitN(plain(m.resultView()), "\n", 2)[0]
}

var percentMark = regexp.MustCompile(`(\d+)%`)

// markOf is the percentage the border shows, or -1 when it shows none.
func markOf(m Model) int {
	found := percentMark.FindStringSubmatch(topBorderOf(m))
	if found == nil {
		return -1
	}
	n, _ := strconv.Atoi(found[1])
	return n
}

func TestALongResultSaysHowFarDownItIsInItsBorder(t *testing.T) {
	m := longModel(t, 100, 30, 200)
	if got := markOf(m); got != 0 {
		t.Fatalf("at the top the border reads %q, want 0%%", topBorderOf(m))
	}
	paged := send(t, m, "pgdown")
	if got := markOf(paged); got < 1 || got > 99 {
		t.Errorf("a page down the border reads %q, want a percentage between 1 and 99", topBorderOf(paged))
	}
	// A line from either end is not the end: 0% and 100% are the ends and
	// nothing else, whatever rounding would have said.
	if got := markOf(send(t, m, "down")); got != 1 {
		t.Errorf("a line down the border reads %d%%, want 1%%, since it is not the top", got)
	}
	bottom := send(t, m, "end")
	if got := markOf(bottom); got != 100 {
		t.Errorf("at the bottom the border reads %q, want 100%%", topBorderOf(bottom))
	}
	if got := markOf(send(t, bottom, "up")); got != 99 {
		t.Errorf("a line above the bottom the border reads %d%%, want 99%%, since it is not the end", got)
	}
	if got := markOf(longModel(t, 100, 30, 5)); got != -1 {
		t.Errorf("a result that fits reads %q, want no percentage: there is nowhere to be", topBorderOf(longModel(t, 100, 30, 5)))
	}
}

// At 44 columns the border is where the capability's name, its summary and the
// time already compete; the mark is a percentage, three or four cells, for that
// reason, and has to leave the border as wide as the terminal.
func TestThePositionMarkFitsTheBorderOfANarrowTerminal(t *testing.T) {
	for _, width := range []int{44, 30, 24} {
		m := send(t, longModel(t, width, 14, 200), "pgdown")
		border := topBorderOf(m)
		if got := lipgloss.Width(border); got != width {
			t.Errorf("width %d: the top border is %d cells wide: %q", width, got, border)
		}
		if width >= 30 && markOf(m) < 0 {
			t.Errorf("width %d: the border says nothing of the position: %q", width, border)
		}
	}
}

// The reported behaviour: home and end did nothing. And what makes end honest is
// that the line it goes to is on the screen: with a bar that takes two lines the
// pane lost its last row to the panel's cut, and the final line of every long
// result was out of reach at 80 columns and below.
func TestHomeAndEndTakeAResultToItsEndsAndTheLastLineIsOnScreen(t *testing.T) {
	for _, size := range [][2]int{{44, 14}, {80, 24}, {100, 30}, {120, 40}, {60, 12}} {
		width, height := size[0], size[1]
		m := longModel(t, width, height, 200)
		bottom := send(t, m, "end")
		drawn := plain(bottom.resultView())
		if !bottom.viewport.AtBottom() {
			t.Errorf("%dx%d: end left the pane at line %d of %d", width, height, bottom.viewport.YOffset(), bottom.viewport.TotalLineCount())
		}
		if !strings.Contains(drawn, "│ line 200 ") {
			t.Errorf("%dx%d: at the bottom the last line is not on screen:\n%s", width, height, drawn)
		}
		if got := lines(bottom.resultView()); got > height {
			t.Errorf("%dx%d: the pane drew %d lines into %d", width, height, got, height)
		}
		top := send(t, bottom, "home")
		if !top.viewport.AtTop() {
			t.Errorf("%dx%d: home left the pane at line %d", width, height, top.viewport.YOffset())
		}
		if !strings.Contains(plain(top.resultView()), "│ line 1 ") {
			t.Errorf("%dx%d: at the top the first line is not on screen:\n%s", width, height, plain(top.resultView()))
		}
	}
}

// The pane is the room the window has less the bar, whatever the bar takes: a
// pager that scrolls by its height and draws fewer lines than that skips one at
// every page.
func TestThePaneIsAsTallAsTheRoomTheBarLeaves(t *testing.T) {
	for _, width := range []int{44, 80, 100, 160} {
		m := longModel(t, width, 24, 200)
		bar := lines(m.footerFor(modeResult))
		if want := 24 - 2 - bar; m.viewport.Height() != want {
			t.Errorf("width %d: the bar takes %d lines and the pane is %d tall, want %d", width, bar, m.viewport.Height(), want)
		}
		shown := lines(strings.TrimRight(plain(m.resultView()), "\n")) - 2 - bar
		if shown != m.viewport.Height() {
			t.Errorf("width %d: the pane draws %d lines of the %d it scrolls by", width, shown, m.viewport.Height())
		}
	}
}

func TestTheBarOfALongResultTeachesPagingAndAShortOneDoesNot(t *testing.T) {
	long := longModel(t, 100, 30, 200)
	if bar := barOf(long); !strings.Contains(bar, "pgup/pgdn page") {
		t.Errorf("the bar of a result that scrolls does not teach the page keys:\n%s", bar)
	}
	short := longModel(t, 100, 30, 5)
	if bar := barOf(short); strings.Contains(bar, "pgup/pgdn") {
		t.Errorf("a result that fits teaches paging it cannot do:\n%s", bar)
	}
	hidden := map[string]bool{}
	for _, it := range long.footerItems(modeResult) {
		for _, k := range hiddenKeys(it) {
			hidden[k] = true
		}
	}
	for _, k := range []string{"home", "end", "space", "f", "b", "u", "d", "ctrl+u", "ctrl+d"} {
		if !hidden[k] {
			t.Errorf("the overlay's list for a result that scrolls does not name %q", k)
		}
	}
}

// The converse of the advertised-keys test, for the entry that lists a pager's
// keys: each one it names moves the pane, from the middle of an answer, so that
// the overlay never lists a key that does nothing.
func TestEveryKeyTheResultTeachesForPagingScrollsThePane(t *testing.T) {
	middle := send(t, longModel(t, 100, 24, 200), "pgdown")
	var page hintItem
	for _, it := range middle.footerItems(modeResult) {
		if it.label == "page" {
			page = it
		}
	}
	if page.display == "" {
		t.Fatal("the bar of a long result has no entry for paging")
	}
	keys := append(append([]string{}, page.keys...), "home", "end")
	for _, key := range keys {
		before := middle.viewport.YOffset()
		moved := send(t, middle, key)
		if after := moved.viewport.YOffset(); after == before {
			t.Errorf("%q is taught as a way to page and left the pane at line %d", key, after)
		}
	}
}

// A list is walked by its cursor, and the rows an action acts on are the one
// under it, so end on a list is the last row and home the first; sending only
// the pane to the bottom left the cursor on a row nobody could see, and x on it
// removed that one.
func TestHomeAndEndOnAListMoveTheCursorAndThePaneTogether(t *testing.T) {
	m := longList(t, 100, 20, 80)
	if !m.interactive() {
		t.Fatal("the fixture is not a list")
	}
	last := send(t, m, "end")
	if last.row != 79 {
		t.Errorf("end put the cursor on row %d, want the last, 79", last.row)
	}
	if pane := plain(last.viewport.View()); !strings.Contains(pane, "task 80") {
		t.Errorf("the last row is not on screen after end:\n%s", pane)
	}
	first := send(t, last, "home")
	if first.row != 0 {
		t.Errorf("home put the cursor on row %d, want the first", first.row)
	}
	if !first.viewport.AtTop() {
		t.Errorf("home left the pane at line %d", first.viewport.YOffset())
	}
	if pane := plain(first.viewport.View()); !strings.Contains(pane, "task 1 ") {
		t.Errorf("the first row is not on screen after home:\n%s", pane)
	}
}

// A list that fits has a cursor and no pane to move: end still goes to the last
// row.
func TestEndOnAShortListGoesToItsLastRow(t *testing.T) {
	m := longList(t, 100, 40, 3)
	if got := send(t, m, "end").row; got != 2 {
		t.Errorf("end put the cursor on row %d of 3, want 2", got)
	}
}

// The confirmation shares the pane and its bar's height: a preview taller than
// the screen on a narrow terminal lost its last line the same way.
func TestTheLastLineOfALongConfirmationIsOnScreen(t *testing.T) {
	var done []int
	m := listResult(t, listRegistry(t, &done))
	narrow, _ := m.Update(tea.WindowSizeMsg{Width: 44, Height: 14})
	removed, _ := narrow.(Model).Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	rm := removed.(Model)
	var body strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&body, "would remove %d\n", i)
	}
	shown, _ := rm.Update(previewMsg{cap: rm.current, view: view.Text{Body: body.String()}, seq: rm.runSeq})
	conf := shown.(Model)
	if conf.mode != modeConfirm {
		t.Fatalf("the preview left the screen %v, want the confirmation", conf.mode)
	}
	for range 40 {
		conf = send(t, conf, "pgdown")
	}
	if got := plain(conf.confirmView()); !strings.Contains(got, "would remove 100") {
		t.Errorf("at the bottom of the preview its last line is not on screen:\n%s", got)
	}
}

// The docs' table of a result's keys was held to nothing, and the pager's keys
// were in none of it. The footer is what the screen answers: every key it teaches,
// the arrows aside, and every named key it lists beside them is a row of the table.
func TestTheDocsTableOfAResultsKeysHasEveryKeyTheFooterOffers(t *testing.T) {
	documented := documentedKeys(t, "## Working with results")
	for _, m := range []Model{longModel(t, 200, 20, 80), longList(t, 200, 20, 80)} {
		for _, it := range m.footerItems(modeResult) {
			for _, key := range strings.Fields(it.display) {
				if strings.ContainsAny(key, "↑↓") || documented[key] {
					continue
				}
				t.Errorf("%s offers %q (%s) and the docs' table of a result's keys does not list it", m.current.ID, key, it.label)
			}
			for _, key := range hiddenKeys(it) {
				// "pgdn" is how the display spells pgdown, and the arrows stand for
				// themselves in the footer's glyph and in the docs' prose.
				if len([]rune(key)) == 1 || key == "pgdown" || key == "up" || key == "down" || documented[key] {
					continue
				}
				t.Errorf("%s answers to %q (%s) and the docs' table of a result's keys does not list it", m.current.ID, key, it.label)
			}
		}
	}
}

// longList is a model on a note-shaped list of n rows, with a key on the row,
// so the cursor and the pane both have something to do.
func longList(t *testing.T, width, height, n int) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "note", Summary: "tasks",
		Capabilities: []plugin.Capability{
			{ID: "note.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Actions: []plugin.Action{
					{Key: "enter", Label: "show", Target: "note.show", Source: plugin.ActionRow},
					{Key: "d", Label: "done", Target: "note.show", Source: plugin.ActionRow},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					table := view.Table{Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Task"}}, Total: n}
					for i := 1; i <= n; i++ {
						table.Rows = append(table.Rows, []string{strconv.Itoa(i), fmt.Sprintf("task %d", i)})
					}
					return table, nil
				}},
			{ID: "note.show", Summary: "show", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "shown"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "note.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	c, _ := reg.Capability("note.list")
	v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	shown, _ := sized.(Model).Update(resultMsg{cap: c, view: v})
	return shown.(Model)
}
