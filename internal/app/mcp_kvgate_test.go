package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
)

// kv.overview shows what kv.list shows — names, kinds, counts and sizes, and no
// value — and over MCP it is behind the gate kv.list is behind: the same safety
// class, the same grant rule, with a connection or without, and the same words in
// its own text about whether it costs one. A page that counted the store for an
// agent on easier terms than the listing it is made of, or on harder ones that
// sent an agent to ask for a grant it does not need, would be a gate with two
// answers to one question.
func TestKvOverviewIsBehindTheGateKvListIsBehind(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	list, ok := reg.Capability("kv.list")
	if !ok {
		t.Fatal("kv.list is not registered")
	}
	overview, ok := reg.Capability("kv.overview")
	if !ok {
		t.Fatal("kv.overview is not registered")
	}

	if overview.Safety != list.Safety || overview.NeedsGrant != list.NeedsGrant ||
		overview.Scope != list.Scope || overview.HumanOnly != list.HumanOnly || overview.Reveals != list.Reveals {
		t.Errorf("kv.overview is declared %s, grant %t, scope %q, human-only %t, reveals %t; kv.list %s, %t, %q, %t, %t",
			overview.Safety, overview.NeedsGrant, overview.Scope, overview.HumanOnly, overview.Reveals,
			list.Safety, list.NeedsGrant, list.Scope, list.HumanOnly, list.Reveals)
	}
	for _, profile := range []string{"", "prod"} {
		if got, want := grant.Required(overview, profile), grant.Required(list, profile); got != want {
			t.Errorf("a call on %q needs a grant: kv.overview %t, kv.list %t", profile, got, want)
		}
	}

	byName := map[string]tool{}
	for _, tl := range surface(t, mcp.Options{}) {
		byName[tl.Name] = tl
	}
	for _, name := range []string{"kv_list", "kv_overview"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s is not a tool an agent is offered", name)
		}
	}
	listTool, overviewTool := byName["kv_list"], byName["kv_overview"]
	if overviewTool.ReadOnly != listTool.ReadOnly || overviewTool.Destructive != nil || listTool.Destructive != nil {
		t.Errorf("the tools are annotated differently: kv_overview %+v, kv_list %+v", overviewTool, listTool)
	}
	// Said in the tool's own text as well as enforced, so a model asks for a
	// grant when it needs one and does not when it does not.
	const says = "Requires a grant"
	if strings.Contains(overviewTool.Description, says) != strings.Contains(listTool.Description, says) {
		t.Errorf("only one of the two says it costs a grant:\n kv_overview: %s\n kv_list: %s",
			overviewTool.Description, listTool.Description)
	}
}
