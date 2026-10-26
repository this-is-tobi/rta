package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A live view is run again under the reader on a timer (refreshInPlace), and
// that run outlived the screen: the answer of one in flight when the reader
// left was painted as a result over wherever they went, the form `e` opened or
// the dashboard, and the keys typed next went to a screen nobody could see. And
// q quit with it running, past the cancel every quit is there to make.

func liveQueueCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.queue", Summary: "what waits", Safety: plugin.Read, Live: true,
		Inputs: []plugin.Field{{Name: "filter", Type: plugin.String, Help: "f"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{Columns: []view.Column{{Name: "ID"}}, Rows: [][]string{{"1"}, {"2"}}}, nil
		},
	}
}

// liveRefreshInFlight is the live view on screen with a tick just landed on it,
// and the command that refresh runs.
func liveRefreshInFlight(t *testing.T) (Model, tea.Cmd) {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{liveQueueCap()}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c := mustCap(t, reg, "demo.queue")
	shown, _ := sized.(Model).Update(resultMsg{cap: c, view: view.Table{Columns: []view.Column{{Name: "ID"}}, Rows: [][]string{{"1"}}}})
	sm := shown.(Model)
	if sm.mode != modeResult {
		t.Fatalf("mode %v, want the live view on screen", sm.mode)
	}
	next, cmd := sm.Update(tickMsg{gen: sm.tickGen})
	if cmd == nil {
		t.Fatal("a tick on the live view did not run it again")
	}
	return next.(Model), cmd
}

func refreshAnswer(t *testing.T, cmd tea.Cmd) resultMsg {
	t.Helper()
	for _, msg := range messagesOf(cmd) {
		if r, ok := msg.(resultMsg); ok {
			return r
		}
	}
	t.Fatal("the refresh answered nothing")
	return resultMsg{}
}

func TestALiveRefreshThatAnswersAfterTheReaderLeftLeavesThemWhereTheyWent(t *testing.T) {
	m, cmd := liveRefreshInFlight(t)
	answer := refreshAnswer(t, cmd)

	back := press(t, m, "esc")
	if back.mode != modeDashboard {
		t.Fatalf("esc on the live view went to %v", back.mode)
	}
	if after, _ := back.Update(answer); after.(Model).mode != modeDashboard {
		t.Errorf("the refresh's answer took the dashboard away for %v", after.(Model).mode)
	}

	form := press(t, m, "e")
	if form.mode != modeForm || form.form == nil {
		t.Fatalf("e on the live view opened %v", form.mode)
	}
	after, _ := form.Update(answer)
	if got := after.(Model); got.mode != modeForm || got.form == nil {
		t.Errorf("the refresh's answer took the form away for %v", got.mode)
	}
}

// The other half of the rule: on the view it refreshes, the answer lands and
// the next refresh is armed.
func TestALiveRefreshStillLandsOnTheViewItRefreshes(t *testing.T) {
	m, cmd := liveRefreshInFlight(t)
	after, next := m.Update(refreshAnswer(t, cmd))
	got := after.(Model)
	if got.mode != modeResult || got.result.view == nil || next == nil {
		t.Fatalf("mode %v, view %v, next refresh armed %v: the refresh did not land", got.mode, got.result.view != nil, next != nil)
	}
	if tbl, ok := got.result.view.(view.Table); !ok || len(tbl.Rows) != 2 {
		t.Errorf("the view on screen is %v, want the refreshed one", got.result.view)
	}
}

// Every quit cancels the run in flight, whichever screen it is pressed on: the
// refresh of a live view goes on under the result screen, and after it while
// its answer is on the way, and a destructive run's dry run under its
// confirmation.
func TestEveryQuitCancelsTheRunInFlight(t *testing.T) {
	base := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := base.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	q := tea.KeyPressMsg{Code: 'q', Text: "q"}
	for _, tc := range []struct {
		name   string
		screen mode
		key    tea.KeyPressMsg
	}{
		{"q on the result", modeResult, q},
		{"q on the dashboard", modeDashboard, q},
		{"ctrl+c on the dashboard", modeDashboard, ctrlC},
		{"q on the plugins pane", modePlugins, q},
		{"q on the catalogue", modeBrowse, q},
		{"q on the profiles pane", modeProfiles, q},
		{"q on a confirmation", modeConfirm, q},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sized.(Model)
			m.mode = tc.screen
			released := false
			m.cancelRun = func() { released = true }
			_, cmd := m.Update(tc.key)
			if cmd == nil {
				t.Fatal("did not quit")
			}
			if !released {
				t.Error("quit with the run in flight left running")
			}
		})
	}
}

func TestCtrlCOnTheSearchBarAndOverAQuestionCancelsTheRunInFlight(t *testing.T) {
	base := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := base.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	search := sized.(Model)
	search.searchEditing = true
	asked := sized.(Model).openDialog(discardFormDialog())
	for name, m := range map[string]Model{"the search bar": search, "a question": asked} {
		released := false
		m.cancelRun = func() { released = true }
		if _, cmd := m.Update(ctrlC); cmd == nil || !released {
			t.Errorf("ctrl+c on %s: quit %v, run cancelled %v", name, cmd != nil, released)
		}
	}
}
