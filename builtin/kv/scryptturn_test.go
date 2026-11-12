package kv

import (
	"context"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// holdTurn takes scryptTurn as another derivation would, and gives it back at
// the latest when the test ends, so a failure cannot leave the package's later
// tests waiting on it.
func holdTurn(t *testing.T) (release func()) {
	t.Helper()
	scryptTurn <- struct{}{}
	held := true
	release = func() {
		if held {
			held = false
			<-scryptTurn
		}
	}
	t.Cleanup(release)
	return release
}

// opening reads the store in the background, for a test to see whether it waits.
func opening(values map[string]any) <-chan *view.Error {
	done := make(chan *view.Error, 1)
	go func() {
		_, verr := load(context.Background(), req(values, false))
		done <- verr
	}()
	return done
}

// Passphrase derivations take turns: a read of a passphrase store waits while
// another derivation holds the turn, so calls side by side never hold more than
// one derivation's memory at once, and it reads once the turn comes free.
func TestAPassphraseDerivationWaitsItsTurn(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "x"}, false)

	release := holdTurn(t)
	done := opening(nil)
	select {
	case verr := <-done:
		t.Fatalf("the store was opened while another derivation held the turn (%v)", verr)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case verr := <-done:
		if verr != nil {
			t.Fatal(verr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the store was never opened once the turn came free")
	}
}

// **A call that ends while it waits gives its turn up.** The wait knew nothing
// of the call: an MCP client that sent a burst of kv.list and cancelled them, or
// closed its session, left every one queued, each to run its derivation in turn
// for nobody while the calls behind it waited. Cancelled, the call returns at
// once with kv.cancelled, and the turn, once free, is not spent on it.
func TestACallThatEndsWhileItWaitsGivesItsTurnUp(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "x"}, false)

	release := holdTurn(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runList(ctx, req(nil, false))
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if verr := view.AsError(err, "x"); verr.Code != "kv.cancelled" {
			t.Errorf("a cancelled kv.list answered %v, want kv.cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kv.list was cancelled and went on waiting for the turn")
	}
	release()
	// The turn it gave up is free: nothing of the cancelled call is queued on it.
	select {
	case scryptTurn <- struct{}{}:
		<-scryptTurn
	case <-time.After(5 * time.Second):
		t.Fatal("the turn was taken after the call that waited for it had ended")
	}
}

// A store locked to a key derives nothing, so it never waits for a passphrase's
// turn.
func TestAKeyStoreDoesNotWaitForADerivation(t *testing.T) {
	setup(t)
	private, public := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "first", "value": "x"}, false)
	text(t, runRekey, map[string]any{"only": true, "recipient": []string{public}, "identity": private}, false)

	holdTurn(t)
	select {
	case verr := <-opening(map[string]any{"identity": private}):
		if verr != nil {
			t.Fatal(verr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a store locked to a key waited for a passphrase derivation")
	}
}
