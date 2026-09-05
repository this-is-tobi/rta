package profile

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// Every rule about "the forward fills the endpoint inputs" was written while
// `kube:` was the only tunnel, and it is on record what happens when a rule's
// twin goes unwritten: the `secrets:` copy of the `set:` shadowing rule
// simply did not exist. `ssh:` is the second scheme, so these tests are the
// twin-hunt made explicit — each one is an existing kube rule asserted to
// hold under `ssh:`, through the same Tunnelled predicate rather than a
// second per-scheme copy.

// fakeSSHOnPath puts a script called ssh at the front of $PATH — fakeKubectl's
// pattern, and the same argument for it: internal/tunnel resolves the binary
// by name at call time, so the test reaches the real exec.LookPath path with
// no test-only seam in the code under test.
func fakeSSHOnPath(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const sshTarget = "tobi@bastion.internal:2222/postgres.internal:5432"

// The whole chain under `ssh:`: Dial opens the tunnel, every endpoint role
// fills from rta's own listener, and the address handed to the plugin
// actually answers — with an echo, because for ssh the listener is rta's and
// an answering port proves nothing about the splice behind it.
func TestAnSSHTargetFillsEndpointsFromAForwardThatAnswers(t *testing.T) {
	// `exec cat`: the probe (stdin /dev/null) sees EOF and exits 0; a spliced
	// connection gets the socket as stdin and stdout, so the tunnel echoes.
	fakeSSHOnPath(t, "exec cat\n")

	conn := config.Connection{SSH: sshTarget}
	got, closeTunnel, verr := Dial(context.Background(), "bastion", conn, tunnelCap())
	if verr != nil {
		t.Fatalf("dial: %v", verr)
	}
	defer closeTunnel()

	port, ok := got["port"].(int)
	if got["host"] != "127.0.0.1" || !ok || port == 0 {
		t.Fatalf("filled %v:%v, want 127.0.0.1 and a live port", got["host"], got["port"])
	}
	if got["addr"] != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Errorf("addr = %v, want the joined endpoint", got["addr"])
	}
	if got["url"] != fmt.Sprintf("http://127.0.0.1:%d", port) {
		t.Errorf("url = %v, want http on the endpoint — the hop that leaves the "+
			"machine is already inside ssh", got["url"])
	}
	if got["sslmode"] != "disable" {
		t.Errorf("sslmode = %v, want the TLS-off value: the forward is loopback", got["sslmode"])
	}

	c, err := net.Dial("tcp", fmt.Sprint(got["addr"]))
	if err != nil {
		t.Fatalf("the address Dial filled in does not answer: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := c.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v — the endpoint answers but the splice carries nothing", buf, err)
	}
	closeTunnel()
	if _, err := net.Dial("tcp", fmt.Sprint(got["addr"])); err == nil {
		t.Fatal("the endpoint still answers after the teardown Dial returned")
	}
}

