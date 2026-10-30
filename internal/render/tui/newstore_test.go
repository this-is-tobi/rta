package tui

import (
	"testing"
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

// A lock already chosen is not asked about: with the passphrase in the
// environment the second stage opens straight after the key.
func TestAChosenLockGoesStraightToTheValue(t *testing.T) {
	m := keyNamed(t, "correct horse battery staple")
	if m.dialog != nil || m.form == nil || !m.form.final {
		t.Errorf("with the passphrase in the environment, naming the key asked %v and opened the second stage %v",
			m.dialog != nil, m.form != nil && m.form.final)
	}
}
