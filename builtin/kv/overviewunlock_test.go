package kv

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A page is one story: the line that says what happens next agrees with the
// line above it, and a passphrase is said to come from where it came from.

// atATerminal makes a CLI call one a person could be asked at, and fails the
// test if anything is.
func atATerminal(t *testing.T) {
	t.Helper()
	origPrompt, origCan := promptPassphrase, canPrompt
	promptPassphrase = func() (string, error) {
		t.Error("the overview asked for the store's passphrase")
		return "", nil
	}
	canPrompt = func(r plugin.Request) bool { return r.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() { promptPassphrase, canPrompt, prompted = origPrompt, origCan, "" })
}

func everyLine(kv view.KeyValue) string {
	lines := make([]string, len(kv.Pairs))
	for i, p := range kv.Pairs {
		lines[i] = p.Key + " " + p.Value
	}
	return strings.Join(lines, "\n")
}

// With nothing at hand, at a terminal, the inventory says what it needs and the
// line beside it says who asks. It used to say "you will be asked" below a line
// saying this page never asks, which was two answers to one question.
func TestTheOverviewAtATerminalSaysOnceThatKvListAsks(t *testing.T) {
	seeded(t)
	atATerminal(t)

	kv := overviewPairs(t, cliReq(nil))
	if got, want := pairValue(kv, "inventory"), "needs a key at hand — set RTA_KV_PASSPHRASE or give --passphrase"; got != want {
		t.Errorf("inventory = %q, want %q", got, want)
	}
	if got, want := pairValue(kv, "unlocks here"), "on request — `rta kv list` asks, this page does not"; got != want {
		t.Errorf("unlocks here = %q, want %q", got, want)
	}
	if page := everyLine(kv); strings.Contains(page, "you will be asked") || strings.Count(page, "this page") != 1 {
		t.Errorf("the page tells two stories about who asks:\n%s", page)
	}
}

// A passphrase that was at hand and did not open the store is said to be at
// hand and not accepted, whether it came from the environment or was given: the
// line above it says the store could not be decrypted, and "yes" and "available"
// answered a different question.
func TestTheOverviewSaysAPassphraseWasNotAccepted(t *testing.T) {
	seeded(t)

	t.Run("from the environment", func(t *testing.T) {
		t.Setenv(passphraseEnv, "not the passphrase")
		kv := overviewPairs(t, plugin.NewRequest(nil, false, false))
		if got := pairValue(kv, "inventory"); !strings.HasPrefix(got, "not opened — could not decrypt the store") {
			t.Errorf("inventory = %q", got)
		}
		if got, want := pairValue(kv, "unlocks here"), "no — the passphrase from the environment was not accepted"; got != want {
			t.Errorf("unlocks here = %q, want %q", got, want)
		}
	})
	t.Run("given", func(t *testing.T) {
		kv := overviewPairs(t, plugin.NewRequest(map[string]any{"passphrase": "not the passphrase"}, false, false))
		if got, want := pairValue(kv, "unlocks here"), "no — the passphrase given was not accepted"; got != want {
			t.Errorf("unlocks here = %q, want %q", got, want)
		}
	})
}

// kv.status never opens the store, so what it says of a passphrase is that one
// is at hand, and names where it came from: a flag, or a form's box, is not the
// environment. A store that was opened says the same, and one that was not says
// that what was at hand was not accepted.
func TestTheUnlockLineNamesWhereThePassphraseCameFrom(t *testing.T) {
	seeded(t)
	const right, wrong = "correct horse battery staple", "not the passphrase"

	for name, tc := range map[string]struct {
		env              string
		values           map[string]any
		status, overview string
	}{
		"given": {
			values: map[string]any{"passphrase": right},
			status: "yes — passphrase given", overview: "yes — passphrase given",
		},
		"given and wrong": {
			values: map[string]any{"passphrase": wrong},
			status: "yes — passphrase given", overview: "no — the passphrase given was not accepted",
		},
		"from the environment": {
			env:    right,
			status: "yes — passphrase from the environment", overview: "yes — passphrase from the environment",
		},
		// A form's box is filled from the environment when it holds one, so the
		// same string in both is the environment's.
		"the environment's, as a form fills it": {
			env: right, values: map[string]any{"passphrase": right},
			status: "yes — passphrase from the environment", overview: "yes — passphrase from the environment",
		},
	} {
		t.Setenv(passphraseEnv, tc.env)
		r := plugin.NewRequest(tc.values, false, false)
		if got := unlockAvailability(r, modePassphrase); got != tc.status {
			t.Errorf("%s: kv.status says %q, want %q", name, got, tc.status)
		}
		if got := pairValue(overviewPairs(t, r), "unlocks here"); got != tc.overview {
			t.Errorf("%s: kv.overview says %q, want %q", name, got, tc.overview)
		}
	}
}

