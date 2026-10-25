package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An agent is told which argument a refusal is about when it is one of the
// tool's arguments: the host's own refusals of a value, of a type, of a range,
// and a handler's refusal of one input. Never one the schema hides — a Local
// input is the operator's, refused like a typo when an agent sends it — and
// never a name the tool does not declare, which a plugin's handler can write.
func TestAnAgentIsToldTheArgumentARefusalIsAbout(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{{
		ID: "demo.item.find", Summary: "find an item", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Required: true, Help: "which item"},
			{Name: "limit", Type: plugin.Int, Min: 1, Max: 10, Default: 5, Help: "how many"},
			{Name: "passphrase", Type: plugin.Secret, Local: true, Help: "resolved by the host"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			switch req.String("name") {
			case "nosuch":
				return nil, view.Errorf("demo.nosuchitem", "no item called nosuch").WithInput("name")
			case "locked":
				return nil, view.Errorf("demo.wrongpass", "the passphrase was refused").WithInput("passphrase")
			case "stray":
				return nil, view.Errorf("demo.stray", "a handler naming nothing declared").WithInput("ghost")
			}
			return view.Text{Body: "found"}, nil
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connectWith(t, reg, Options{})
	told := func(args map[string]any) map[string]any {
		t.Helper()
		res := callTool(t, s, "demo_item_find", args)
		var m map[string]any
		if err := json.Unmarshal([]byte(contentText(t, res)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tc := range []struct {
		args        map[string]any
		code, input string
	}{
		{map[string]any{"name": "nosuch"}, "demo.nosuchitem", "name"},
		{map[string]any{}, "core.input.missing", "name"},
		{map[string]any{"name": "x", "limit": 99}, "core.input.range", "limit"},
		{map[string]any{"name": "x", "limit": "many"}, "core.mcp.badargs", "limit"},
		{map[string]any{"name": "locked"}, "demo.wrongpass", ""},
		{map[string]any{"name": "stray"}, "demo.stray", ""},
	} {
		m := told(tc.args)
		input, named := m["input"]
		if m["code"] != tc.code || (tc.input == "") == named || (named && input != tc.input) {
			t.Errorf("%v: the agent was told %v, want %s naming %q", tc.args, m, tc.code, tc.input)
		}
	}
}

// told is the error JSON an agent reads for a refused call.
func told(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(contentText(t, res)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A warning crosses the plugin wire as an error does, holding whatever name a
// handler wrote, so what an agent is told of one is held to what it is told of
// an error: a declared argument it sends, never a Local input or a name the
// tool does not have.
func TestAWarningNamesOnlyAnArgumentTheAgentSends(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{{
		ID: "demo.item.list", Summary: "list items", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Help: "which items"},
			{Name: "passphrase", Type: plugin.Secret, Local: true, Help: "resolved by the host"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{Warnings: []view.Error{
				{Code: "demo.a", Message: "a", Input: "name"},
				{Code: "demo.b", Message: "b", Input: "passphrase"},
				{Code: "demo.c", Message: "c", Input: "ghost"},
			}}, nil
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := told(t, callTool(t, connectWith(t, reg, Options{}), "demo_item_list", nil))
	ws, _ := m["warnings"].([]any)
	if len(ws) != 3 {
		t.Fatalf("the agent was told %v, want the three warnings", m)
	}
	for i, want := range []string{"name", "", ""} {
		w, _ := ws[i].(map[string]any)
		input, named := w["input"]
		if (want == "") == named || (named && input != want) {
			t.Errorf("warning %d told the agent %v, want it naming %q", i, w, want)
		}
	}
}

// A path outside the server's roots is refused naming the argument that held
// it, so an agent knows which one to send again.
func TestAPathOutsideTheRootsNamesItsArgument(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	guard, err := pathguard.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{{
		ID: "demo.list", Summary: "lists", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "dir", Type: plugin.Path, Help: "where"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "listed"}, nil },
	}}}); err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Paths: guard})
	m := told(t, callTool(t, s, "demo_list", map[string]any{"dir": t.TempDir()}))
	if m["code"] != "core.mcp.path.outside" || m["input"] != "dir" {
		t.Errorf("the agent was told %v, want core.mcp.path.outside naming dir", m)
	}
}

// The two arguments the host adds to a tool's schema are the tool's arguments
// to an agent as much as its own inputs are, so one of the wrong shape is
// refused naming it: detail on a tool with a detailed view, and profile on one
// a connection can be named for.
func TestAHostArgumentOfTheWrongShapeIsNamed(t *testing.T) {
	m := told(t, callTool(t, connect(t, Options{}), "demo_item_page", map[string]any{"detail": "true"}))
	if m["code"] != "core.mcp.badargs" || m["input"] != "detail" {
		t.Errorf("a detail of the wrong shape told the agent %v, want core.mcp.badargs naming detail", m)
	}
	f := newProfileFixture(t, twoProfiles)
	m = told(t, f.call(t, map[string]any{"profile": 3, "sql": "select 1"}))
	if m["code"] != "core.mcp.badargs" || m["input"] != "profile" {
		t.Errorf("a profile of the wrong shape told the agent %v, want core.mcp.badargs naming profile", m)
	}
}
