package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
)

// A plugin whose section rta could not apply is refused before any gate: the
// grant covering the call is not spent on it, and the agent is told only that
// the operator's configuration was not applied, while the record says which
// section and how to fix it. It ran on the plugin's defaults.
func TestAWithheldPluginIsRefusedBeforeTheGate(t *testing.T) {
	stale := pluginconf.Problem{Section: "demo@0000000000", Reason: "this pin does not match the installed demo",
		Hint: "the installed one is `demo@123456789abc`; `rta config repin --plugin demo` points the section at it"}
	s := connect(t, Options{Withheld: func(ns string) (pluginconf.Problem, bool) { return stale, ns == "demo" }})
	issue(t, grant.Grant{Target: "demo.item.rm", MaxUses: 1})

	text := refusalText(t, callTool(t, s, "demo_item_rm", map[string]any{"name": "first"}))
	if !strings.Contains(text, "core.config.unapplied") || !strings.Contains(text, "rta agent log --detail") {
		t.Errorf("the agent is not told the call cannot run, or where the reason is:\n%s", text)
	}
	if strings.Contains(text, "demo@0000000000") || strings.Contains(text, "123456789abc") {
		t.Errorf("the agent is told the operator's section and build:\n%s", text)
	}
	if rec := lastRecord(t); rec.Code != "core.config.unapplied" || !strings.Contains(rec.Note, "rta config repin --plugin demo") {
		t.Errorf("the record does not say which section and how to fix it: %+v", rec)
	}
	grants, verr := grant.Load()
	if verr != nil || len(grants) != 1 || grants[0].Uses != 0 {
		t.Errorf("the grant was spent on a call that could not run: %+v (%v)", grants, verr)
	}
}
