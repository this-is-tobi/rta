package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/view"
)

// On a list, the page keys and the wheel moved the pane and left the cursor
// where it was: a page down put the highlighted row above the top of the pane,
// and the next action acted on it all the same — x on the grant roster revoked
// a grant nobody could see — while the next j brought the pane back to it, so
// a page could not be turned at all. They walk the cursor with the pane, as
// home and end do.

// rowOnScreen says whether the row the cursor is on is drawn in the pane.
func rowOnScreen(m Model) bool {
	return regexp.MustCompile(fmt.Sprintf(`\btask %d\b`, m.row+1)).MatchString(plain(m.viewport.View()))
}

func TestEveryPageKeyOnAListMovesTheCursorWithThePane(t *testing.T) {
	m := longList(t, 100, 20, 80)
	m.row = 40
	m.renderResult()
	if !rowOnScreen(m) {
		t.Fatal("the fixture's cursor is not on screen")
	}
	var page hintItem
	for _, it := range m.footerItems(modeResult) {
		if it.label == "page" {
			page = it
		}
	}
	if page.display == "" {
		t.Fatal("the bar of a long list has no entry for paging")
	}
	forward := map[string]bool{"pgdown": true, "space": true, " ": true, "f": true, "ctrl+d": true}
	for _, key := range page.keys {
		moved := send(t, m, key)
		switch {
		case moved.row == m.row:
			t.Errorf("%q moved the pane and left the cursor on row %d", key, moved.row)
		case forward[key] != (moved.row > m.row):
			t.Errorf("%q moved the cursor from row %d to %d, the wrong way", key, m.row, moved.row)
		}
		if !rowOnScreen(moved) {
			t.Errorf("after %q the cursor's row %d is off the pane", key, moved.row)
		}
	}
}

