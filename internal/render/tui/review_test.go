package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/recent"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Defects an adversarial review of this work found, each pinned where it was.

// tlsPlugin has the shape s3 has: a host the environment fills, and a Bool
// beside it that decides whether the transport is protected.
func tlsPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	return plugin.Plugin{Name: "obj", Summary: "object store", Capabilities: []plugin.Capability{{
		ID: "obj.list", Summary: "list", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "endpoint", Type: plugin.String, Config: "endpoint", Default: "localhost:9000"},
			{Name: "tls", Type: plugin.Bool, Config: "tls", Help: "use HTTPS"},
		},
		Run: run,
	}}}
}

// A Bool the environment filled in follows the picker like everything else.
//
// values() returned from the bool branch before the display check, so a
// profile-seeded `tls` came back as a caller value and beat the profile layer.
// Switching the picker from a local endpoint to a production one then moved the
// endpoint and left the transport setting behind — plaintext against a
// production endpoint, which is worse than either half on its own.
func TestABoolFromTheEnvironmentFollowsThePicker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", dir)
	cfg := config.Config{Profiles: map[string]config.Profile{
		"local": {Plugins: map[string]config.Connection{
			"obj": conn(map[string]any{"endpoint": "localhost:9000", "tls": false})}},
		"prod": {Plugins: map[string]config.Connection{
			"obj": conn(map[string]any{"endpoint": "s3.example.com", "tls": true})}},
	}}
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(tlsPlugin()); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	switchOn(t, &m, "local")

	c := reg.Capabilities()[0]
	model, _ := m.startForm(c, nil)
	form := model.(Model).form
	if *form.bools["tls"] {
		t.Fatal("the form did not open on local's tls=false")
	}

	*form.bindings[profileInput] = "prod"
	values := form.values()
	if got, answered := values["tls"]; answered {
		t.Errorf("an untouched switch answered %v — it is a display, not an answer", got)
	}
	name, filled, _, verr := m.resolveProfile(c, values)
	if verr != nil {
		t.Fatal(verr)
	}
	got := plugin.Resolve(c, plugin.Inputs{
		Caller: withoutPicker(c, values), Profile: filled, ProfileName: name,
	})
	if got["endpoint"] != "s3.example.com" || got["tls"] != true {
		t.Errorf("the run would reach %v with tls=%v — the endpoint moved and the transport did not",
			got["endpoint"], got["tls"])
	}
}

// Clearing a box the environment filled in does not pin the declared default.
//
// An emptied box fell back to Field.Default and handed it over as a caller
// value, which beats the profile layer — so blanking a host and picking another
// environment ran against the *declared* default rather than the environment
// the picker named.
func TestClearingAnEnvironmentBoxDoesNotPinTheDeclaredDefault(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	form := openRunForm(t, m)

	*form.bindings["host"] = ""
	*form.bindings[profileInput] = "prod"
	values := form.values()
	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, values)
	if verr != nil {
		t.Fatal(verr)
	}
	got := plugin.Resolve(c, plugin.Inputs{
		Caller: withoutPicker(c, values), Profile: filled, ProfileName: name,
	})
	if got["host"] != "prod.internal" {
		t.Errorf("the run would reach %v, want the environment the picker names", got["host"])
	}
}

