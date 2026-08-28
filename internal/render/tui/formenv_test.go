package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A form and the environment it is aimed at.
//
// These are about one property: what the boxes say and where the call goes are
// the same answer. Every one of them fails if the seed and the picker are ever
// built from different environments again.

// switchOn puts the machine on an environment and lands the bind, the way the
// update loop does.
func switchOn(t *testing.T, m *Model, name string) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{Active: name}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, m)
	if m.active != name {
		t.Fatalf("active = %q after switching to %q", m.active, name)
	}
}

// openRunForm opens db.status's form on m and hands back the form.
func openRunForm(t *testing.T, m Model) *capForm {
	t.Helper()
	model, _ := m.startForm(dbPlugin().Capabilities[0], nil)
	next, ok := model.(Model)
	if !ok {
		t.Fatalf("startForm returned %T", model)
	}
	if next.form == nil {
		t.Fatal("no form was opened")
	}
	return next.form
}

// The boxes open showing the environment that is switched on, because a form
// that shows the base configuration while the run goes somewhere else is a
// screen that is believed and wrong.
func TestAFormOpensOnTheSwitchedOnEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")

	form := openRunForm(t, m)
	if got := *form.bindings["host"]; got != "staging.internal" {
		t.Errorf("host box = %q, want staging.internal", got)
	}
	if got := *form.bindings[profileInput]; got != "staging" {
		t.Errorf("picker = %q, want staging", got)
	}
}

// Nothing switched on leaves the declared default in the box, unchanged.
func TestAFormWithNothingSwitchedOnKeepsItsDefaults(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	form := openRunForm(t, m)
	if got := *form.bindings["host"]; got != "localhost" {
		t.Errorf("host box = %q, want the declared default", got)
	}
	if got := *form.bindings[profileInput]; got != profileNoneLabel {
		t.Errorf("picker = %q, want the base configuration", got)
	}
}

// Changing the picker moves the call. The box below it was filled in by the
// form, not by a person, so it must not pin the run to the environment they
// just navigated away from.
//
// This is the whole reason capForm.displayed exists: a seeded value handed back
// as a caller value beats the profile layer, so without it the picker would say
// prod and the connection would be staging's.
func TestChangingThePickerMovesTheCall(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	form := openRunForm(t, m)

	*form.bindings[profileInput] = "prod"
	values := form.values()
	if got, ok := values["host"]; ok {
		t.Errorf("an untouched box answered %q — it is a display, not an answer", got)
	}

	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, values)
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "prod" {
		t.Fatalf("resolved environment = %q, want prod", name)
	}
	// What the handler would actually receive.
	got := plugin.Resolve(c, plugin.Inputs{
		Caller: withoutPicker(c, values), Profile: filled, ProfileName: name,
	})
	if got["host"] != "prod.internal" {
		t.Errorf("the run would reach %v, want prod.internal", got["host"])
	}
	if _, leaked := got[profileInput]; leaked {
		t.Error("the environment question reached the handler's values")
	}
}

// Picking the base configuration means exactly that, including for the boxes
// the environment had filled in.
func TestPickingTheBaseConfigurationClearsTheEnvironmentsValues(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	form := openRunForm(t, m)

	*form.bindings[profileInput] = profileNoneLabel
	values := form.values()
	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, values)
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "" {
		t.Fatalf("resolved environment = %q, want none", name)
	}
	got := plugin.Resolve(c, plugin.Inputs{Caller: withoutPicker(c, values), Profile: filled, ProfileName: name})
	if got["host"] != "localhost" {
		t.Errorf("the run would reach %v, want the declared default", got["host"])
	}
}

// An edited box is a real answer and still wins over the environment — the
// precedence plugin.Resolve documents, preserved.
func TestATypedValueStillBeatsTheEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	form := openRunForm(t, m)

	*form.bindings["host"] = "typed.internal"
	values := form.values()
	if got := values["host"]; got != "typed.internal" {
		t.Fatalf("host = %v, want what was typed", got)
	}
	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, values)
	if verr != nil {
		t.Fatal(verr)
	}
	got := plugin.Resolve(c, plugin.Inputs{Caller: withoutPicker(c, values), Profile: filled, ProfileName: name})
	if got["host"] != "typed.internal" {
		t.Errorf("the run would reach %v, want what was typed", got["host"])
	}
}

