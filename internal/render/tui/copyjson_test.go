package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func resultOnScreen(t *testing.T) Model {
	t.Helper()
	m := New(testRegistry(t), config.Dashboard{}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c := plugin.Capability{ID: "demo.json", Summary: "json", Safety: plugin.Read}
	next, _ = next.(Model).Update(resultMsg{cap: c, view: view.Text{Body: "THE-JSON-BODY"}})
	got := next.(Model)
	if got.mode != modeResult {
		t.Fatalf("the result did not reach the screen: mode=%v", got.mode)
	}
	return got
}

// `y` puts the view on the system clipboard as well as sending it to the
// terminal. OSC 52 alone said "copied as JSON" whether or not the terminal took
// it, and Terminal.app never does, so the first paste after the flash found
// whatever had been there before.
func TestCopyingAViewAsJSONReachesTheSystemClipboard(t *testing.T) {
	stdin := fakeClipboard(t)
	next, cmd := resultOnScreen(t).Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	got := next.(Model)
	if got.flash != "copied as JSON" {
		t.Errorf("flash = %q, want the copy confirmed", got.flash)
	}
	if cmd == nil {
		t.Error("the terminal was sent nothing: over ssh only OSC 52 reaches the local clipboard")
	}
	raw, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("no clipboard program was given the JSON: %v", err)
	}
	if !strings.Contains(string(raw), "THE-JSON-BODY") {
		t.Errorf("the clipboard holds %q, want the view as JSON", raw)
	}
}

// With no clipboard program the flash does not claim a copy it cannot know
// happened: the terminal was sent the value and may or may not have kept it.
func TestCopyingAViewAsJSONAdmitsWhenOnlyTheTerminalWasTold(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	next, cmd := resultOnScreen(t).Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if got := next.(Model).flash; !strings.HasPrefix(got, "sent to the terminal") {
		t.Errorf("flash = %q, want the admission that only the terminal was told", got)
	}
	if cmd == nil {
		t.Error("nothing was sent to the terminal either")
	}
}
