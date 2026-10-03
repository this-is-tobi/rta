package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A description naming a capability by ID sent a model after a tool its list
// does not contain: the tools are kv_get and net_probe, and the text said
// kv.get and net.probe. Only a whole ID is renamed; the words that merely
// begin like one are somebody's error code or file name.
func TestADescriptionNamesOtherToolsByTheirToolNames(t *testing.T) {
	tools := map[string]bool{"kv.get": true, "net.probe": true, "git.status": true}
	for name, tc := range map[string]struct{ in, want string }{
		"backticked":               {"use `kv.get` to read it", "use `kv_get` to read it"},
		"bare":                     {"git.status already answers which paths changed", "git_status already answers which paths changed"},
		"before a full stop":       {"ask net.probe.", "ask net_probe."},
		"an error code":            {"refused as git.status.timeout", "refused as git.status.timeout"},
		"a file":                   {"reads config.worktree", "reads config.worktree"},
		"a capability not offered": {"see kv.copy", "see kv.copy"},
		"in a path":                {"under docs/kv.get", "under docs/kv.get"},
		"twice":                    {"kv.get, then kv.get again", "kv_get, then kv_get again"},
		"nothing to rename":        {"plain words", "plain words"},
	} {
		if got := nameTools(tc.in, tools); got != tc.want {
			t.Errorf("%s: nameTools(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

// rta's own line stays a command line: the operator types the dotted ID, so
// renaming it there would hand them a command that does not exist.
func TestTheGrantCommandKeepsTheCapabilityID(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.thing.get", Summary: "see demo.thing.get", Safety: plugin.Write,
		Scope: "key",
	}
	desc := toolDef(c, Options{tools: map[string]bool{"demo.thing.get": true}}).Description
	if !strings.Contains(desc, "see demo_thing_get") {
		t.Errorf("the plugin's mention was not renamed:\n%s", desc)
	}
	if !strings.Contains(desc, "rta grant allow demo.thing.get") {
		t.Errorf("the operator's command lost its ID:\n%s", desc)
	}
}
