package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// walkModel is a catalogue of two reads: one with a box it cannot run
// without, so enter opens its form, and one with nothing to ask, so enter
// runs it.
func walkModel(t *testing.T, dash config.Dashboard) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	answer := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "found"}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "walk", Summary: "walk", Capabilities: []plugin.Capability{
		{ID: "walk.find", Summary: "find one", Safety: plugin.Read, Run: answer,
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Required: true, Help: "what to find"}}},
		{ID: "walk.all", Summary: "list all", Safety: plugin.Read, Run: answer},
	}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, dash, nil)
	m.width, m.height = 100, 40
	return m
}

// search types query into the search box, opened with /.
func search(t *testing.T, m Model, query string) Model {
	t.Helper()
	m = pump(t, m, keyMsg("/"))
	for _, r := range query {
		m = pump(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// esc walks back one screen at a time: the result to the form that sent it,
// open on what was typed; the form to the search it was opened from, with
// the query and its match; the search to the dashboard. It went from the
// result straight to the dashboard, the search and the form both gone, so a
// call run again with one box changed was typed again from the search.
// Running it again with `r` is the same screen, and keeps the form behind it.
func TestEscWalksBackOneScreenAtATime(t *testing.T) {
	m := search(t, walkModel(t, config.Dashboard{}), "walk.find")
	sel := m.searchSel
	m = pump(t, m, keyMsg("enter"))
	if m.mode != modeForm || m.form == nil {
		t.Fatalf("enter on the match opened mode %v, want its form", m.mode)
	}
	m.form.form = typeInto(m.form.form, "mine")
	m = pump(t, m, keyMsg("ctrl+s"))
	if m.mode != modeResult {
		t.Fatalf("the form sent its run and the screen is mode %v, want the result", m.mode)
	}
	m = pump(t, m, keyMsg("r"))
	if m.mode != modeResult {
		t.Fatalf("r on the result left mode %v", m.mode)
	}

	m = pump(t, m, keyMsg("esc"))
	if m.mode != modeForm || m.form == nil {
		t.Fatalf("esc on the result went to mode %v, want the form that sent it", m.mode)
	}
	if got := *m.form.bindings["name"]; got != "mine" {
		t.Errorf("the form came back with name %q, want what was typed", got)
	}

	m = pump(t, m, keyMsg("esc"))
	if m.mode != modeDashboard || !m.searchEditing || m.query != "walk.find" || m.searchSel != sel {
		t.Fatalf("esc on the form left mode %v, the search open %v on %q at match %d; want the search it was opened from",
			m.mode, m.searchEditing, m.query, m.searchSel)
	}

	m = pump(t, m, keyMsg("esc"))
	if m.searchEditing || m.query != "" {
		t.Errorf("esc on the search left it open on %q, want the dashboard", m.query)
	}
}

// A match with nothing to ask runs on enter, and esc on its answer is the
// search again: there is no form between them to walk back to.
func TestEscOnAQuickRunIsTheSearchAgain(t *testing.T) {
	m := search(t, walkModel(t, config.Dashboard{}), "walk.all")
	m = pump(t, m, keyMsg("enter"))
	if m.mode != modeResult {
		t.Fatalf("enter on a match with nothing to ask opened mode %v, want its result", m.mode)
	}
	m = pump(t, m, keyMsg("esc"))
	if m.mode != modeDashboard || !m.searchEditing || m.query != "walk.all" || m.form != nil {
		t.Errorf("esc on the answer left mode %v, the search open %v on %q, a form %v; want the search",
			m.mode, m.searchEditing, m.query, m.form != nil)
	}
}

// A flow the search did not start walks back to the dashboard and no further:
// a tile's result has no form and no query behind it.
func TestATilesResultWalksBackToTheDashboard(t *testing.T) {
	m := walkModel(t, config.Dashboard{Tiles: []config.Tile{{ID: "walk.all"}}})
	m.selected = 1
	m = pump(t, m, keyMsg("enter"))
	if m.mode != modeResult {
		t.Fatalf("enter on the tile opened mode %v, want its result", m.mode)
	}
	m = pump(t, m, keyMsg("esc"))
	if m.mode != modeDashboard || m.searchEditing || m.query != "" || m.form != nil {
		t.Errorf("esc on a tile's result left mode %v, the search open %v on %q, a form %v; want the dashboard",
			m.mode, m.searchEditing, m.query, m.form != nil)
	}
}
