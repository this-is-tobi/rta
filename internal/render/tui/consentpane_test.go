package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Answering a parked call from the screen the operator is already looking at.
// Everything below is against the real catalogue: the wiring is
// one table entry, and what makes it work is a set of properties of the
// capabilities it names — which is exactly the kind of agreement that rots
// silently when one side is edited.

func realRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestTheConsentQueueCanBeAnsweredFromTheTUI(t *testing.T) {
	reg := realRegistry(t)
	acts := capActions(reg, "agent.pending")
	if len(acts) != 3 {
		t.Fatalf("agent.pending offers %d actions, want show, allow and deny", len(acts))
	}
	byKey := map[string]capAction{}
	for _, a := range acts {
		byKey[a.key] = a
	}
	allow, ok := byKey["a"]
	if !ok || allow.cap.ID != "agent.allow" {
		t.Fatalf("a = %+v, want agent.allow", allow)
	}
	deny, ok := byKey["d"]
	if !ok || deny.cap.ID != "agent.deny" {
		t.Fatalf("d = %+v, want agent.deny", deny)
	}
	for _, a := range acts {
		if a.src != srcRow {
			t.Fatalf("%s does not act on the row under the cursor", a.cap.ID)
		}
	}
	// enter reads before you answer, which is the whole of decision 10:
	// approving an outcome rather than an intention.
	show, ok := byKey["enter"]
	if !ok || show.cap.ID != "agent.show" {
		t.Fatalf("enter = %+v, want agent.show", show)
	}
	// And the detail page can answer without going back to the list.
	from := map[string]string{}
	for _, a := range capActions(reg, "agent.show") {
		from[a.key] = a.cap.ID
		if a.src != srcSelf {
			t.Fatalf("%s on the detail page does not act on the request it is showing", a.cap.ID)
		}
	}
	if from["a"] != "agent.allow" || from["d"] != "agent.deny" {
		t.Fatalf("the detail page offers %v", from)
	}
}

func TestTheRowUnderTheCursorIsTheRequestThatGetsAnswered(t *testing.T) {
	// The identity comes from the first column, so agent.pending's first
	// column has to be the id agent.allow takes — the one agreement between
	// two files that nothing else checks.
	reg := realRegistry(t)
	pending, ok := reg.Capability("agent.pending")
	if !ok {
		t.Fatal("no agent.pending")
	}
	allow, _ := reg.Capability("agent.allow")
	keys, _ := keyFields(allow)
	if len(keys) != 1 || keys[0].Name != "id" {
		t.Fatalf("agent.allow's identity is %+v, want a single positional id", keys)
	}
	_ = pending

	m := Model{reg: reg, row: 1}
	m.current = pending
	tbl := view.Table{
		Columns: []view.Column{{Name: "id"}, {Name: "capability"}},
		Rows:    [][]string{{"aaaa1111", "kv.get"}, {"bbbb2222", "kv.rm"}},
	}
	next, _ := m.runAction(capAction{key: "d", label: "deny", cap: mustCap(t, reg, "agent.deny"), src: srcRow, bare: true}, tbl)
	nm := next.(Model)
	if got := nm.lastValues["id"]; got != "bbbb2222" {
		t.Fatalf("the answer was aimed at %v, not at the row under the cursor", got)
	}
}

func TestDenyIsOneKeyAndAllowStopsToConfirm(t *testing.T) {
	// The asymmetry is the security property. It used to fall out of the
	// declarations alone — deny had nothing left to ask — until the remote
	// consent flow gave deny `--server` and a passphrase; now the spec table
	// declares it per action, and this test is what keeps the declaration
	// honest: deny and show run bare, allow never does, so granting access
	// still stops for a form (--ttl above all) while the safe answer and the
	// reading both stay one key.
	reg := realRegistry(t)
	bare := map[string]bool{}
	for _, capID := range []string{"agent.pending", "agent.show"} {
		for _, spec := range capActionSpecs[capID] {
			bare[capID+" "+spec.key] = spec.bare
		}
	}
	for _, want := range []string{"agent.pending enter", "agent.pending d", "agent.show d"} {
		if !bare[want] {
			t.Errorf("%s is not bare — the one-key answer now opens a form", want)
		}
	}
	for _, notWant := range []string{"agent.pending a", "agent.show a"} {
		if bare[notWant] {
			t.Errorf("%s is bare — granting access has to stop for a confirmation", notWant)
		}
	}
	// bare waives only the optional-field form, so it is sound exactly while
	// nothing beyond the row's id is required. A future required input on
	// these would make the one-key answer a validation error instead of a
	// form — loud, but wrong: it belongs in this table as a decision.
	for _, capID := range []string{"agent.deny", "agent.show"} {
		c := mustCap(t, reg, capID)
		for _, f := range c.Inputs {
			if f.Required && f.Name != "id" {
				t.Errorf("%s requires %q, which a bare action never asks for", capID, f.Name)
			}
		}
	}
	deny := mustCap(t, reg, "agent.deny")
	if deny.Safety == plugin.Destructive {
		t.Fatal("deny is destructive, so it would stop for a confirmation it does not need")
	}
	allow := mustCap(t, reg, "agent.allow")
	if rest := fieldsAfter(allow, map[string]any{"id": "x"}); len(rest) == 0 {
		t.Fatal("allow runs on a keypress — granting access has to stop for a confirmation")
	}
}

