package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
)

// A profile that writes its screen in its own entry has a view named after it:
// switching the environment on draws it, V cycles to it with the others, and
// what the dashboard writes while it is drawn goes back into the entry.

// inlineModel is a sized model whose config has one profile, shop-prod, that
// shows alpha alone, opened on the dashboard: block with nothing switched on.
func inlineModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	cfg := config.Config{Profiles: map[string]config.Profile{"shop-prod": {
		Plugins:   map[string]config.Connection{"alpha": {}},
		Dashboard: config.ProfileDashboard{Inline: &config.View{Tiles: []config.Tile{{ID: "alpha.info"}}}},
	}}}
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	out := sized.(Model)
	out.selected = 1
	return out
}

func TestSwitchingToAProfileDrawsTheScreenWrittenInItsEntry(t *testing.T) {
	m := inlineModel(t)
	if got := tileIDs(m.tiles); !slices.Contains(got, "beta.info") {
		t.Fatalf("the default screen = %v", got)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "shop-prod"}); verr != nil {
		t.Fatal(verr)
	}
	m.syncActive()
	if m.view != "shop-prod" {
		t.Fatalf("view = %q, want the view named after the profile", m.view)
	}
	if got := tileIDs(m.tiles); !slices.Equal(got, []string{"alpha.info"}) {
		t.Errorf("shop-prod's screen = %v, want alpha alone", got)
	}
	if !strings.Contains(m.dashboardView(), "view shop-prod") {
		t.Errorf("the header does not say which view is drawn:\n%s", m.dashboardView())
	}

	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	m.syncActive()
	if m.view != "" || !slices.Contains(tileIDs(m.tiles), "beta.info") {
		t.Errorf("switched off, view = %q, tiles = %v: the dashboard: block did not come back", m.view, tileIDs(m.tiles))
	}
}

// V goes through every view there is, the profile's own among them, and a
// switch undoes the pick.
func TestVCyclesToAProfilesOwnScreenAndASwitchUndoesIt(t *testing.T) {
	m := inlineModel(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if m.view != "shop-prod" || m.flash != "view: shop-prod" {
		t.Fatalf("view = %q, flash = %q after V", m.view, m.flash)
	}
	if got := tileIDs(m.tiles); !slices.Equal(got, []string{"alpha.info"}) {
		t.Errorf("the screen after V = %v", got)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	if m = next.(Model); m.view != "" {
		t.Errorf("the second V left view = %q, want the dashboard: block", m.view)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if m.view != "shop-prod" || m.viewPick != "shop-prod" {
		t.Fatalf("view = %q, pick = %q", m.view, m.viewPick)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "shop-prod"}); verr != nil {
		t.Fatal(verr)
	}
	m.syncActive()
	if m.viewPick != "" || m.view != "shop-prod" {
		t.Errorf("after the switch, view = %q, pick = %q", m.view, m.viewPick)
	}
}

// H on that screen takes the tile off it, and the write goes into the profile's
// entry: nothing appears under views:, and the dashboard: block is as it was.
func TestWhatTheDashboardWritesWhileAProfilesScreenIsDrawnGoesIntoItsEntry(t *testing.T) {
	m := inlineModel(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if got := m.tiles[1].cap.ID; got != "alpha.info" {
		t.Fatalf("the selected tile is %s", got)
	}
	m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	cfg := savedConfig(t)
	block := cfg.Profiles["shop-prod"].Dashboard.Inline
	if block == nil || len(block.Tiles) != 0 {
		t.Errorf("the entry's screen = %+v after H took its tile off", block)
	}
	if len(cfg.Dashboard.Views) != 0 || len(cfg.Dashboard.Hidden) != 0 || len(cfg.Dashboard.Tiles) != 0 {
		t.Errorf("the write landed outside the entry: %+v", cfg.Dashboard)
	}
	if cfg.Profiles["shop-prod"].Plugins["alpha"].Set != nil || len(cfg.Profiles["shop-prod"].Plugins) != 1 {
		t.Errorf("the entry lost its connections: %+v", cfg.Profiles["shop-prod"].Plugins)
	}
}
