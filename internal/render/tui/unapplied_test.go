package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// withheldConfig is a PluginConfig whose section for the plugin p names was not
// applied, and whose every section, with displaced, a dev build set aside.
type withheldConfig struct {
	p         pluginconf.Problem
	displaced bool
}

func (w withheldConfig) For(string) map[string]any { return nil }
func (w withheldConfig) Section(string) string     { return "" }
func (w withheldConfig) Withheld(ns string) (pluginconf.Problem, bool) {
	return w.p, w.p.Section != "" && strings.HasPrefix(w.p.Section, ns+"@")
}
func (w withheldConfig) Displaced(string) bool { return w.displaced }

// A tab is a call too. On a plugin whose section rta could not apply it asks the
// service nothing, as the run would not: the listing would have gone to the
// plugin's default address with the credential the environment exports.
func TestATabOnAWithheldPluginAsksTheServiceNothing(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_S3_SECRET_KEY", "exported")
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	m.pluginCfg = withheldConfig{p: pluginconf.Problem{Section: "s3@0000000000", Reason: "a stale pin"}}
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	nm = pastThePicker(t, nm)

	next, cmd := nm.Update(tabKey)
	if cmd != nil {
		cmd()
	}
	if lr.calls() != 0 {
		t.Fatalf("tab asked the service on the plugin's defaults, with secret-key %q",
			lr.last(t).String("secret-key"))
	}
	if got := next.(Model).flashBad; !strings.Contains(got, "plugins.s3@0000000000") {
		t.Errorf("the refusal does not name the section rta could not apply: %q", got)
	}
}

// A dev build's tab holds the exported credential as its run does
// (plugin.Inputs.HoldAmbient): the listing goes where nobody said, so the
// credential does not go with it.
func TestATabOnADisplacedPluginHoldsTheExportedCredential(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_S3_SECRET_KEY", "exported")
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	m.pluginCfg = withheldConfig{displaced: true}
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	nm = pastThePicker(t, nm)

	fetchFromCluster(t, nm)
	if got := lr.last(t).String("secret-key"); got != "" {
		t.Errorf("a dev build's listing carried the exported credential %q to an address nobody named", got)
	}
}

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
	cfg := configOf(withheldConfig{p: pluginconf.Problem{Section: "pg@0000000000", Reason: "a stale pin"}}, c)

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
