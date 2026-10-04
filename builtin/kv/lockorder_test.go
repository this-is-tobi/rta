package kv

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// terminal makes a person reachable at a terminal on the CLI and records, in
// order, every question put to them: the passphrase of a store that exists, the
// one chosen for a store about to be made, and the value. The order is the thing
// under test.
type terminal struct {
	events   []string
	existing string
	first    string
	again    string
	value    string
}

func newTerminal(t *testing.T) *terminal {
	t.Helper()
	tm := &terminal{existing: "correct horse battery staple", first: "correct horse battery staple",
		again: "correct horse battery staple", value: "s3cret"}
	origPrompt, origNew, origValue, origCan := promptPassphrase, promptNewPassphrase, promptValue, canPrompt
	promptPassphrase = func() (string, error) {
		tm.events = append(tm.events, "passphrase")
		return tm.existing, nil
	}
	promptNewPassphrase = func() (string, string, error) {
		tm.events = append(tm.events, "new passphrase")
		return tm.first, tm.again, nil
	}
	promptValue = func(string) ([]byte, bool, error) {
		tm.events = append(tm.events, "value")
		return []byte(tm.value), false, nil
	}
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	prompted = ""
	t.Cleanup(func() {
		promptPassphrase, promptNewPassphrase, promptValue, canPrompt, prompted = origPrompt, origNew, origValue, origCan, ""
	})
	return tm
}

func verrOf(t *testing.T, err error) *view.Error {
	t.Helper()
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("want a *view.Error, got %v", err)
	}
	return ve
}

// The first store is locked with a passphrase somebody chose, so it is asked for
// before the secret it will hold — and typed twice, because a typo in the only
// prompt there was is a store nobody can open with its first secret inside.
func TestFirstSetChoosesThePassphraseBeforeTheValue(t *testing.T) {
	setup(t)
	tm := newTerminal(t)
	tm.first, tm.again = "chosen passphrase", "chosen passphrase"

	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"new passphrase", "value"}; !slices.Equal(tm.events, want) {
		t.Fatalf("asked %q, want %q", tm.events, want)
	}
	got, err := runGet(context.Background(), req(map[string]any{"key": "db", "passphrase": "chosen passphrase"}, false))
	if err != nil {
		t.Fatalf("the store does not open with the passphrase that was chosen: %v", err)
	}
	if got.(view.Text).Body != "s3cret" {
		t.Errorf("stored %q", got.(view.Text).Body)
	}
}

// Two answers that differ choose nothing: no store is made, and the value is
// never asked for, so nothing typed is lost to a passphrase that was a typo.
func TestFirstSetRefusesPassphrasesThatDiffer(t *testing.T) {
	dir := setup(t)
	tm := newTerminal(t)
	tm.first, tm.again = "chosen passphrase", "chosen passphrasf"

	_, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""}))
	if ve := verrOf(t, err); ve.Code != "kv.passphrase.mismatch" || ve.Hint == "" {
		t.Fatalf("want kv.passphrase.mismatch with a hint, got %+v", ve)
	}
	if want := []string{"new passphrase"}; !slices.Equal(tm.events, want) {
		t.Errorf("asked %q, want %q: the value must not be asked for after a typo", tm.events, want)
	}
	if _, statErr := os.Stat(dir + "/" + storeFile); !os.IsNotExist(statErr) {
		t.Errorf("a store was made from two answers that differ: %v", statErr)
	}
}

// The same choice when the value is given rather than typed: a first store made
// by `kv set key value` is locked by the same twice-typed passphrase.
func TestFirstSetWithAValueStillChoosesThePassphraseTwice(t *testing.T) {
	setup(t)
	tm := newTerminal(t)
	tm.first, tm.again = "one", "two"

	_, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "value": "given", "passphrase": ""}))
	if ve := verrOf(t, err); ve.Code != "kv.passphrase.mismatch" {
		t.Fatalf("want kv.passphrase.mismatch, got %+v", ve)
	}
	if !slices.Equal(tm.events, []string{"new passphrase"}) {
		t.Errorf("asked %q", tm.events)
	}
}

// An existing store is opened before the secret is asked for: a wrong passphrase
// used to be found out after the value was typed, and the value went with it.
func TestSetChecksThePassphraseBeforeAskingForTheValue(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "seed", "value": "x"}, false)
	tm := newTerminal(t)
	tm.existing = "not the passphrase"

	_, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""}))
	if ve := verrOf(t, err); ve.Code != "kv.wrongpass" {
		t.Fatalf("want kv.wrongpass, got %+v", ve)
	}
	if want := []string{"passphrase"}; !slices.Equal(tm.events, want) {
		t.Errorf("asked %q, want %q: a secret was typed for a store that would not open", tm.events, want)
	}

	tm.events, tm.existing, prompted = nil, "correct horse battery staple", ""
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db", "passphrase": ""})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"passphrase", "value"}; !slices.Equal(tm.events, want) {
		t.Errorf("asked %q, want %q: one passphrase prompt, then the value", tm.events, want)
	}
}

// A label for a key the store does not hold makes the entry, and its value is
// asked for under the lock: the first store's passphrase still comes before it.
func TestFirstSetOfALabelledKeyChoosesThePassphraseBeforeTheValue(t *testing.T) {
	setup(t)
	tm := newTerminal(t)

	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "api-token", "description": "prod API", "passphrase": "",
	})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"new passphrase", "value"}; !slices.Equal(tm.events, want) {
		t.Errorf("asked %q, want %q", tm.events, want)
	}
}

// Nothing is asked for a store that a dry run would not touch: the value is
// typed so the answer can name it, and no passphrase is chosen for a store that
// will not be made.
func TestDryRunSetChoosesNoPassphrase(t *testing.T) {
	dir := setup(t)
	tm := newTerminal(t)

	dry := plugin.NewRequest(map[string]any{"key": "db"}, true, false).WithSurface(plugin.SurfaceCLI)
	if _, err := runSet(context.Background(), dry); err != nil {
		t.Fatal(err)
	}
	if want := []string{"value"}; !slices.Equal(tm.events, want) {
		t.Errorf("asked %q, want %q", tm.events, want)
	}
	if _, statErr := os.Stat(dir + "/" + storeFile); !os.IsNotExist(statErr) {
		t.Errorf("a dry run made a store: %v", statErr)
	}
}

// A passphrase already in the environment is nobody's to type, first store or
// not, and a store locked to a key asks for none.
func TestNoPassphraseIsAskedWhereNoneIsNeeded(t *testing.T) {
	setup(t)
	tm := newTerminal(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db"})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"value"}; !slices.Equal(tm.events, want) {
		t.Errorf("with the passphrase in the environment, asked %q, want %q", tm.events, want)
	}

	setupWithConfig(t)
	tm = newTerminal(t)
	if _, err := runInit(context.Background(), cliReq(map[string]any{"generate": true})); err != nil {
		t.Fatal(err)
	}
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "db"})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"value"}; !slices.Equal(tm.events, want) {
		t.Errorf("with the store locked to a key, asked %q, want %q", tm.events, want)
	}
}

// What a person is told before choosing: the three facts that cannot be taken
// back, in the words the docs use.
func TestNewStoreNoteSaysWhatTheChoiceCommitsTo(t *testing.T) {
	note := strings.Join(strings.Fields(newStoreNote()), " ")
	for _, want := range []string{
		"cannot be recovered",
		"`rta kv init --generate`",
		"key file",
		"anything running as you can read",
		"Agents cannot use a passphrase store unless " + passphraseEnv + " is set in their server's environment",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
}
