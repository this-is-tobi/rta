package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A read an agent is refused over a profile is relayed as the grant for that
// read, and the grant on the whole plugin comes after it with what it covers.
// It was the plugin's alone: the operator who pasted it let the agent's next
// call, a delete, through as well.
func TestARefusedReadOverAProfileOffersThePluginSecond(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := writeFile(dir+"/config.yaml",
		"profiles:\n  scratch:\n    plugins:\n      prom:\n        set:\n          host: prom.internal\n"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	connected := func(id string, safety plugin.Safety) plugin.Capability {
		return plugin.Capability{ID: id, Summary: id, Safety: safety,
			Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ran"}, nil
			}}
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "prom", Summary: "prom", Capabilities: []plugin.Capability{
		connected("prom.overview", plugin.Read),
		connected("prom.query", plugin.Read),
		connected("prom.series.rm", plugin.Destructive),
	}}); err != nil {
		t.Fatal(err)
	}
	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	s := connectWith(t, reg, Options{
		Origin: reg.Origin, Config: resolver.For, Profiles: cfg, Reload: config.Load, Agent: "auditbot",
	})

	res := callTool(t, s, "prom_overview", map[string]any{"profile": "scratch"})
	if !res.IsError {
		t.Fatalf("an ungranted read over a profile ran: %s", contentText(t, res))
	}
	text := contentText(t, res)
	narrow := strings.Index(text, "rta grant allow prom.overview --profile scratch --agent auditbot --ttl 15m")
	wide := strings.Index(text, "rta grant allow prom --profile scratch --agent auditbot --ttl 15m")
	if narrow < 0 || wide < 0 || wide < narrow {
		t.Fatalf("the read's own grant is not relayed first and the plugin's second:\n%s", text)
	}
	if !strings.Contains(text, "3 capabilities an agent needs a grant for, 1 destructive (prom.series.rm)") {
		t.Errorf("the plugin is offered without what it covers:\n%s", text)
	}

	// The offer is worded from the catalogue and the call, never from the
	// operator's profiles, so a name nobody configured is refused in the
	// same words: the refusals still cannot be told apart.
	invented := callTool(t, s, "prom_overview", map[string]any{"profile": "made-up"})
	if a, b := strings.ReplaceAll(text, "scratch", "X"), strings.ReplaceAll(contentText(t, invented), "made-up", "X"); a != b {
		t.Errorf("a configured profile and an invented one are refused differently:\n  %s\n  %s", a, b)
	}
}
