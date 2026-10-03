package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// sized is a registry whose one capability answers with as many bytes as it
// is asked for.
func sized(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "size", Summary: "size", Capabilities: []plugin.Capability{{
			ID: "size.dump", Summary: "answers with that many bytes", Safety: plugin.Read, Idempotent: true,
			Inputs: []plugin.Field{{Name: "bytes", Type: plugin.Int, Required: true, Help: "how many"}},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: strings.Repeat("x", req.Int("bytes"))}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// connectWithData is connectWith over a data directory of its own, which is
// what the record and the grants the call touches are kept in.
func connectWithData(t *testing.T, reg *registry.Registry, opts Options) *sdk.ClientSession {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	return connectWith(t, reg, opts)
}

// An answer is held in the gRPC buffer, the decoded view, the cleaned copy and
// the JSON twice, and a model cannot read one of any size anyway: past what an
// operator allows, the answer is withheld and the caller told its size and how
// to ask for less. The call ran, and the record says so beside why the answer
// was not given.
func TestAnAnswerOverTheLimitIsWithheldWithItsSizeAndHowToAskForLess(t *testing.T) {
	s := connectWithData(t, sized(t), Options{MaxResult: 1 << 20})
	res := callTool(t, s, "size_dump", map[string]any{"bytes": 3 << 20})
	if !res.IsError {
		t.Fatal("an answer of 3 MiB came back through a 1 MiB limit")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	for _, want := range []string{"core.result.toolarge", "3.0 MiB", "1.0 MiB", "narrow what the call asks for", "--max-result"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q: %.300s", want, text)
		}
	}
	if len(text) > 4<<10 {
		t.Errorf("the refusal is %d bytes: it carries the answer it withheld", len(text))
	}

	rows, err := agentlog.Read(1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the record: %v, %v", rows, err)
	}
	if rows[0].Outcome != agentlog.Ran || rows[0].Code != "core.result.toolarge" {
		t.Errorf("the row is %s %q, want a call that ran with the code that says its answer was withheld",
			rows[0].Outcome, rows[0].Code)
	}

	if res := callTool(t, s, "size_dump", map[string]any{"bytes": 512 << 10}); res.IsError {
		t.Fatalf("an answer under the limit was withheld: %.200s", res.Content[0].(*sdk.TextContent).Text)
	}
}

// The limit is on by default: a server nobody configured does not hand out an
// answer of any size.
func TestAnAnswerIsBoundedWhereTheOperatorSetNoLimit(t *testing.T) {
	s := connectWithData(t, sized(t), Options{})
	if res := callTool(t, s, "size_dump", map[string]any{"bytes": 9 << 20}); !res.IsError {
		t.Fatal("an answer of 9 MiB came back from a server with no limit set")
	}
	if res := callTool(t, s, "size_dump", map[string]any{"bytes": 1 << 20}); res.IsError {
		t.Fatalf("an answer of 1 MiB was withheld by the default: %.200s", res.Content[0].(*sdk.TextContent).Text)
	}
}
