package tui

import (
	"context"
	"strings"
	"testing"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Adding a plugin to an environment, and configuring it in the same visit.

// twoPluginModel registers a second plugin so "the operator changed their
// mind about which one" is a thing the form can be asked to do. Only db
// declares config keys, so the presence of a `set.` box is itself the answer
// to which plugin the form is built on.
func twoPluginModel(t *testing.T) Model {
	t.Helper()
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{}},
	}})
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	if err := m.reg.Register(plugin.Plugin{Name: "plain", Summary: "nothing to configure",
		Capabilities: []plugin.Capability{
			{ID: "plain.ping", Summary: "ping", Safety: plugin.Read, Run: run},
		}}); err != nil {
		t.Fatal(err)
	}
	m.plugins = pluginRows(m.reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.profileOpen = "staging"
	return m
}

// kubectlPlugin is shaped like plugins/cnpg: it reaches its cluster through
// kubectl, so it has something to configure — a context — and nothing at all
// that a port-forward could fill. The two facts together are what the
// connection editor got wrong, and no fixture here had them until now.
func kubectlPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "clusters", Summary: "reads a cluster through kubectl",
		Capabilities: []plugin.Capability{
			{ID: "clusters.list", Summary: "list", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					{Name: "context", Type: plugin.String, Config: "context", Local: true,
						Help: "kubeconfig context to use"},
				}},
		},
	}
}

func configurablePlainModel(t *testing.T) Model {
	t.Helper()
	m := twoPluginModel(t)
	if err := m.reg.Register(kubectlPlugin()); err != nil {
		t.Fatal(err)
	}
	m.plugins = pluginRows(m.reg, config.Dashboard{}, nil)
	m.pluginSel = pluginIndex(t, m, "clusters")
	return m
}

func setBoxes(cf *capForm) []string {
	var out []string
	for _, f := range cf.fields {
		if name, ok := strings.CutPrefix(f.Name, profileSetPrefix); ok {
			out = append(out, name)
		}
	}
	return out
}

func pluginIndex(t *testing.T, m Model, name string) int {
	t.Helper()
	for i, row := range m.plugins {
		if row.plugin.Name == name {
			return i
		}
	}
	t.Fatalf("no %s in the pane", name)
	return 0
}

// **A new entry could be given a plugin but not configured, and that was never
// a decision.**
//
// The config boxes come from whichever plugin the entry names, and the name was
// read off the file's key — which a new entry does not have yet. So adding pg
// to an environment meant picking it, submitting, finding the entry you had
// just made, opening it again, and only then seeing `host` and `port`. Five
// steps for one intention.
//
// The plugin is known before the form is built: it is the one under the cursor
// in the pane the operator pressed `n` from.
func TestANewEntryIsConfigurableOnTheWayIn(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "db")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	if got := *nm.form.bindings[profilePluginField]; got != "db" {
		t.Fatalf("the new entry was seeded with %q, not the plugin under the cursor", got)
	}
	if boxes := setBoxes(nm.form); len(boxes) == 0 {
		t.Error("a new entry offers no configuration — the plugin it names declares three keys")
	}
}

// And when the operator names a different one, the boxes below follow. The
// same move the run form makes when its environment picker moves, for the same
// reason: what is below depends on the answer above.
func TestNamingADifferentPluginRebuildsWhatItConfigures(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "plain")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	if boxes := setBoxes(nm.form); len(boxes) != 0 {
		t.Fatalf("the fixture's first plugin already offers %v", boxes)
	}

	// The binding directly, so this measures the rebuild and not tab.
	*nm.form.bindings[profilePluginField] = "db"
	after, cmd, rebuilt := nm.reseedOnConnPluginChange()
	if !rebuilt {
		t.Fatal("naming an installed plugin did not rebuild the form")
	}
	nm = after.(Model)
	if cmd != nil {
		cmd()
	}
	if boxes := setBoxes(nm.form); len(boxes) == 0 {
		t.Error("the form still describes the plugin that is no longer named")
	}
	if got := *nm.form.bindings[profilePluginField]; got != "db" {
		t.Errorf("the rebuild lost the name that caused it: %q", got)
	}
}

// A half-typed name is not a plugin, and a form thrown away on every keystroke
// is worse than one built late. The rebuild waits for a name that resolves.
func TestAHalfTypedPluginNameRebuildsNothing(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "plain")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	for _, partial := range []string{"d", "db-", "nosuch"} {
		*nm.form.bindings[profilePluginField] = partial
		if _, _, rebuilt := nm.reseedOnConnPluginChange(); rebuilt {
			t.Errorf("%q rebuilt the form, and it names no installed plugin", partial)
		}
	}
}

