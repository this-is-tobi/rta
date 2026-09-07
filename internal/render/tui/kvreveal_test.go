package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// The run this test used to stop short of: what happens once the unlock
// form is behind the caller and the value actually comes back. runAction's
// refreshPending decision, made before the form ever opens, is what decides
// it — kv.get is Write, the same class every mutating row action here is,
// and the resultMsg handler in tui.go used to fold any refreshPending
// result that was not m.isTop into a flash-and-reload: the secret became
// the footer text on the very list it was revealed from, in a screen-share-
// and scrollback-visible line, exactly where kv.list's own comment above
// promises it will not be.
func TestRevealingLandsOnItsOwnPageRatherThanFlashingTheValue(t *testing.T) {
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
	if next.refreshPending {
		t.Fatal("refreshPending is true for kv.get — its result would take the flash-and-reload " +
			"branch instead of landing on its own page")
	}

	// The unlock form is behind us now; this is the run it opened for,
	// completing with the value db-password holds.
	const secret = "hunter2-the-actual-value"
	final, _ := next.Update(resultMsg{cap: get, view: view.Text{Body: secret}})
	fm := final.(Model)
	if fm.mode != modeResult {
		t.Errorf("mode = %v, want modeResult — the value did not land on its own page", fm.mode)
	}
	if strings.Contains(fm.flash, secret) {
		t.Errorf("the secret reached the flash line: %q", fm.flash)
	}
}

// flashText's second layer, for a capability flashSafe has vouched for: a
// Text result that is not actually a one-liner — multiple lines, or just
// long — falls back to the generic "<capability> done" rather than being
// drawn as itself, even though flashSafe cleared it to be shown at all.
func TestFlashTextFallsBackForAnythingThatIsNotAOneLiner(t *testing.T) {
	set := plugin.Capability{ID: "kv.set"} // flashSafe: true, unlike kv.get
	for name, v := range map[string]view.Text{
		"multi-line": {Body: "line one\nline two"},
		"too long":   {Body: strings.Repeat("x", maxFlashLen+1)},
	} {
		t.Run(name, func(t *testing.T) {
			got := flashText(resultMsg{cap: set, view: v})
			if got != "kv.set done" {
				t.Errorf("flashText = %q, want the generic fallback", got)
			}
		})
	}
	// The control: an ordinary short confirmation still draws as itself.
	if got := flashText(resultMsg{cap: set, view: view.Text{Body: "copied to clipboard"}}); got != "copied to clipboard" {
		t.Errorf("flashText = %q, want the one-liner drawn as itself", got)
	}
}

// The first layer: a capability neither map has an opinion on — or one
// alwaysOwnPage claims — never has its result drawn as itself, whatever its
// shape, because flashText never reaches runAction's own-page capabilities
// in practice but must still fail safe if it ever did.
func TestFlashTextFallsBackForAnyCapabilityFlashSafeDoesNotName(t *testing.T) {
	get := plugin.Capability{ID: "kv.get"} // alwaysOwnPage: true, not flashSafe
	if got := flashText(resultMsg{cap: get, view: view.Text{Body: "hunter2"}}); got != "kv.get done" {
		t.Errorf("flashText = %q, want the generic fallback for a capability flashSafe does not name", got)
	}
	unknown := plugin.Capability{ID: "future.reveal"} // in neither map
	if got := flashText(resultMsg{cap: unknown, view: view.Text{Body: "s3cr3t"}}); got != "future.reveal done" {
		t.Errorf("flashText = %q, want the generic fallback for an unclassified capability", got)
	}
}
