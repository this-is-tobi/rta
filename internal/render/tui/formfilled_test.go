package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A form and the boxes its environment answers for it.
//
// These are about one property, and it is the property an empty box could not
// carry on its own: whether the operator has to type this. A credential the
// environment supplies and a credential nobody supplies both open as an empty
// masked box, and every test here fails the moment those two look the same
// again.

// vltCap is the capability secretPlugin declares, fetched from a model's own
// registry so a test asserts against the same declaration the form is built
// from rather than a copy of it.
func vltCap(t *testing.T, m Model) plugin.Capability {
	t.Helper()
	for _, c := range m.reg.Capabilities() {
		if c.ID == "vlt.read" {
			return c
		}
	}
	t.Fatal("vlt.read is not registered")
	return plugin.Capability{}
}

// notesFor annotates c's own inputs for the named environment and hands back
// the note that landed on one box, or "" — the whole path runForm takes,
// minus the form.
func notesFor(t *testing.T, m Model, name string, conn config.Connection,
	seed map[string]any, input string,
) string {
	t.Helper()
	c := vltCap(t, m)
	for _, f := range environmentNotes(c, c.Inputs, seed, name, conn) {
		if f.Name != input {
			continue
		}
		for _, declared := range c.Inputs {
			if declared.Name == input {
				return strings.TrimPrefix(strings.TrimPrefix(f.Help, declared.Help), " — ")
			}
		}
	}
	return ""
}

// The box names the entry, so an operator can see that the environment has an
// answer and which stored answer it is — a stale reference to an entry that
// was renamed is exactly the thing this makes visible.
func TestAMaskedBoxNamesTheEntryTheEnvironmentFillsItFrom(t *testing.T) {
	m := credentialModel(t, "password")
	got := notesFor(t, m, "staging",
		config.Connection{Secrets: map[string]string{"password": "kv:prod-db-password"}},
		map[string]any{"host": "h"}, "password")

	if !strings.Contains(got, "kv:prod-db-password") {
		t.Errorf("the password box says %q, which does not name the entry", got)
	}
	if !strings.Contains(got, "staging") {
		t.Errorf("the password box says %q, which does not name the environment", got)
	}
}

// A `kube:` reference is named the same way. The scheme is half the answer:
// "the cluster holds it" and "this machine's store holds it" send an operator
// looking in different places when it turns out to be wrong.
func TestAClusterReferenceIsNamedWithItsScheme(t *testing.T) {
	m := credentialModel(t, "password")
	got := notesFor(t, m, "staging",
		config.Connection{Secrets: map[string]string{"password": "kube:pg-creds/password"}},
		nil, "password")

	if !strings.Contains(got, "kube:pg-creds/password") {
		t.Errorf("the password box says %q, want the kube reference named in full", got)
	}
}

