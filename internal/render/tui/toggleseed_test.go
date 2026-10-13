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

// A toggle nobody touched is not an answer: it holds off when nothing says
// otherwise, and moving the picker to an environment that does say otherwise
// must show that — the form was rebuilt with the untouched off handed back as a
// value the person gave, which then outranked the environment it was moved to.
func TestAnUntouchedToggleFollowsThePickerToAnEnvironmentThatSetsIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"alpha": {Plugins: map[string]config.Connection{"db": {Set: map[string]any{"host": "alpha.internal"}}}},
		"bravo": {Plugins: map[string]config.Connection{"db": {Set: map[string]any{"tls": true}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	c := plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true},
			{Name: "tls", Type: plugin.Bool, Config: "tls", Local: true},
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40

	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	if got := *nm.form.bools["tls"]; got {
		t.Fatal("the toggle is on under the base configuration, which says nothing about it")
	}

	for _, want := range []string{"alpha", "bravo"} {
		model, _ = nm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		nm = model.(Model)
		nm.form.form = startedForm(nm.form)
		if got := *nm.form.bindings[profileInput]; got != want {
			t.Fatalf("the picker is on %q, want %q", got, want)
		}
	}
	if got := *nm.form.bools["tls"]; !got {
		t.Error("the toggle is off under an environment that sets it on: the untouched off was kept as an answer")
	}
	if v, given := nm.form.values()["tls"]; given {
		t.Errorf("tls = %v came back as a caller value from a box that only displays the environment's", v)
	}
}
