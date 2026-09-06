package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Typing a label into its box recomposes the entry's key: `db` + `analytics`
// lands in the file as `db/analytics`, beside the default it does not touch.
func TestConnFormSavesALabeledInstance(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db": {Set: map[string]any{"host": "main.internal"}},
		}},
	}})
	m.profileOpen = "staging"

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profilePluginField] = "db"
	*nm.form.bindings[profileInstanceField] = "analytics"

	after, _ := nm.saveConnForm()
	nm = after.(Model)
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["staging"]
	if _, _, ok := p.ForInstance("db", "analytics"); !ok {
		t.Fatalf("db/analytics was not written, flash: %q — keys: %v", nm.flash, p.PluginKeys())
	}
	if _, conn, ok := p.ForInstance("db", ""); !ok || conn.Set["host"] != "main.internal" {
		t.Errorf("saving the labeled instance disturbed the default: %v", p.Plugins)
	}
}

// A malformed label is refused at save, before it lands in the file.
func TestConnFormRefusesABadInstanceLabel(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profilePluginField] = "db"
	*nm.form.bindings[profileInstanceField] = "Bad_Label"

	after, _ := nm.saveConnForm()
	nm = after.(Model)
	if !strings.Contains(nm.flash, "instance label") {
		t.Errorf("flash = %q, want the label refused by name", nm.flash)
	}
	cfg, _ := config.LoadFile()
	if len(cfg.Profiles["staging"].Plugins) != 1 {
		t.Errorf("the file gained an entry anyway: %v", cfg.Profiles["staging"].PluginKeys())
	}
}

// An existing labeled entry opens with the label in its own box and the base
// key in the plugin box — the shape the two are edited in.
func TestConnFormOpensALabeledEntrySplit(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db/analytics": {Set: map[string]any{"host": "analytics.internal"}},
		}},
	}})
	m.profileOpen = "staging"

	next, _ := m.startConnForm("db/analytics")
	nm := next.(Model)
	if got := *nm.form.bindings[profilePluginField]; got != "db" {
		t.Errorf("plugin box = %q, want the base key", got)
	}
	if got := *nm.form.bindings[profileInstanceField]; got != "analytics" {
		t.Errorf("instance box = %q", got)
	}
}

// The picker offers the refs a call would accept — never the bare name that
// several labeled entries would make Lookup refuse.
func TestThePickerOffersInstanceRefs(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db/main":      {Set: map[string]any{"host": "main.internal"}},
			"db/analytics": {Set: map[string]any{"host": "analytics.internal"}},
		}},
	}})
	c := dbPlugin().Capabilities[0]
	f := m.profilePicker(c, "")
	if f == nil {
		t.Fatal("no picker for a profile with two instances")
	}
	for _, want := range []string{"staging/analytics", "staging/main"} {
		if !slices.Contains(f.Options, want) {
			t.Errorf("options %v are missing %q", f.Options, want)
		}
	}
	if slices.Contains(f.Options, "staging") {
		t.Errorf("options %v offer the bare name Lookup would refuse", f.Options)
	}
}

// Picking an instance seeds the form from it. The picker offers
// `staging/analytics` exactly when a bare name would be refused, and the seed
// looked that ref up as a profile name and found nothing — so the boxes
// opened on the base configuration under a picker naming the instance, while
// the run went through Lookup and reached it.
func TestPickingAnInstanceSeedsTheFormFromIt(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db/main":      {Set: map[string]any{"host": "main.internal"}},
			"db/analytics": {Set: map[string]any{"host": "analytics.internal"}},
		}},
	}})
	c := dbPlugin().Capabilities[0]
	name, seeded, _ := m.profileSeed(c, "staging/analytics")
	if name != "staging/analytics" || seeded["host"] != "analytics.internal" {
		t.Fatalf("seed = %q %v, want the analytics instance's host", name, seeded)
	}
	// And through the form itself, which is what the picker rebuilds on a
	// move: the box shows the instance's host, marked as the environment's.
	cf := m.runForm(c, c.Inputs, map[string]any{}, map[string]any{profileInput: "staging/analytics"})
	if got := *cf.bindings["host"]; got != "analytics.internal" {
		t.Errorf("the host box opened on %q under a picker naming staging/analytics", got)
	}
	if !cf.derived["host"] {
		t.Error("the seeded host is not marked as the environment's, so it would outrank the layer it came from")
	}
}

// A labeled instance's credential has no environment channel: the pane says
// to use a `secrets:` reference, and the export-line copy offers nothing —
// a pasted export that fills the default instead is the confident wrong
// answer this pane exists to prevent.
func TestLabeledInstanceCredentialsHaveNoEnvChannel(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"sec/analytics": {},
		}},
	}})
	// dbPlugin declares no credential; the question here is about one.
	if err := m.reg.Register(plugin.Plugin{
		Name: "sec", Summary: "a plugin with a credential",
		Capabilities: []plugin.Capability{{
			ID: "sec.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	rows := m.profileRows()
	if len(rows) != 1 || len(rows[0].conns) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	creds := rows[0].conns[0].credentials
	if len(creds) == 0 {
		t.Fatal("the declared credential is missing from the pane")
	}
	for _, cr := range creds {
		if cr.env != "" {
			t.Errorf("credential %q shows env %q for a labeled instance", cr.input, cr.env)
		}
		if !strings.Contains(cr.source(), "secrets:") {
			t.Errorf("source %q does not steer at a secrets: reference", cr.source())
		}
	}
	m.profiles = rows
	m.profileSel = 0
	if m.selectedProfileHasUnsetCredential() {
		t.Error("the pane offers export lines it cannot write")
	}
}