// The coordinate survives a rebuild onto another plugin a forward can reach,
// and the old plugin's keys do not: those described something the entry no
// longer names, while the coordinate still describes somewhere the new plugin
// can be pointed.
func TestARebuildKeepsTheCoordinateAndDropsTheOtherPluginsKeys(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	if err := m.reg.Register(otherDBPlugin()); err != nil {
		t.Fatal(err)
	}
	m.plugins = pluginRows(m.reg, config.Dashboard{}, nil)
	m.pluginSel = pluginIndex(t, m, "db")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profileKubeField] = "homelab/db/svc/postgres:5432"
	*nm.form.bindings[profileSetPrefix+"host"] = "db.staging.internal"
	*nm.form.bindings[profilePluginField] = "db2"

	after, _, rebuilt := nm.reseedOnConnPluginChange()
	if !rebuilt {
		t.Fatal("no rebuild")
	}
	nm = after.(Model)
	if got := *nm.form.bindings[profileKubeField]; got != "homelab/db/svc/postgres:5432" {
		t.Errorf("the coordinate did not survive the rebuild: %q", got)
	}
	if _, still := nm.form.bindings[profileSetPrefix+"host"]; still {
		t.Error("a key belonging to the plugin that is no longer named is still on the form")
	}
}

// **A plugin that declares no input a forward can fill is offered no
// coordinate box at all**, and the heading says why where the box was.
//
// This is the bug that shipped. The box was offered to every plugin, it
// completed against the operator's real cluster, and it was the most prominent
// thing on the screen — so for a plugin that reaches its cluster through
// kubectl and takes no host or port, it read as *the* way to point the plugin
// somewhere. The profile saved, and every run of it was then refused:
// "declares no input a tunnel can fill, so the forward would be opened and
// ignored". The resolver had the rule all along; the one screen that could
// have prevented the state did not ask.
func TestNoCoordinateBoxForAPluginNoForwardCanReach(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "plain")
	m.width, m.height = 110, 44

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)

	for _, gone := range []string{profileKubeField, profileSSHField} {
		if _, offered := nm.form.bindings[gone]; offered {
			t.Errorf("%s is offered for a plugin no forward can reach", gone)
		}
	}
	// And db, which declares the pair, still gets both.
	m.pluginSel = pluginIndex(t, m, "db")
	next, _ = m.startConnForm("")
	nm = next.(Model)
	nm.form.form = startedForm(nm.form)
	for _, want := range []string{profileKubeField, profileSSHField} {
		if _, offered := nm.form.bindings[want]; !offered {
			t.Errorf("%s is missing for a plugin a forward can fill", want)
		}
	}
}

// The absence is explained rather than left to be noticed: every other plugin
// in the profile has that box, so a missing one reads as a missing feature
// until the heading names it as a fact about this plugin.
func TestTheMissingCoordinateSaysWhyItIsMissing(t *testing.T) {
	noHistory(t)
	m := configurablePlainModel(t)
	m.width, m.height = 110, 44

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	out := plain(nm.formView())

	if strings.Contains(out, "how to reach it") {
		t.Errorf("the coordinate heading is drawn over boxes that are not there:\n%s", out)
	}
	if !strings.Contains(out, "needs no forward") {
		t.Errorf("the form never says why there is no coordinate box:\n%s", out)
	}
}

// And the save refuses a coordinate that arrived some other way — typed before
// the picker finished resolving the name, or already in the file from an
// artifact since rebuilt without its endpoint roles. Nothing is written: unlike
// an endpoint key a forward shadows, there is nothing here to salvage.
func TestSavingRefusesACoordinateThePluginCannotUse(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "db")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	// The coordinate is typed while db is named, then the name is changed to a
	// plugin that cannot use it — without the intervening rebuild that would
	// have taken the box away.
	*nm.form.bindings[profileKubeField] = "homelab/db/svc/postgres:5432"
	*nm.form.bindings[profilePluginField] = "plain"

	after, _ := nm.saveConnForm()
	nm = after.(Model)
	if !strings.Contains(nm.flash, "no input a tunnel can fill") {
		t.Errorf("the save did not say why it refused: %q", nm.flash)
	}
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, written := cfg.Profiles["staging"].Plugins["plain"]; written {
		t.Error("a profile every run would refuse was written anyway")
	}
}

// The TLS toggle is offered exactly where the coordinate boxes are — it is a
// fact about whichever one is filled, so a plugin no forward can reach has
// nothing for it to describe either.
func TestTLSToggleFollowsTheCoordinateBoxes(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "plain")
	m.width, m.height = 110, 44

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	if _, offered := nm.form.bools[profileTLSField]; offered {
		t.Error("the TLS toggle is offered for a plugin no forward can reach")
	}

	m.pluginSel = pluginIndex(t, m, "db")
	next, _ = m.startConnForm("")
	nm = next.(Model)
	nm.form.form = startedForm(nm.form)
	if _, offered := nm.form.bools[profileTLSField]; !offered {
		t.Error("the TLS toggle is missing for a plugin a forward can fill")
	}
}