// An exported variable beats the reference at run time (see profile.Fill), so
// it is what the box has to name. Naming the reference here would be the
// screen disagreeing with the keypress in the one place it must not.
func TestAnExportedVariableIsNamedAheadOfTheReferenceItBeats(t *testing.T) {
	m := credentialModel(t, "password")
	t.Setenv(plugin.ProfileEnvVar("staging", "password"), "hunter2")

	got := notesFor(t, m, "staging",
		config.Connection{Secrets: map[string]string{"password": "kv:prod-db-password"}},
		nil, "password")

	if !strings.Contains(got, "$RTA_PROFILE_STAGING_PASSWORD") {
		t.Errorf("the password box says %q, want the variable that actually wins", got)
	}
	if !strings.Contains(got, "kv:prod-db-password") {
		t.Errorf("the password box says %q, want it to say what the variable beats", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("the box printed the credential itself: %q", got)
	}
}

// A labeled instance has no environment channel at all — Bind returns before
// it ever looks — so a variable that happens to be exported under the bare
// name must not be named here. Telling somebody to export it would teach a
// habit that fills the default instance and leaves this one empty.
func TestALabeledInstanceIsNotSentAfterAVariableItCannotRead(t *testing.T) {
	m := credentialModel(t, "password")
	t.Setenv(plugin.ProfileEnvVar("staging", "password"), "hunter2")

	got := notesFor(t, m, "staging/analytics",
		config.Connection{Secrets: map[string]string{"password": "kv:analytics-password"}},
		nil, "password")

	if strings.Contains(got, "RTA_PROFILE") {
		t.Errorf("a labeled instance was pointed at %q, a channel it has no access to", got)
	}
	if !strings.Contains(got, "kv:analytics-password") {
		t.Errorf("the password box says %q, want the reference it does read", got)
	}
}

// Silence when the environment has no answer. "not set" under every
// unconfigured credential is what would make the line that does say something
// get skipped — and an environment aimed at a database that trusts the local
// socket needs no password at all.
func TestABoxNothingFillsIsLeftExactlyAsItWas(t *testing.T) {
	m := credentialModel(t, "password")
	if got := notesFor(t, m, "staging", config.Connection{}, nil, "password"); got != "" {
		t.Errorf("an unconfigured credential box gained %q", got)
	}
}

// A box already showing a value needs no note about where a value would come
// from: what is in the seed is what won, and Fill applies the same precedence
// when the call is made.
func TestABoxThatAlreadyShowsItsAnswerGainsNothing(t *testing.T) {
	m := credentialModel(t, "password")
	conn := config.Connection{
		Set:     map[string]any{"host": "staging.internal"},
		Secrets: map[string]string{"host": "kube:pg-creds/host"},
	}
	if got := notesFor(t, m, "staging", conn, map[string]any{"host": "staging.internal"}, "host"); got != "" {
		t.Errorf("a box showing staging.internal also gained %q", got)
	}
}

// A non-credential input a `secrets:` mapping fills is blank for the other
// reason — Bind is pure, so nothing resolves a reference until the run — and
// gets the same line. Mapping `user` onto a cluster Secret's `username` key is
// an ordinary thing to want, and it left an empty box too.
func TestAnOrdinaryInputAReferenceFillsIsNamedAsWell(t *testing.T) {
	m := credentialModel(t, "password")
	got := notesFor(t, m, "staging",
		config.Connection{Secrets: map[string]string{"host": "kube:pg-creds/hostname"}},
		nil, "host")

	if !strings.Contains(got, "kube:pg-creds/hostname") {
		t.Errorf("the host box says %q, want the reference that fills it", got)
	}
}

// The capability's own declaration is never written to. runForm keeps that
// slice to rebuild the form on every picker move, and the registry hands the
// same one to every surface — an annotation stamped into it would follow one
// environment's answer onto every later form, including forms opened on a
// different environment entirely.
func TestAnnotatingAFormLeavesTheDeclarationAlone(t *testing.T) {
	m := credentialModel(t, "password")
	c := vltCap(t, m)
	before := make([]string, len(c.Inputs))
	for i, f := range c.Inputs {
		before[i] = f.Help
	}

	environmentNotes(c, c.Inputs, nil, "staging",
		config.Connection{Secrets: map[string]string{"password": "kv:prod-db-password"}})

	for i, f := range c.Inputs {
		if f.Help != before[i] {
			t.Errorf("%s's declared help became %q, was %q", f.Name, f.Help, before[i])
		}
	}
}

// The base configuration answers nothing, and says so by saying nothing.
func TestNoEnvironmentMeansNoNotes(t *testing.T) {
	m := credentialModel(t, "password")
	got := notesFor(t, m, "",
		config.Connection{Secrets: map[string]string{"password": "kv:prod-db-password"}},
		nil, "password")
	if got != "" {
		t.Errorf("the base configuration produced %q", got)
	}
}

// referencedModel is a shell over two environments that reference different
// entries for the same credential, so moving the picker has something to move
// between.
func referencedModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	entry := func(ref string) config.Connection {
		return config.Connection{
			Set:     map[string]any{"host": "h"},
			Secrets: map[string]string{"password": ref},
		}
	}
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"vlt": entry("kv:staging-password")}},
		"prod":    {Plugins: map[string]config.Connection{"vlt": entry("kv:prod-password")}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(secretPlugin("password")); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 110, 44
	m.profiles = m.profileRows()
	return m
}

// The line is on the screen an operator actually looks at, not only in the
// field it was computed for.
func TestTheRunFormShowsTheEntryUnderTheBox(t *testing.T) {
	m := referencedModel(t)
	switchOn(t, &m, "staging")

	model, _ := m.startForm(vltCap(t, m), nil)
	nm, ok := model.(Model)
	if !ok {
		t.Fatalf("startForm returned %T", model)
	}
	nm.form.form = startedForm(nm.form)
	out := plain(nm.formView())

	if !strings.Contains(out, "kv:staging-password") {
		t.Errorf("the form never names the entry that fills the box:\n%s", out)
	}
}

// An environment whose credentials cannot be fetched is still the environment.
//
// bindCmd runs off the update loop, where no passphrase can be asked for, so a
// `kv:` reference against a store nothing has unlocked fails there and the
// capability never reaches the bind — which is the ordinary state of a
// profile that references one, not an exotic failure. Answering that with the
// base configuration left the base configuration's host in the box under a
// picker still reading "staging". The reference is exactly what this operator
// needs to see, and it was the case they could not see it in.
func TestAnEnvironmentWhoseCredentialsCannotBeFetchedStillFillsTheForm(t *testing.T) {
	m := referencedModel(t)
	switchOn(t, &m, "staging")

	model, _ := m.startForm(vltCap(t, m), nil)
	nm, ok := model.(Model)
	if !ok {
		t.Fatalf("startForm returned %T", model)
	}
	if got := nm.form.bindings["host"]; got == nil || *got != "h" {
		t.Errorf("host box = %v, want staging's own value — the bind failing is not the environment going away", got)
	}
	nm.form.form = startedForm(nm.form)
	if out := plain(nm.formView()); !strings.Contains(out, "kv:staging-password") {
		t.Errorf("the form never names the reference whose fetch is what failed:\n%s", out)
	}
}

