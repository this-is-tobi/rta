package kv

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A first store made at a terminal asks who will read it before anything is
// chosen. A yes locks it to a key generated for it: no passphrase is asked,
// the receipt names the key and that it is the one copy, and the store opens
// again with nothing typed, as it does for an agent's server.
func TestAYesLocksTheFirstStoreToAKeyMadeForIt(t *testing.T) {
	setup(t)
	tm := newTerminal(t)
	tm.key = true

	out, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"who reads", "value"}; !slices.Equal(tm.events, want) {
		t.Fatalf("asked %q, want %q", tm.events, want)
	}
	if mode, _, _ := currentMode(); mode != modeKeys {
		t.Fatalf("the store is locked with %q, want a key", mode)
	}
	body := out.(view.Text).Body
	if !strings.Contains(body, "locked to a key generated at "+defaultIdentity()) || !strings.Contains(body, "back it up") {
		t.Errorf("the receipt does not name the key it made:\n%s", body)
	}
	got, err := runGet(context.Background(), plugin.NewRequest(map[string]any{"key": "db"}, false, false))
	if err != nil {
		t.Fatalf("the store does not open with the key found by itself: %v", err)
	}
	if got.(view.Text).Body != "s3cret" {
		t.Errorf("stored %q", got.(view.Text).Body)
	}
}

// A no is the passphrase store a first write has always made, chosen twice
// after the question, and the question is put once: the second write opens
// the store it made.
func TestANoIsThePassphraseStoreAsBefore(t *testing.T) {
	setup(t)
	tm := newTerminal(t)

	out, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"who reads", "new passphrase", "value"}; !slices.Equal(tm.events, want) {
		t.Fatalf("asked %q, want %q", tm.events, want)
	}
	if mode, _, _ := currentMode(); mode != modePassphrase {
		t.Fatalf("the store is locked with %q, want a passphrase", mode)
	}
	if body := out.(view.Text).Body; !strings.Contains(body, "locked with the passphrase this call used") {
		t.Errorf("the receipt does not say the store is locked with a passphrase:\n%s", body)
	}
}

// The question is put only when nothing has chosen the lock: a passphrase in
// the environment is one, and so is a key this machine already holds.
func TestTheQuestionIsNotPutWhenTheLockIsChosen(t *testing.T) {
	setup(t)
	tm := newTerminal(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""})); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tm.events, "who reads") {
		t.Errorf("asked who reads a store whose passphrase the environment gives: %q", tm.events)
	}

	setup(t)
	tm = newTerminal(t)
	if _, err := generateIdentity(plugin.SurfaceCLI, defaultIdentity()); err != nil {
		t.Fatal(err)
	}
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""})); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tm.events, "who reads") {
		t.Errorf("asked who reads a store a key on this machine already locks: %q", tm.events)
	}
}

// generate is the same yes with no terminal to ask at, and is refused where it
// does nothing — a store already made — or says the opposite of another lock
// named beside it.
func TestGenerateLocksANewStoreToAKeyAndIsRefusedBesideAnotherLock(t *testing.T) {
	setup(t)
	out, err := runSet(context.Background(), plugin.NewRequest(map[string]any{
		"key": "db", "value": "v", "generate": true,
	}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if mode, _, _ := currentMode(); mode != modeKeys || !strings.Contains(out.(view.Text).Body, defaultIdentity()) {
		t.Fatalf("generate made a store locked with %q:\n%s", mode, out.(view.Text).Body)
	}
	_, err = runSet(context.Background(), plugin.NewRequest(map[string]any{
		"key": "other", "value": "v", "generate": true,
	}, false, false))
	if ve := verrOf(t, err); ve.Code != "kv.set.generate" || !strings.Contains(ve.Hint, "kv rekey") {
		t.Errorf("generate over a store already made = %+v, want kv.set.generate pointing at kv rekey", ve)
	}

	for name, values := range map[string]map[string]any{
		"a passphrase": {"key": "db", "value": "v", "generate": true, "passphrase": "correct horse battery staple"},
		"a key named":  {"key": "db", "value": "v", "generate": true, "identity": "/somewhere/key"},
	} {
		setup(t)
		_, err := runSet(context.Background(), plugin.NewRequest(values, false, false))
		if ve := verrOf(t, err); ve.Code != "kv.set.generate" || ve.Input != "generate" {
			t.Errorf("generate beside %s = %+v, want kv.set.generate on generate", name, ve)
		}
		if fileExists(storePath()) || fileExists(defaultIdentity()) {
			t.Errorf("generate beside %s made a store or a key before refusing", name)
		}
	}
}
