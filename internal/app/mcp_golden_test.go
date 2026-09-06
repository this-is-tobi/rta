package app

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/toolcall"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The MCP surface, written down.
//
// Every capability becomes a tool automatically, which is the point and also
// the risk: an edit to a plugin changes what an AI agent is offered, and
// nothing in an ordinary review makes that visible. This golden file is the
// diff. A new capability appears as an added tool; a renamed input, a changed
// safety class, a field newly marked Local — each shows up as a changed line,
// in review, before it reaches anybody's agent.
//
// It is taken through a real client session over the in-memory transport
// rather than by inspecting our own structs, so what is pinned is what a
// client is actually told.
//
// Update with: go test ./internal/app -update
//
// The test lives here rather than in internal/mcp because the real registry
// is assembled here, and a golden file of a made-up registry would guard
// nothing.

var update = flag.Bool("update", false, "rewrite the golden files")

// tool is the shape worth pinning: what a client is told, minus the ordering
// and framing details of whichever SDK version is installed.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ReadOnly    bool           `json:"read_only_hint,omitempty"`
	Destructive *bool          `json:"destructive_hint,omitempty"`
	Idempotent  bool           `json:"idempotent_hint,omitempty"`
	Schema      map[string]any `json:"input_schema"`
}

func surface(t *testing.T, opts mcp.Options) []tool {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := mcp.NewServer(reg, "golden", opts)
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "golden", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]tool, 0, len(res.Tools))
	for _, tl := range res.Tools {
		entry := tool{Name: tl.Name, Description: tl.Description}
		if tl.Annotations != nil {
			entry.ReadOnly = tl.Annotations.ReadOnlyHint
			entry.Destructive = tl.Annotations.DestructiveHint
			entry.Idempotent = tl.Annotations.IdempotentHint
		}
		// Round-trip the schema so the file holds JSON a person can read
		// rather than the SDK's internal representation.
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &entry.Schema); err != nil {
			t.Fatal(err)
		}
		out = append(out, entry)
	}
	return out
}

func golden(t *testing.T, name string, got any) {
	t.Helper()
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run `go test ./internal/app -update` to create it", err)
	}
	if string(want) != string(data) {
		t.Errorf("the MCP surface changed. If that was deliberate, re-run with -update and review the diff:\n" +
			"  go test ./internal/app -update")
	}
}

// What an agent sees, which is now one surface rather than three: every
// capability that is not for the person at the terminal is a tool, and what
// a call costs is a grant rather than a flag. A capability newly marked
// HumanOnly — or un-marked — shows up here as a diff in review.
func TestGoldenSurface(t *testing.T) {
	golden(t, "mcp-surface.json", surface(t, mcp.Options{}))
}

// …and the same surface over HTTP transport, so the locality gate's effect
// on the real registry is pinned too: every plugin.Capability.HostSpecific
// capability should be missing from this file and from no other.
func TestGoldenRemoteSurface(t *testing.T) {
	golden(t, "mcp-surface-remote.json", surface(t, mcp.Options{Remote: true}))
}

// The gate is the load-bearing part, and there is one of it: everything an
// agent can see is either free to call or costs a grant a person issued.
// Nothing is reachable on the strength of a flag, and nothing that changes
// anything is free.
//
// Asserted over the real registry rather than a fixture, because the failure
// this catches is a capability shipped with the wrong safety class — a write
// declared Read is reachable by every agent forever, and that mistake is
// made in a plugin, not here.
func TestNothingThatChangesAnythingIsFree(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]plugin.Capability{}
	for _, c := range reg.Capabilities() {
		byName[toolcall.Name(c.ID)] = c
	}
	for _, tl := range surface(t, mcp.Options{}) {
		c, ok := byName[tl.Name]
		if !ok {
			t.Errorf("%s is advertised and is in no catalogue", tl.Name)
			continue
		}
		if c.HumanOnly {
			t.Errorf("%s is for the person at the terminal and was advertised", tl.Name)
		}
		if !tl.ReadOnly && !grant.Required(c, "") {
			t.Errorf("%s can change something and costs no grant", tl.Name)
		}
		if tl.ReadOnly && grant.Required(c, "") && !c.NeedsGrant {
			t.Errorf("%s is annotated read-only and costs a grant for no declared reason", tl.Name)
		}
	}
}
