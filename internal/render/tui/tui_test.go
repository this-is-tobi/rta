package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// TestMain forces a real $TERM before any test builds a Program.
//
// charmbracelet/ultraviolet's TerminalRenderer picks its capabilities from
// $TERM (xtermCaps, terminal_renderer.go): an unrecognized or empty value
// gets zero capabilities, including capICH, which routes every line insert
// through its IRM fallback (ansi.SetModeInsertReplace) instead of the direct
// ansi.InsertCharacter it uses everywhere else. That fallback path is where
// Review traced a real output-corruption bug (a footer line cut off
// mid-word around a stray "insert mode" toggle) — reproduced by unsetting
// $TERM, and gone the instant a real terminal type was forced, so this is
// not a bug in anything this package renders (confirmed independently:
// resultView's own returned string is correct byte-for-byte in every case)
// and not something a real user can hit — every actual terminal exports
// $TERM, and none of the ones this app is meant to run in leave it unset.
// It is, unpinned, a real source of environment-dependent flakiness in this
// package's own test suite: any sandbox or CI runner that starts a process
// with $TERM empty (this one did) would see the exact same false failure,
// on a test that has nothing to do with terminal capability negotiation.
// Forcing a realistic value once, for the whole test binary, makes every
// test in this package describe what happens in a real terminal — which is
// the only terminal this package is meant to run in — rather than whatever
// the ambient environment's $TERM happened to be.
func TestMain(m *testing.M) {
	os.Setenv("TERM", "xterm-256color")
	// A data directory of this binary's own, for the whole package.
	//
	// Tests here run real capabilities, and a run now records what it was
	// given so a later completion can offer it back (internal/recent). Without
	// this the fixtures land in the developer's own ~/.local/share/rta and
	// come back as suggestions in their real shell — which is both a dirty
	// test and a surprising thing to do to somebody's machine. Set for every
	// test rather than in the helpers, because the rule is about the package
	// and a helper is something a new test can forget to call.
	dir, err := os.MkdirTemp("", "rta-tui-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("RTA_DATA_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{
				ID: "demo.hello", Summary: "say hello", Safety: plugin.Read,
				// Counter makes each run's frame unique, so re-runs are
				// observable through bubbletea's diff renderer.
				//
				// Atomic because the dashboard refreshes its tiles as a
				// bubbletea batch, and a batch runs its commands on separate
				// goroutines — so two refreshes of this one capability land in
				// this closure at once. A plain int++ here is a data race in
				// the fixture, which -race reported as a failure of whichever
				// test happened to be running: seen once in about ten full
				// runs, and nothing to do with the code under test.
				Run: func() plugin.Handler {
					var n atomic.Int64
					return func(context.Context, plugin.Request) (view.View, error) {
						return view.Text{Body: fmt.Sprintf("HELLO-FROM-CAPABILITY run=%d", n.Add(1))}, nil
					}
				}(),
			},
			{
				ID: "demo.needy", Summary: "needs input", Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "target", Type: plugin.String, Positional: true, Required: true, Help: "t"},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: "GOT:" + req.String("target")}, nil
				},
			},
			{
				ID: "demo.boom", Summary: "destroy things", Safety: plugin.Destructive,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "BOOM-EXECUTED"}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// newTest starts the shell and navigates from the dashboard into browse,
// where most interaction tests live.
func newTest(t *testing.T) *teatest.TestModel {
	t.Helper()
	tm := newDashboard(t)
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	return tm
}

// newDashboard starts the shell on its landing dashboard.
func newDashboard(t *testing.T) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, New(testRegistry(t), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
}

// framePatience is how long a test waits for the TUI to paint what it is
// looking for.
//
// Generous, and it costs nothing when things work: every wait here returns
// as soon as the frame arrives, so this bounds only the failure case. What
// it buys is that a failure means the frame never came, rather than that the
// machine was busy — these tests drive a real Bubble Tea program through a
// real renderer, and under -race on a loaded host the first paint is not a
// few milliseconds' work. A deadline tight enough to fail on load is a test
// that reports the machine instead of the code.
const framePatience = 30 * time.Second

// waitFor blocks until one frame contains every wanted string. teatest's
// Output is a stream — separate WaitFor calls consume bytes, so strings that
// appear in the same frame must be asserted in a single call.
func waitFor(t *testing.T, tm *teatest.TestModel, wants ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		for _, want := range wants {
			if !bytes.Contains(bts, []byte(want)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(framePatience))
}

func quit(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(framePatience))
}

func TestBrowseListsCapabilities(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.hello")
	quit(t, tm)
}

func TestDashboardIsTheLanding(t *testing.T) {
	tm := newDashboard(t)
	// Fallback tiles auto-pick runnable read capabilities: demo.hello only
	// (boom is destructive, needy has required inputs) — and it runs.
	waitFor(t, tm, "dashboard", "demo.hello", "HELLO-FROM-CAPABILITY")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(framePatience))
}

func TestDashboardBKeyOpensBrowse(t *testing.T) {
	tm := newDashboard(t)
	waitFor(t, tm, "dashboard")
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "capabilities") // browse status bar
	quit(t, tm)
}

func TestDashboardEnterOpensTileDetail(t *testing.T) {
	tm := newDashboard(t)
	// Tile 0 is the search tile; tile 1 auto-picks demo.hello.
	waitFor(t, tm, "HELLO-FROM-CAPABILITY")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyRight})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Full result pane: capability header + result footer keys.
	waitFor(t, tm, "say hello", "re-run")
	// esc returns to the dashboard, not browse.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitFor(t, tm, "dashboard")
	quit(t, tm)
}

func TestDashboardLiveSearchOpensMatch(t *testing.T) {
	tm := newDashboard(t)
	waitFor(t, tm, "⌕ search")
	// Selection starts on the search bar: enter focuses it, typing filters
	// on the fly, enter opens the selected match.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "needy" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	waitFor(t, tm, "demo.needy") // live match, shown in the tile
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "target") // needy's input form opened directly
	quit(t, tm)
}

func TestDashboardSearchTile(t *testing.T) {
	tm := newDashboard(t)
	// The search bar shows the registry inventory when idle.
	waitFor(t, tm, "search", "3 capabilities")
	quit(t, tm)
}

func TestBrowseEscReturnsToDashboard(t *testing.T) {
	// The first wait is for the browse table's own column heading, not for
	// "capabilities" — which the *dashboard* also prints, in its search bar
	// ("3 capabilities"). Waiting on a string both screens contain meant that
	// whenever the `b` keypress had not been handled yet, the wait matched the
	// dashboard nobody had left, Esc then did nothing, and the last wait sat
	// there for a header that was already correct and so was never repainted
	// — bubbletea's renderer emits diffs, and an unchanged line is not a
	// diff. It failed roughly one run in ten, and raising the patience only
	// made it fail slower.
	tm := newTest(t)
	waitFor(t, tm, headID)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitFor(t, tm, "dashboard")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(framePatience))
}

func TestDashboardExcludesUnsafeTiles(t *testing.T) {
	tiles := buildTiles(testRegistry(t), config.Dashboard{})
	sawSearch := false
	for _, ti := range tiles {
		if ti.search {
			sawSearch = true
			continue // static inventory tile, runs nothing
		}
		if ti.cap.Safety != plugin.Read {
			t.Errorf("non-read tile %s", ti.cap.ID)
		}
		if formNeeded(ti.cap) {
			t.Errorf("tile %s needs inputs", ti.cap.ID)
		}
	}
	if !sawSearch {
		t.Error("search tile missing")
	}
}

// Items are sorted by ID: demo.boom, demo.hello, demo.needy.

func TestRunCapabilityShowsResult(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.hello")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // -> demo.hello
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "HELLO-FROM-CAPABILITY")
	quit(t, tm)
}

func TestEscReturnsToBrowse(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.hello")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // -> demo.hello
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "HELLO-FROM-CAPABILITY")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitFor(t, tm, "capabilities")
	quit(t, tm)
}

func TestFormCollectsInputAndRuns(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.needy")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // -> demo.hello
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // -> demo.needy
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "target") // form field visible
	for _, r := range "wow" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // submit field -> form completes
	waitFor(t, tm, "GOT:wow")                    // capability ran with typed value
	quit(t, tm)
}

func TestFormEscCancels(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.needy")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "target")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitFor(t, tm, "capabilities") // back to browse
	quit(t, tm)
}

func TestDestructiveRequiresConfirm(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.boom")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // boom is first (sorted)
	waitFor(t, tm, "destructive — run it?")
	// Decline: confirm defaults to the negative option.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "capabilities") // back to browse, nothing ran
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(framePatience))
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(tm.FinalOutput(t))
	if bytes.Contains(buf.Bytes(), []byte("BOOM-EXECUTED")) {
		t.Error("declined destructive capability executed")
	}
}

func TestDestructiveConfirmedRuns(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.boom")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "destructive — run it?")
	// Approve: 'y' selects the affirmative in huh confirms.
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "BOOM-EXECUTED")
	quit(t, tm)
}

func TestResultFooterOffersRerunAndCopy(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.hello")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // -> demo.hello
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "HELLO-FROM-CAPABILITY", "re-run", "copy json")
	quit(t, tm)
}

func TestCopyShowsFlash(t *testing.T) {
	tm := newTest(t)
	waitFor(t, tm, "demo.hello")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "HELLO-FROM-CAPABILITY")
	tm.Send(tea.KeyPressMsg{Code: 'y'})
	waitFor(t, tm, "copied as JSON")
	quit(t, tm)
}

