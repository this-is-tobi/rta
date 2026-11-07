package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// keyNamed is kv.set's form with its key named and its first stage sent, on a
// machine with no store, no key for one, and passphrase as the environment
// gives it.
func keyNamed(t *testing.T, passphrase string) Model {
	t.Helper()
	t.Setenv("RTA_KV_PASSPHRASE", passphrase)
	t.Setenv("RTA_KV_IDENTITY", "")
	m, reg := realModel(t, 100, 30)
	c, ok := reg.Capability(kvSet)
	if !ok {
		t.Fatal("no kv.set in the registry")
	}
	next, _ := m.open(c)
	m = next.(Model)
	if m.form == nil || m.form.final {
		t.Fatalf("kv.set opened %v, want its first stage, the key", m.mode)
	}
	m.form.form = typeInto(m.form.form, "db")
	return pump(t, m, keyMsg("ctrl+s"))
}

// The first write makes the store, so once its key is named the form asks
// what the terminal asks before the value: whether a key file locks it, focus
// on the passphrase a first write has always made. Either answer opens the
// second stage with the answer in its generate box, and nothing is made until
// the form is sent.
func TestTheFirstStoreIsAskedAboutOnceItsKeyIsNamed(t *testing.T) {
	for _, c := range []struct {
		key  string
		want bool
	}{{"y", true}, {"n", false}, {"enter", false}} {
		m := keyNamed(t, "")
		if m.dialog == nil || m.dialog.id != dialogNewStore || m.dialog.focus != focusCancel {
			t.Fatalf("naming the key of a first store asked %+v, want the new-store question on its passphrase answer", m.dialog)
		}
		next, _, _ := m.dialogKeys(keyMsg(c.key))
		m = next.(Model)
		if m.dialog != nil || m.form == nil || !m.form.final {
			t.Fatalf("%s left the dialog %v and the form %v, want the second stage", c.key, m.dialog != nil, m.form != nil)
		}
		if got, _ := m.form.values()["generate"].(bool); got != c.want {
			t.Errorf("%s opened the second stage with generate %v, want %v", c.key, got, c.want)
		}
	}
}

// A store locked to a passphrase is asked for it twice, as a terminal asks: the
// passphrase box is required, a second one follows it, the boxes of the other
// lock are gone, and the form is not sent while the two differ or the passphrase
// is short. Sent with the two the same, it makes a store that passphrase opens.
func TestANewStoresPassphraseIsAskedTwice(t *testing.T) {
	m := keyNamed(t, "")
	next, _, _ := m.dialogKeys(keyMsg("n"))
	m = next.(Model)
	for _, gone := range []string{"generate", "identity"} {
		if _, ok := m.form.bindings[gone]; ok {
			t.Errorf("the second stage still asks %s, which the question answered", gone)
		}
	}
	if _, ok := m.form.bindings[againInput]; !ok {
		t.Fatal("the second stage asks for the passphrase once")
	}
	if err := m.form.validator(plugin.Field{Name: "passphrase"})("short"); err == nil {
		t.Error("a new store's passphrase under the floor was accepted")
	}
	m = typeAt(t, m, "value", "s3cret")
	m = typeAt(t, m, "passphrase", "correct horse battery")
	m = typeAt(t, m, againInput, "correct horse batterY")
	m = pump(t, m, keyMsg("ctrl+s"))
	if m.form == nil {
		t.Fatal("the form was sent with two passphrases that differ")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("RTA_DATA_DIR"), "kv.age")); err == nil {
		t.Fatal("a store was made from two passphrases that differ")
	}

	m.form.form = settleForm(m.form.form, keyMsg("backspace"))
	m.form.form = typeInto(m.form.form, "y")
	m = pump(t, m, keyMsg("ctrl+s"))
	if m.form != nil {
		t.Fatalf("the form was not sent with the two passphrases the same: %v", m.form.form.Errors())
	}
	if verr := kv.StartSession("correct horse battery", kv.MinSessionTTL); verr != nil {
		t.Errorf("the store the form made does not open with its passphrase: %v", verr)
	}
	kv.ForgetSession()
}

// typeAt types text into the box name, moving down the form to it with enter as
// a person does.
func typeAt(t *testing.T, m Model, name, text string) Model {
	t.Helper()
	for range 12 {
		if got, ok := m.focusedInput(); ok && got == name {
			m.form.form = typeInto(m.form.form, text)
			return m
		}
		m.form.form = settleForm(m.form.form, keyMsg("enter"))
	}
	t.Fatalf("enter never reached the %s box", name)
	return m
}

// The run refuses two passphrases that differ, for a path that reached it past
// the box's own check, and makes nothing.
func TestTwoPassphrasesThatDifferMakeNoStore(t *testing.T) {
	_, reg := realModel(t, 100, 30)
	c, _ := reg.Capability(kvSet)
	_, err := passphraseTwice(c).Run(context.Background(), plugin.NewRequest(map[string]any{
		"key": "db", "value": "s3cret", "passphrase": "correct horse battery", againInput: "something else",
	}, false, false).WithSurface(plugin.SurfaceTUI))
	if verr := view.AsError(err, "x"); verr.Code != "kv.passphrase.differs" {
		t.Errorf("err = %v, want kv.passphrase.differs", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("RTA_DATA_DIR"), "kv.age")); err == nil {
		t.Error("a store was made from two passphrases that differ")
	}
}

// Both of the question's buttons make a store, so esc is neither: it goes back
// to the key, which is in its box, and the bar and the title say so, where they
// said "cancel" over a key that went on to the passphrase.
func TestEscOnTheNewStoreQuestionGoesBackToTheKey(t *testing.T) {
	m := keyNamed(t, "")
	bar := plain(m.footerFor(m.mode))
	for _, want := range []string{"n use a passphrase", "esc back"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar does not say %q:\n%s", want, bar)
		}
	}
	if strings.Contains(bar, "cancel") {
		t.Errorf("the bar offers a cancel the question does not have:\n%s", bar)
	}
	if view := plain(m.View().Content); !strings.Contains(view, "esc goes back") {
		t.Errorf("the question's title does not say what esc does:\n%s", view)
	}
	next, _, _ := m.dialogKeys(keyMsg("esc"))
	m = next.(Model)
	if m.dialog != nil || m.form == nil || m.form.final {
		t.Fatalf("esc left the dialog %v and the form %v, want the first stage", m.dialog != nil, m.form != nil)
	}
	if got := *m.form.bindings["key"]; got != "db" {
		t.Errorf("the key box holds %q after going back, want the key named", got)
	}
}

// A lock already chosen is not asked about: with the passphrase in the
// environment the second stage opens straight after the key.
func TestAChosenLockGoesStraightToTheValue(t *testing.T) {
	m := keyNamed(t, "correct horse battery staple")
	if m.dialog != nil || m.form == nil || !m.form.final {
		t.Errorf("with the passphrase in the environment, naming the key asked %v and opened the second stage %v",
			m.dialog != nil, m.form != nil && m.form.final)
	}
}
