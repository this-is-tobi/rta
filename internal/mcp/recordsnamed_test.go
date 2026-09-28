package mcp

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A row's records are the records the call named, whether or not the gate
// judged them: a scoped read that needs no grant is let through before any
// grant is looked at, and its row keeps the record it named all the same,
// exactly. The field and the pages describing it said the records a call
// "was judged on", which such a row's are not.
func TestAScopedCallThatNeedsNoGrantKeepsTheRecordItNamed(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{{
			ID: "demo.item.show", Summary: "show an item", Safety: plugin.Read, Idempotent: true,
			Scope:  "name",
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Help: "which item"}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "shown"}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{})
	padded := "prod/db" + string(rune(0x200b))
	if _, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "demo_item_show", Arguments: map[string]any{"name": padded},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read %d entries: %v", len(entries), err)
	}
	e := entries[0]
	if e.Outcome != agentlog.Ran || e.Auth != agentlog.Open {
		t.Fatalf("the call was not let through without a grant: %+v", e)
	}
	if !slices.Equal(e.Records, []string{padded}) {
		t.Errorf("records = %q, want the record the call named, exactly", e.Records)
	}
}
