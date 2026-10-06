package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// The dashboard draws one view at a time, writes to the one it draws, and V
// draws another without writing anything.

// viewsModel is a sized model whose config states a view that shows alpha alone,
// opened on the dashboard: block as bare `rta` opens on a machine whose
// profile selects none.
func viewsModel(t *testing.T) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	cfg := config.Config{}
	cfg.SetBlock("only-alpha", config.Dashboard{Tiles: []config.Tile{{ID: "alpha.info"}}})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	out := sized.(Model)
	// Off the search bar, which takes every letter typed as a query.
	out.selected = 1
	return out
}

func TestVDrawsTheNextViewAndRoundAgainWithoutWritingAnything(t *testing.T) {
	m := viewsModel(t)
	before := savedConfig(t)
	if got := tileIDs(m.tiles); !slices.Contains(got, "beta.info") {
		t.Fatalf("the default screen = %v", got)
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if m.view != "only-alpha" || m.flash != "view: only-alpha" {
		t.Fatalf("view = %q, flash = %q after V", m.view, m.flash)
	}
	if got := tileIDs(m.tiles); !slices.Equal(got, []string{"alpha.info"}) {
		t.Errorf("the view's screen = %v, want alpha alone", got)
	}
	if !strings.Contains(m.dashboardView(), "view only-alpha") {
		t.Errorf("the header does not say which view is drawn:\n%s", m.dashboardView())
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if m.view != "" || m.flash != "view: default" {
		t.Errorf("view = %q, flash = %q after the second V", m.view, m.flash)
	}
	if got := tileIDs(m.tiles); !slices.Contains(got, "beta.info") {
		t.Errorf("the default screen did not come back: %v", got)
	}
	if after := savedConfig(t); dashStamp("", after.Dashboard) != dashStamp("", before.Dashboard) {
		t.Errorf("looking at a view wrote the config:\n%+v", after.Dashboard)
	}
}

func TestVSaysWhatToDoWhenThereAreNoViews(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	on := sized.(Model)
	on.selected = 1
	next, _ := on.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	if got := next.(Model).flash; !strings.Contains(got, "no views yet") || !strings.Contains(got, "--view") {
		t.Errorf("flash = %q", got)
	}
}

// H on the screen of a view takes the tile off that view: the block, and every
// other view, is as it was.
func TestWhatTheDashboardWritesGoesToTheViewItDraws(t *testing.T) {
	m := viewsModel(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
	m = next.(Model)
	if got := m.tiles[1].cap.ID; got != "alpha.info" {
		t.Fatalf("the selected tile is %s", got)
	}
	m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	cfg := savedConfig(t)
	if got := cfg.Dashboard.Views["only-alpha"]; len(got.Tiles) != 0 {
		t.Errorf("the view still states %+v after H took its tile off", got.Tiles)
	}
	if len(cfg.Dashboard.Hidden) != 0 || len(cfg.Dashboard.Tiles) != 0 {
		t.Errorf("the block took what the view's screen did: hidden %v, tiles %v", cfg.Dashboard.Hidden, cfg.Dashboard.Tiles)
	}
}

// A switch is asking for that environment's screen, so it undoes a pick, and a
// profile that selects a view draws it.
func TestAProfilesViewIsDrawnWhileItIsOnAndAPickIsUndoneBySwitching(t *testing.T) {
	m := viewsModel(t)
	cfg := savedConfig(t)
	cfg.Profiles = map[string]config.Profile{"prod": {Dashboard: "only-alpha"}}
	m.viewPick = config.DefaultView
	if got := m.wantView(cfg); got != "" {
		t.Errorf("a pick of the default was overridden: %q", got)
	}
	m.viewPick = "only-alpha"
	if got := m.wantView(cfg); got != "only-alpha" {
		t.Errorf("a pick was not honoured: %q", got)
	}
	m.viewPick = "gone"
	m.active = "prod"
	if got := m.wantView(cfg); got != "only-alpha" {
		t.Errorf("a pick of a view the file no longer states did not fall back to the profile's: %q", got)
	}
	m.viewPick, m.active = "", "prod"
	if got := m.wantView(cfg); got != "only-alpha" {
		t.Errorf("the profile's view was not drawn: %q", got)
	}
	m.active = ""
	if got := m.wantView(cfg); got != "" {
		t.Errorf("a view was drawn with nothing switched on: %q", got)
	}
}