// bare's blast radius is the whole action table, so its soundness net has
// to be too — the consent-pane test above pins the four entries the
// feature shipped with, and without this walk a one-line table edit could
// hand any capability a one-key mutation with nothing failing.
func TestBareIsDeclaredOnlyWhereItIsSafe(t *testing.T) {
	reg := realRegistry(t)
	for capID, specs := range capActionSpecs {
		for _, spec := range specs {
			if !spec.bare {
				continue
			}
			c, ok := reg.Capability(spec.id)
			if !ok {
				t.Errorf("%s %s: bare on unknown capability %q", capID, spec.key, spec.id)
				continue
			}
			// runAction checks Destructive before bare, so this flag would be
			// inert there — which is exactly why it is refused here: a bare
			// destructive entry is a claim about the action that the code
			// quietly does not honour, and the next refactor of that ordering
			// would have a lie already waiting in the table.
			if c.Safety == plugin.Destructive {
				t.Errorf("%s %s: bare on destructive %s", capID, spec.key, spec.id)
			}
			// One non-Read capability may run on a keypress, and it is named:
			// deny is the fail-safe direction, taking nothing and granting
			// nothing. Anything else mutating in one key is a decision for
			// this list, not a side effect of setting a flag.
			if c.Safety != plugin.Read && spec.id != "agent.deny" {
				t.Errorf("%s %s: bare on %s (%s) — a one-key mutation is for the fail-safe direction only",
					capID, spec.key, spec.id, c.Safety)
			}
			// bare waives only the optional-field form; a required field it
			// never asks for would turn the one-key action into a validation
			// error. Positional identity comes from the row, so only the
			// non-positional kind can strand it.
			for _, f := range c.Inputs {
				if f.Required && !f.Positional {
					t.Errorf("%s %s: %s requires %q, which a bare action never asks for",
						capID, spec.key, spec.id, f.Name)
				}
			}
		}
	}
}

// The other half of the invariant, pinned on the code rather than the
// table: whatever the table says, bare must never waive the destructive
// confirmation, because runAction consults Destructive first.
func TestBareNeverWaivesTheDestructiveConfirmation(t *testing.T) {
	reg := realRegistry(t)
	m := Model{reg: reg}
	c := plugin.Capability{ID: "x.rm", Safety: plugin.Destructive,
		Inputs: []plugin.Field{{Name: "id", Type: plugin.String, Positional: true, Required: true}}}
	tbl := view.Table{Columns: []view.Column{{Name: "id"}}, Rows: [][]string{{"a1"}}}
	next, _ := m.runAction(capAction{key: "x", label: "rm", cap: c, src: srcRow, bare: true}, tbl)
	if nm := next.(Model); nm.mode != modeForm {
		t.Fatalf("a bare destructive action skipped its confirmation (mode %v)", nm.mode)
	}
}

func TestTheOverviewTileLeadsToTheQueueAndTheRecord(t *testing.T) {
	reg := realRegistry(t)
	var got []string
	for _, a := range capActions(reg, "agent.overview") {
		got = append(got, a.key+"="+a.cap.ID)
		if a.src != srcNone {
			t.Fatalf("%s asks for a row on a tile that has none", a.cap.ID)
		}
	}
	want := "w=agent.pending g=agent.log"
	if strings.Join(got, " ") != want {
		t.Fatalf("overview actions = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestAnswerKeysDoNotCollideWithTheKeysEveryScreenOwns(t *testing.T) {
	// A row action shadows the global binding of the same key, so an
	// answer key that is also navigation would take a key away from the
	// screen it is on.
	reserved := map[string]string{
		"q": "quit", "r": "re-run", "e": "edit inputs", "y": "copy json",
		"c": "configure", "b": "browse", "/": "search",
		"h": "left", "j": "down", "k": "up", "l": "right",
	}
	for _, capID := range []string{"agent.pending", "agent.overview"} {
		for _, spec := range capActionSpecs[capID] {
			if what, clash := reserved[spec.key]; clash {
				t.Errorf("%s binds %q, which every screen already uses for %s", capID, spec.key, what)
			}
		}
	}
}

func mustCap(t *testing.T, reg *registry.Registry, id string) plugin.Capability {
	t.Helper()
	c, ok := reg.Capability(id)
	if !ok {
		t.Fatalf("no capability %q", id)
	}
	return c
}
