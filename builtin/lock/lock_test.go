package lock

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/grant"
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
	if verr != nil || len(locks) != 1 || locks[0].By != grant.FromCommand {
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

// suggestLockedNames offers what lock.rm can actually lift, narrowed to the
// kind already chosen — an agent lock does not belong on a list offered
// under --kind credential, where it cannot match anything.
func TestSuggestLockedNamesIsFilteredByKind(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, err := capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "agent", "name": "claude"})); err != nil {
		t.Fatal(err)
	}
	if _, err := capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "credential", "name": "svc-token"})); err != nil {
		t.Fatal(err)
	}
	if got := suggestLockedNames(context.Background(), req(map[string]any{"kind": "agent"})); len(got) != 1 || got[0] != "claude" {
		t.Errorf("agent suggestions = %v, want [claude]", got)
	}
	if got := suggestLockedNames(context.Background(), req(map[string]any{"kind": "credential"})); len(got) != 1 || got[0] != "svc-token" {
		t.Errorf("credential suggestions = %v, want [svc-token]", got)
	}
	// A typo in kind is silent, per the Suggest contract, not a crash.
	if got := suggestLockedNames(context.Background(), req(map[string]any{"kind": "agnet"})); got != nil {
		t.Errorf("suggestions for a bad kind = %v, want nil", got)
	}
}

// The remote flow checks what it can before the passphrase is asked, so a
// typo costs a retype rather than an unlock and a round trip — the server
// would refuse the same thing, but only after both. Every refusal Build can
// produce is a typo of this kind: the kind, the principal's grammar, an
// over-long note, a ttl that is not a window. Pinned by the error arriving
// with no operator key and no remotes.yaml on this machine at all: anything
// attempted first would fail for one of those reasons instead.
func TestATypoIsRefusedBeforeAnythingElse(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml")) // never the machine's own remotes.yaml
	_, err := capByID(t, "lock.add").Run(context.Background(),
		req(map[string]any{"kind": "agnet", "name": "claude"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.lock.kind" {
		t.Fatalf("err = %v, want core.lock.kind", err)
	}
	for _, tc := range []struct {
		name   string
		values map[string]any
		code   string
	}{
		{"kind", map[string]any{"kind": "agnet", "name": "claude", "server": "work"}, "core.lock.kind"},
		{"ttl", map[string]any{"kind": "agent", "name": "claude", "ttl": "soon", "server": "work"}, "core.lock.ttl"},
		{"note", map[string]any{"kind": "agent", "name": "claude", "note": strings.Repeat("n", 300), "server": "work"}, "core.lock.note"},
		{"name", map[string]any{"kind": "agent", "name": "not a name", "server": "work"}, "grant.agent.charset"},
	} {
		_, err := capByID(t, "lock.add").Run(context.Background(), req(tc.values))
		verr, ok := err.(*view.Error)
		if !ok || verr.Code != tc.code {
			t.Errorf("a bad %s with --server: %v, want %s before any unlock", tc.name, err, tc.code)
		}
	}
	// A dry run aimed at a server says "would lock" only for a lock the
	// server would take.
	dry := plugin.NewRequest(map[string]any{"kind": "agent", "name": "not a name", "server": "work"}, true, true).
		WithSurface(plugin.SurfaceCLI)
	if v, err := capByID(t, "lock.add").Run(context.Background(), dry); err == nil {
		t.Errorf("a dry run with --server answered %+v for a name the real call refuses", v)
	}
}