// One connection stating both schemes is two statements about where a call
// goes; refused by the report and the resolver in the same words, never
// resolved by preference.
func TestBothTunnelsAtOnceAreRefusedEverywhere(t *testing.T) {
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
        ssh: `+sshTarget+`
`)
	reg := tunnelledRegistry(t)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a connection stating kube: and ssh: resolved — which forward did it open?")
	}
	if verr.Code != "core.profile.tunnel" {
		t.Errorf("code = %s, want core.profile.tunnel", verr.Code)
	}
	if !strings.Contains(verr.Message, "states both") {
		t.Errorf("message = %q, want it to name the double statement", verr.Message)
	}
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "states both") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}
}

// A target that is not a target is caught before the call that needs it, by
// both the report and the resolver — the kube twin of this test earned its
// shape when the two disagreed.
func TestAMalformedSSHTargetIsCaughtByBothCheckAndLookup(t *testing.T) {
	for _, spec := range []string{
		"bastion.internal",                     // no destination
		"bastion.internal/postgres.internal",   // destination without port
		"-oProxyCommand=evil/db.internal:5432", // an option is not a host
		"tobi@/db.internal:5432",               // empty host
		"bastion.internal/db.internal:99999",   // port out of range
	} {
		cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+spec+`
`)
		reg := tunnelledRegistry(t)
		problems := Check(cfg, reg)
		_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
		switch {
		case len(problems) == 0 && verr == nil:
			t.Errorf("%q was accepted by both, and it is not an ssh target", spec)
		case len(problems) == 0:
			t.Errorf("%q: Lookup refused it and the report calls the profile fine", spec)
		case verr == nil:
			t.Errorf("%q: the report calls it invalid and Lookup resolved it anyway", spec)
		}
	}
}

// The `set:` shadowing rule holds under `ssh:` — same predicate, same words,
// with the scheme named so the operator removes the right line.
func TestAnEndpointSetKeyBesideAnSSHTargetIsRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+sshTarget+`
        set:
          host: stale.internal
          database: app
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a profile stating both an ssh target and set.host resolved — the file " +
			"names two destinations and one of them is dead")
	}
	if verr.Code != "core.profile.set" {
		t.Errorf("code = %s, want core.profile.set", verr.Code)
	}
	if !strings.Contains(verr.Message, "overridden by the forward `ssh:` opens") {
		t.Errorf("message = %q, want the ssh forward named as what shadows it", verr.Message)
	}

	// The refusal is about the shadowed key, not the pairing — the same
	// target over only non-endpoint keys resolves.
	clean := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+sshTarget+`
        set:
          database: app
`)
	if _, verr := Lookup(clean, tunnelCap(), "homelab", reg); verr != nil {
		t.Errorf("a non-endpoint set: key beside the ssh target was refused: %s", verr.Message)
	}
}

// And the `secrets:` twin — the rule whose kube original only exists because
// its absence was a bug once.
func TestASecretMappedOntoAnSSHForwardedInputIsRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+sshTarget+`
        secrets:
          host: kv:prod-db-host
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a secret mapped onto the input the ssh forward fills resolved — " +
			"fetched and discarded on every call")
	}
	if verr.Code != "core.profile.secrets" {
		t.Errorf("code = %s, want core.profile.secrets", verr.Code)
	}
	if !strings.Contains(verr.Message, "overridden by the forward `ssh:` opens") {
		t.Errorf("message = %q, want the ssh forward named as what shadows it", verr.Message)
	}
}

// A `kube:` secret reference needs a `kube:` coordinate specifically — an ssh
// tunnel reaches a TCP port, not an apiserver — and the hint must not steer
// the operator into the both-tunnels refusal.
func TestAClusterSecretBesideAnSSHTunnelNamesTheRealChoice(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+sshTarget+`
        secrets:
          password: kube:postgres-creds/password
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a kube: secret resolved with no coordinate to read it through")
	}
	if !strings.Contains(verr.Hint, "cannot read a Kubernetes Secret") {
		t.Errorf("hint = %q, want the ssh-aware choice, not 'add kube:' beside an ssh tunnel", verr.Hint)
	}
	// The run path says the same thing: kubeSecrets is where the read would
	// have happened, and its refusal must carry the same choice.
	conn := config.Connection{SSH: sshTarget,
		Secrets: map[string]string{"password": "kube:postgres-creds/password"}}
	if _, kerr := kubeSecrets(context.Background(), "homelab", conn); kerr == nil ||
		!strings.Contains(kerr.Hint, "cannot read a Kubernetes Secret") {
		t.Errorf("kubeSecrets hint = %v, want the same ssh-aware choice", kerr)
	}
}

// An `ssh:` target against a plugin with no endpoint role would be opened and
// ignored — the untunnellable rule holds for the second scheme, and its hint
// names the line to remove.
func TestAnSSHTargetThePluginCannotBePointedAtIsRefused(t *testing.T) {
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        ssh: `+sshTarget+`
`)
	_, verr := Lookup(cfg, pgCap(), "homelab", pgRegistry(t))
	if verr == nil {
		t.Fatal("a profile whose plugin declares no endpoint role resolved under ssh:, so " +
			"the call would run against the default host while the tunnel is ignored")
	}
	if verr.Code != "core.profile.untunnellable" {
		t.Errorf("code = %s, want core.profile.untunnellable", verr.Code)
	}
	if !strings.Contains(verr.Hint, "`ssh:`") {
		t.Errorf("hint = %q, want it to name the ssh: line", verr.Hint)
	}
}
