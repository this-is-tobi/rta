package kv

import (
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
		_, verr := load(req(values, false))
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
