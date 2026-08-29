package tui

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// `v` on the secret list reveals the entry under the cursor.
//
// It was deliberately absent, on the reasoning that "a secret shown because a
// key was pressed on a list is a secret shown by accident" — right about the
// risk and wrong about where the friction lives. The unlock is what makes a
// reveal deliberate, not the typing: every kv action has the passphrase and
// identity still to ask for, so what an accidental keypress produces is a form
// naming the entry, and the value arrives afterwards on its own page.
//
// The properties below are what make that true, and every one of them is an
// agreement between this table and a capability declared somewhere else — the
// kind that rots silently when one side is edited.

func TestTheSecretListCanRevealTheEntryUnderTheCursor(t *testing.T) {
	reg := realRegistry(t)
	byKey := map[string]capAction{}
	for _, a := range capActions(reg, "kv.list") {
		byKey[a.key] = a
	}
	reveal, ok := byKey["v"]
	if !ok {
		t.Fatal("kv.list offers no reveal action")
	}
	if reveal.cap.ID != "kv.get" {
		t.Fatalf("v runs %s, want kv.get", reveal.cap.ID)
	}
	if reveal.src != srcRow {
		t.Error("the reveal does not take its subject from the row under the cursor")
	}
	// And from the entry's own page, so reading about a secret and reading it
	// are not on different screens.
	for _, a := range capActions(reg, "kv.show") {
		if a.key == "v" {
			if a.cap.ID != "kv.get" || a.src != srcSelf {
				t.Errorf("kv.show's v = %+v, want kv.get about this page's own entry", a)
			}
			return
		}
	}
	t.Error("kv.show offers no reveal action")
}

// The keystroke opens the unlock form; it never produces a value.
//
// This is the whole safety argument, so it is asserted rather than described:
// `fieldsAfter` still has the passphrase and identity to ask for, so runAction
// takes the form branch. If kv.get ever loses those inputs — or the TUI starts
// pre-filling them — this stops being a deliberate act and the test says so.
func TestRevealingOpensTheUnlockFormRatherThanTheValue(t *testing.T) {
	reg := realRegistry(t)
	list, ok := reg.Capability("kv.list")
	if !ok {
		t.Fatal("kv.list is not registered")
	}
	get, ok := reg.Capability("kv.get")
	if !ok {
		t.Fatal("kv.get is not registered")
	}
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.current = list
	m.lastValues = map[string]any{}
	m.row = 1

	tbl := view.Table{
		Columns: []view.Column{{Name: "Key"}, {Name: "Kind"}},
		Rows:    [][]string{{"api-token", "token"}, {"db-password", "password"}},
	}
	model, _ := m.runAction(capAction{key: "v", label: "reveal", cap: get, src: srcRow}, tbl)
	next := model.(Model)
	if next.form == nil {
		t.Fatal("revealing ran straight to the value: one keystroke on a list put a secret on screen")
	}
	// The entry it is about is the row under the cursor, and is not asked
	// about again — the form is the unlock, not a second chance to mistype a
	// name.
	if got := next.form.values()["key"]; got != "db-password" {
		t.Errorf("key = %v, want the row under the cursor", got)
	}
	if _, asked := next.form.bindings["key"]; asked {
		t.Error("the form asks for the key again, which is not what it is there for")
	}
}
