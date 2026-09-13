package kv

import (
	"context"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A store session is the passphrase the TUI's masked form unlocked with,
// held in this process for a while so the next action in the same TUI does
// not ask for it again. These tests pin the properties that make that safe:
// only the TUI starts one, it expires when idle, a wrong passphrase or a
// rekey ends it, and the environment still wins over it.

// unlockedFromTheTUI creates a store and then opens it from the TUI surface.
// Two steps on purpose: a session starts on a successful *open*, and the
// write that creates a store has nothing to open yet — see session.go.
func unlockedFromTheTUI(t *testing.T, passphrase string) {
	t.Helper()
	text(t, runSet, map[string]any{"key": "k", "value": "v", "passphrase": passphrase}, false)
	openedFrom(t, plugin.SurfaceTUI, passphrase)
}

func openedFrom(t *testing.T, surface plugin.Surface, passphrase string) {
	t.Helper()
	r := plugin.NewRequest(map[string]any{"key": "k", "passphrase": passphrase}, false, false).
		WithSurface(surface)
	if _, err := runShow(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

// The write that creates a store proves nothing by decrypting, so it starts
// no session even from the TUI: the first *open* afterwards does. One extra
// ask, once, on a store born in a TUI form — the price of "remembered only
// after it opened the store" being the single rule.
func TestCreatingAStoreFromTheTUIDoesNotStartASession(t *testing.T) {
	setup(t)
	freezeClock(t)
	r := plugin.NewRequest(map[string]any{"key": "k", "value": "v", "passphrase": "correct horse battery staple"}, false, false).
		WithSurface(plugin.SurfaceTUI)
	if _, err := runSet(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionPassphrase(); ok {
		t.Error("creating the store started a session before anything opened it")
	}
	openedFrom(t, plugin.SurfaceTUI, "correct horse battery staple")
	if _, ok := SessionPassphrase(); !ok {
		t.Error("the first open after creation did not start the session")
	}
}

func freezeClock(t *testing.T) *time.Time {
	t.Helper()
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	orig := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = orig; forgetSession() })
	forgetSession()
	return &at
}

func TestRevealUsesTheStoreSessionAfterATUIUnlock(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	got, verr := Reveal("k")
	if verr != nil {
		t.Fatalf("Reveal after a TUI unlock: %+v", verr)
	}
	if got != "v" {
		t.Errorf("Reveal = %q, want v", got)
	}
	if names := Names(); len(names) != 1 || names[0] != "k" {
		t.Errorf("Names = %v, want [k]", names)
	}
}

// A CLI command is one process per command and an MCP server has no person
// at the other end: neither is a session, and a passphrase that arrived
// through them must not outlive the call it arrived for.
func TestOnlyTheTUIStartsASession(t *testing.T) {
	setup(t)
	freezeClock(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	for _, surface := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP, plugin.SurfaceUnknown} {
		forgetSession()
		openedFrom(t, surface, "correct horse battery staple")
		if _, ok := SessionPassphrase(); ok {
			t.Errorf("%s: an unlock started a session", surface)
		}
		if _, verr := Reveal("k"); verr == nil {
			t.Errorf("%s: Reveal opened the store after an unlock that is not the TUI's", surface)
		}
	}
}

func TestTheSessionExpiresWhenIdle(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	*at = at.Add(sessionIdle + time.Second)
	if _, ok := SessionPassphrase(); ok {
		t.Error("the session survived past its idle limit")
	}
	if _, verr := Reveal("k"); verr == nil {
		t.Error("Reveal opened the store on an expired session")
	}
}

// Idle, not absolute: every use pushes the deadline out, so a TUI in steady
// use never asks again, and one left open on a desk does.
func TestUsingTheSessionKeepsItAlive(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	*at = at.Add(10 * time.Minute)
	if _, verr := Reveal("k"); verr != nil {
		t.Fatalf("Reveal at +10m: %+v", verr)
	}
	*at = at.Add(10 * time.Minute) // +20m: past the original deadline, inside the slid one
	if _, ok := SessionPassphrase(); !ok {
		t.Fatal("a session used at +10m was gone at +20m")
	}
	*at = at.Add(sessionIdle + time.Second)
	if _, ok := SessionPassphrase(); ok {
		t.Error("a session nobody used past its idle limit was still warm")
	}
}

// A wrong passphrase means whatever is remembered is not the store's any
// more, whichever request carried it. Re-asking beats retrying a stale
// value silently until the deadline.
func TestAWrongPassphraseEndsTheSession(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	wrong := plugin.NewRequest(map[string]any{"key": "k", "passphrase": "not it"}, false, false).
		WithSurface(plugin.SurfaceTUI)
	_, err := runShow(context.Background(), wrong)
	if ve := view.AsError(err, "z"); ve == nil || ve.Code != "kv.wrongpass" {
		t.Fatalf("want kv.wrongpass, got %v", err)
	}
	if _, ok := SessionPassphrase(); ok {
		t.Error("the session outlived a wrong passphrase")
	}
}

func TestRekeyEndsTheSession(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	r := plugin.NewRequest(map[string]any{"generate": true, "passphrase": "correct horse battery staple"}, false, false).
		WithSurface(plugin.SurfaceTUI)
	if _, err := runRekey(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionPassphrase(); ok {
		t.Error("the session outlived a rekey that changed what opens the store")
	}
}

// The environment is a channel the operator set on purpose; a session is a
// convenience. When both are present the deliberate one answers, even when
// it is wrong — a session must never paper over a misconfigured environment.
func TestTheEnvironmentBeatsTheSession(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	t.Setenv(passphraseEnv, "the operator's stale export")
	if _, verr := Reveal("k"); verr == nil {
		t.Error("Reveal used the session over an environment that names a different passphrase")
	}
}

// The badge reads the deadline without touching it: looking at the header
// every render must not be what keeps a session alive.
func TestPeekingAtTheDeadlineDoesNotExtendIt(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	until, ok := SessionUntil()
	if !ok || !until.Equal(at.Add(sessionIdle)) {
		t.Fatalf("SessionUntil = %v, %v; want %v", until, ok, at.Add(sessionIdle))
	}
	*at = at.Add(10 * time.Minute)
	if again, _ := SessionUntil(); !again.Equal(until) {
		t.Errorf("peeking moved the deadline from %v to %v", until, again)
	}
}