// And it moves with the picker, like every other thing the environment
// decides. A note left behind on the environment somebody navigated away from
// is the same failure as a host box left behind: a screen that is believed and
// wrong.
func TestMovingThePickerMovesTheNote(t *testing.T) {
	m := referencedModel(t)
	switchOn(t, &m, "staging")

	model, _ := m.startForm(vltCap(t, m), nil)
	nm := model.(Model)
	*nm.form.bindings[profileInput] = "prod"

	moved, _, ok := nm.reseedOnPickerMove()
	if !ok {
		t.Fatal("moving the picker rebuilt nothing")
	}
	rebuilt := moved.(Model)
	rebuilt.form.form = startedForm(rebuilt.form)
	out := plain(rebuilt.formView())

	if !strings.Contains(out, "kv:prod-password") {
		t.Errorf("the form does not name prod's entry after the picker moved:\n%s", out)
	}
	if strings.Contains(out, "kv:staging-password") {
		t.Errorf("the form still names staging's entry after the picker moved:\n%s", out)
	}
}

// source() and formNote() are two wordings of one decision, and the decision
// is winner()'s. The pair is pinned together because they are read side by
// side by the same operator — the pane says where a credential comes from, the
// form says it again under the box — and the day they disagree is the day one
// of them is lying about which channel the run will use.
func TestThePaneAndTheFormNameTheSameChannel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		row      credentialRow
		inSource string
		inNote   string
	}{
		{
			name:     "a reference and nothing else",
			row:      credentialRow{env: "RTA_PROFILE_STAGING_PASSWORD", ref: config.SecretRef{Scheme: "kv", Ref: "db"}},
			inSource: "kv:db",
			inNote:   "kv:db",
		},
		{
			name:     "an exported variable and nothing else",
			row:      credentialRow{env: "RTA_PROFILE_STAGING_PASSWORD", exported: true},
			inSource: "$RTA_PROFILE_STAGING_PASSWORD",
			inNote:   "$RTA_PROFILE_STAGING_PASSWORD",
		},
		{
			name: "both, where the variable wins",
			row: credentialRow{env: "RTA_PROFILE_STAGING_PASSWORD", exported: true,
				ref: config.SecretRef{Scheme: "kv", Ref: "db"}},
			inSource: "$RTA_PROFILE_STAGING_PASSWORD",
			inNote:   "$RTA_PROFILE_STAGING_PASSWORD",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.source(); !strings.Contains(got, tc.inSource) {
				t.Errorf("pane says %q, want it to name %q", got, tc.inSource)
			}
			if got := tc.row.formNote("staging"); !strings.Contains(got, tc.inNote) {
				t.Errorf("form says %q, want it to name %q", got, tc.inNote)
			}
			if !tc.row.satisfied() {
				t.Error("the row reports nothing supplies it")
			}
		})
	}
}

// The unsupplied wordings are the pane's alone: the form says nothing at all,
// and the pane keeps the two different reasons it has always told apart.
func TestNothingSuppliedKeepsThePanesTwoAnswersAndTheFormsSilence(t *testing.T) {
	labeled := credentialRow{input: "password"}
	if got := labeled.source(); !strings.Contains(got, "labeled instance") {
		t.Errorf("a labeled instance's row says %q", got)
	}
	plain := credentialRow{input: "password", env: "RTA_PROFILE_STAGING_PASSWORD"}
	if got := plain.source(); got != "not set" {
		t.Errorf("an unset credential's row says %q, want \"not set\"", got)
	}
	for _, row := range []credentialRow{labeled, plain} {
		if got := row.formNote("staging"); got != "" {
			t.Errorf("the form gained %q for a credential nothing supplies", got)
		}
		if row.satisfied() {
			t.Error("the row reports something supplies it")
		}
	}
}

// A capability a profile may not fill gets nothing, whatever the environment
// maps: ProfileFillable is the gate, and a Path input is outside it because a
// profile chooses where a call goes and never what it reads or writes.
func TestAnInputAProfileMayNotFillIsNotAnnotated(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "y", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "out", Type: plugin.Path, Local: true, EnvFallback: true}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	got := environmentNotes(c, c.Inputs, nil, "staging",
		config.Connection{Secrets: map[string]string{"out": "kv:somewhere"}})
	if got[0].Help != "" {
		t.Errorf("a Path input gained %q", got[0].Help)
	}
}
