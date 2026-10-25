package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A wrong passphrase opened an error page whose first key, r, ran the same wrong
// passphrase again, and the way to type another was e. It is answered on the form
// instead, as a login answers one: the passphrase box empty, the length kept, and
// the reason on the bar.
func TestAWrongPassphraseIsAnsweredOnTheUnlockForm(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 30
	m.origin = modeDashboard
	unlock := unlockCapability("the store's passphrase")
	m.current = unlock
	m.lastValues = map[string]any{"passphrase": "wrong-one", "for": "2h"}

	out, _ := m.Update(resultMsg{cap: unlock,
		err: view.Errorf("kv.wrongpass", "could not decrypt the store").WithHint("wrong passphrase, or the store file is corrupt")})
	got := out.(Model)
	if got.mode != modeForm || got.form == nil || got.form.cap.ID != unlockID {
		t.Fatalf("a wrong passphrase left mode %v (form %v), want the unlock form again", got.mode, got.form != nil)
	}
	if box := boxText(got, "passphrase"); box != "" {
		t.Errorf("the passphrase box holds %q, want it empty for the next try", box)
	}
	if box := boxText(got, "for"); box != "2h" {
		t.Errorf("the length box holds %q, want the 2h it was given", box)
	}
	if !strings.Contains(got.flashBad, "could not decrypt the store") {
		t.Errorf("the bar says %q, want the reason", got.flashBad)
	}
	if bar := plain(got.footerFor(modeForm)); !strings.Contains(bar, "could not decrypt the store") {
		t.Errorf("the form's bar does not show the reason:\n%s", bar)
	}

	// Anything else that goes wrong unlocking is still a page to read.
	other, _ := m.Update(resultMsg{cap: unlock, err: view.Errorf("kv.read", "the store file cannot be read")})
	if other.(Model).mode == modeForm {
		t.Error("a store that cannot be read sent the person back to the passphrase box")
	}
}