// `e` reopens on the environment the result on screen came from, not on
// whatever is switched on now — otherwise editing a query run against prod
// would quietly re-aim it.
func TestEditingReopensOnTheEnvironmentTheResultCameFrom(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	c := dbPlugin().Capabilities[0]

	if got := m.pickedProfile(c, map[string]any{profileInput: "prod"}); got != "prod" {
		t.Errorf("reopened on %q, want prod", got)
	}
	if got := m.pickedProfile(c, map[string]any{profileInput: profileNoneLabel}); got != "" {
		t.Errorf("reopened on %q, want the base configuration", got)
	}
	if got := m.pickedProfile(c, nil); got != "staging" {
		t.Errorf("reopened on %q with nothing carried, want the switch", got)
	}
}

// The environment's answer survives a run, so `r` re-runs the same thing.
//
// resolveProfile used to delete the picker's answer out of the caller's own
// map, which is the map the shell keeps for re-run and for `e`.
func TestResolvingAnEnvironmentLeavesTheCallersValuesAlone(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	values := map[string]any{profileInput: "prod"}
	if _, _, _, verr := m.resolveProfile(dbPlugin().Capabilities[0], values); verr != nil {
		t.Fatal(verr)
	}
	if got := values[profileInput]; got != "prod" {
		t.Errorf("the caller's map now says %v — a re-run would go somewhere else", got)
	}
}

// A credential never reaches a box. Seeding a masked input paints its length in
// dots, which is a thing to know about somebody's passphrase.
func TestTheSeedLeavesCredentialsOut(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		{Name: "host", Type: plugin.String, Config: "host"},
		{Name: "password", Type: plugin.Secret, Config: "password"},
	}}
	got := withoutSecrets(c, map[string]any{"host": "h", "password": "hunter2"})
	if got["host"] != "h" {
		t.Errorf("host = %v, want it kept", got["host"])
	}
	if _, on := got["password"]; on {
		t.Error("the credential was seeded into the form")
	}
}

// secretPlugin declares credentials, so a connection has something to attach.
func secretPlugin(inputs ...string) plugin.Plugin {
	fields := []plugin.Field{{Name: "host", Type: plugin.String, Config: "host"}}
	for _, name := range inputs {
		// Local and EnvFallback, never Config: a Secret may not be filled from a
		// plaintext file, which is exactly why a profile references one instead.
		fields = append(fields, plugin.Field{
			Name: name, Type: plugin.Secret, Local: true, EnvFallback: true})
	}
	return plugin.Plugin{
		Name: "vlt", Summary: "a plugin with credentials",
		Capabilities: []plugin.Capability{{
			ID: "vlt.read", Summary: "read", Safety: plugin.Read, Inputs: fields,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}
}

// credentialModel is a shell open on a one-plugin environment whose plugin
// declares the named credentials, ready for the credential editor.
func credentialModel(t *testing.T, inputs ...string) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"vlt": conn(map[string]any{"host": "h"})}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(secretPlugin(inputs...)); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.profiles = m.profileRows()
	m.profileOpen = "staging"
	return m
}

// credentialForm opens the credential editor over such an environment.
func credentialForm(t *testing.T, inputs ...string) *capForm {
	t.Helper()
	return openCredentialForm(t, credentialModel(t, inputs...))
}