// TestRerunTriggersExecution asserts re-run at the model level: the terminal
// stream is diff-rendered (only changed cells are emitted), so substring
// assertions on re-painted frames are unreliable by design.
func TestRerunTriggersExecution(t *testing.T) {
	runs := 0
	c := plugin.Capability{
		ID: "demo.hello", Summary: "say hello", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			runs++
			return view.Text{Body: "X"}, nil
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{cap: c, view: view.Text{Body: "X"}})

	rerun, cmd := withResult.(Model).Update(tea.KeyPressMsg{Code: 'r'})
	if rerun.(Model).mode != modeRunning || cmd == nil {
		t.Fatal("r did not restart the capability")
	}
	// Drain the batch: one of its commands is the actual run.
	drain(t, cmd)
	if runs != 1 {
		t.Fatalf("capability ran %d times, want 1", runs)
	}
}

// drain executes a Cmd tree until every leaf message has been produced.
func drain(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			drain(t, sub)
		}
	default:
	}
}

func TestFormValueConversion(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "count", Type: plugin.Int, Default: 3},
			{Name: "name", Type: plugin.String},
			{Name: "tags", Type: plugin.StringSlice},
		},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	*cf.bindings["count"] = "7"
	*cf.bindings["name"] = " padded "
	*cf.bindings["tags"] = "a, b ,c"
	got := cf.values()
	if got["count"] != 7 {
		t.Errorf("count = %v", got["count"])
	}
	if got["name"] != "padded" {
		t.Errorf("name = %v", got["name"])
	}
	tags := got["tags"].([]string)
	if len(tags) != 3 || tags[1] != "b" {
		t.Errorf("tags = %v", tags)
	}
	// An emptied box answers nothing — plugin.Resolve lays the declared
	// default at its own layer when the request is built, so handing it back
	// here would only ever *change* something when a layer that should
	// outrank it (a profile, a forward's endpoint, config) was about to.
	*cf.bindings["count"] = ""
	if v, given := cf.values()["count"]; given {
		t.Errorf("an emptied box came back as a caller value: %v", v)
	}
	// And a box still showing the declared default is a display, not an
	// answer, for the same reason.
	fresh := newCapForm(c, c.Inputs, nil, true, nil)
	if v, given := fresh.values()["count"]; given {
		t.Errorf("an untouched declared default came back as a caller value: %v", v)
	}
}

