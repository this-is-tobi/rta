package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The provisioning flow at the capability layer: enroll a roster as this
// machine's guard, watch local issuance close, and watch the off-switch
// clear what the operators signed — no passphrase at either end, because
// in remote mode there is none on this machine to ask for.
func TestGuardRemoteClosesLocalIssuance(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	line, verr := operatorid.RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	rosterPath := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(rosterPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := guardCap(t, "grant.guard.remote").Run(context.Background(),
		reqTUI(map[string]any{"operators": rosterPath, "url": "https://rta.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "operators" && strings.Contains(p.Value, "tobi") {
			found = true
		}
	}
	if !found {
		t.Fatal("the enrollment does not name tobi")
	}
	if !guard.Remote() {
		t.Fatal("the guard is not in remote mode")
	}

	// Local issuance: the passphrase flow reaches Unlock, and there is no
	// key to unlock — refused by construction, not by a missing grant.
	_, err = guardCap(t, "grant.allow").Run(context.Background(),
		reqTUI(map[string]any{"target": "kv.get", "ttl": "15m", "passphrase": "anything"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.guard.remote" {
		t.Fatalf("local allow under the remote guard: %v, want core.guard.remote", err)
	}

	// Off clears without a passphrase — there is none to ask for.
	if _, err := guardCap(t, "grant.guard.off").Run(context.Background(), reqTUI(nil)); err != nil {
		t.Fatal(err)
	}
	if guard.Enabled() {
		t.Fatal("the guard survived its off switch")
	}
	if held, verr := core.Load(); verr != nil || len(held) != 0 {
		t.Fatalf("held = %v, %v — want an empty store", held, verr)
	}
}

// Tearing down the remote guard from a non-interactive shell — the shape
// of an agent's command tool — is refused: rta must not be a quieter
// teardown than the rm it cannot prevent.
func TestRemoteGuardOffNeedsATerminal(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("p"); verr != nil {
		t.Fatal(verr)
	}
	line, _ := operatorid.RosterLine("tobi")
	rosterPath := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(rosterPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := guardCap(t, "grant.guard.remote").Run(context.Background(),
		reqTUI(map[string]any{"operators": rosterPath, "url": "https://rta.example.com"})); err != nil {
		t.Fatal(err)
	}
	_, err := guardCap(t, "grant.guard.off").Run(context.Background(),
		plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceCLI))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.guard.remote.terminal" {
		t.Fatalf("err = %v, want core.guard.remote.terminal", err)
	}
	if !guard.Enabled() {
		t.Fatal("the refusal still removed the guard")
	}
}