// A capability that declares `profile` as its own data keeps it.
//
// `profile` is reserved on a capability a profile can fill and nowhere else,
// because builtin/grant declares it as data: `grant revoke pg --profile
// staging` names the grant to take back. Stripping it unconditionally handed
// runRevoke an empty profile, which skips its own narrowing filter and revokes
// every grant on pg across every connection — the widest possible reading of
// the narrowest possible request, from a form that showed the narrow one.
func TestACapabilitysOwnProfileInputIsNotStripped(t *testing.T) {
	data := plugin.Capability{
		ID: "grant.revoke", Summary: "revoke", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String, Positional: true},
			{Name: "profile", Type: plugin.String, Help: "only the grant for this connection"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	if plugin.Profilable(data) {
		t.Fatal("the fixture is profilable, so it is not the case under test")
	}
	values := map[string]any{"target": "pg", "profile": "staging"}
	got := withoutPicker(data, values)
	if got["profile"] != "staging" {
		t.Errorf("the capability's own profile input was stripped: %v", got)
	}
}

// A plain field with nothing declared still offers what this operator used.
//
// completing() returned the input untouched whenever a field had no Suggest,
// which is exactly the set internal/recent exists for — a bucket, a database, a
// schema — so the values were recorded on every run and offered on none.
func TestAPlainFieldStillOffersWhatWasUsed(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "store.list", Summary: "list", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	recent.Record(plugin.SurfaceCLI, c, map[string]any{"bucket": "my-prod-bucket"})

	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	m.form.form = typeInto(m.form.form, "my-")
	m = pressTab(t, m)
	if got := *cf.bindings["bucket"]; got != "my-prod-bucket" {
		t.Errorf("after tab the box holds %q — the shortlist was never attached", got)
	}
}

// A credential never leaves in a completion request.
//
// The snapshot a sibling's Suggest is answered with had no filter, and huh
// re-evaluates every field's suggestions on every keystroke — so typing into a
// masked box shipped each prefix of the passphrase to the plugin subprocess.
func TestACompletionRequestCarriesNoCredential(t *testing.T) {
	noHistory(t)
	var saw []string
	c := plugin.Capability{
		ID: "obj.set", Summary: "set", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true},
			{Name: "content-type", Type: plugin.String, Suggest: func(_ context.Context, req plugin.Request) []string {
				saw = append(saw, req.String("secret-key"))
				return []string{"text/plain"}
			}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	cf.syncs["secret-key"].Set("hunter2")
	if got := cf.candidates(c.Inputs[1]); len(got) != 1 {
		t.Fatalf("candidates = %v", got)
	}
	for _, s := range saw {
		if strings.Contains(s, "hunter2") {
			t.Fatalf("the credential reached a Suggest: %q", s)
		}
	}
	if len(saw) == 0 {
		t.Fatal("the suggestion never ran, so nothing is under test")
	}
}

// A row action keeps the environment the row came from.
//
// `profile` is reserved on a capability a profile can fill, so it is never one
// of its declared inputs and `asked` filters it out — leaving a form whose row
// identity came from one connection and whose call was aimed at whatever
// happened to be switched on.
func TestARowActionKeepsTheEnvironmentTheRowCameFrom(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	switchOn(t, &m, "staging")
	list := dbPlugin().Capabilities[1] // db.list, has a schema input
	m.current = list
	m.lastValues = map[string]any{profileInput: "prod", "schema": "public"}

	if got := m.pickedProfile(list, map[string]any{profileInput: "prod"}); got != "prod" {
		t.Fatalf("the fixture does not carry a picker: %q", got)
	}
	// runAction copies it into base, which is what pickedProfile reads.
	base := map[string]any{}
	if plugin.Profilable(list) {
		if picked, ok := m.lastValues[profileInput]; ok {
			base[profileInput] = picked
		}
	}
	if got := m.pickedProfile(list, base); got != "prod" {
		t.Errorf("the action would open on %q, want the environment the row came from", got)
	}
}

// A comma list completes however it was typed, spaces or not.
func TestACommaListCompletesWithoutASpace(t *testing.T) {
	declared := []string{"italian", "recipe"}
	for _, tc := range []struct{ typed, want string }{
		{"recipe, ita", "recipe, italian"},
		{"recipe,ita", "recipe,italian"},
		{"recipe,   ita", "recipe,   italian"},
	} {
		got := extending(tc.typed, declared)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("extending(%q) = %v, want [%q]", tc.typed, got, tc.want)
		}
		if !strings.HasPrefix(got[0], tc.typed) {
			t.Errorf("%q does not extend %q, so bubbles will not match it", got[0], tc.typed)
		}
	}
}