// A key that has a passphrase of its own is named once on the page: the
// inventory used to name it, explain that an agent signs and does not decrypt,
// and send the reader to a key with none, and the line beside it named the same
// file again.
func TestTheOverviewNamesALockedKeyOnce(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "hunter2")
	stubKeyPrompt(t, "hunter2")
	if _, err := runSet(t.Context(), cliReq(map[string]any{"key": "token", "value": "v", "identity": path})); err != nil {
		t.Fatal(err)
	}

	t.Run("at a terminal", func(t *testing.T) {
		keyPassphrases = map[string]string{}
		kv := overviewPairs(t, cliReq(map[string]any{"identity": path}))
		inventory := pairValue(kv, "inventory")
		if want := "needs a key at hand — the key's own passphrase: set RTA_KV_PASSPHRASE or give --passphrase"; inventory != want {
			t.Errorf("inventory = %q, want %q", inventory, want)
		}
		if got, want := pairValue(kv, "unlocks here"), "on request — identity "+path+" is passphrase-protected, `rta kv list` asks, this page does not"; got != want {
			t.Errorf("unlocks here = %q, want %q", got, want)
		}
		if page := everyLine(kv); strings.Count(page, path) != 1 {
			t.Errorf("the key is named %d times:\n%s", strings.Count(page, path), page)
		}
	})
	t.Run("where nobody is asked", func(t *testing.T) {
		keyPassphrases = map[string]string{}
		kv := overviewPairs(t, plugin.NewRequest(map[string]any{"identity": path}, false, false))
		if page := everyLine(kv); strings.Count(page, path) != 1 || strings.Contains(page, "ssh-agent") {
			t.Errorf("the key is named %d times, or the page explains ssh-agent:\n%s", strings.Count(page, path), page)
		}
		if got := pairValue(kv, "inventory"); !strings.HasPrefix(got, "needs a key at hand — the key's own passphrase: ") {
			t.Errorf("inventory = %q", got)
		}
	})
	t.Run("a passphrase given for it that is wrong", func(t *testing.T) {
		keyPassphrases = map[string]string{}
		kv := overviewPairs(t, plugin.NewRequest(map[string]any{"identity": path, "passphrase": "nope"}, false, false))
		if got := pairValue(kv, "inventory"); !strings.HasPrefix(got, "not opened — wrong passphrase for "+path) {
			t.Errorf("a key was at hand and refused: inventory = %q", got)
		}
		if got, want := pairValue(kv, "unlocks here"), "no — the passphrase given was not accepted by the key"; got != want {
			t.Errorf("unlocks here = %q, want %q", got, want)
		}
		if page := everyLine(kv); strings.Count(page, path) != 1 {
			t.Errorf("the key is named %d times:\n%s", strings.Count(page, path), page)
		}
	})
}

// A key that is at hand and is not one of the store's is at hand and refused,
// not "yes".
func TestTheOverviewSaysAKeyTheStoreRefusedWasNotAccepted(t *testing.T) {
	setupWithConfig(t)
	dir := t.TempDir()
	right, _ := writeSSHKeypair(t, dir, "id_right")
	other, _ := writeSSHKeypair(t, dir, "id_other")
	if _, err := runSet(t.Context(), cliReq(map[string]any{"key": "token", "value": "v", "identity": right})); err != nil {
		t.Fatal(err)
	}

	kv := overviewPairs(t, plugin.NewRequest(map[string]any{"identity": other}, false, false))
	if got := pairValue(kv, "inventory"); !strings.HasPrefix(got, "not opened — that key cannot decrypt the store") {
		t.Errorf("inventory = %q", got)
	}
	if got, want := pairValue(kv, "unlocks here"), "no — identity "+other+" was not accepted by the store"; got != want {
		t.Errorf("unlocks here = %q, want %q", got, want)
	}
}
