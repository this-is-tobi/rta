package tunnel

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// **A credential helper that outlives kubectl must not wedge rta.**
//
// A kubeconfig's exec credential plugin is handed kubectl's own stderr, so a
// helper that forks something and exits — kubelogin shelling out to a browser
// for an OIDC device flow — leaves that write end open after kubectl is gone.
// os/exec's Wait, with WaitDelay unset, ends in an unguarded receive on the
// copying goroutine's channel: it blocks until every pipe sees EOF, which
// nothing is going to deliver. Not slow. Forever, and past the caller's
// context, because what is stuck is os/exec's goroutines rather than the
// process — cancelling kills kubectl and does not close the pipes.
//
// Secrets runs on every call whose connection names `secret:`, so this wedged
// the whole rta invocation with no error and no ceiling. Over MCP it is a tool
// call that never answers.
func TestSecretsDoesNotHangBehindAnOrphanedCredentialHelper(t *testing.T) {
	fakeKubectl(t, "(sleep 30) &\necho '{\"data\":{\"password\":\"cHc=\"}}'\nexit 0\n")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Secrets(ctx, "homelab-pg", Target{Kube: homelab,
			Secret: "pg", From: map[string]string{"password": "password"}})
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Secrets never returned: kubectl exited and nothing bounded the wait " +
			"for pipes an orphan is still holding")
	}
}

// The answer survives the bound. ErrWaitDelay here means "the process finished
// and something else was holding the pipes", which is not a reason to throw
// away bytes that parse — so the credential still comes back.
func TestSecretsStillReturnsTheCredentialWhenTheDelayFires(t *testing.T) {
	fakeKubectl(t, "(sleep 30) &\necho '{\"data\":{\"password\":\"aHVudGVyMg==\"}}'\nexit 0\n")

	got, verr := Secrets(context.Background(), "homelab-pg", Target{Kube: homelab,
		Secret: "pg", From: map[string]string{"password": "password"}})
	if verr != nil {
		t.Fatalf("a complete answer was refused because an orphan held the pipes: %v", verr)
	}
	if got["password"] != "hunter2" {
		t.Errorf("password = %q", got["password"])
	}
}

// **kubectl must never block writing to a stdout nobody reads.**
//
// It prints "Handling connection for <port>" for every connection it accepts,
// before it moves a byte. rta stopped reading the moment it had the listener
// line, so the pipe filled at 64 KiB — about 2100 of those lines — and kubectl
// blocked inside its own Fprintf. From the plugin's side that is a forward
// that still accepts connections and carries nothing on any of them.
func TestTheForwardKeepsReadingKubectlsStdout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "drained")
	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:1 -> 5432'\n"+
			"i=0; while [ $i -lt 5000 ]; do echo 'Handling connection for 1'; i=$((i+1)); done\n"+
			"touch %s\nwhile true; do sleep 1; done\n", marker))

	tun, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr != nil {
		t.Fatal(verr)
	}
	defer tun.Close()

	// 5000 lines is ~150 KiB, comfortably past the 64 KiB pipe: without a
	// reader the child stalls around line 2184 and never reaches the marker.
	if !awaitMarker(marker, true) {
		t.Error("kubectl blocked writing to a stdout nobody reads — past this point every " +
			"connection is accepted and carries nothing")
	}
}

// A forward that fails while an orphan holds the pipes used to take out both
// non-context arms of awaitForwarding's select at once: the scanner never sees
// EOF, so `lines` is never closed, and Wait never returns, so `exited` never
// closes. Open then sat for the whole openCeiling and reported "did not come
// up in time" — discarding kubectl's real, already-buffered explanation.
func TestAFailedForwardReportsWhatKubectlSaidRatherThanATimeout(t *testing.T) {
	fakeKubectl(t, "(sleep 30) &\n"+
		"echo 'Error from server (NotFound): services \"postgres\" not found' >&2\nexit 1\n")

	start := time.Now()
	_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr == nil {
		t.Fatal("a failed forward reported success")
	}
	if verr.Code == "tunnel.open.timeout" {
		t.Errorf("waited %s and reported a timeout for a failure kubectl had already explained",
			time.Since(start).Round(time.Millisecond))
	}
	if elapsed := time.Since(start); elapsed > openCeiling/2 {
		t.Errorf("took %s to report a failure that was on stderr immediately", elapsed.Round(time.Millisecond))
	}
}
