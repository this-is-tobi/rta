package app

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/mcp"
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

// What an agent sees with no flags at all: the read-only surface, which is
// the one most people will ever expose.
func TestGoldenReadOnlySurface(t *testing.T) {
	golden(t, "mcp-surface-read.json", surface(t, mcp.Options{}))
}

// …and everything an operator can turn on, so the write and destructive
// tiers are pinned too — those are the ones a grant stands between.
func TestGoldenFullSurface(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	// Every namespace and every destructive ID, spelled out. --allow-write is
	// a list of plugins rather than one boolean, so "the whole surface" is
	// now something a test has to enumerate — which is the point of the
	// change: there is no longer a single value that means "and everything
	// installed later, too".
	var destructive, namespaces []string
	for _, c := range reg.Capabilities() {
		if c.Safety == "destructive" {
			destructive = append(destructive, c.ID)
		}
	}
	for _, p := range reg.Plugins() {
		namespaces = append(namespaces, p.Name)
	}
	golden(t, "mcp-surface-all.json", surface(t, mcp.Options{
		AllowWrite: namespaces, AllowDestructive: destructive,
	}))
}

// …and the same full surface again, over HTTP transport, so the locality
// gate's effect on the real registry is pinned exactly like the safety
// gate's is above: every plugin.Capability.HostSpecific capability should be
// missing from this file and from no other, and a capability newly marked
// HostSpecific — or un-marked — shows up here as a diff in review.
func TestGoldenRemoteSurface(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var destructive, namespaces []string
	for _, c := range reg.Capabilities() {
		if c.Safety == "destructive" {
			destructive = append(destructive, c.ID)
		}
	}
	for _, p := range reg.Plugins() {
		namespaces = append(namespaces, p.Name)
	}
	golden(t, "mcp-surface-remote.json", surface(t, mcp.Options{
		AllowWrite: namespaces, AllowDestructive: destructive, Remote: true,
	}))
}

// The safety gate is the load-bearing part: nothing that can write or destroy
// may appear on the default surface, whatever anybody edits.
func TestDefaultSurfaceIsReadOnly(t *testing.T) {
	for _, tl := range surface(t, mcp.Options{}) {
		if !tl.ReadOnly {
			t.Errorf("%s is exposed by default without a read-only annotation", tl.Name)
		}
	}
}
