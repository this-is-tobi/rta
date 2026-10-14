package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func unknownToolAnswer(t *testing.T, s *sdk.ClientSession, name string) string {
	t.Helper()
	_, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name})
	if err == nil {
		t.Fatalf("%s answered a call", name)
	}
	return err.Error()
}

// An agent that mistypes one of two hundred tool names was told only that the
// tool is unknown, and had to fetch the whole list again or guess at another
// tool; the command line and `rta explain` both answer a near miss with the
// name it was probably meant to be.
func TestAMisspeltToolNameIsAnsweredWithTheNearestOfferedOne(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connect(t, Options{})
	for typed, want := range map[string]string{
		"demo_item_lis":  "demo_item_list",
		"demo_item_lisT": "demo_item_list",
		"demo_item_sett": "demo_item_set",
	} {
		if got := unknownToolAnswer(t, s, typed); !strings.Contains(got, "did you mean "+want+"?") {
			t.Errorf("%s was answered %q, want it to offer %s", typed, got, want)
		}
	}
	entries, err := agentlog.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Code != "core.mcp.unknown" {
		t.Fatalf("the probe left no row once it was answered with a suggestion: %+v", entries)
	}
}

// What is offered back is what is offered and nothing else, and a name nothing
// is near is answered as it always was. A capability that is never a tool
// stays indistinguishable from a name that does not exist: it is judged by the
// same rule over the same offered names, so it can neither be suggested nor
// be told apart by what it is answered with.
func TestTheSuggestedToolIsOnlyEverOneThatIsOffered(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := connect(t, Options{})
	if _, ok := listTools(t, s)["demo_item_human"]; ok {
		t.Fatal("the fixture's human-only capability is offered as a tool")
	}
	for _, typed := range []string{"demo_item_human", "demo_item_huma"} {
		got := unknownToolAnswer(t, s, typed)
		if strings.Contains(got, "did you mean demo_item_human?") {
			t.Errorf("%s was answered with the capability that is not a tool: %q", typed, got)
		}
		if !strings.Contains(got, "did you mean demo_item_humanonly?") {
			t.Errorf("%s was answered %q, want the offered tool that is nearest", typed, got)
		}
	}
	if got := unknownToolAnswer(t, s, "zzz_nothing_like_a_tool"); strings.Contains(got, "did you mean") ||
		!strings.Contains(got, `unknown tool "zzz_nothing_like_a_tool"`) {
		t.Errorf("a name nothing is near was answered %q, want the plain refusal", got)
	}
	if got := unknownToolAnswer(t, s, strings.Repeat("demo_item_", 5000)); strings.Contains(got, "did you mean") {
		t.Errorf("a name of fifty thousand letters was matched to a tool")
	}
}

// areaServer offers tools in three areas, one of which holds a capability only
// a person may use beside a tool that writes: the shape in which a name two
// edits from a tool is a different thing, not a slip.
func areaServer(t *testing.T) *sdk.ClientSession {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	var caps []plugin.Capability
	for _, id := range []string{"kv.get", "kv.history", "kv.init", "kv.list", "kv.recipients", "kv.rekey",
		"kv.rename", "kv.restore", "pg.schema.dump", "pg.table.list", "git.log"} {
		safety := plugin.Read
		if id == "kv.init" || id == "kv.rename" {
			safety = plugin.Write
		}
		caps = append(caps, plugin.Capability{ID: id, Summary: "works", Safety: safety, Run: run})
	}
	caps = append(caps, plugin.Capability{ID: "kv.edit", Summary: "for a person", Safety: plugin.Write,
		HumanOnly: true, Run: run})
	reg := registry.New()
	for _, area := range []string{"kv", "pg", "git"} {
		p := plugin.Plugin{Name: area, Summary: area}
		for _, c := range caps {
			if plugin.Namespace(c.ID) == area {
				p.Capabilities = append(p.Capabilities, c)
			}
		}
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return connectWith(t, reg, Options{})
}

// A tool name is three words, and two tools two edits apart are two tools: a
// suggestion is in the area the word names and one slip from it, or the start
// of the only tool it could be the start of. The spellings an agent writes out
// of a capability's ID or a flag are the underscore they stand for.
func TestASuggestedToolIsInTheSameAreaAndOneSlipAway(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	s := areaServer(t)
	for typed, want := range map[string]string{
		"kv_gett":       "kv_get",
		"kv_gte":        "kv_get",
		"KV_GET":        "kv_get",
		"kv.get":        "kv_get",
		"kv-get":        "kv_get",
		"pg.table.list": "pg_table_list",
		"pg_schema":     "pg_schema_dump",
		"git_lg":        "git_log",
		"kv_histry":     "kv_history",
	} {
		if got := unknownToolAnswer(t, s, typed); !strings.Contains(got, "did you mean "+want+"?") {
			t.Errorf("%s was answered %q, want it to offer %s", typed, got, want)
		}
	}
	for typed, why := range map[string]string{
		// A person's verb two edits from a tool that writes: what the old
		// allowance called a typo.
		"kv_edit": "kv_init is a different tool, and it writes",
		// The start of four tools names none of them.
		"kv_re": "four tools start with it",
		// One edit from kv_list, in an area that is not kv's.
		"kb_list": "the area has a typo in it",
		// One edit from git_log, in another area's.
		"pg_log": "no pg tool is one slip from it, and git is not its area",
		// Two letters dropped from kv_history, which is a second slip.
		"kv_hstry": "it is two edits from the only tool near it",
	} {
		if got := unknownToolAnswer(t, s, typed); strings.Contains(got, "did you mean") {
			t.Errorf("%s was answered %q, but %s", typed, got, why)
		}
	}
}

// The capabilities nobody but a person may use, over the real catalogue and on
// both transports. Each was an unknown tool that was answered with the nearest
// word by a third of its length, and so with a tool of another kind: `kv_edit`
// with the one that writes (`kv_init`), `agent_log` with `git_log`, `lock_list`
// with `note_list`. A name that is never a tool is answered as one that does
// not exist, by whatever it is near.
func TestNoCapabilityOnlyAPersonCanUseIsAnsweredWithATool(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, remote := range []bool{false, true} {
		// Seventy probes in a row is what the refusal backoff exists to slow.
		s := connectWith(t, reg, Options{Remote: remote, refusals: newBackoff(1<<30, time.Minute, 0, 0)})
		offered := listTools(t, s)
		checked := 0
		for _, c := range reg.Capabilities() {
			if !c.HumanOnly {
				continue
			}
			name := plugin.ToolName(c.ID)
			if _, isTool := offered[name]; isTool {
				t.Fatalf("%s is for a person and is offered as a tool", name)
			}
			checked++
			if got := unknownToolAnswer(t, s, name); strings.Contains(got, "did you mean") {
				t.Errorf("remote=%v: %s, which is never a tool, was answered %q", remote, name, got)
			}
		}
		if checked < 30 {
			t.Fatalf("remote=%v: only %d capabilities are for a person alone, so this checks too little", remote, checked)
		}
	}
}
