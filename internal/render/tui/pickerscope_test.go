package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The environment picker of a run form stays inside the environment that is
// switched on, and says how to see the others.
//
// Every model here is over dbPlugin with a configuration built in the test, and
// no form opens a store: the only thing paid for is the picker and the form
// around it.

// scopeConfig is three environments with a different number of connections to db
// each, so a count and an order can be told apart from an accident.
func scopeConfig() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"db/main":  conn(map[string]any{"host": "main.homelab"}),
			"db/media": conn(map[string]any{"host": "media.homelab"}),
		}},
		"preprod": {Plugins: map[string]config.Connection{
			"db": conn(map[string]any{"host": "pg.preprod"}),
		}},
		"prod": {Plugins: map[string]config.Connection{
			"db":           conn(map[string]any{"host": "pg.prod"}),
			"db/analytics": conn(map[string]any{"host": "analytics.prod"}),
		}},
	}}
}

// scopedModel is a model over scopeConfig with an environment switched on, or
// none for "".
func scopedModel(t *testing.T, on string) Model {
	t.Helper()
	m := profileModel(t, scopeConfig())
	selectEnvironment(t, on)
	return m
}

// selectEnvironment writes the selection the way `rta use` does and nothing
// more: the picker reads the selection from the file, as the run reads it, and
// binding the environment's values is a step these tests have no use for — and
// one that answers with several messages for an environment holding several
// connections, which switchOn's single-message helper does not take.
func selectEnvironment(t *testing.T, name string) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{Active: name}); verr != nil {
		t.Fatal(verr)
	}
}

func pickerOptions(t *testing.T, m Model, on string) []string {
	t.Helper()
	f := m.profilePicker(dbPlugin().Capabilities[0], on)
	if f == nil {
		t.Fatal("no picker")
	}
	return f.Options
}

func wantOptions(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("options\n got  %q\n want %q", got, want)
	}
}

// With an environment on, the picker lists the base configuration, that
// environment's connections, the entry that states one, and last the entry that
// brings the others in, with how many there are.
func TestThePickerListsOnlyTheEnvironmentThatIsOn(t *testing.T) {
	wantOptions(t, pickerOptions(t, scopedModel(t, "homelab"), ""),
		profileNoneLabel, "homelab/main", "homelab/media", adHocPickLabel, "— other environments (3) —")
	wantOptions(t, pickerOptions(t, scopedModel(t, "prod"), ""),
		profileNoneLabel, "prod", "prod/analytics", adHocPickLabel, "— other environments (3) —")
}

// Expanded, every environment is listed, the one that is on first and the rest in
// their own order, and the last entry takes the others off again.
func TestTheExpandedPickerListsEveryEnvironmentWithTheOneThatIsOnFirst(t *testing.T) {
	m := scopedModel(t, "prod")
	m.allEnvironments = true
	wantOptions(t, pickerOptions(t, m, ""),
		profileNoneLabel, "prod", "prod/analytics", "homelab/main", "homelab/media", "preprod",
		adHocPickLabel, "— only prod —")
}

// With nothing switched on there is nothing to isolate to: the picker lists
// everything in its own order, whatever the switch says, and has no entry about
// the others.
func TestThePickerListsEverythingWhenNoEnvironmentIsOn(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		m := scopedModel(t, "")
		m.allEnvironments = expanded
		wantOptions(t, pickerOptions(t, m, ""),
			profileNoneLabel, "homelab/main", "homelab/media", "preprod", "prod", "prod/analytics", adHocPickLabel)
	}
}

// The entry is there to reach connections that are not listed; with none it is
// absent. An environment that cannot be used is not a connection the list would
// show, so it does not count.
func TestTheOtherEnvironmentsEntryIsAbsentWhenThereAreNone(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{"db": conn(map[string]any{"host": "a"})}},
		"broken":  {Plugins: map[string]config.Connection{"db": conn(map[string]any{"nosuchkey": "b"})}},
	}})
	selectEnvironment(t, "homelab")
	wantOptions(t, pickerOptions(t, m, ""), profileNoneLabel, "homelab", adHocPickLabel)

	m.allEnvironments = true
	wantOptions(t, pickerOptions(t, m, ""), profileNoneLabel, "homelab", adHocPickLabel)
}

// An environment that is on and has no connection to this plugin still has a
// picker, and the others are behind the entry: the person is working in an
// environment that does not reach it, and the list of everybody else's is the
// one to ask for.
func TestTheEnvironmentThatIsOnMayHoldNoConnectionToThePlugin(t *testing.T) {
	cfg := scopeConfig()
	cfg.Profiles["bare"] = config.Profile{}
	m := profileModel(t, cfg)
	selectEnvironment(t, "bare")
	wantOptions(t, pickerOptions(t, m, ""), profileNoneLabel, adHocPickLabel, "— other environments (5) —")
}

// A run form is built on the narrow list until the entry is chosen.
func TestAFormOpensOnTheNarrowList(t *testing.T) {
	m := scopedModel(t, "homelab")
	cf := openRunForm(t, m)
	if got := cf.fields[0].Options; !slices.Contains(got, "— other environments (3) —") || slices.Contains(got, "prod") {
		t.Errorf("the form opened on %q", got)
	}
}

