package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A box that states a value a read ignores says so under the box. Shaped like
// kube: its reads take a namespace as their scope, which a call names and no
// profile fills, and the one capability that provisions in a namespace is where a
// profile's value for it lands.
func TestTheConnectionEditorSaysWhichCapabilitiesAValueDoesNotReach(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"cluster": {}}},
	}}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	read := func(id string) plugin.Capability {
		return plugin.Capability{ID: id, Summary: id, Safety: plugin.Read, Run: run, Scope: "namespace",
			Inputs: []plugin.Field{
				{Name: "namespace", Type: plugin.String, Config: "namespace", Help: "namespace to read"},
				{Name: "context", Type: plugin.String, Config: "context", Local: true, Help: "kubeconfig context"},
			}}
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "cluster", Summary: "cluster", Capabilities: []plugin.Capability{
		read("cluster.pod.list"), read("cluster.event.list"),
		{ID: "cluster.sa.provision", Summary: "provision", Safety: plugin.Write, Run: run, Scope: "name",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true},
				{Name: "namespace", Type: plugin.String, Config: "namespace", Local: true, Help: "namespace to provision in"},
			}},
	}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 140, 60
	m.profileOpen = "staging"

	model, _ := m.startConnForm("cluster")
	nm := model.(Model)
	helps := map[string]string{}
	for _, f := range nm.form.fields {
		helps[f.Name] = f.Help
	}
	const note = "a value stated here does not reach cluster.event.list, cluster.pod.list: " +
		"they take it as their scope, which a call names"
	if got := helps[profileSetPrefix+"namespace"]; !strings.Contains(got, note) || !strings.HasPrefix(got, "namespace to read") {
		t.Errorf("the namespace box says %q, want its help and then %q", got, note)
	}
	if got := helps[profileSetPrefix+"context"]; strings.Contains(got, "does not reach") {
		t.Errorf("a key every capability takes was noted: %q", got)
	}
	nm.form.form = startedForm(nm.form)
	if view := ansi.Strip(nm.formView()); !strings.Contains(view, "does not reach cluster.event.list") {
		t.Errorf("the note is not on screen:\n%s", view)
	}
}
