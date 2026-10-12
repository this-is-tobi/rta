package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// The comment on a refused save says the operator stays on the form with the
// boxes in front of them, and the form was dropped before the refusal was made:
// the model came back in form mode with no form, and the next key press sent
// the person to the browse screen with a flash about boxes they could no longer
// see — everything they had typed gone.
func TestARefusedConnectionSaveKeepsTheFormAndWhatWasTyped(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{Set: map[string]any{"schema": "public"}})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	*nm.form.bindings[profileKubeField] = "homelab/databases/svc/postgres:5432"
	*nm.form.bindings[profileSSHField] = sshEditorTarget
	*nm.form.bindings[profileSetPrefix+"schema"] = "analytics"
	model, _ = nm.saveConnForm()
	nm = model.(Model)

	if nm.mode != modeForm || nm.form == nil || !nm.form.connEditing {
		t.Fatalf("a refused save left mode %v and form %v, not the editor", nm.mode, nm.form)
	}
	if got := *nm.form.bindings[profileKubeField]; got != "homelab/databases/svc/postgres:5432" {
		t.Errorf("the coordinate that was typed is %q on the rebuilt form", got)
	}
	if got := *nm.form.bindings[profileSetPrefix+"schema"]; got != "analytics" {
		t.Errorf("a box that was not the problem lost what was typed in it: %q", got)
	}
	if nm.flash == "" || nm.flashBad == "" {
		t.Errorf("the refusal was not said: flash %q", nm.flash)
	}
}