// todoRegistry mimics the todo plugin's shape so the static tile-action
// specs resolve without importing the real built-in.
func todoRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	noop := func(context.Context, plugin.Request) (view.View, error) {
		return view.Text{Body: "ok"}, nil
	}
	err := reg.Register(plugin.Plugin{
		Name: "todo", Summary: "tasks",
		Capabilities: []plugin.Capability{
			{ID: "todo.list", Summary: "list", Safety: plugin.Read, Idempotent: true, Run: noop},
			{ID: "todo.add", Summary: "add", Safety: plugin.Write,
				Inputs: []plugin.Field{
					{Name: "title", Type: plugin.String, Positional: true, Required: true, Help: "t"},
					{Name: "body", Type: plugin.Text, Help: "b"},
				}, Run: noop},
			{ID: "todo.edit", Summary: "edit", Safety: plugin.Write,
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"},
				}, Run: noop},
			{ID: "todo.done", Summary: "done", Safety: plugin.Write,
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"},
				}, Run: noop},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestDashboardTileActionsOpenForm drives the todo tile's `a` shortcut at the
// model level: it must open the add form with the dashboard as origin, and
// esc must land back on the dashboard.
func TestDashboardTileActionsOpenForm(t *testing.T) {
	m := New(todoRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "todo.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	// Tile 0 is search; tile 1 is todo.list with actions attached.
	sel, _ := sized.(Model).Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := sel.(Model).tiles[1].actions; len(got) != 3 {
		t.Fatalf("todo tile actions = %d, want 3", len(got))
	}

	formed, _ := sel.(Model).Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	fm := formed.(Model)
	if fm.mode != modeForm || fm.current.ID != "todo.add" || fm.origin != modeDashboard {
		t.Fatalf("a: mode=%v current=%s origin=%v", fm.mode, fm.current.ID, fm.origin)
	}

	back, _ := fm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back.(Model).mode != modeDashboard {
		t.Fatalf("esc from action form must return to dashboard, got %v", back.(Model).mode)
	}
}

// TestDashboardActionKeysDoNotFireOnOtherTiles: the search tile has no
// actions, so `a` is inert there.
func TestDashboardActionKeysDoNotFireOnOtherTiles(t *testing.T) {
	m := New(todoRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "todo.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	same, _ := sized.(Model).Update(tea.KeyPressMsg{Code: 'a', Text: "a"}) // selection on search tile
	if same.(Model).mode != modeDashboard {
		t.Fatalf("a on the search tile changed mode to %v", same.(Model).mode)
	}
}

// TestDashboardScrollFollowsSelection: on a short terminal the grid windows
// itself and the selection drags the window along.
func TestDashboardScrollFollowsSelection(t *testing.T) {
	reg := todoRegistry(t)
	tiles := []config.Tile{{ID: "todo.list"}, {ID: "todo.list"}, {ID: "todo.list"}, {ID: "todo.list"}, {ID: "todo.list"}}
	m := New(reg, config.Dashboard{Tiles: tiles}, nil)
	// 60 cols -> 1 column grid. todo.list's content here is one line ("ok"),
	// so each row shrinks to tileMinHeight; height 15 -> one visible tile row
	// at that floor (avail 7, one 6-row band fits and a second does not).
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	sm := sized.(Model)
	if sm.dashRowsVisible() != 1 {
		t.Fatalf("visible rows = %d, want 1", sm.dashRowsVisible())
	}
	if sm.scroll != 0 {
		t.Fatalf("initial scroll = %d", sm.scroll)
	}

	cur := sm
	for i := 0; i < 3; i++ {
		next, _ := cur.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		cur = next.(Model)
	}
	// Tile 3 sits on grid row 2 (the search bar above never scrolls).
	if cur.selected != 3 || cur.scroll != 2 {
		t.Fatalf("selected=%d scroll=%d, want 3/2", cur.selected, cur.scroll)
	}
	if frame := cur.dashboardView(); !strings.Contains(frame, "more") {
		t.Error("scrolled dashboard missing the more marker")
	}

	// Back to the top: the window follows upward too.
	for i := 0; i < 3; i++ {
		next, _ := cur.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		cur = next.(Model)
	}
	if cur.scroll != 0 {
		t.Fatalf("scroll after returning to top = %d", cur.scroll)
	}
}

// TestPrefillTwoStageForm drives the full edit-in-place flow through teatest:
// the identity stage first, then the edit stage seeded with current values —
// the form shows the old content, like editing an issue.
func TestPrefillTwoStageForm(t *testing.T) {
	reg := testRegistry(t)
	err := reg.Register(plugin.Plugin{
		Name: "rec", Summary: "records",
		Capabilities: []plugin.Capability{{
			ID: "rec.edit", Summary: "edit a record", Safety: plugin.Write, Idempotent: true,
			Inputs: []plugin.Field{
				{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "record id"},
				{Name: "title", Type: plugin.String, Help: "new title"},
			},
			Prefill: func(_ context.Context, req plugin.Request) (map[string]any, error) {
				return map[string]any{"title": fmt.Sprintf("OLD-%d", req.Int("id"))}, nil
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: fmt.Sprintf("EDITED %d -> %s", req.Int("id"), req.String("title"))}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "rec.edit")
	// Sorted: demo.boom, demo.hello, demo.needy, rec.edit.
	for i := 0; i < 3; i++ {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "record id") // stage 1: identity only
	tm.Send(tea.KeyPressMsg{Code: '7', Text: "7"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Stage 2 opens seeded with the record's current title.
	waitFor(t, tm, "OLD-7")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // accept prefilled value
	// The capability ran with the merged identity + (unchanged) content.
	waitFor(t, tm, "EDITED 7 -> OLD-7")
	quit(t, tm)
}

// listRegistry builds a todo-shaped plugin whose list returns a live table,
// so the interactive-list machinery is exercised against real specs.
func listRegistry(t *testing.T, doneLog *[]int) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "todo", Summary: "tasks",
		Capabilities: []plugin.Capability{
			{ID: "todo.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{
						Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Task"}},
						Rows:    [][]string{{"1", "first"}, {"2", "second"}},
						Total:   2,
					}, nil
				}},
			{ID: "todo.show", Summary: "show", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: fmt.Sprintf("SHOW-%d", req.Int("id"))}, nil
				}},
			{ID: "todo.add", Summary: "add", Safety: plugin.Write,
				Inputs: []plugin.Field{{Name: "title", Type: plugin.String, Positional: true, Required: true, Help: "t"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "added"}, nil
				}},
			{ID: "todo.edit", Summary: "edit", Safety: plugin.Write, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"},
					{Name: "title", Type: plugin.String, Help: "t"},
				},
				Prefill: func(_ context.Context, req plugin.Request) (map[string]any, error) {
					return map[string]any{"title": fmt.Sprintf("CURRENT-%d", req.Int("id"))}, nil
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "edited"}, nil
				}},
			{ID: "todo.done", Summary: "done", Safety: plugin.Write, Idempotent: true,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					*doneLog = append(*doneLog, req.Int("id"))
					return view.Text{Body: fmt.Sprintf("done: %d", req.Int("id"))}, nil
				}},
			{ID: "todo.rm", Summary: "remove", Safety: plugin.Destructive,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "removed"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// listResult drives the model to an interactive todo.list result.
func listResult(t *testing.T, reg *registry.Registry) Model {
	t.Helper()
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "todo.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	c, _ := reg.Capability("todo.list")
	v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	withRes, _ := sized.(Model).Update(resultMsg{cap: c, view: v})
	rm := withRes.(Model)
	if !rm.interactive() {
		t.Fatal("todo.list result did not become an interactive list")
	}
	return rm
}

// TestListRowNavigationAndDirectAction: ↑↓ move the row; `d` runs todo.done
// with the selected row's id and schedules a list refresh.
func TestListRowNavigationAndDirectAction(t *testing.T) {
	var doneLog []int
	m := listResult(t, listRegistry(t, &doneLog))

	moved, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := moved.(Model).row; got != 1 {
		t.Fatalf("row after down = %d", got)
	}

	acted, cmd := moved.(Model).Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	am := acted.(Model)
	if am.mode != modeRunning || am.current.ID != "todo.done" || !am.refreshPending {
		t.Fatalf("d: mode=%v current=%s refresh=%v", am.mode, am.current.ID, am.refreshPending)
	}
	drain(t, cmd)
	if len(doneLog) != 1 || doneLog[0] != 2 {
		t.Fatalf("done ran with %v, want [2] (row 2's id)", doneLog)
	}
}

// TestListEnterShowsRowAndEscReturns: enter opens show for the selected row;
// esc from that detail re-runs the list.
func TestListEnterShowsRowAndEscReturns(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := listResult(t, reg)

	shown, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sm := shown.(Model)
	if sm.mode != modeRunning || sm.current.ID != "todo.show" {
		t.Fatalf("enter: mode=%v current=%s", sm.mode, sm.current.ID)
	}
	if sm.refreshPending {
		t.Fatal("show is a read: it must not schedule a refresh")
	}
	// Deliver show's result, then esc must re-open the list.
	c, _ := reg.Capability("todo.show")
	withShow, _ := sm.Update(resultMsg{cap: c, view: view.Text{Body: "SHOW-1"}})
	back, cmd2 := withShow.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	bm := back.(Model)
	if bm.mode != modeRunning || bm.current.ID != "todo.list" {
		t.Fatalf("esc from row detail: mode=%v current=%s", bm.mode, bm.current.ID)
	}
	_ = cmd
	_ = cmd2
}

// TestListEditOpensPrefilledForm: `e` skips the identity stage (the row is
// the identity) and opens the form seeded with current content.
func TestListEditOpensPrefilledForm(t *testing.T) {
	var doneLog []int
	m := listResult(t, listRegistry(t, &doneLog))

	edited, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	em := edited.(Model)
	if em.mode != modeForm || em.current.ID != "todo.edit" {
		t.Fatalf("u: mode=%v current=%s", em.mode, em.current.ID)
	}
	if em.form.base["id"] != 1 {
		t.Fatalf("form base = %v, want id 1 from the selected row", em.form.base)
	}
	if got := *em.form.bindings["title"]; got != "CURRENT-1" {
		t.Fatalf("edit form not prefilled: title = %q", got)
	}
}

// resultKeys checks capActionSpecs before its own generic "edit inputs"
// case, so a capability declaring its own "e" action used to make the
// generic case unreachable — todo.list's own action is "u" precisely so
// both stay reachable. This is the other half of
// TestListEditOpensPrefilledForm: that test is "u" reaches the row action;
// this one is "e" now reaches the generic form instead of being swallowed
// by it.
func TestGenericEditInputsIsReachableOnACapabilityWithItsOwnAction(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "todo", Summary: "tasks",
		Capabilities: []plugin.Capability{
			{ID: "todo.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{{Name: "all", Type: plugin.Bool, Help: "show done too"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{
						Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Task"}},
						Rows:    [][]string{{"1", "first"}},
						Total:   1,
					}, nil
				}},
			{ID: "todo.edit", Summary: "edit", Safety: plugin.Write, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "i"},
					{Name: "title", Type: plugin.String, Help: "t"},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "edited"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := listResult(t, reg)

	// "u" still reaches the row action.
	viaU, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if um := viaU.(Model); um.mode != modeForm || um.current.ID != "todo.edit" {
		t.Fatalf("u: mode=%v current=%s, want todo.edit", um.mode, um.current.ID)
	}

	// "e" now reaches the generic edit-inputs case instead of being
	// swallowed by capActionSpecs — before the fix, todo.list declaring no
	// "e" entry of its own meant this already worked; the real regression
	// coverage is that renaming todo.list's OWN action away from "e" is
	// what makes this assertion meaningful for capabilities that used to
	// collide (todo.list among them, in production).
	viaE, _ := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	em := viaE.(Model)
	if em.mode != modeForm || em.current.ID != "todo.list" {
		t.Fatalf("e: mode=%v current=%s, want the generic edit-inputs form on todo.list itself", em.mode, em.current.ID)
	}
}

// A dashboard config can watch the same capability twice against two
// different `with:` targets (buildTiles does not deduplicate by ID) —
// reproduced exactly as the reviewer who found this did: two tiles, same
// capability, different host. tileIndexFor used to match by capability ID
// alone, always finding the first of the two, so the second tile's own
// results were silently painted into the first tile's slot.
func TestTileIndexForDisambiguatesTwoTilesOfTheSameCapability(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "obj", Summary: "object store",
		Capabilities: []plugin.Capability{
			{ID: "obj.get", Summary: "get", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{{Name: "host", Type: plugin.String}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dash := config.Dashboard{Tiles: []config.Tile{
		{ID: "obj.get", With: map[string]any{"host": "alpha"}},
		{ID: "obj.get", With: map[string]any{"host": "beta"}},
	}}
	m := New(reg, dash, nil)

	// index 0 is the always-prepended search tile, so alpha is 1 and beta is 2.
	if m.tiles[1].values["host"] != "alpha" || m.tiles[2].values["host"] != "beta" {
		t.Fatalf("tiles = %+v, want alpha at 1 and beta at 2", m.tiles)
	}

	betaResult := tileMsg{id: "obj.get", idx: 2, v: view.Text{Body: "beta's own result"}}
	if got := m.tileIndexFor(betaResult); got != 2 {
		t.Fatalf("tileIndexFor(beta's result) = %d, want 2 (beta's own slot), not alpha's", got)
	}
	alphaResult := tileMsg{id: "obj.get", idx: 1, v: view.Text{Body: "alpha's own result"}}
	if got := m.tileIndexFor(alphaResult); got != 1 {
		t.Fatalf("tileIndexFor(alpha's result) = %d, want 1", got)
	}
}

// TestListRemoveAsksConfirmation: `x` on a row opens a confirm-only form for
// the destructive remove; declining returns to the list.
func TestListRemoveAsksConfirmation(t *testing.T) {
	var doneLog []int
	m := listResult(t, listRegistry(t, &doneLog))

	removed, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	rm := removed.(Model)
	if rm.mode != modeForm || rm.current.ID != "todo.rm" || rm.form.confirm == nil {
		t.Fatalf("x: mode=%v current=%s confirm=%v", rm.mode, rm.current.ID, rm.form.confirm)
	}
	back, _ := rm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	bm := back.(Model)
	if bm.mode != modeRunning || bm.current.ID != "todo.list" {
		t.Fatalf("esc from confirm: mode=%v current=%s", bm.mode, bm.current.ID)
	}
}

// detailPage drives the model from an interactive list into the detail page
// of the selected row.
func detailPage(t *testing.T, reg *registry.Registry, m Model) Model {
	t.Helper()
	shown, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	c, _ := reg.Capability("todo.show")
	withShow, _ := shown.(Model).Update(resultMsg{cap: c, view: view.Text{Body: "SHOW-1"}})
	dm := withShow.(Model)
	if !dm.atTop() || dm.current.ID != "todo.show" {
		t.Fatalf("detail page is not actionable: current=%s trail=%d", dm.current.ID, len(dm.trail))
	}
	return dm
}

// TestDetailPageActsOnItsOwnSubject: a record's page knows which record it is
// about, so its actions need no row selection — the same edit/done/remove
// keys work from the page as from the list.
func TestDetailPageActsOnItsOwnSubject(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := detailPage(t, reg, listResult(t, reg))

	acted, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	am := acted.(Model)
	if am.mode != modeRunning || am.current.ID != "todo.done" || !am.refreshPending {
		t.Fatalf("d on detail: mode=%v current=%s refresh=%v", am.mode, am.current.ID, am.refreshPending)
	}
	drain(t, cmd)
	if len(doneLog) != 1 || doneLog[0] != 1 {
		t.Fatalf("done ran with %v, want [1] (the page's own subject)", doneLog)
	}
}

// TestDetailPageEditIsPrefilled: `e` on a record's page opens its edit form
// seeded with current content, with no identity stage to type through.
func TestDetailPageEditIsPrefilled(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := detailPage(t, reg, listResult(t, reg))

	edited, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	em := edited.(Model)
	if em.mode != modeForm || em.current.ID != "todo.edit" {
		t.Fatalf("u on detail: mode=%v current=%s", em.mode, em.current.ID)
	}
	if em.form.base["id"] != 1 {
		t.Fatalf("form base = %v, want the page's own id", em.form.base)
	}
	if got := *em.form.bindings["title"]; got != "CURRENT-1" {
		t.Fatalf("edit form not prefilled: title = %q", got)
	}
}

// TestDetailPageEditReturnsToTheSamePage: a non-destructive action reloads
// the page it was launched from, so the edit is visible immediately.
func TestDetailPageEditReturnsToTheSamePage(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := detailPage(t, reg, listResult(t, reg))

	acted, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	done, _ := reg.Capability("todo.done")
	back, _ := acted.(Model).Update(resultMsg{cap: done, view: view.Text{Body: "done: 1"}})
	bm := back.(Model)
	if bm.mode != modeRunning || bm.current.ID != "todo.show" {
		t.Fatalf("after done: mode=%v current=%s, want the page reloaded", bm.mode, bm.current.ID)
	}
	if bm.flash == "" {
		t.Error("the outcome of the action should be flashed")
	}
}

// TestDetailPageRemoveReturnsToTheList: removing the record the page is about
// destroys the page — reloading it would only show "no such task", so the
// trail unwinds one level instead.
func TestDetailPageRemoveReturnsToTheList(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := detailPage(t, reg, listResult(t, reg))

	removed, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	rm := removed.(Model)
	if rm.mode != modeForm || rm.current.ID != "todo.rm" {
		t.Fatalf("x on detail: mode=%v current=%s", rm.mode, rm.current.ID)
	}
	if !rm.subjectGone {
		t.Fatal("removing the page's own subject must be recorded as destroying it")
	}
	rmCap, _ := reg.Capability("todo.rm")
	back, _ := rm.Update(resultMsg{cap: rmCap, view: view.Text{Body: "removed"}})
	bm := back.(Model)
	if bm.mode != modeRunning || bm.current.ID != "todo.list" {
		t.Fatalf("after remove: mode=%v current=%s, want the list", bm.mode, bm.current.ID)
	}
	if len(bm.trail) != 1 {
		t.Fatalf("trail = %d entries, want just the list", len(bm.trail))
	}
}

// TestEmptyListStillOffersAdd: an empty list renders as a friendly sentence
// rather than a table. Its actions must survive that — otherwise a fresh
// list is the one list you can never add to.
func TestEmptyListStillOffersAdd(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "todo.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	c, _ := reg.Capability("todo.list")
	empty, _ := sized.(Model).Update(resultMsg{cap: c, view: view.Text{Body: "Nothing to do"}})
	em := empty.(Model)

	if em.interactive() {
		t.Error("no rows: there is nothing to navigate")
	}
	if !em.atTop() {
		t.Fatal("an empty list is still a view you can act from")
	}
	added, _ := em.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if am := added.(Model); am.mode != modeForm || am.current.ID != "todo.add" {
		t.Fatalf("a on empty list: mode=%v current=%s", am.mode, am.current.ID)
	}
	// A row action has no row to act on and must stay inert.
	inert, _ := em.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if got := inert.(Model).current.ID; got == "todo.done" {
		t.Error("a row action fired with no row selected")
	}
}

// detailRegistry exposes a capability that reports whether it was asked for
// its detailed view.
func detailRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "deep", Summary: "deep",
		Capabilities: []plugin.Capability{{
			ID: "deep.thing", Summary: "a thing", Safety: plugin.Read, Idempotent: true,
			Detailed: true,
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				if req.Bool("detail") {
					return view.Text{Body: "FULL-REPORT"}, nil
				}
				return view.Text{Body: "COMPACT"}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestTilePreviewIsCompactDetailIsFull: the dashboard tile shows the compact
// view; opening it dedicates the page, so the capability is asked for its
// full report.
func TestTilePreviewIsCompactDetailIsFull(t *testing.T) {
	reg := detailRegistry(t)
	tiles := buildTiles(reg, config.Dashboard{Tiles: []config.Tile{{ID: "deep.thing"}}})
	// Tile 1 is the capability (0 is the search bar); its refresh is compact.
	msg := tileCmd(1, tiles[1], nil, "", nil, config.Connection{})().(tileMsg)
	if body := msg.v.(view.Text).Body; body != "COMPACT" {
		t.Errorf("tile preview = %q, want COMPACT", body)
	}

	// Opening the tile runs the same capability for the whole screen.
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "deep.thing"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sel, _ := sized.(Model).Update(tea.KeyPressMsg{Code: tea.KeyDown}) // -> tile 1
	opened, cmd := sel.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if opened.(Model).mode != modeRunning {
		t.Fatalf("enter did not run the tile: %v", opened.(Model).mode)
	}
	var got string
	collect(t, cmd, func(msg tea.Msg) {
		if r, ok := msg.(resultMsg); ok {
			got = r.view.(view.Text).Body
		}
	})
	if got != "FULL-REPORT" {
		t.Errorf("opened tile = %q, want FULL-REPORT", got)
	}
}

// collect walks a Cmd tree and hands every produced message to fn.
func collect(t *testing.T, cmd tea.Cmd, fn func(tea.Msg)) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			collect(t, sub, fn)
		}
	default:
		fn(msg)
	}
}

// TestStringSliceDefaultRendersJoined: a []string default (e.g. a Prefill
// result handing back a task's current tags) must render as "a, b" in the
// bound input, not Go's fmt.Sprint("[a b]") — which would both look wrong
// and, if resubmitted unchanged, corrupt the value into a single garbage tag.
func TestStringSliceDefaultRendersJoined(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "tags", Type: plugin.StringSlice}},
	}
	cf := newCapForm(c, c.Inputs, map[string]any{"tags": []string{"backend", "urgent"}}, true, nil)
	if got := *cf.bindings["tags"]; got != "backend, urgent" {
		t.Fatalf("bound default = %q, want %q", got, "backend, urgent")
	}
	// Submitting unchanged must round-trip to the same two tags, not a
	// single "[backend urgent]" garbage entry.
	got := cf.values()["tags"].([]string)
	if len(got) != 2 || got[0] != "backend" || got[1] != "urgent" {
		t.Fatalf("round-tripped tags = %v", got)
	}
}

// TestSecretFieldBindsAndRoundTrips: a Secret field must behave exactly like
// String for typing and submission — masking is a huh EchoMode display
// concern (verified by inspection: form.go passes EchoModePassword), not a
// data-shape one, so what's testable here is that the value round-trips.
func TestSecretFieldBindsAndRoundTrips(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "passphrase", Type: plugin.Secret}},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	if _, ok := cf.bindings["passphrase"]; !ok {
		t.Fatal("Secret field did not get a binding")
	}
	*cf.bindings["passphrase"] = "hunter2"
	if got := cf.values()["passphrase"]; got != "hunter2" {
		t.Errorf("passphrase = %v, want hunter2", got)
	}
}

