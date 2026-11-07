package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// areasRegistry has five areas, each there to be told apart from the others by
// something a filter could get wrong: kv holds a write and a capability only a
// person may use, pg a write, sys one that describes the machine (the locality
// gate hides it over HTTP), and lock nothing but a person's.
func areasRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil }
	capability := func(id string, safety plugin.Safety, tweak func(*plugin.Capability)) plugin.Capability {
		c := plugin.Capability{ID: id, Summary: "works", Safety: safety, Run: run}
		if tweak != nil {
			tweak(&c)
		}
		return c
	}
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		{Name: "kv", Summary: "store", Capabilities: []plugin.Capability{
			capability("kv.list", plugin.Read, nil),
			capability("kv.set", plugin.Write, nil),
			capability("kv.edit", plugin.Write, func(c *plugin.Capability) { c.HumanOnly = true }),
		}},
		{Name: "pg", Summary: "postgres", Capabilities: []plugin.Capability{
			capability("pg.query", plugin.Read, nil),
			capability("pg.drop", plugin.Write, nil),
		}},
		{Name: "sys", Summary: "host", Capabilities: []plugin.Capability{
			capability("sys.overview", plugin.Read, func(c *plugin.Capability) { c.HostSpecific = true }),
		}},
		{Name: "lock", Summary: "freeze an agent", Capabilities: []plugin.Capability{
			capability("lock.add", plugin.Write, func(c *plugin.Capability) { c.HumanOnly = true }),
		}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func toolNames(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	var names []string
	for name := range listTools(t, s) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// With no area named nothing changes, and with some named the list holds the
// tools of those and no others. The first half is what makes the second one
// worth believing: a filter that always emptied the list would pass it.
func TestAreasListOnlyTheToolsOfTheNamedAreas(t *testing.T) {
	reg := areasRegistry(t)
	all := []string{"kv_list", "kv_set", "pg_drop", "pg_query", "sys_overview"}
	if got := toolNames(t, connectWith(t, reg, Options{})); !slices.Equal(got, all) {
		t.Fatalf("with no area named the list is %v, want %v", got, all)
	}
	for areas, want := range map[string][]string{
		"pg":    {"pg_drop", "pg_query"},
		"kv,pg": {"kv_list", "kv_set", "pg_drop", "pg_query"},
		"PG":    {"pg_drop", "pg_query"},
		"sys":   {"sys_overview"},
	} {
		got := toolNames(t, connectWith(t, reg, Options{Areas: strings.Split(areas, ",")}))
		if !slices.Equal(got, want) {
			t.Errorf("--area %s lists %v, want %v", areas, got, want)
		}
	}
}

// What a client is told about the server is built from the tools it was sent,
// so a model is not pointed at an area it cannot reach and left to guess a
// spelling for a tool that is not there.
func TestTheHandshakeNamesOnlyTheAreasOffered(t *testing.T) {
	reg := areasRegistry(t)
	// What follows the sentence that lists them is the end of the handshake:
	// nothing here takes a profile or reveals a value.
	areasSaid := func(opts Options) string {
		_, said, _ := strings.Cut(connectWith(t, reg, opts).InitializeResult().Instructions, areasInstructions)
		return said
	}
	if got := areasSaid(Options{Areas: []string{"pg"}}); got != "pg." {
		t.Errorf("a server limited to pg names the areas %q, want pg alone", got)
	}
	if got := areasSaid(Options{Areas: []string{"pg", "kv"}}); got != "kv, pg." {
		t.Errorf("a server limited to pg and kv names the areas %q, want both, sorted", got)
	}
	if got := areasSaid(Options{}); got != "kv, pg, sys." {
		t.Errorf("a server limited to nothing names the areas %q, want every area it lists", got)
	}
}

// A tool outside the areas is not on the server, so a call naming it is what a
// call naming any tool the server does not have is: an unknown tool, written to
// the record, and answered with a suggestion drawn from the tools that are
// listed and from none that are not.
func TestACallToAToolOutsideTheAreasIsAnUnknownTool(t *testing.T) {
	reg := areasRegistry(t)
	open := Options{refusals: newBackoff(1<<30, time.Minute, 0, 0)}
	limited := open
	limited.Areas = []string{"pg"}

	if res, err := connectWith(t, reg, open).CallTool(context.Background(), &sdk.CallToolParams{Name: "kv_list"}); err != nil || res.IsError {
		t.Fatalf("kv_list on a server with no area named: %v %+v", err, res)
	}

	s := connectWith(t, reg, limited)
	got := unknownToolAnswer(t, s, "kv_list")
	if !strings.Contains(got, `unknown tool "kv_list"`) {
		t.Fatalf("kv_list on a server limited to pg was answered %q, want an unknown tool", got)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 || entries[0].Code != "core.mcp.unknown" {
		t.Errorf("the probe left %+v (%v), want a core.mcp.unknown row", entries, err)
	}
	if got := unknownToolAnswer(t, s, "kv_lis"); strings.Contains(got, "did you mean") {
		t.Errorf("a slip of kv_list was answered %q, naming a tool this server does not list", got)
	}
	if got := unknownToolAnswer(t, s, "pg_quer"); !strings.Contains(got, "did you mean pg_query?") {
		t.Errorf("a slip of a tool that is listed was answered %q, want its name", got)
	}
}

// An area is only ever a reason to list less. It brings back nothing the other
// reasons took away, and the gate is the one a call met before.
func TestAreasOnlyNarrow(t *testing.T) {
	reg := areasRegistry(t)

	if got := toolNames(t, connectWith(t, reg, Options{Areas: []string{"kv"}})); slices.Contains(got, "kv_edit") {
		t.Errorf("naming kv listed %v, which holds a capability that is for the person at the terminal", got)
	}
	if got := toolNames(t, connectWith(t, reg, Options{Areas: []string{"lock"}})); len(got) != 0 {
		t.Errorf("naming an area that holds nothing but a person's capabilities listed %v", got)
	}
	got := toolNames(t, connectWith(t, reg, Options{Areas: []string{"pg", "sys"}, Remote: true}))
	if want := []string{"pg_drop", "pg_query"}; !slices.Equal(got, want) {
		t.Errorf("--area pg --area sys over HTTP lists %v, want %v: the locality gate still hides sys", got, want)
	}
}

// The gate, the locks, the roles and consent run per call and never see a tool
// that is not listed. In an area that is listed they run as they did, and a
// grant for a tool outside the areas is inert: it does not make the tool
// callable, and it is still there when the filter is lifted.
func TestAreasLeaveTheGateWhereItWas(t *testing.T) {
	reg := areasRegistry(t)
	s := connectWith(t, reg, Options{Areas: []string{"kv"}, refusals: newBackoff(1<<30, time.Minute, 0, 0)})
	res := callTool(t, s, "kv_set", nil)
	if !res.IsError || !strings.Contains(contentText(t, res), "core.grant.required") {
		t.Fatalf("a write in a listed area was answered %q, want the gate's refusal", contentText(t, res))
	}

	now := time.Now()
	if verr := grant.Save([]grant.Grant{{Target: "pg.drop", Issued: now, Expires: now.Add(time.Hour)}}); verr != nil {
		t.Fatal(verr)
	}
	if got := unknownToolAnswer(t, s, "pg_drop"); !strings.Contains(got, `unknown tool "pg_drop"`) {
		t.Errorf("a granted tool outside the areas was answered %q, want an unknown tool", got)
	}
	lifted := connectWith(t, reg, Options{})
	if res := callTool(t, lifted, "pg_drop", nil); res.IsError {
		t.Errorf("the grant stopped covering pg.drop when the filter was lifted: %s", contentText(t, res))
	}
}

// An area nothing can be listed for is said at startup and does not stop the
// server: an operator who typed it is the one who can act on it, and a plugin
// installed on another machine is an ordinary state for a shared client config.
func TestProblemsNamesAnAreaNothingIsListedFor(t *testing.T) {
	reg := areasRegistry(t)
	for name, tc := range map[string]struct {
		opts Options
		want string
	}{
		"a typo":                {Options{Areas: []string{"kvv"}}, "--area kvv names no area this server has, so it offers nothing for it — did you mean kv?"},
		"nothing like it":       {Options{Areas: []string{"zzzzzz"}}, "the areas are kv, pg, sys"},
		"a person's area":       {Options{Areas: []string{"lock"}}, "--area lock is never a tool"},
		"hidden over HTTP":      {Options{Areas: []string{"sys"}, Remote: true}, "--area sys offers nothing over HTTP"},
		"an empty name":         {Options{Areas: []string{""}}, `--area "" names no area this server has`},
		"one of several is bad": {Options{Areas: []string{"pg", "kvv"}}, "--area kvv names no area"},
	} {
		got := strings.Join(tc.opts.Problems(reg), "\n")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: Problems says %q, want it to contain %q", name, got, tc.want)
		}
	}
	for name, opts := range map[string]Options{
		"no area":         {},
		"areas it has":    {Areas: []string{"kv", "pg"}},
		"another case":    {Areas: []string{"PG"}},
		"sys over stdio":  {Areas: []string{"sys"}},
		"an untrusted ap": {Areas: []string{"fresh"}, Untrusted: []string{"fresh"}},
	} {
		got := opts.Problems(reg)
		if name == "an untrusted ap" {
			// The line that already says the plugin was not run has the remedy;
			// a second one that the area is unknown would only repeat it.
			if len(got) != 1 || !strings.Contains(got[0], "rta plugin trust fresh") {
				t.Errorf("%s: Problems says %q, want the one line about the untrusted plugin", name, got)
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%s: Problems says %q, want nothing", name, got)
		}
	}
}

// What the startup banner says --http hides is what the locality gate took out
// of the list, not what the list never held.
func TestRemoteBlockedCountsOnlyWhatWouldBeListed(t *testing.T) {
	reg := areasRegistry(t)
	if got := (Options{Remote: true}).RemoteBlocked(reg); !slices.Equal(got, []string{"sys.overview"}) {
		t.Fatalf("RemoteBlocked = %v, want sys.overview", got)
	}
	if got := (Options{Remote: true, Areas: []string{"pg"}}).RemoteBlocked(reg); len(got) != 0 {
		t.Errorf("RemoteBlocked = %v for a server that never listed sys, want nothing", got)
	}
}

func TestAreasAreTheNamespacesAToolCanComeFrom(t *testing.T) {
	reg := areasRegistry(t)
	if got, want := Areas(reg), []string{"kv", "pg", "sys"}; !slices.Equal(got, want) {
		t.Errorf("Areas = %v, want %v: lock holds only a person's capabilities", got, want)
	}
	for _, opts := range []Options{{}, {Areas: []string{"pg"}}, {Remote: true}, {Areas: []string{"kv", "sys"}, Remote: true}} {
		listed := len(listTools(t, connectWith(t, reg, opts)))
		if offered := len(opts.Offered(reg)); offered != listed {
			t.Errorf("%+v: Offered counts %d tools and the client is sent %d", opts.Areas, offered, listed)
		}
	}
}
