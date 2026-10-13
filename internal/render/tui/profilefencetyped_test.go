package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A box is held to the list as the declaration types the box: `5433` is an
// integer to a port's list and `30s` the same duration as `30000ms`, a list-shaped
// input is held an element at a time, and a number or a duration that does not
// read at all is the type's own refusal and not the fence's.
func TestABoxIsHeldToItsFenceByTheTypeItDeclares(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {Only: map[string]any{
			"port":    []any{5432, 5433},
			"wait":    []any{"30s"},
			"schemas": []any{"app", "jobs"},
			"ratio":   []any{0.5},
		}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		Inputs: []plugin.Field{
			{Name: "port", Type: plugin.Int, Config: "port"},
			{Name: "wait", Type: plugin.Duration, Config: "wait"},
			{Name: "schemas", Type: plugin.StringSlice, Config: "schemas"},
			{Name: "ratio", Type: plugin.Float, Config: "ratio"},
			{Name: "free", Type: plugin.String, Config: "free"},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	m.profileOpen = "staging"
	model, _ := m.startConnForm("db")
	nm := model.(Model)

	field := func(name string) plugin.Field {
		for _, f := range nm.form.fields {
			if f.Name == profileSetPrefix+name {
				return f
			}
		}
		t.Fatalf("no set.%s box", name)
		return plugin.Field{}
	}
	for _, tc := range []struct {
		box, typed string
		refused    string
	}{
		{"port", "5433", ""},
		{"port", "5434", "must be one of 5432, 5433"},
		{"wait", "30s", ""},
		{"wait", "30000ms", ""},
		{"wait", "45s", "must be one of 30s"},
		{"schemas", "app, jobs", ""},
		{"schemas", "app, other", "must be one of app, jobs"},
		{"ratio", "0.5", ""},
		{"ratio", "0.25", "must be one of 0.5"},
		{"free", "anything", ""},
	} {
		err := nm.form.validator(field(tc.box))(tc.typed)
		switch {
		case tc.refused == "" && err != nil:
			t.Errorf("set.%s = %q was refused: %v", tc.box, tc.typed, err)
		case tc.refused != "" && (err == nil || err.Error() != tc.refused):
			t.Errorf("set.%s = %q: %v, want %q", tc.box, tc.typed, err, tc.refused)
		}
	}
	// What the type refuses first is the type's.
	if err := nm.form.validator(field("port"))("high"); err == nil || err.Error() != "must be an integer" {
		t.Errorf("a port that is not a number: %v", err)
	}
}