// Passing over the entry while walking down the list changes nothing; only
// choosing it brings the others in.
func TestPassingOverTheOtherEnvironmentsEntryChangesNothing(t *testing.T) {
	m := scopedModel(t, "homelab")
	nm := openFormOn(t, m, dbPlugin().Capabilities[0])
	label := otherEnvironmentsLabel(3)

	passed, rebuilt := hoverOn(nm, label)
	if rebuilt || passed.allEnvironments || passed.form.adHocFrom != nil {
		t.Fatalf("hovering on the entry changed the form (rebuilt %v, all %v)", rebuilt, passed.allEnvironments)
	}
	if got := *passed.form.bindings[profileInput]; got != label {
		t.Errorf("the cursor was moved off the entry it is on: %q", got)
	}
}

// Choosing it rebuilds the same form with every environment listed, the cursor on
// the first connection that is not the one that is on, and what was typed kept.
// The choice is the session's, so the next form starts wide too.
func TestChoosingOtherEnvironmentsListsThemAndKeepsWhatWasTyped(t *testing.T) {
	m := scopedModel(t, "homelab")
	c := dbPlugin().Capabilities[1]
	nm := openFormOn(t, m, c)
	nm = typedThenBackAtPicker(t, nm, "schema", "analytics")

	wide, ok := chooseEntry(nm, otherEnvironmentsLabel(3))
	if !ok {
		t.Fatal("choosing the entry did not rebuild the form")
	}
	if !wide.allEnvironments {
		t.Error("the choice was not kept for the session")
	}
	wantOptions(t, wide.form.fields[0].Options,
		profileNoneLabel, "homelab/main", "homelab/media", "preprod", "prod", "prod/analytics",
		adHocPickLabel, "— only homelab —")
	if got := *wide.form.bindings[profileInput]; got != "preprod" {
		t.Errorf("the cursor is on %q, want the first connection of another environment", got)
	}
	if got := *wide.form.bindings["schema"]; got != "analytics" {
		t.Errorf("what was typed was lost: schema = %q", got)
	}
	if wide.form.cap.ID != c.ID {
		t.Errorf("the form is now about %q", wide.form.cap.ID)
	}

	again := wide.profilePicker(dbPlugin().Capabilities[0], "")
	if again == nil || !slices.Contains(again.Options, "prod/analytics") {
		t.Errorf("another form started narrow in the same session: %v", again)
	}
}

// The last entry of the expanded list goes back to the narrow one, on the
// environment that is on, with what was typed kept.
func TestChoosingOnlyTheEnvironmentThatIsOnNarrowsTheListAgain(t *testing.T) {
	m := scopedModel(t, "homelab")
	m.allEnvironments = true
	c := dbPlugin().Capabilities[1]
	nm := openFormOn(t, m, c)
	nm = typedThenBackAtPicker(t, nm, "schema", "analytics")

	narrow, ok := chooseEntry(nm, onlyEnvironmentLabel("homelab"))
	if !ok {
		t.Fatal("choosing the entry did not rebuild the form")
	}
	if narrow.allEnvironments {
		t.Error("the session is still expanded")
	}
	wantOptions(t, narrow.form.fields[0].Options,
		profileNoneLabel, "homelab/main", "homelab/media", adHocPickLabel, "— other environments (3) —")
	if got := *narrow.form.bindings[profileInput]; got != "homelab/main" {
		t.Errorf("the cursor is on %q, want the environment that is on", got)
	}
	if got := *narrow.form.bindings["schema"]; got != "analytics" {
		t.Errorf("what was typed was lost: schema = %q", got)
	}
}

// A form handed a choice made earlier, such as the connection a tile is pinned to
// or the one the last run went through, shows it: the list is expanded for that
// form instead of falling back to the base configuration under it, and the
// session is left as it was.
func TestARememberedChoiceOutsideTheEnvironmentExpandsThatFormOnly(t *testing.T) {
	m := scopedModel(t, "homelab")
	c := dbPlugin().Capabilities[0]

	f := m.profilePicker(c, "prod/analytics")
	if f == nil || f.Default != "prod/analytics" {
		t.Fatalf("the remembered choice is not what the picker opens on: %+v", f)
	}
	if !slices.Contains(f.Options, "prod") || f.Options[len(f.Options)-1] != "— only homelab —" {
		t.Errorf("the list was not expanded for it: %q", f.Options)
	}

	model, _ := m.startForm(c, map[string]any{profileInput: "prod/analytics"})
	nm := model.(Model)
	if got := *nm.form.bindings[profileInput]; got != "prod/analytics" {
		t.Errorf("the form opened on %q", got)
	}
	if nm.allEnvironments {
		t.Error("opening one form on another environment's connection expanded every form")
	}
	wantOptions(t, pickerOptions(t, nm, ""),
		profileNoneLabel, "homelab/main", "homelab/media", adHocPickLabel, "— other environments (3) —")
}

