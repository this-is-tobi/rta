package mcp

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The ledger row of a call on github.com written with a Cyrillic letter for its
// i drew the host as the one it imitates, in the record column and in the
// arguments, and the name it resolves as is the one thing that tells them
// apart. The call is refused for want of a grant, so nothing leaves the
// machine. The letter is planted by code point: a source file may not hold a
// lookalike.
func TestTheLedgerSaysWhatALookalikeHostResolvesAs(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{})
	host := "https://g" + string(rune(0x456)) + "thub.com/"
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "http_get", Arguments: map[string]any{"url": host}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "core.grant.required") {
		t.Fatalf("a call with no grant was not refused for want of one: %+v", res)
	}

	c, ok := reg.Capability("agent.log")
	if !ok {
		t.Fatal("no agent.log")
	}
	v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("agent log is %T", v)
	}
	const note = " (xn--gthub-n2e.com)"
	for column, want := range map[string]string{"record": host + note, "arguments": "url=" + host + note} {
		col := slices.IndexFunc(table.Columns, func(c view.Column) bool { return c.Name == column })
		if col < 0 || len(table.Rows) != 1 {
			t.Fatalf("agent log has no %s column or not one row: %+v", column, table)
		}
		if got := table.Rows[0][col]; !strings.Contains(got, want) {
			t.Errorf("the ledger's %s cell is %q, want it to hold %q", column, got, want)
		}
	}
}
