package lock

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func capByID(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %s", id)
	return plugin.Capability{}
}

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceCLI)
}

func TestLockAddListRmAtTheTerminal(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, err := capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "agent", "name": "claude", "note": "incident"})); err != nil {
		t.Fatal(err)
	}
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 || locks[0].By != "terminal" {
		t.Fatalf("the placed lock: %+v, %v", locks, verr)
	}
	v, err := capByID(t, "lock.list").Run(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok || len(table.Rows) != 1 || table.Rows[0][1] != "claude" {
		t.Fatalf("list = %+v", v)
	}
	v, err = capByID(t, "lock.rm").Run(context.Background(),
		req(map[string]any{"kind": "agent", "name": "claude"}))
	if err != nil {
		t.Fatal(err)
	}
	if kv, ok := v.(view.KeyValue); !ok || kv.Pairs[0].Key != "unlocked" {
		t.Fatalf("rm = %+v", v)
	}
	// Lifting what is not there says so instead of pretending.
	v, _ = capByID(t, "lock.rm").Run(context.Background(),
		req(map[string]any{"kind": "agent", "name": "claude"}))
	if kv, ok := v.(view.KeyValue); !ok || kv.Pairs[0].Key != "nothing to lift" {
		t.Fatalf("second rm = %+v", v)
	}
}

// Both directions matter: add would let an agent deny service to its
// operator's other agents, rm would let it unfreeze itself. Neither is a
// tool.
func TestNoLockCapabilityIsReachableOverMCP(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.HumanOnly {
			t.Errorf("%s is reachable over MCP", c.ID)
		}
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	dry := plugin.NewRequest(map[string]any{"kind": "agent", "name": "claude"}, true, true).
		WithSurface(plugin.SurfaceCLI)
	v, err := capByID(t, "lock.add").Run(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	if txt, ok := v.(view.Text); !ok || !strings.Contains(txt.Body, "would lock") {
		t.Fatalf("dry add = %+v", v)
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Fatalf("a dry run placed a lock: %+v", locks)
	}
}

func TestATypodKindIsRefusedBeforeAnythingElse(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	_, err := capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "agnet", "name": "claude"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.lock.kind" {
		t.Fatalf("err = %v, want core.lock.kind", err)
	}
	// The remote flow checks the kind before the passphrase is asked, so a
	// typo costs a retype, not an unlock — pinned by the error arriving
	// with no operator key on this machine at all.
	_, err = capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "agnet", "name": "claude", "server": "work"}))
	verr, ok = err.(*view.Error)
	if !ok || verr.Code != "core.lock.kind" {
		t.Fatalf("remote err = %v, want core.lock.kind before any unlock", err)
	}
}
