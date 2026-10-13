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

// everyTypePlugin is a plugin with one config-keyed input of every type, each
// with and without a declared default, because the shapes a form holds a
// "nothing" in differ: a text box is empty, a toggle is off, a picker holds its
// first choice, a list is no elements.
func everyTypePlugin() plugin.Plugin {
	return plugin.Plugin{
		Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "no-owner", Type: plugin.Bool, Config: "no-owner"},
				{Name: "tls", Type: plugin.Bool, Config: "tls", Default: true},
				{Name: "quiet", Type: plugin.Bool, Config: "quiet", Default: false},
				{Name: "retries", Type: plugin.Int, Config: "retries"},
				{Name: "port", Type: plugin.Int, Config: "port", Default: 5432},
				{Name: "ratio", Type: plugin.Float, Config: "ratio"},
				{Name: "weight", Type: plugin.Float, Config: "weight", Default: 0.5},
				{Name: "wait", Type: plugin.Duration, Config: "wait"},
				{Name: "patience", Type: plugin.Duration, Config: "patience", Default: "30s"},
				{Name: "tags", Type: plugin.StringSlice, Config: "tags"},
				{Name: "schemas", Type: plugin.StringSlice, Config: "schemas", Default: []string{"public", "app"}},
				{Name: "format", Type: plugin.String, Config: "format", Options: []string{"plain", "custom"}},
				{Name: "mode", Type: plugin.String, Config: "mode", Default: "prefer",
					Options: []string{"disable", "prefer"}},
				{Name: "kinds", Type: plugin.StringSlice, Config: "kinds", Options: []string{"table", "view"}},
				{Name: "levels", Type: plugin.StringSlice, Config: "levels", Default: []string{"warn"},
					Options: []string{"warn", "error"}},
				{Name: "host", Type: plugin.String, Config: "host", Default: "localhost"},
				{Name: "note", Type: plugin.String, Config: "note"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		}},
	}
}

// everyTypeModel is a model holding the plugin above, with an environment that
// names it and states nothing.
func everyTypeModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(everyTypePlugin()); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 60
	m.profileOpen = "staging"
	return m
}

// Saving the connection editor without touching a box writes nothing, whatever
// the type of the input behind the box. A profile's `set:` outranks the base
// `plugins:` section, so a key the form invented — the `false` a toggle holds
// when nothing says otherwise — would silently override what the operator wrote
// there for this environment.
func TestAnUntouchedConnectionSaveWritesNoKey(t *testing.T) {
	m := everyTypeModel(t)
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	for _, f := range everyTypePlugin().Capabilities[0].Inputs {
		if !nm.form.derived[profileSetPrefix+f.Name] {
			t.Fatalf("set.%s is not a display of a default: the file does not state it", f.Name)
		}
	}
	_, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if set := onDisk.Profiles["staging"].Plugins["db"].Set; len(set) != 0 {
		t.Errorf("an untouched save wrote %v into set:", set)
	}
}

// The plugin configuration editor reaches the same comparison, and a phantom
// `false` there is as wrong: it pins the toggle off in the section every
// environment without its own statement reads.
func TestAnUntouchedPluginConfigSaveWritesNoKey(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: everyTypePlugin()})
	nm := next.(Model)
	_, _ = nm.saveConfigForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if section := onDisk.Plugins["db"]; len(section) != 0 {
		t.Errorf("an untouched save wrote %v into plugins.db", section)
	}
}

// A toggle the person turned on is still written: the rule drops a display of
// the off state a toggle holds when nothing says otherwise, and never an answer.
func TestAToggleThatWasTurnedOnIsStillWritten(t *testing.T) {
	m := everyTypeModel(t)
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	*nm.form.bools[profileSetPrefix+"no-owner"] = true
	_, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Set["no-owner"]; got != true {
		t.Errorf("set.no-owner = %v, want the toggle that was turned on", got)
	}
}