// The way a person meets it: a result that went through another environment's
// connection, and `e` to edit its inputs.
func TestEditingTheInputsOfARunThroughAnotherEnvironmentKeepsItsConnectionListed(t *testing.T) {
	m := scopedModel(t, "homelab")
	c := dbPlugin().Capabilities[0]
	m.current = c
	m.lastValues = map[string]any{profileInput: "prod/analytics"}
	m.result = resultMsg{cap: c, view: view.Text{Body: "ok"}}
	m.mode = modeResult
	m.renderResult()

	opened := press(t, m, "e")
	if opened.form == nil {
		t.Fatal("e opened no form")
	}
	if got := *opened.form.bindings[profileInput]; got != "prod/analytics" {
		t.Errorf("the form opened on %q, not on the connection the run went through", got)
	}
	if got := opened.form.fields[0].Options; !slices.Contains(got, "prod/analytics") {
		t.Errorf("the connection the run went through is not listed: %q", got)
	}
	if opened.allEnvironments {
		t.Error("editing one run expanded every form")
	}
}

// A remembered choice that is nowhere in the list, such as an environment since
// renamed, falls back to the base configuration as it always did, and does not
// expand anything for it.
func TestARememberedChoiceNoListHoldsFallsBackToTheBaseConfiguration(t *testing.T) {
	m := scopedModel(t, "homelab")
	f := m.profilePicker(dbPlugin().Capabilities[0], "ghost")
	if f == nil || f.Default != profileNoneLabel {
		t.Fatalf("picker = %+v, want it on the base configuration", f)
	}
	wantOptions(t, f.Options,
		profileNoneLabel, "homelab/main", "homelab/media", adHocPickLabel, "— other environments (3) —")
}

// The entries that move the picker are never an environment to start a form on.
func TestTheEntriesThatMoveThePickerAreNotAnEnvironmentToOpenOn(t *testing.T) {
	m := scopedModel(t, "homelab")
	c := dbPlugin().Capabilities[0]
	for _, label := range []string{otherEnvironmentsLabel(3), onlyEnvironmentLabel("homelab")} {
		if got := m.pickedProfile(c, map[string]any{profileInput: label}); got != "" {
			t.Errorf("a form was to open on %q: %q", label, got)
		}
		if !isScopePick(label) {
			t.Errorf("%q is not recognised as an entry that moves the picker", label)
		}
	}
	for _, name := range []string{"homelab", "homelab/main", "prod/analytics", profileNoneLabel} {
		if isScopePick(name) {
			t.Errorf("%q was taken for an entry that moves the picker", name)
		}
	}
}

// A form completed with the cursor on the entry, as a fast submit does, brings the
// others in and does not run with the entry as its environment.
func TestAFastSubmitOnTheOtherEnvironmentsEntryExpandsInsteadOfRunning(t *testing.T) {
	for _, submit := range []tea.KeyPressMsg{
		{Code: 's', Mod: tea.ModCtrl}, {Code: tea.KeyEnter, Mod: tea.ModAlt},
	} {
		m := scopedModel(t, "homelab")
		nm := openFormOn(t, m, dbPlugin().Capabilities[0])
		nm.mode = modeForm
		nm = keyThrough(t, nm, tea.KeyPressMsg{Code: tea.KeyEnd})
		nm = keyThrough(t, nm, submit)

		if nm.mode != modeForm || nm.form == nil || nm.result.err != nil {
			t.Fatalf("%v: it left the form (mode %v, result err %v)", submit, nm.mode, nm.result.err)
		}
		if !nm.allEnvironments {
			t.Errorf("%v: the others were not brought in", submit)
		}
	}
}

// The keys a person presses: down through the connections, past the entry that
// states one without opening it, onto the entry that brings the others in, and
// enter on it.
func TestWalkingDownToTheOtherEnvironmentsAndChoosingThem(t *testing.T) {
	m := scopedModel(t, "homelab")
	nm := openFormOn(t, m, dbPlugin().Capabilities[0])
	if nm.mode != modeForm {
		t.Fatalf("mode = %v, want the form", nm.mode)
	}

	var seen []string
	for range 4 {
		nm = keyThrough(t, nm, keyDown)
		if nm.form == nil || nm.form.adHocFrom != nil || nm.allEnvironments {
			t.Fatalf("a key down changed more than the cursor (after %v)", seen)
		}
		seen = append(seen, *nm.form.bindings[profileInput])
	}
	want := []string{"homelab/main", "homelab/media", adHocPickLabel, otherEnvironmentsLabel(3)}
	if !slices.Equal(seen, want) {
		t.Fatalf("the cursor walked through %q, want %q", seen, want)
	}

	nm = keyThrough(t, nm, keyEnter)
	if !nm.allEnvironments || nm.form == nil || nm.form.adHocFrom != nil {
		t.Fatalf("enter on the entry did not bring the others in (all %v)", nm.allEnvironments)
	}
	if got := *nm.form.bindings[profileInput]; got != "preprod" {
		t.Errorf("the cursor is on %q", got)
	}
	if got := nm.form.fields[0].Options; !slices.Contains(got, "prod/analytics") ||
		!strings.HasPrefix(got[len(got)-1], onlyEnvironmentPrefix) {
		t.Errorf("the list is not the expanded one: %q", got)
	}
}