// Stated beside a coordinate, it is written as an ordinary peer of `kube:` —
// the TUI's half of what internal/profile.endpointValues then acts on.
func TestSavingWithTLSOnAndACoordinateWritesIt(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "db")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profileKubeField] = "homelab/db/svc/postgres:5432"
	*nm.form.bools[profileTLSField] = true

	after, _ := nm.saveConnForm()
	nm = after.(Model)
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Profiles["staging"].Plugins["db"].TunnelTLS {
		t.Errorf("tunnelTLS: true was not written, flash: %q", nm.flash)
	}
}

// A toggle left on while the coordinate above it is cleared is repaired at
// save rather than refused — the same reason the endpoint-key removal a few
// lines up is: a Bool has no empty to choose, and this screen does not redraw
// itself the moment a sibling box changes, so refusing would make the form
// unsaveable until the operator noticed a switch nothing drew their eye to.
func TestSavingRepairsATLSToggleLeftOnWithNoCoordinate(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db": {Kube: "homelab/db/svc/postgres:5432", TunnelTLS: true},
		}}},
	})
	m.profiles = m.profileRows()
	m.profileOpen = "staging"

	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profileKubeField] = ""

	after, _ := nm.saveConnForm()
	nm = after.(Model)
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["staging"].Plugins["db"].TunnelTLS {
		t.Error("tunnelTLS: true survived clearing the coordinate it describes")
	}
	if !strings.Contains(nm.flash, "tunnelTLS") {
		t.Errorf("the repair has no receipt: %q", nm.flash)
	}
}

// Every box titled with the word an operator actually typed — `kube`,
// `tls`, `host` — never with this form's own internal binding key
// (`profile-kube`, `set.host`) that key exists only to keep one plugin's
// field from colliding with another's in a form-wide map. A box titled
// "set.host" is an implementation detail on screen instead of the word the
// config file itself uses.
func TestFieldTitlesNeverLeakTheFormsOwnPrefixes(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "db")
	m.width, m.height = 110, 44

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	out := plain(nm.formView())

	for _, leaked := range []string{"profile-plugin", "profile-kube", "profile-ssh", "profile-tls", "set.host", "set.port"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the form shows its own internal field name %q instead of a title:\n%s", leaked, out)
		}
	}
	for _, want := range []string{"kube", "ssh", "tls", "host", "port"} {
		if !strings.Contains(out, want) {
			t.Errorf("the form is missing the plain title %q:\n%s", want, out)
		}
	}
}

// The two headings, which are what makes the column read as three questions
// rather than six boxes.
func TestTheConnectionEditorSaysWhereEachSectionStarts(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "db")
	m.width, m.height = 110, 44

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	out := plain(nm.formView())

	for _, want := range []string{"how to reach it", "what staging changes about it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the form has no %q heading:\n%s", want, out)
		}
	}
	// A heading is passed over, not tabbed into: huh's Note skips itself.
	first := nm.form.form.GetFocusedField()
	nm = pressTab(t, nm)
	nm = pressTab(t, nm) // off the plugin box, which lists then advances
	if landed := nm.form.form.GetFocusedField(); landed == first {
		t.Fatal("tab did not leave the first field")
	} else if landed != huh.Field(nm.form.inputs[profileKubeField]) {
		t.Error("tab landed on the heading rather than on the box under it")
	}
}

// **Adding a plugin that cannot work without a credential goes straight to
// the credential.**
//
// The entry is written either way; what changes is that the screen after it is
// the one the operator was always going to have to find. Three keystrokes and
// a search of the pane, removed.
func TestAddingAPluginThatNeedsACredentialAsksForItNext(t *testing.T) {
	noHistory(t)
	m := profileNeedingACredential(t, "staging")
	delete(m.profiles[0].p.Plugins, "vaulty")
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{}},
	}}); err != nil {
		t.Fatal(err)
	}
	m.profiles = m.profileRows()
	m.profileOpen = "staging"
	m.pluginSel = pluginIndex(t, m, "vaulty")

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	after, _ := nm.saveConnForm()
	nm = after.(Model)

	if nm.mode != modeForm || nm.form == nil || !nm.form.credentialEditing {
		t.Fatalf("after adding vaulty the screen is %v, not the credential editor", nm.mode)
	}
	if !strings.Contains(nm.flash, "needs a credential") {
		t.Errorf("the second form arrives unexplained: %q", nm.flash)
	}
}

// And editing an existing entry does not: the operator came to change a host.
func TestEditingAnEntryDoesNotDivertToTheCredential(t *testing.T) {
	noHistory(t)
	m := profileNeedingACredential(t, "staging")
	m.profileOpen = "staging"

	next, _ := m.startConnForm("vaulty")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	after, _ := nm.saveConnForm()
	nm = after.(Model)

	if nm.mode == modeForm && nm.form != nil && nm.form.credentialEditing {
		t.Error("editing an entry took the operator somewhere they did not ask to go")
	}
}