// --- Fitting the terminal -------------------------------------------------

// wordy is a capability with more inputs than a short terminal has rows —
// kv.set asks eight questions, and a laptop with a split terminal has fewer
// than eight lines to spare.
func wordy() plugin.Capability {
	c := plugin.Capability{
		ID: "demo.wordy", Summary: "asks a lot", Safety: plugin.Destructive,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
	for _, name := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		c.Inputs = append(c.Inputs, plugin.Field{Name: name, Type: plugin.String, Help: "the " + name})
	}
	return c
}

// lines counts rendered rows the way a terminal does.
func lines(s string) int { return len(strings.Split(strings.TrimRight(s, "\n"), "\n")) }

// The property: a form never renders past the bottom of the screen. Without
// a height the last fields — including the destructive confirmation, the one
// nobody may miss — are simply painted off the terminal.
func TestFormFitsTheTerminal(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	opened, _ := sized.(Model).open(wordy())
	om := opened.(Model)
	if om.mode != modeForm {
		t.Fatalf("mode = %v, want a form", om.mode)
	}
	if got := lines(om.formView()); got > 18 {
		t.Errorf("form rendered %d lines into an 18-line terminal", got)
	}
}

// …and it follows a resize, rather than keeping the height of a window that
// no longer exists.
func TestFormRefitsOnResize(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	opened, _ := sized.(Model).open(wordy())

	shrunk, _ := opened.(Model).Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	if got := lines(shrunk.(Model).formView()); got > 14 {
		t.Errorf("after shrinking, the form rendered %d lines into 14", got)
	}
}

// The result pane is a viewport, so long output scrolls rather than spilling.
func TestLongResultFitsTheTerminal(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	long := view.Text{Body: strings.Repeat("a line of output\n", 200)}
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: plugin.Capability{ID: "demo.long", Summary: "s", Safety: plugin.Read}, view: long,
	})
	if got := lines(withResult.(Model).resultView()); got > 20 {
		t.Errorf("result pane rendered %d lines into a 20-line terminal", got)
	}
}

// The wheel scrolls the pane under it: people try it before they look for a
// key, and a pane that ignores it reads as stuck.
func TestWheelScrollsTheResult(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	long := view.Text{Body: strings.Repeat("a line of output\n", 200)}
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: plugin.Capability{ID: "demo.long", Summary: "s", Safety: plugin.Read}, view: long,
	})
	rm := withResult.(Model)
	before := rm.viewport.YOffset()
	scrolled, _ := rm.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	after := scrolled.(Model)
	if got := after.viewport.YOffset(); got <= before {
		t.Errorf("wheel down left the viewport at %d", got)
	}
}

