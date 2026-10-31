package tui

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// withheldConfig is a PluginConfig whose section for pg was not applied.
type withheldConfig struct{ p pluginconf.Problem }

func (w withheldConfig) For(string) map[string]any { return nil }
func (w withheldConfig) Section(string) string     { return "" }
func (w withheldConfig) Withheld(ns string) (pluginconf.Problem, bool) {
	return w.p, ns == "pg"
}
func (w withheldConfig) Displaced(string) bool { return false }

// A plugin whose section rta could not apply is refused in the TUI as it is at
// a terminal, the dry run a confirmation previews included, and its handler is
// never reached: it ran on the plugin's defaults.
func TestAWithheldPluginIsRefusedInTheTUI(t *testing.T) {
	ran := false
	c := plugin.Capability{ID: "pg.status", Summary: "status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ran = true
			return view.Text{Body: "ok"}, nil
		}}
	cfg := configOf(withheldConfig{pluginconf.Problem{Section: "pg@0000000000", Reason: "a stale pin"}}, c)

	rm, ok := runCmd(context.Background(), 1, c, nil, false, cfg, "", nil, config.Connection{}, false)().(resultMsg)
	if !ok || rm.err == nil || rm.err.Code != "core.config.unapplied" {
		t.Errorf("the run was not refused for the section: %+v", rm)
	}
	pm, ok := runCmd(context.Background(), 1, c, nil, false, cfg, "", nil, config.Connection{}, true)().(previewMsg)
	if !ok || pm.err == nil || pm.err.Code != "core.config.unapplied" {
		t.Errorf("the dry run was not refused for the section: %+v", pm)
	}
	if ran {
		t.Error("the handler ran without the operator's section")
	}
}