// soloCredentialModel is an environment whose plugin has exactly one input a
// connection can fill: the credential itself.
//
// Needed because the ordinary fixture declares a Config-keyed `host` as well,
// and the credential form offers every fillable input rather than only the
// Secrets — mapping `user` onto a cluster Secret's `username` key is the case
// that drove that. So "one input is not a question" needs a plugin that
// genuinely has one.
func soloCredentialModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"vlt": {}}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "vlt", Summary: "a plugin with one credential",
		Capabilities: []plugin.Capability{{
			ID: "vlt.read", Summary: "read", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.profiles = m.profileRows()
	m.profileOpen = "staging"
	return m
}

func openCredentialForm(t *testing.T, m Model) *capForm {
	t.Helper()
	model, _ := m.startCredentialForm()
	next, ok := model.(Model)
	if !ok {
		t.Fatalf("startCredentialForm returned %T", model)
	}
	if next.form == nil {
		t.Fatalf("no credential form was opened (flash: %q)", next.flash)
	}
	return next.form
}

// The follow-up field is the one the source question implies, and only that
// one. A box the form will not read is a box that has to explain itself.
func TestTheCredentialFormAsksOnlyWhatTheSourceNeeds(t *testing.T) {
	form := credentialForm(t, "password")

	// The store is empty in a fresh data directory, so referencing is not even
	// offered and there is no entry picker to show.
	if _, offered := form.bindings[credEntryField]; offered {
		t.Error("an entry picker was built with nothing in the store to reference")
	}

	*form.bindings[credSourceField] = credSourceStore
	if form.hidden(credValueField) {
		t.Error("storing a new value did not ask for the value")
	}

	*form.bindings[credSourceField] = credSourceEnv
	if !form.hidden(credValueField) {
		t.Error("the environment-variable answer was still asked for a value to store")
	}
	if got := form.values(); got[credValueField] != nil {
		t.Errorf("a hidden field answered %v", got[credValueField])
	}
}

// With entries in the store, referencing one is offered — and the picker
// appears only for that answer.
func TestTheCredentialFormShowsTheEntryPickerOnlyWhenReferencing(t *testing.T) {
	t.Setenv("RTA_KV_PASSPHRASE", "correct horse battery staple")
	m := credentialModel(t, "password")
	if verr := kv.Store("existing-entry", "s3cret", "a test entry"); verr != nil {
		t.Fatal(verr)
	}
	form := openCredentialForm(t, m)

	bound, offered := form.bindings[credEntryField]
	if !offered {
		t.Fatal("no entry picker was built although the store has an entry")
	}
	if *bound != "existing-entry" {
		t.Errorf("entry picker = %q, want the stored entry", *bound)
	}
	if *form.bindings[credSourceField] != credSourceRef {
		t.Errorf("source = %q, want referencing offered first", *form.bindings[credSourceField])
	}
	if form.hidden(credEntryField) {
		t.Error("referencing an entry did not ask which one")
	}

	*form.bindings[credSourceField] = credSourceStore
	if !form.hidden(credEntryField) {
		t.Error("storing a new value still asked which existing entry to point at")
	}
	if form.hidden(credValueField) {
		t.Error("storing a new value did not ask for the value")
	}
}

// Answering "an environment variable" ends the form there, because there is
// nothing further rta can do about it — a child process cannot set a variable
// in the shell that started it.
//
// Driven by keys rather than by writing the binding, because the select writes
// its own value back on enter: this is the sequence a person actually presses.
func TestTheEnvironmentAnswerAsksNothingFurther(t *testing.T) {
	// The solo fixture, so the input picker is absent and the first field on
	// screen is the source question this test is about.
	form := openCredentialForm(t, soloCredentialModel(t))
	// The store is empty, so the offers are store-a-new-value then the
	// environment variable. Down, then enter.
	form.form = settleForm(form.form, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := *form.bindings[credSourceField]; got != credSourceEnv {
		t.Fatalf("source = %q after one press, want the environment variable", got)
	}
	form.form = settleForm(form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	if form.form.State != huh.StateCompleted {
		t.Error("the form asked something further after the one answer it can do nothing about")
	}
}

// One fillable input is not a question. A picker with one entry is a keystroke
// that teaches nothing, and the form still knows which input it is editing.
func TestTheCredentialFormDoesNotAskWhichWhenThereIsOnlyOne(t *testing.T) {
	form := openCredentialForm(t, soloCredentialModel(t))
	if _, asked := form.bindings[credInputField]; asked {
		t.Error("a picker with one answer was put on screen")
	}
	if got := form.values()[credInputField]; got != "password" {
		t.Errorf("the form carries %v as the credential being edited, want password", got)
	}

	two := credentialForm(t, "password", "token")
	if _, asked := two.bindings[credInputField]; !asked {
		t.Error("two credentials were not offered as a choice")
	}
}

// The editors write exactly what is in their boxes: an untouched value there is
// the value being kept, not a display of somewhere else.
func TestTheEditorsKeepUntouchedValues(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	m.profileOpen = "staging"
	m.profiles = m.profileRows()

	model, _ := m.startConnForm("db")
	next, ok := model.(Model)
	if !ok {
		t.Fatalf("startConnForm returned %T", model)
	}
	if next.form == nil {
		t.Fatal("no connection form was opened")
	}
	// The file-stated fields are answers being kept, never displays — the
	// editor marks only what the file does not state.
	if next.form.derived[profileSetPrefix+"host"] {
		t.Fatal("a file-stated value is marked as a display, so saving would drop it")
	}
	got := next.form.values()
	if v := got[profileSetPrefix+"host"]; v != "staging.internal" {
		t.Errorf("host = %v, want the value being kept", v)
	}
	if v := got[profilePluginField]; !strings.Contains(str(v), "db") {
		t.Errorf("plugin = %v, want db", v)
	}
}