// A closed set is a picker, so what comes back is one of the options — and
// leaving it alone still yields the declared default.
func TestClosedSetBindsThroughThePicker(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "mode", Type: plugin.String, Default: "fast", Options: []string{"fast", "slow"}},
			{Name: "kinds", Type: plugin.StringSlice, Options: []string{"a", "b", "c"}},
		},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	if got := cf.values()["mode"]; got != "fast" {
		t.Errorf("untouched picker = %v, want the default", got)
	}
	*cf.bindings["mode"] = "slow"
	*cf.slices["kinds"] = []string{"a", "c"}
	got := cf.values()
	if got["mode"] != "slow" {
		t.Errorf("mode = %v", got["mode"])
	}
	if kinds, _ := got["kinds"].([]string); len(kinds) != 2 || kinds[0] != "a" {
		t.Errorf("kinds = %v", got["kinds"])
	}
}

// A path field is a text input that binds like any other — the completion is
// live on top of it, not instead of it, so a path nobody suggested still
// submits. An output file that already exists is not the case that matters.
func TestPathFieldBindsAndKeepsWhatWasTyped(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "out", Type: plugin.Path, Help: "where to write it"}},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	binding, ok := cf.bindings["out"]
	if !ok {
		t.Fatal("path field bound nothing")
	}
	*binding = "~/does/not/exist/yet.pem"
	if got := cf.values()["out"]; got != "~/does/not/exist/yet.pem" {
		t.Errorf("out = %v, want what was typed", got)
	}
}

// A row names one record, so a row action must work even when the capability
// itself allows being called without one (`grant revoke --all`).
func TestRowActionOnAnOptionalPositional(t *testing.T) {
	var got map[string]any
	revoke := plugin.Capability{
		ID: "demo.revoke", Summary: "revoke", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String, Positional: true},
			{Name: "all", Type: plugin.Bool},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			got = map[string]any{"target": req.String("target")}
			return view.Text{Body: "revoked"}, nil
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.row = 1
	tbl := view.Table{
		Columns: []view.Column{{Name: "Capability"}},
		Rows:    [][]string{{"kv.get"}, {"todo.rm"}},
	}
	acted, _ := sm.runAction(capAction{key: "x", label: "revoke", cap: revoke, src: srcRow}, tbl)
	am := acted.(Model)
	// Other fields remain, so it opens a form seeded with the row's identity
	// rather than firing blind.
	if am.mode != modeForm {
		t.Fatalf("mode = %v, want a seeded form", am.mode)
	}
	if am.form.base["target"] != "todo.rm" {
		t.Errorf("form seeded with %v, want the selected row", am.form.base)
	}
	_ = got
}

// A refresh chain from a visit the user has left must not outlive it.
//
// Every return to the dashboard restarts refreshing, since a tile can be
// stale after any amount of time away — but without a generation to check,
// each return left its predecessor's timer armed and re-arming itself
// forever: a stale chain's tickMsg{} looked exactly as valid as the current
// chain's. Ten trips through browse and back used to leave ten live timers,
// each firing every tile on every 5-second tick.
func TestStaleTickChainsAreDropped(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.origin = modeDashboard

	// Three round trips back to the dashboard, the way esc from browse (or
	// the plugins pane, or a closed result) does it: each restarts
	// refreshing, which is supposed to retire whatever chain came before.
	for range 3 {
		next, cmd := sm.closeToOrigin()
		sm = next.(Model)
		if cmd == nil {
			t.Fatal("returning to the dashboard did not restart refreshing")
		}
	}
	if sm.tickGen != 3 {
		t.Fatalf("tickGen = %d, want 3 after three restarts", sm.tickGen)
	}

	// The very first chain's tick, arriving late: it belongs to a visit that
	// is long gone, and must be dropped rather than re-armed.
	after, cmd := sm.Update(tickMsg{gen: 0})
	if cmd != nil {
		t.Error("a stale tick chain re-armed itself instead of being dropped")
	}
	if after.(Model).tickGen != 3 {
		t.Error("a dropped stale tick still advanced the generation")
	}

	// The current chain's tick: this is the one still allowed to keep going.
	after, cmd = sm.Update(tickMsg{gen: 3})
	if cmd == nil {
		t.Error("the current tick chain was dropped along with the stale ones")
	}
	if got := after.(Model).tickGen; got != 4 {
		t.Errorf("tickGen after the live tick = %d, want 4", got)
	}
}

