package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// agreeConfig gives two namespaces connections and a third one none, so every
// answer below has a tool that takes a profile and a tool that does not,
// whatever its safety class.
const agreeConfig = `
profiles:
  staging:
    plugins:
      pg:
        set:
          host: staging.internal
      cache:
        set:
          host: cache.staging.internal
`

// agreeServer is a server over six tools: one that reads and one that needs a
// grant in each of a namespace with connections (pg), one without (db), and a
// namespace nothing could configure a connection for (clock).
func agreeServer(t *testing.T) *sdk.ClientSession {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	connected := func(id string, safety plugin.Safety) plugin.Capability {
		return plugin.Capability{ID: id, Summary: "works", Safety: safety, Run: run,
			Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host", Local: true}}}
	}
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		{Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{
			connected("pg.query", plugin.Read), connected("pg.drop", plugin.Write)}},
		{Name: "cache", Summary: "cache", Capabilities: []plugin.Capability{connected("cache.stats", plugin.Read)}},
		{Name: "db", Summary: "db", Capabilities: []plugin.Capability{
			connected("db.status", plugin.Read), connected("db.drop", plugin.Write)}},
		{Name: "clock", Summary: "clock", Capabilities: []plugin.Capability{
			{ID: "clock.now", Summary: "the time", Safety: plugin.Read, Run: run}}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := writeFile(dir+"/config.yaml", agreeConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	return connectWith(t, reg, Options{Origin: reg.Origin, Config: resolver.For, Profiles: cfg, Reload: config.Load})
}

// Whether a tool takes a profile is one fact, and every place that says
// something about it said it from its own copy of the condition: the schema
// published the argument, listed it as required, the tool's text told a model
// a grant names a connection, and the host refused a call that named none.
// Held together over a server with connections in some namespaces and not in
// others, so an edit to one of them cannot leave a client validating against
// a schema the host does not hold it to.
func TestEveryAnswerAboutAToolsProfileAgrees(t *testing.T) {
	s := agreeServer(t)
	// takes is whether a call names a connection; grant is whether the tool
	// costs a grant, which is when its text says what the grant names.
	table := map[string]struct{ takes, grant bool }{
		"pg_query": {true, false}, "pg_drop": {true, true}, "cache_stats": {true, false},
		"db_status": {false, false}, "db_drop": {false, true}, "clock_now": {false, false},
	}
	tools := listTools(t, s)
	if len(tools) != len(table) {
		t.Fatalf("the server offers %d tools, the table has %d", len(tools), len(table))
	}
	for name, row := range table {
		want := row.takes
		tool := tools[name]
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		_, published := schema.Properties["profile"]
		if published != want {
			t.Errorf("%s: the schema publishes profile = %v, want %v", name, published, want)
		}
		if required := slices.Contains(schema.Required, "profile"); required != want {
			t.Errorf("%s: the schema requires profile = %v, want %v", name, required, want)
		}
		if said := strings.Contains(tool.Description, "--profile <profile>"); said != (row.takes && row.grant) {
			t.Errorf("%s: the text tells a model a grant names a connection = %v, want %v:\n%s",
				name, said, row.takes && row.grant, tool.Description)
		}
		if row.grant && !row.takes && !strings.Contains(tool.Description, "grant allow db.drop") {
			t.Errorf("%s: a tool with no connection is told to ask for a grant naming one:\n%s", name, tool.Description)
		}
		res := callTool(t, s, name, map[string]any{})
		refused := res.IsError && strings.Contains(contentText(t, res), "core.profile.required")
		if refused != want {
			t.Errorf("%s: a call naming no profile is refused for it = %v, want %v: %s", name, refused, want, contentText(t, res))
		}

		// The argument is taken exactly where it is published, and the list of
		// what a refused call may send is the schema's own: a call that names a
		// profile where the schema has none is an argument the tool does not
		// have, and is never told it was.
		named := contentText(t, callTool(t, s, name, map[string]any{"profile": "staging"}))
		if unknown := strings.Contains(named, `unknown argument: \"profile\"`); unknown == want {
			t.Errorf("%s: a call naming a profile is refused as an unknown argument = %v, want %v: %s", name, unknown, !want, named)
		}
		if !want && strings.Contains(named, "grant allow") {
			t.Errorf("%s: a call naming a profile where there is none is sent to a command that cannot work: %s", name, named)
		}
		var accepted []string
		for property := range schema.Properties {
			accepted = append(accepted, property)
		}
		slices.Sort(accepted)
		hinted := []string{}
		var refusal struct {
			Hint string `json:"hint"`
		}
		if err := json.Unmarshal([]byte(contentText(t, callTool(t, s, name, map[string]any{"bogus": 1}))), &refusal); err != nil {
			t.Fatal(err)
		}
		if list, ok := strings.CutPrefix(refusal.Hint, "accepted arguments: "); ok {
			hinted = strings.Split(list, ", ")
			slices.Sort(hinted)
		} else if refusal.Hint != "this tool takes no arguments" {
			t.Errorf("%s: the refusal's hint is %q", name, refusal.Hint)
		}
		if len(accepted) == 0 {
			accepted = []string{}
		}
		if !slices.Equal(hinted, accepted) {
			t.Errorf("%s: the refusal lists %v, the schema publishes %v", name, hinted, accepted)
		}
	}
}
