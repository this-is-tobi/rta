package kv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A store about to be created is locked with a passphrase somebody chooses, and
// a short one is refused whichever way it arrives: the flag, the environment an
// agent's server inherits, a form's box, or the prompt. Nothing is written, so
// the refusal costs the person nothing but a longer answer.
func TestANewStoreIsNotLockedWithAShortPassphrase(t *testing.T) {
	for name, set := range map[string]func(t *testing.T) plugin.Request{
		"flag": func(t *testing.T) plugin.Request {
			return plugin.NewRequest(map[string]any{"key": "k", "value": "v", "passphrase": "short"}, false, false)
		},
		"environment": func(t *testing.T) plugin.Request {
			t.Setenv(passphraseEnv, "short")
			return plugin.NewRequest(map[string]any{"key": "k", "value": "v"}, false, false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			_, err := runSet(context.Background(), set(t))
			var ve *view.Error
			if !errors.As(err, &ve) || ve.Code != "kv.passphrase.short" {
				t.Fatalf("err = %#v, want kv.passphrase.short", err)
			}
			if !strings.Contains(ve.Message, "5 characters") || !strings.Contains(ve.Message, "at least 8") {
				t.Errorf("message = %q, want the length and the floor", ve.Message)
			}
			if fileExists(storePath()) {
				t.Error("a store was written under a passphrase that was refused")
			}
		})
	}
}

// Over MCP the passphrase is the environment's, which is the operator's, so the
// refusal says whose it is to change.
func TestAShortPassphraseInAnAgentsEnvironmentIsTheOperatorsToChange(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "short")
	_, err := runSet(context.Background(), plugin.NewRequest(map[string]any{"key": "k", "value": "v"}, false, false).
		WithSurface(plugin.SurfaceMCP))
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "kv.passphrase.short" {
		t.Fatalf("err = %#v, want kv.passphrase.short", err)
	}
	if !strings.Contains(ve.Hint, "operator") || !strings.Contains(ve.Hint, passphraseEnv) {
		t.Errorf("hint = %q, want the operator and the variable to change", ve.Hint)
	}
}

// The prompt is the one place a passphrase is chosen by a person, and the floor
// holds there too: what is typed twice and short is refused before the value
// is asked for, so the secret is not typed into a store that will not be made.
func TestAShortPassphraseTypedAtThePromptIsRefusedBeforeTheValue(t *testing.T) {
	setup(t)
	asked := stubValuePrompt(t, "never reached", nil)
	origNew, origCan := promptNewPassphrase, canPrompt
	promptNewPassphrase = func() (string, string, error) { return "short", "short", nil }
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() { promptNewPassphrase, canPrompt = origNew, origCan })
	prompted = ""
	t.Cleanup(func() { prompted = "" })

	_, err := runSet(context.Background(), cliReq(map[string]any{"key": "k"}))
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "kv.passphrase.short" {
		t.Fatalf("err = %#v, want kv.passphrase.short", err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked for the value of %q after refusing the passphrase", *asked)
	}
	if fileExists(storePath()) {
		t.Error("a store was written under a passphrase that was refused")
	}
}

// The floor is on setting and never on using. A store made before it existed,
// under a passphrase of three characters, opens, reads and takes new keys under
// that same passphrase, since a write to an existing store re-encrypts it under
// the one it was opened with and chooses nothing.
func TestAStoreLockedUnderAShortPassphraseKeepsOpeningAndTakingWrites(t *testing.T) {
	setup(t)
	r, err := age.NewScryptRecipient("abc")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	if verr := saveTo(plugin.SurfaceCLI, store{Entries: map[string]entry{"old": {Value: []byte("from before")}}},
		[]age.Recipient{r}, nil); verr != nil {
		t.Fatal(verr)
	}

	ctx := context.Background()
	got, err := runGet(ctx, plugin.NewRequest(map[string]any{"key": "old", "passphrase": "abc"}, false, false))
	if err != nil {
		t.Fatalf("a store under a short passphrase did not open: %v", err)
	}
	if body := got.(view.Text).Body; body != "from before" {
		t.Errorf("read %q", body)
	}
	if _, err := runSet(ctx, plugin.NewRequest(map[string]any{"key": "new", "value": "v", "passphrase": "abc"}, false, false)); err != nil {
		t.Fatalf("a write to a store under a short passphrase was refused: %v", err)
	}
}

// A passphrase of eight characters is long enough: the floor is a floor.
func TestAnEightCharacterPassphraseMakesAStore(t *testing.T) {
	setup(t)
	if _, err := runSet(context.Background(), plugin.NewRequest(
		map[string]any{"key": "k", "value": "v", "passphrase": "12345678"}, false, false)); err != nil {
		t.Fatal(err)
	}
}

// What a person is told before choosing says the floor, so the refusal is not
// the first they hear of it.
func TestTheNewStoreNoteSaysTheFloor(t *testing.T) {
	if note := newStoreNote(); !strings.Contains(note, "at least 8 characters") {
		t.Errorf("note = %q, want the floor", note)
	}
}