// A row action on a StringSlice-scoped capability (net.hosts.rm's hostname,
// grant's target/scope) must name exactly the one record the row is about —
// not the type nothing declared, and not every record because of it.
// rowKey's own switch only special-cases Int and Float; for everything else
// it hands back the raw cell string, so this is really pinning that
// plugin.Request.StringSlice treats a bare string as one value rather than
// none — the same fix that closed a grant bypass on kv.env (see
// pkg/plugin.TestStringSliceAcceptsABareString). If a future change moves
// that coercion somewhere StringSlice can no longer reach, a hosts-list
// remove action silently starts acting on nothing instead of the row you
// pressed x on.
func TestRowActionSuppliesAStringSliceScopeAsOneRecord(t *testing.T) {
	rm := plugin.Capability{
		ID: "demo.hosts.rm", Summary: "remove hostnames", Safety: plugin.Destructive, Scope: "hostname",
		Inputs: []plugin.Field{
			{Name: "hostname", Type: plugin.StringSlice, Positional: true, Required: true},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return view.Text{Body: strings.Join(req.StringSlice("hostname"), ",")}, nil
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.row = 0
	tbl := view.Table{
		Columns: []view.Column{{Name: "Hostname"}},
		Rows:    [][]string{{"myhost.local"}},
	}
	acted, _ := sm.runAction(capAction{key: "x", label: "remove", cap: rm, src: srcRow}, tbl)
	am := acted.(Model)
	// Destructive: it opens a confirmation form seeded with the row's
	// identity, rather than firing blind.
	if am.mode != modeForm {
		t.Fatalf("mode = %v, want a seeded confirmation form", am.mode)
	}
	got := plugin.NewRequest(am.form.base, false, false).StringSlice("hostname")
	if len(got) != 1 || got[0] != "myhost.local" {
		t.Errorf("StringSlice(hostname) = %v, want exactly [myhost.local]", got)
	}
}

// Suggestions see what earlier stages already answered — which is what lets a
// suggestion depend on the record being edited rather than on the whole store.
func TestFormSuggestionsSeeEarlierAnswers(t *testing.T) {
	noHistory(t)
	var sawID int
	c := plugin.Capability{
		ID: "demo.edit", Summary: "edit", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "id", Type: plugin.Int, Positional: true, Required: true},
			{Name: "tag", Type: plugin.StringSlice, Suggest: func(_ context.Context, req plugin.Request) []string {
				sawID = req.Int("id")
				return []string{"backend\tused twice", "urgent"}
			}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	base := map[string]any{"id": 7}
	cf := newCapForm(c, fieldsAfter(c, base), nil, true, base)
	if got := cf.candidates(c.Inputs[1]); len(got) != 2 {
		t.Errorf("candidates = %v, want both declared tags", got)
	}
	if sawID != 7 {
		t.Errorf("Suggest saw id %d, want the record the form is about", sawID)
	}
}

// And they see a sibling in the same form, recomputed as it is filled in.
//
// They used to be frozen into a static list when the form was built, so a
// capability like `grant allow` — whose `scope` completes from whichever
// `target` was named — offered nothing in the TUI while completing perfectly
// from a shell. Field.Suggest is documented to receive "what the caller has
// supplied so far"; this is that promise holding on the surface where the
// caller is still typing.
func TestFormSuggestionsSeeASiblingBeingTyped(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.allow", Summary: "allow", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String},
			{Name: "scope", Type: plugin.String, Suggest: func(_ context.Context, req plugin.Request) []string {
				return []string{req.String("target") + "-scope"}
			}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	if got := cf.candidates(c.Inputs[1]); len(got) != 1 || got[0] != "-scope" {
		t.Fatalf("candidates before typing = %v, want the empty target's", got)
	}
	cf.syncs["target"].Set("kv")
	if got := cf.candidates(c.Inputs[1]); len(got) != 1 || got[0] != "kv-scope" {
		t.Errorf("candidates = %v, want them recomputed from the sibling", got)
	}
}

// A comma-separated list completes the item being typed, not the whole box.
//
// bubbles matches a suggestion against everything in the field, so a plain list
// stopped matching the moment a first item was accepted — `note add --tag`
// typing "recipe, ita" offered nothing.
func TestAListCompletesTheItemBeingTyped(t *testing.T) {
	declared := []string{"italian", "recipe", "urgent"}
	if got := extending("rec", declared); len(got) != 1 || got[0] != "recipe" {
		t.Errorf("extending(%q) = %v, want recipe", "rec", got)
	}
	got := extending("recipe, ita", declared)
	if len(got) != 1 || got[0] != "recipe, italian" {
		t.Errorf("extending = %v, want the whole box with the item completed", got)
	}
	if len(extending("recipe, ", declared)) != 3 {
		t.Error("an empty trailing item does not offer everything")
	}
}

// A capability that offers nothing must not break the form.
func TestFormWithoutSuggestionsStillBuilds(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.plain", Summary: "plain", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "name", Type: plugin.String}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	if cf := newCapForm(c, c.Inputs, nil, true, nil); cf.form == nil {
		t.Error("no form built")
	}
}

// A slow capability must not take the terminal hostage: esc leaves the run
// and lands back where it was launched from.
func TestEscapeAbandonsARunningCapability(t *testing.T) {
	slow := plugin.Capability{
		ID: "demo.slow", Summary: "takes its time", Safety: plugin.Read,
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	started, _ := sized.(Model).open(slow)
	sm := started.(Model)
	if sm.mode != modeRunning {
		t.Fatalf("mode = %v, want running", sm.mode)
	}

	left, _ := sm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	lm := left.(Model)
	if lm.mode == modeRunning {
		t.Fatal("esc left the shell stuck on the running screen")
	}
	if lm.flash == "" {
		t.Error("leaving a run should say so")
	}

	// …and the result that eventually arrives belongs to nobody: it must not
	// paint over whatever the user moved on to.
	stale, _ := lm.Update(resultMsg{cap: slow, view: view.Text{Body: "too late"}, seq: sm.runSeq})
	if got := stale.(Model).mode; got == modeResult {
		t.Error("an abandoned run's result was displayed")
	}
}

// …and cancelling really releases the handler, rather than leaving it running
// with nobody to receive it.
func TestCancellingARunReleasesTheHandler(t *testing.T) {
	slow := plugin.Capability{
		ID: "demo.slow", Summary: "takes its time", Safety: plugin.Read,
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := runCmd(ctx, 1, slow, nil, false, nil, "", nil, config.Connection{})

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	cancel()
	select {
	case msg := <-done:
		if got := msg.(resultMsg).seq; got != 1 {
			t.Errorf("seq = %d, want the run it belonged to", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept running after its context was cancelled")
	}
}

// A capability that hides part of its own data by default owes the surface a
// way to ask for the rest: `todo.list` hides completed tasks, so without the
// toggle the re-open action could never find a row to act on.
func TestToggleReRunsTheViewWithTheFlagFlipped(t *testing.T) {
	var sawAll []bool
	list := plugin.Capability{
		ID: "todo.list", Summary: "list tasks", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "all", Type: plugin.Bool}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			sawAll = append(sawAll, req.Bool("all"))
			return view.Table{Columns: []view.Column{{Name: "ID"}}, Rows: [][]string{{"1"}}}, nil
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sm := sized.(Model)
	sm.trail = []runRef{{cap: list, values: map[string]any{}}}
	sm.current, sm.lastValues = list, map[string]any{}
	shown, _ := sm.Update(resultMsg{cap: list, view: view.Table{
		Columns: []view.Column{{Name: "ID"}}, Rows: [][]string{{"1"}},
	}})

	toggled, cmd := shown.(Model).Update(tea.KeyPressMsg{Code: 'A', Text: "A"})
	tm := toggled.(Model)
	if on, _ := tm.lastValues["all"].(bool); !on {
		t.Fatalf("toggle left values = %v", tm.lastValues)
	}
	// The trail remembers it, so coming back from an action lands on the list
	// as the user left it, not as it was first opened.
	if on, _ := tm.trail[len(tm.trail)-1].values["all"].(bool); !on {
		t.Error("the trail did not remember the toggle")
	}
	if cmd == nil {
		t.Fatal("toggling did not re-run the view")
	}
	// …and pressing it again puts it back.
	back, _ := tm.Update(resultMsg{cap: list, view: view.Table{Columns: []view.Column{{Name: "ID"}}}, seq: tm.runSeq})
	again, _ := back.(Model).Update(tea.KeyPressMsg{Code: 'A', Text: "A"})
	if on, _ := again.(Model).lastValues["all"].(bool); on {
		t.Error("the toggle does not toggle back")
	}
}

// The catalogue is a table now — one row per capability, columns that line
// up down the whole list. Filtering it is the thing most likely to break
// when the rendering changes, because the rows a delegate is asked to draw
// during a filter are a different, shorter set with different indices.
//
// These drive the model directly rather than scraping frames. A terminal
// frame after the first is a diff — it shows what changed, not what is on
// screen — so "this row is gone" cannot be asserted from the output stream,
// and keystrokes delivered to a running program race the filter command they
// trigger. What the tests actually want to know is what the shell decided,
// which is a question about state.
func browsing(t *testing.T) Model {
	t.Helper()
	m := New(testRegistry(t), config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = um.(Model)
	m.mode = modeBrowse
	return m
}

// filterFor types a filter and returns the model mid-filter.
func filterFor(t *testing.T, m Model, query string) Model {
	t.Helper()
	m = pump(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range query {
		m = pump(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func visibleCaps(m Model) []string {
	var out []string
	for _, it := range m.list.VisibleItems() {
		if c, ok := it.(capItem); ok {
			out = append(out, c.c.ID)
		}
	}
	return out
}

func TestBrowseFilterNarrowsToMatches(t *testing.T) {
	m := filterFor(t, browsing(t), "boom")
	if got := visibleCaps(m); len(got) != 1 || got[0] != "demo.boom" {
		t.Fatalf("filtering for \"boom\" left %v, want just demo.boom", got)
	}
}

func TestBrowseFilterWithNoMatchesLeavesNothing(t *testing.T) {
	m := filterFor(t, browsing(t), "zzzznope")
	if got := visibleCaps(m); len(got) != 0 {
		t.Fatalf("a filter matching nothing left %v", got)
	}
}

// Enter on a filtered catalogue runs the match, not whatever the cursor was
// on before the filter narrowed the list under it.
//
// This is the bug the rewrite uncovered and it predates it: a filter shortens
// the list under the cursor, the index is left past the end of what
// survived, and SelectedItem returns nothing at all. The old guard asked "is
// the cursor on a header", which nothing satisfies, so the cursor was left
// pointing at no row — filter for a capability, press enter, and the
// catalogue simply did not respond.
func TestBrowseFilterThenEnterRunsTheMatch(t *testing.T) {
	m := filterFor(t, browsing(t), "hello")
	if got := visibleCaps(m); len(got) != 1 {
		t.Fatalf("expected one match, got %v", got)
	}
	m = pump(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // apply the filter
	m = pump(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // run the match
	if m.current.ID != "demo.hello" {
		t.Fatalf("enter ran %q, want the filtered match demo.hello", m.current.ID)
	}
}

// The cursor must always be somewhere enter can act on, whatever the filter
// did to the list underneath it — including matching nothing at all, and
// including a filter narrow enough to drop every row above the cursor.
func TestTheCursorIsNeverLeftOnSomethingEnterCannotOpen(t *testing.T) {
	for _, query := range []string{"", "demo", "boom", "hello", "needy", "zzz", "o"} {
		m := browsing(t)
		if query != "" {
			m = filterFor(t, m, query)
		}
		caps := visibleCaps(m)
		if len(caps) == 0 {
			continue // nothing to land on is a legitimate state
		}
		if !m.onCapability() {
			t.Errorf("filter %q: cursor is not on a capability, but %d are visible: %v",
				query, len(caps), caps)
		}
	}
}

// Escape from a filtered catalogue clears the filter first and only leaves
// on the second press. Stranding a filter would mean the next visit opens
// pre-narrowed with no sign of why; leaving on the first press would make
// "undo that search" and "go back" the same key with the same result.
func TestBrowseEscapeClearsTheFilterBeforeLeaving(t *testing.T) {
	m := filterFor(t, browsing(t), "boom")
	if len(visibleCaps(m)) != 1 {
		t.Fatalf("filter did not narrow: %v", visibleCaps(m))
	}
	m = pump(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := len(visibleCaps(m)); got != 3 {
		t.Fatalf("the first escape left %d capabilities visible, want the filter cleared to 3", got)
	}
	if m.mode != modeBrowse {
		t.Fatalf("the first escape also left the catalogue: mode=%v", m.mode)
	}
	m = pump(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != modeDashboard {
		t.Errorf("the second escape did not return to the dashboard: mode=%v", m.mode)
	}
}

// pump runs an update and drains the commands it produced, including
// batched ones, feeding each result back in. It is the smallest stand-in for
// the runtime that makes asynchronous list filtering observable in a test.
//
// Commands are given a short deadline and abandoned if they miss it. That is
// not impatience: a cursor blink is a command that sleeps half a second and
// then schedules another one exactly like it, so draining honestly never
// terminates and draining synchronously makes every test that types a
// character take seconds. The commands this needs — the list's filter — are
// immediate.
func pump(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	o, cmd := m.Update(msg)
	m = o.(Model)
	queue := []tea.Cmd{cmd}
	for range 40 {
		if len(queue) == 0 {
			break
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var out tea.Msg
		select {
		case out = <-done:
		case <-time.After(25 * time.Millisecond):
			continue // a timer; nothing this test observes depends on it
		}
		switch v := out.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, v...)
		default:
			o, next := m.Update(v)
			m = o.(Model)
			queue = append(queue, next)
		}
	}
	return m
}

// Every path that turns a view into bytes somebody can read runs Redact. The
// clipboard copy did not, so a value the screen was masking went out in full —
// and a clipboard is read by more things than a terminal is, and outlives the
// session that filled it.
func TestCopyAsJSONMasksRedactedValues(t *testing.T) {
	secret := view.KeyValue{
		Pairs:    []view.Pair{{Key: "user", Value: "tobi"}, {Key: "token", Value: "hunter2"}},
		Redacted: []string{"token"},
	}
	raw, err := json.MarshalIndent(view.Envelope{View: view.Redact(secret)}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Errorf("the clipboard payload leaks the secret:\n%s", raw)
	}
	if !strings.Contains(string(raw), view.Mask) {
		t.Errorf("the clipboard payload is not masked:\n%s", raw)
	}
	if !strings.Contains(string(raw), "tobi") {
		t.Errorf("the clipboard payload dropped the non-secret field:\n%s", raw)
	}
}

// TestExplicitDetailPreferenceReachesTheHandler: runCmd asks a Detailed
// capability for its full view because the result pane owns the screen — but
// as a default, not an override. It used to set detail=true unconditionally,
// one line after toggleView had set it to false, so the D toggle on the
// kv.list pane could set the value and never make it stick.
func TestExplicitDetailPreferenceReachesTheHandler(t *testing.T) {
	reg := detailRegistry(t)
	c, ok := reg.Capability("deep.thing")
	if !ok {
		t.Fatal("deep.thing is not registered")
	}
	ran := func(values map[string]any) string {
		var got string
		collect(t, runCmd(context.Background(), 0, c, values, false, nil, "", nil, config.Connection{}), func(msg tea.Msg) {
			if r, ok := msg.(resultMsg); ok {
				got = r.view.(view.Text).Body
			}
		})
		return got
	}
	if got := ran(nil); got != "FULL-REPORT" {
		t.Errorf("no preference expressed = %q, want FULL-REPORT", got)
	}
	if got := ran(map[string]any{"detail": false}); got != "COMPACT" {
		t.Errorf("detail=false = %q, want COMPACT — the toggle was overruled", got)
	}
	if got := ran(map[string]any{"detail": true}); got != "FULL-REPORT" {
		t.Errorf("detail=true = %q, want FULL-REPORT", got)
	}
}

// TestDetailToggleTurnsTheDetailPageOff: one press of D on a Detailed
// result pane must reach the handler as detail=false. Both halves of the
// old bug are in this path — runCmd forced true back on, and the toggle
// read its starting state from a map where detail was simply absent, so the
// first press "turned on" a page that was already on.
func TestDetailToggleTurnsTheDetailPageOff(t *testing.T) {
	var saw []bool
	// kv.list is the ID viewToggleSpecs binds D to, so this is the real
	// keymap rather than a fixture that only resembles it.
	list := plugin.Capability{
		ID: "kv.list", Summary: "list keys", Safety: plugin.Read, Detailed: true,
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			saw = append(saw, req.Bool("detail"))
			return view.Text{Body: "keys"}, nil
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sm := sized.(Model)
	sm.trail = []runRef{{cap: list, values: map[string]any{}}}
	sm.current, sm.lastValues = list, map[string]any{}
	shown, _ := sm.Update(resultMsg{cap: list, view: view.Text{Body: "keys"}})

	// The pane is detailed before anything is pressed, so the footer says so.
	if !strings.Contains(shown.(Model).resultView(), "✓") {
		t.Error("the detail toggle reads as off over a page runCmd made detailed")
	}

	toggled, cmd := shown.(Model).Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	tm := toggled.(Model)
	if on, _ := tm.lastValues["detail"].(bool); on {
		t.Fatalf("D left detail on: %v", tm.lastValues)
	}
	collect(t, cmd, func(tea.Msg) {})
	if len(saw) != 1 || saw[0] {
		t.Fatalf("handler saw detail=%v, want exactly one run with false", saw)
	}
}

// TestPartialPageShowsWhatItCouldNotProduce: a Sections view carrying
// Warnings is a page that dropped something. Dropped sections leave no trace
// — "no such sensor on this platform" and "that sensor errored" both render
// as a heading that is not there — so a degraded page used to look exactly
// like a whole one, and the glance the dashboard exists for came back
// reassured.
func TestPartialPageShowsWhatItCouldNotProduce(t *testing.T) {
	c := plugin.Capability{ID: "sys.overview", Summary: "overview", Safety: plugin.Read}
	page := view.Sections{
		Items: []view.Section{
			{Title: "host", View: view.Text{Body: "laptop"}},
			// Nested, because a detail page is sections of sections and a
			// sensor that failed two levels down is just as absent.
			{Title: "sensors", View: view.Sections{
				Items:    []view.Section{{Title: "fans", View: view.Text{Body: "2100 rpm"}}},
				Warnings: []view.Error{{Code: "sys.temp.unsupported", Message: "no thermal sensor"}},
			}},
		},
		Warnings: []view.Error{{
			Code: "sys.mem.denied", Message: "memory stats need root", Hint: "run with sudo",
		}},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	shown, _ := sized.(Model).Update(resultMsg{cap: c, view: page})
	pane := shown.(Model).viewport.View()

	// The content still leads: the sections that answered are what the page
	// was opened for.
	for _, want := range []string{"laptop", "2100 rpm"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the pane lost its content: %q missing", want)
		}
	}
	// The degradation is unmissable, at both depths, with its hint — and
	// said exactly once.
	//
	// Counting rather than Contains, because the first version of this drew
	// every warning twice: the pane renders through cli.Render, which already
	// prints them under the sections, and the TUI appended its own block on
	// top. strings.Contains is structurally blind to that — it passes just as
	// cleanly over two copies as over one — so the test agreed with a pane
	// that read "memory stats need root" twice in two different layouts, the
	// second of them truncated.
	for _, want := range []string{
		"memory stats need root", "no thermal sensor", "run with sudo",
	} {
		switch n := strings.Count(pane, want); {
		case n == 0:
			t.Errorf("the pane silently dropped %q:\n%s", want, pane)
		case n > 1:
			t.Errorf("the pane says %q %d times; one renderer owns this:\n%s", want, n, pane)
		}
	}
	// …and the status line above the fold says the page is not all of it,
	// for a reader who never scrolls to the bottom.
	if !strings.Contains(pane, "partial (2 warnings)") {
		t.Errorf("the status line does not call the page partial:\n%s", pane)
	}
}

// TestCompletePageSaysNothingAboutWarnings: the degradation notice is worth
// nothing if a whole page carries it too.
func TestCompletePageSaysNothingAboutWarnings(t *testing.T) {
	c := plugin.Capability{ID: "sys.overview", Summary: "overview", Safety: plugin.Read}
	page := view.Sections{Items: []view.Section{{Title: "host", View: view.Text{Body: "laptop"}}}}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	shown, _ := sized.(Model).Update(resultMsg{cap: c, view: page})
	pane := shown.(Model).viewport.View()
	for _, unwanted := range []string{"INCOMPLETE", "partial"} {
		if strings.Contains(pane, unwanted) {
			t.Errorf("a complete page reports %q:\n%s", unwanted, pane)
		}
	}
}

// TestPluralNounPicksTheNounForm pins the copy: "1 warnings" reads as a bug
// in the tool, not a typo in the string.
func TestPluralNounPicksTheNounForm(t *testing.T) {
	for n, want := range map[int]string{0: "warnings", 1: "warning", 2: "warnings"} {
		if got := pluralNoun(n, "warning"); got != want {
			t.Errorf("pluralNoun(%d) = %q, want %q", n, got, want)
		}
	}
}

// The -y words, because "1 capabilities" was on the plugin inventory for every
// plugin declaring exactly one — `debug` does — and that pane is asking to be
// read carefully.
//
// A consonant before the y is the whole rule: "key" and "day" keep their y and
// take an s, and getting that backwards would trade one wrong plural for
// another in a codebase that counts keys.
func TestPluralNounKnowsTheYWords(t *testing.T) {
	for word, want := range map[string]string{
		"capability": "capabilities",
		"entry":      "entries",
		"key":        "keys",
		"day":        "days",
		"warning":    "warnings",
	} {
		if got := pluralNoun(2, word); got != want {
			t.Errorf("pluralNoun(2, %q) = %q, want %q", word, got, want)
		}
		if got := pluralNoun(1, word); got != word {
			t.Errorf("pluralNoun(1, %q) = %q, want it unchanged", word, got)
		}
	}
}

// The catalogue was the one pane that asked the terminal for mouse reporting
// and then had no case for the events it got. Spinning the wheel over
// eighty-five rows did nothing, and the terminal could not fall back to its
// own scrollback either, because we had taken the events in order to drop
// them. "A pane that ignores it reads as stuck" is the comment above the
// handler; this was the pane.
func TestTheWheelScrollsTheCatalogue(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	mm := sized.(Model)
	mm.mode = modeBrowse

	before := mm.list.Index()
	next, _ := mm.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	down := next.(Model)
	if down.list.Index() <= before {
		t.Fatalf("the wheel left the cursor at %d", down.list.Index())
	}
	back, _ := down.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if up := back.(Model); up.list.Index() >= down.list.Index() {
		t.Errorf("the wheel only goes one way: %d then %d", down.list.Index(), up.list.Index())
	}
	// And it lands somewhere enter can act on, exactly like the arrow keys:
	// a wheel that parks the cursor on a section heading is a wheel that
	// breaks the next keystroke.
	if _, ok := down.list.SelectedItem().(capItem); !ok {
		t.Error("the wheel parked the cursor on a section heading")
	}
}

// The list holds more rows than it shows and advances a page at a time, so
// pressing down at the bottom replaces the whole screen — including the row
// the cursor was on. With the list's own pagination switched off, nothing
// anywhere said a second page existed, which is what makes a catalogue that
// moves perfectly well read as one that is stuck.
func TestTheCatalogueSaysWhichPageYouAreOn(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	// Tall enough for everything: no pages, so no page counter to read.
	tall, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 200})
	if got := tall.(Model).catalogueCount(); strings.Contains(got, "page") {
		t.Errorf("a catalogue that fits on one screen has no pages to report: %q", got)
	}
	// Short enough to paginate: say so, and say where.
	short, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 6})
	sm := short.(Model)
	if sm.list.Paginator.TotalPages < 2 {
		t.Fatalf("expected the catalogue to paginate at height 6, got %d page(s) for %d items",
			sm.list.Paginator.TotalPages, len(sm.list.Items()))
	}
	if got := sm.catalogueCount(); !strings.Contains(got, "page 1/") {
		t.Errorf("a paginated catalogue must say so: %q", got)
	}
}

// TestEditingInputsKeepsAnExplicitDetailPreference: `D` turns detail off, then
// `e` changes a filter — the run that follows must still be compact.
//
// `detail` is a reserved input and so is never a form field. The form rebuilds
// the value map from its own bindings, which dropped it, and runCmd's
// "detailed unless somebody said otherwise" default then put it straight back:
// the toggle worked from every path except through the form, which is the one
// path that looks most like it should preserve what came before.
func TestEditingInputsKeepsAnExplicitDetailPreference(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "kv", Summary: "secrets",
		Capabilities: []plugin.Capability{
			{ID: "kv.list", Summary: "list", Safety: plugin.Read, Idempotent: true, Detailed: true,
				Inputs: []plugin.Field{{Name: "match", Type: plugin.String, Help: "filter"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: fmt.Sprintf("detail=%t", req.Bool("detail"))}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Capability("kv.list")

	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	// Seeded rather than navigated to: the trail is what makes a pane the top
	// of its own view, and kv.list has no row actions here to build one.
	sm.trail = []runRef{{cap: c, values: map[string]any{}}}
	sm.current, sm.lastValues = c, map[string]any{}
	shown, _ := sm.Update(resultMsg{cap: c, view: view.Text{Body: "detail=true"}})

	// D turns it off. This half already worked.
	off, _ := shown.(Model).Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	om := off.(Model)
	if on, given := om.lastValues["detail"].(bool); !given || on {
		t.Fatalf("D: lastValues[detail] = %v, want an explicit false", om.lastValues["detail"])
	}

	// Back to a result, then edit the inputs.
	om.trail = []runRef{{cap: c, values: om.lastValues}}
	again, _ := om.Update(resultMsg{cap: c, view: view.Text{Body: "detail=false"}})
	edited, _ := again.(Model).Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	em := edited.(Model)
	if em.mode != modeForm {
		t.Fatalf("e: mode = %v, want a form", em.mode)
	}
	// values() is verbatim what completing the form assigns to lastValues, so
	// asserting on it asserts the run without driving huh to completion.
	got := em.form.values()
	if on, given := got["detail"].(bool); !given || on {
		t.Fatalf("the form dropped an explicit detail preference: values = %v", got)
	}
}

// A capability opened fresh starts from its own defaults. unasked exists to
// keep one run's toggles, not to leak them into the next capability — a
// detail preference set on kv.list must not arrive pre-set on todo.add.
func TestOpeningAFreshCapabilityCarriesNothing(t *testing.T) {
	var doneLog []int
	reg := listRegistry(t, &doneLog)
	c, _ := reg.Capability("todo.add")

	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "todo.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.lastValues = map[string]any{"detail": false, "stale": "from the last run"}

	opened, _ := sm.open(c)
	om := opened.(Model)
	if om.mode != modeForm {
		t.Fatalf("open: mode = %v, want a form", om.mode)
	}
	if len(om.form.base) != 0 {
		t.Fatalf("a fresh capability inherited %v from the previous run", om.form.base)
	}
}

// The TUI draws strings that never pass through cli.Render, and the promise
// that renderers neutralise "every view string" was true of the
// renderer and false of the surface. cli.Render sanitises its own local copy;
// everything the TUI draws around that copy was raw.
//
// Reproduced before this was fixed: a Sections item title carrying an OSC 8
// hyperlink went to the terminal verbatim, immediately after rta's own prefix,
// in rta's own colour, inside rta's panel border — a live link whose visible
// text and target were both attacker-chosen.
//
// The property is asserted against rta rather than against bubbletea. What
// currently keeps OSC 52 off the tty from here is that ultraviolet's cell
// renderer drops it, which is undocumented, untested by us, and one dependency
// bump from restoring the clipboard hijack sanitize.go was written for.
func TestNothingTheTUIDrawsCarriesAnEscape(t *testing.T) {
	const evil = "safe\x1b]8;;mailto:pay@evil.example\x07LOOKS-FINE\x1b]8;;\x07\rOVERWRITE"
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "todo", Summary: "tasks",
		Capabilities: []plugin.Capability{
			{ID: "todo.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Sections{
						// A title beside a table, which is the composed detail
						// page every plugin.Page builds.
						Items: []view.Section{{Title: evil, View: view.Table{
							Columns: []view.Column{{Name: evil}},
							Rows:    [][]string{{evil}},
						}}},
						Warnings: []view.Error{{Code: "x.partial", Message: evil, Hint: evil}},
					}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Capability("todo.list")
	v, _ := c.Run(context.Background(), plugin.NewRequest(nil, false, false))

	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.trail = []runRef{{cap: c, values: map[string]any{}}}
	sm.current = c
	shown, _ := sm.Update(resultMsg{cap: c, view: v})
	rm := shown.(Model)

	// Styled output legitimately contains rta's own SGR sequences (ESC [ … m)
	// from lipgloss, so the assertion names what rta never emits: OSC (ESC ]),
	// which is the hyperlink, title and clipboard family; BEL, which
	// terminates them; and a bare CR, which redraws the line already drawn.
	for what, got := range map[string]string{
		"the result pane": rm.resultView(),
		"the meta line":   rm.resultMeta(),
	} {
		for label, seq := range map[string]string{
			"an OSC introducer": "\x1b]",
			"a BEL":             "\a",
			"a carriage return": "\r",
		} {
			if strings.Contains(got, seq) {
				t.Errorf("%s carries %s: %q", what, label, got)
			}
		}
		if strings.Contains(got, "mailto:pay@evil.example") {
			t.Errorf("%s carries the attacker's link target", what)
		}
		// And the readable part of the value survives: a control that also
		// loses the answer is not a control anybody keeps.
		if !strings.Contains(got, "LOOKS-FINE") {
			t.Errorf("%s dropped the value along with the escape: %q", what, got)
		}
	}

	// And the value a row action would act on must be the value on screen.
	// It used to come from the raw view while the cell came from the
	// sanitised copy, so the identity shown and the identity used were
	// different strings by construction.
	if tbl, ok := rm.result.view.(view.Sections).Items[0].View.(view.Table); ok {
		if strings.ContainsAny(tbl.Rows[0][0], "\x1b\a\r") {
			t.Errorf("the stored row still holds the raw value: %q", tbl.Rows[0][0])
		}
	}
}

// Suggest runs at form time and returns whatever exists right now — tags,
// hostnames, keys somebody else wrote. It never passes through Validate,
// which only ever sees the declaration.
func TestSuggestionsAreCleaned(t *testing.T) {
	f := plugin.Field{
		Name: "tag", Type: plugin.String, Help: "tag",
		Suggest: func(context.Context, plugin.Request) []string {
			return []string{"ok\x1b]0;PWNED\x07", "fine"}
		},
	}
	got := candidateValues(f, context.Background(), plugin.NewRequest(nil, false, false))
	for _, v := range got {
		if strings.ContainsAny(v, "\x1b\a\r") {
			t.Errorf("a suggestion carries a control sequence: %q", v)
		}
	}
	if len(got) != 2 {
		t.Errorf("suggestions were dropped rather than cleaned: %v", got)
	}
}

// The dashboard path, which the test above does not reach. A tile runs a
// capability on a refresh timer and stores what comes back; the result pane
// is a different message with a different handler, and cleaning one said
// nothing about the other.
//
// It was already safe, and safe only because cli.Render — the single reader
// of a tile's view — sanitises its own local copy. That is the arrangement
// that produced the runAction defect, where the cell on
// screen came from the sanitised copy while the row's identity came from the
// raw one. This asserts the model's own state instead, so a second reader
// added later inherits a clean string rather than a latent bug.
func TestNothingADashboardTileStoresCarriesAnEscape(t *testing.T) {
	const evil = "safe\x1b]52;c;cGF5QGV2aWwuZXhhbXBsZQ==\x07shown\rOVERWRITE"
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.status", Summary: "status", Safety: plugin.Read, Idempotent: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.KeyValue{Pairs: []view.Pair{{Key: evil, Value: evil}}}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m := New(reg, config.Dashboard{}, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	// Deliver the tile result the way refreshTiles would.
	idx := -1
	for i, ti := range m.tiles {
		if !ti.search && ti.cap.ID == "demo.status" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("demo.status got no tile: %v", tileIDs(m.tiles))
	}
	v, runErr := m.tiles[idx].cap.Run(context.Background(), plugin.NewRequest(nil, false, false))
	if runErr != nil {
		t.Fatal(runErr)
	}
	updated, _ = m.Update(tileMsg{idx: idx, v: v})
	m = updated.(Model)

	stored, ok := m.tiles[idx].view.(view.KeyValue)
	if !ok {
		t.Fatalf("tile view is %T", m.tiles[idx].view)
	}
	// Names what rta never emits, rather than any ESC: lipgloss styling is
	// legitimately full of SGR, so asserting "no escapes at all" would fail on
	// rta's own colour.
	for what, seq := range map[string]string{
		"an OSC introducer": "\x1b]",
		"a BEL":             "\x07",
		"a bare CR":         "\r",
	} {
		for _, p := range stored.Pairs {
			if strings.Contains(p.Key, seq) || strings.Contains(p.Value, seq) {
				t.Errorf("a tile stored %s: %q / %q", what, p.Key, p.Value)
			}
		}
	}
	// And the visible text survives, or the cleaner is just deleting data.
	if !strings.Contains(stored.Pairs[0].Value, "shown") {
		t.Errorf("the cleaner dropped the value with the escape: %q", stored.Pairs[0].Value)
	}
}
