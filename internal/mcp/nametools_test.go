package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
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

// An input's help is the plugin's text, written once with the capability's ID,
// and it reaches a model in the schema, where the rename the description gets
// did not reach it: "`kube.context.list` shows them" named a tool that is
// called kube_context_list.
func TestAnInputsHelpNamesOtherToolsByTheirToolNames(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.context.set", Summary: "switch", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Help: "a context from `demo.context.list`, not demo.context.set.timeout"}},
	}
	tool := toolDef(c, Options{tools: map[string]bool{"demo.context.list": true, "demo.context.set": true}})
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "a context from `demo_context_list`, not demo.context.set.timeout") {
		t.Errorf("the schema still names a tool by its ID, or renamed an error code:\n%s", raw)
	}
}

// A namespace with connections takes only calls that name one, and a grant
// that names none covers none of them: the command the description hands the
// operator has to name a profile, or it is run, succeeds and changes nothing.
// And it names the capability, as the refusal does: the plugin's would have
// let every capability of it through on that profile, destructive ones
// included.
func TestAProfiledToolsGrantCommandNamesTheCapabilityAndAProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	const yaml = "profiles:\n  staging:\n    plugins:\n      demo:\n        set:\n          host: staging.internal\n"
	if err := os.WriteFile(dir+"/config.yaml", []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := plugin.Capability{
		ID: "demo.query", Summary: "run a query", Safety: plugin.Read, NeedsGrant: true,
		Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true}},
	}
	with := toolDef(c, Options{Profiles: cfg}).Description
	if !strings.Contains(with, "ask the operator to run `rta grant allow demo.query --profile <profile>`") {
		t.Errorf("a profiled namespace is handed a grant command that covers no call:\n%s", with)
	}
	if strings.Contains(with, "grant allow demo --profile") {
		t.Errorf("the description hands on a grant on the whole plugin for one capability:\n%s", with)
	}
	without := toolDef(c, Options{}).Description
	if !strings.Contains(without, "ask the operator to run `rta grant allow demo.query`") || strings.Contains(without, "--profile") {
		t.Errorf("a namespace with no connections lost its capability command:\n%s", without)
	}
}
