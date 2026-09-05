package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/view"
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

// A roster of nothing but role=read keys can watch a server, but a guard
// trusting nobody who signs could never honour a grant again — refused as
// the misconfiguration it is. A mixed roster enrolls the signers and names
// who stayed out, so the read-only rows are a decision on the record, not
// a silent drop.
func TestGuardRemoteNeedsASigner(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("one"); verr != nil {
		t.Fatal(verr)
	}
	watcher, _ := operatorid.RosterLine("dash")
	dir := t.TempDir()
	allRead := filepath.Join(dir, "watchers")
	if err := os.WriteFile(allRead, []byte(watcher+" role=read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := guardCap(t, "grant.guard.remote").Run(context.Background(),
		reqTUI(map[string]any{"operators": allRead, "url": "https://rta.example.com"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.guard.remote.readonly" {
		t.Fatalf("an all-read roster enrolled: %v, want core.guard.remote.readonly", err)
	}
	if guard.Enabled() {
		t.Fatal("the refusal still enabled the guard")
	}

	if err := os.Remove(operatorid.Path()); err != nil {
		t.Fatal(err)
	}
	if _, verr := operatorid.Init("two"); verr != nil {
		t.Fatal(verr)
	}
	signerLine, _ := operatorid.RosterLine("tobi")
	mixed := filepath.Join(dir, "operators")
	if err := os.WriteFile(mixed, []byte(signerLine+"\n"+watcher+" role=read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := guardCap(t, "grant.guard.remote").Run(context.Background(),
		reqTUI(map[string]any{"operators": mixed, "url": "https://rta.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	if !guard.Remote() {
		t.Fatal("the guard is not in remote mode")
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key != "operators" {
			continue
		}
		if !strings.Contains(p.Value, "tobi") || strings.Contains(p.Value, "dash") ||
			!strings.Contains(p.Value, "role=read") {
			t.Fatalf("operators cell = %q — want the signer named, the watcher counted out", p.Value)
		}
		return
	}
	t.Fatal("no operators pair in the enrollment view")
}
