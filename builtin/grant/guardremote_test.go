package grant

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"

	"github.com/this-is-tobi/rta/internal/filelock"
	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/view"
)

// holdTransition takes the lock the guard's own state changes take, so a test
// can stop a `guard on`, `off` or `remote` at the moment it waits for it and
// see what the rest of the machine can do meanwhile. The returned func lets it
// go; so does the end of the test.
func holdTransition(t *testing.T) func() {
	t.Helper()
	release, err := filelock.Acquire(guard.Path()+".lock",
		15*time.Second, 5*time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	let := func() { once.Do(release) }
	t.Cleanup(let)
	return let
}

// waitCleared waits until the grant file holds nothing, which is the moment a
// held guard transition has cleared the grants and not yet changed the guard.
func waitCleared(t *testing.T, transition <-chan error) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		select {
		case err := <-transition:
			t.Fatalf("the transition finished while its lock was held: %v", err)
		default:
		}
		if held, verr := core.Load(); verr == nil && len(held) == 0 {
			return
		}
	}
	t.Fatal("the transition never cleared the grants")
}

// pending gives a write started during a held transition the time to land if
// it can: it returns as soon as the write does, or after a pause long enough
// that the write is plainly waiting on a lock. The write's answer is read from
// the returned func either way, once the transition has been let go.
func pending[T any](done <-chan T) func() T {
	select {
	case v := <-done:
		return func() T { return v }
	case <-time.After(200 * time.Millisecond):
		return func() T { return <-done }
	}
}

func rosterOf(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for i := range n {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "operator-%d %s\n", i, base64.StdEncoding.EncodeToString(pub))
	}
	path := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func unguardedGrant() core.Grant {
	now := time.Now()
	return core.Grant{Target: "note.add", Agent: "test", Issued: now, Expires: now.Add(15 * time.Minute)}
}

// A `grant allow` that lands while a roster is being enrolled is refused under
// the new guard, not stored unsigned beside it. The clear and the enrollment
// were two writes under two locks, and an issuance between them passed the
// guard check (still off) and wrote a row the enrolled guard then refused as
// forged on every read, taking every grant-gated call with it.
func TestAGrantIssuedWhileARosterIsEnrolledIsRefusedNotStoredUnsigned(t *testing.T) {
	setup(t)
	if verr := core.Issue(unguardedGrant(), true); verr != nil {
		t.Fatal(verr)
	}
	enroll := guardCap(t, "grant.guard.remote")
	roster := rosterOf(t, 1)

	let := holdTransition(t)
	enrolled := make(chan error, 1)
	go func() {
		_, err := enroll.Run(context.Background(),
			reqTUI(map[string]any{"operators": roster, "url": "https://rta.example.com"}))
		enrolled <- err
	}()
	waitCleared(t, enrolled)

	issued := make(chan *view.Error, 1)
	go func() { issued <- core.Issue(unguardedGrant(), true) }()
	answer := pending(issued)
	let()
	if err := <-enrolled; err != nil {
		t.Fatalf("the enrollment failed: %v", err)
	}

	if landed := answer(); landed == nil {
		t.Error("an unsigned grant was stored during the enrollment")
	} else if landed.Code != "core.grant.guard.unsigned" {
		t.Errorf("the issuance was refused with %s, want core.grant.guard.unsigned", landed.Code)
	}
	if _, verr := core.Load(); verr != nil {
		t.Fatalf("the grants do not read back after the enrollment: %v", verr)
	}
	if !guard.Remote() {
		t.Fatal("the guard is not in remote mode")
	}
}

// An enrollment the guard turns away leaves the grants where they were. The
// roster's size is checked when the guard state is written, and that came
// after the grants were cleared, so a roster too large to enroll cleared every
// grant and enabled nothing.
func TestAnEnrollmentTheGuardRefusesLeavesTheGrants(t *testing.T) {
	setup(t)
	if verr := core.Issue(unguardedGrant(), true); verr != nil {
		t.Fatal(verr)
	}
	_, err := guardCap(t, "grant.guard.remote").Run(context.Background(),
		reqTUI(map[string]any{"operators": rosterOf(t, 4000), "url": "https://rta.example.com"}))
	if got := code(t, err); got != "core.guard.remote.size" {
		t.Fatalf("code = %s, want core.guard.remote.size", got)
	}
	if guard.Enabled() {
		t.Fatal("the refusal still enabled the guard")
	}
	if held, verr := core.Load(); verr != nil || len(held) != 1 {
		t.Fatalf("held = %+v, %v — want the grant that stood before", held, verr)
	}
}

// The provisioning flow at the capability layer: enroll a roster as this
// machine's guard, watch local issuance close, and watch the off-switch
// clear what the operators signed — no passphrase at either end, because
// in remote mode there is none on this machine to ask for.
func TestGuardRemoteClosesLocalIssuance(t *testing.T) {
	setup(t)
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
	setup(t)
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
	setup(t)
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