func TestTheWheelOnAListMovesTheCursorWithThePane(t *testing.T) {
	m := longList(t, 100, 20, 80)
	down := sent(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if down.row != wheelStep || !rowOnScreen(down) {
		t.Errorf("the wheel down put the cursor on row %d (on screen %v), want %d", down.row, rowOnScreen(down), wheelStep)
	}
	far := m
	for range 30 {
		far = sent(t, far, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	if far.row != 79 || !rowOnScreen(far) {
		t.Errorf("the wheel to the end put the cursor on row %d (on screen %v), want the last", far.row, rowOnScreen(far))
	}
	up := sent(t, far, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if up.row != 79-wheelStep || !rowOnScreen(up) {
		t.Errorf("the wheel up put the cursor on row %d (on screen %v), want %d", up.row, rowOnScreen(up), 79-wheelStep)
	}
}

// The pages end where the list does: a page past the last row is the last row.
func TestAPagePastTheEndOfAListRestsOnItsLastRow(t *testing.T) {
	m := longList(t, 100, 20, 80)
	for range 20 {
		m = send(t, m, "pgdown")
	}
	if m.row != 79 || !rowOnScreen(m) {
		t.Errorf("pages past the end left the cursor on row %d (on screen %v), want the last", m.row, rowOnScreen(m))
	}
	for range 20 {
		m = send(t, m, "pgup")
	}
	if m.row != 0 || !m.viewport.AtTop() {
		t.Errorf("pages past the top left the cursor on row %d and the pane at line %d", m.row, m.viewport.YOffset())
	}
}

// A list that fits has every row on screen and nothing to page, and the bar
// teaches no paging there: the page keys leave its cursor alone, as they
// always left its pane.
func TestTheKeysOfAPagerLeaveAListThatFitsAlone(t *testing.T) {
	m := longList(t, 100, 40, 5)
	for _, key := range []string{"pgdown", "space", "f", "ctrl+d"} {
		if got := send(t, m, key).row; got != 0 {
			t.Errorf("%q on a list that fits moved the cursor to row %d", key, got)
		}
	}
	if got := sent(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown}).row; got != 0 {
		t.Errorf("the wheel on a list that fits moved the cursor to row %d", got)
	}
}

// pageAroundList is longList's list of n rows as one section of a page, with
// more than a pane of text above it and below it.
func pageAroundList(t *testing.T, width, height, n int) Model {
	t.Helper()
	m := longList(t, width, height, n)
	tbl, _, _ := rowTable(m.result.raw)
	text := func(tag string) view.Text {
		var b strings.Builder
		for i := 1; i <= 2*height; i++ {
			fmt.Fprintf(&b, "%s line %d\n", tag, i)
		}
		return view.Text{Body: b.String()}
	}
	page := view.Sections{Items: []view.Section{
		{Title: "above", View: text("above")},
		{Title: "tasks", View: tbl},
		{Title: "below", View: text("below")},
	}}
	return sent(t, m, resultMsg{cap: m.current, view: page})
}

// A list can be one part of a page: the grant roster draws the guard and the
// roles in force above its table and a note below it. Walked to the list's
// last or first row, the cursor stayed there and the render pulled the pane
// back onto it, so what lay beyond the table could not be paged or wheeled to.
// The cursor walks while its row is on the pane and has rows left that way;
// either side of that, the pane turns as on any long answer.
func TestThePagerReachesAPageBeyondItsList(t *testing.T) {
	opened := pageAroundList(t, 100, 20, 3)
	if !opened.interactive() || !opened.scrolls() || opened.viewport.AtTop() || opened.viewport.AtBottom() {
		t.Fatalf("the fixture is not a long page open on its list: interactive %v, scrolls %v, at line %d of %d",
			opened.interactive(), opened.scrolls(), opened.viewport.YOffset(), opened.viewport.TotalLineCount())
	}
	ways := []struct {
		name     string
		down, up tea.Msg
	}{
		{"pgdown and pgup", keyMsg("pgdown"), keyMsg("pgup")},
		{"space and b", keyMsg("space"), keyMsg("b")},
		{"ctrl+d and ctrl+u", keyMsg("ctrl+d"), keyMsg("ctrl+u")},
		{"the wheel", tea.MouseWheelMsg{Button: tea.MouseWheelDown}, tea.MouseWheelMsg{Button: tea.MouseWheelUp}},
	}
	for _, w := range ways {
		bottom := opened
		for range 60 {
			bottom = sent(t, bottom, w.down)
		}
		if !bottom.viewport.AtBottom() {
			t.Errorf("%s: down never reached the end of the page, the pane is at line %d of %d",
				w.name, bottom.viewport.YOffset(), bottom.viewport.TotalLineCount())
		}
		// On the way past the list, the cursor walked it.
		if bottom.row != 2 {
			t.Errorf("%s: down passed the list with the cursor on row %d, want its last", w.name, bottom.row)
		}
		back := bottom
		for range 60 {
			back = sent(t, back, w.up)
		}
		if !back.viewport.AtTop() || back.row != 0 {
			t.Errorf("%s: back up left the pane at line %d and the cursor on row %d, want the top and the first row",
				w.name, back.viewport.YOffset(), back.row)
		}
	}
}

// Past the list, the cursor's row is off the pane, and a row's action pressed
// there would act on a row nobody can see: the press brings the row back, and
// the next one acts.
func TestARowActionOffThePaneBringsTheRowBackFirst(t *testing.T) {
	m := pageAroundList(t, 100, 20, 3)
	for range 60 {
		m = send(t, m, "pgdown")
	}
	if rowOnScreen(m) {
		t.Fatal("the fixture's cursor is still on screen at the end of the page")
	}
	m = send(t, m, "enter")
	if m.mode != modeResult || m.current.ID != "note.list" {
		t.Fatalf("enter with the row off the pane ran %s", m.current.ID)
	}
	if !rowOnScreen(m) || !strings.Contains(m.flash, "press enter again") {
		t.Errorf("enter left the row off the pane (on screen %v) and said %q", rowOnScreen(m), m.flash)
	}
	if m = send(t, m, "enter"); m.current.ID == "note.list" && m.mode == modeResult {
		t.Error("the second enter, on the row now on screen, did not act")
	}
}
