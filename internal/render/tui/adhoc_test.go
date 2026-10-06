package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A connection stated from the run form. The CLI's flags are the same
// connection (internal/app/adhoc.go); what is proven here is the form: that it
// is offered where a forward has something to fill and nowhere else, that what
// it states is what the run reaches and what the result says, that it is held
// to what a stored connection is held to, and that leaving, replacing and
// dropping it each leave the screen telling the truth.

const stagingPg = "staging/databases/svc/postgres:5432"

// pastThePicker moves the cursor off the environment picker, which the form of a
// plugin a forward can fill opens on whenever an environment is configured — and
// which a test that does not apply the command startForm returns (firstFocus)
// finds itself on whatever is configured.
func pastThePicker(t *testing.T, m Model) Model {
	t.Helper()
	if _, ok := m.form.bindings[profileInput]; !ok {
		return m
	}
	m.form.form = settleForm(m.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

// adHocPlugin is a database the way plugins/pg is one: an address a forward can
// fill, a user, and a credential a reference may fill. seen records what the
// handler was handed.
func adHocPlugin(seen *map[string]any) plugin.Plugin {
	inputs := []plugin.Field{
		{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true,
			Endpoint: plugin.EndpointHost},
		{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
			Endpoint: plugin.EndpointPort},
		{Name: "user", Type: plugin.String, Config: "user", Local: true},
		{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
	}
	run := func(_ context.Context, req plugin.Request) (view.View, error) {
		if seen != nil {
			*seen = map[string]any{
				"host": req.String("host"), "port": req.Int("port"), "password": req.String("password"),
				"profile": req.Profile(), "tunnel": req.Tunnel(),
			}
		}
		return view.Text{Body: "ok"}, nil
	}
	return plugin.Plugin{
		Name: "pgx", Summary: "a database with a credential",
		Capabilities: []plugin.Capability{
			{ID: "pgx.status", Summary: "status", Safety: plugin.Read, Inputs: inputs, Run: run},
			{ID: "pgx.list", Summary: "list", Safety: plugin.Read, Inputs: inputs, Run: run},
		},
	}
}

// twoCredentialPlugins declares its credentials out of order, and a second
// plugin beside it declares one of its own, so the order the form asks in and
// the plugin it asks about are each pinned.
func twoCredentialPlugins() []plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	endpoint := plugin.Field{Name: "addr", Type: plugin.String, Config: "addr", Local: true,
		Endpoint: plugin.EndpointAddress}
	return []plugin.Plugin{
		{Name: "obj", Summary: "an object store", Capabilities: []plugin.Capability{{
			ID: "obj.ls", Summary: "ls", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{endpoint,
				{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true},
				{Name: "access-key", Type: plugin.Secret, Local: true, EnvFallback: true}},
		}}},
		{Name: "cache", Summary: "a cache", Capabilities: []plugin.Capability{{
			ID: "cache.ping", Summary: "ping", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{endpoint,
				{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true}},
		}}},
	}
}

// configOnlyPlugin has a configurable input and no address a forward could fill:
// it can be pointed at a profile, and a coordinate would mean nothing to it.
func configOnlyPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "cfgonly", Summary: "configurable, not forwardable",
		Capabilities: []plugin.Capability{{
			ID: "cfgonly.go", Summary: "go", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{{Name: "region", Type: plugin.String, Config: "region", Local: true}},
		}},
	}
}

// adHocModel is a model over the given plugins with an empty configuration: no
// profiles, nothing switched on, which is the state this feature is for.
func adHocModel(t *testing.T, plugins ...plugin.Plugin) Model {
	t.Helper()
	return adHocModelWith(t, config.Config{}, plugins...)
}

func adHocModelWith(t *testing.T, cfg config.Config, plugins ...plugin.Plugin) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	return m
}

func capNamed(t *testing.T, m Model, id string) plugin.Capability {
	t.Helper()
	for _, c := range m.reg.Capabilities() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

// holding is m with a connection held for a plugin, as stating one would.
func holding(m Model, ns string, conn config.Connection) Model {
	m.adHoc = map[string]config.Connection{ns: conn}
	return m
}

// openFormOn opens a capability's run form and hands back the model it is on.
func openFormOn(t *testing.T, m Model, c plugin.Capability) Model {
	t.Helper()
	nm, _ := openFormCmd(t, m, c)
	return nm
}

// openFormCmd is openFormOn with the command the form asked the event loop for.
func openFormCmd(t *testing.T, m Model, c plugin.Capability) (Model, tea.Cmd) {
	t.Helper()
	model, cmd := m.startForm(c, nil)
	nm, ok := model.(Model)
	if !ok || nm.form == nil {
		t.Fatalf("startForm returned %T with no form", model)
	}
	nm.form.form = startedForm(nm.form)
	return nm, cmd
}

// pickAdHoc moves the picker to the entry that asks for a connection and lets
// the form react, as the update loop does after every key.
func pickAdHoc(t *testing.T, m Model) Model {
	t.Helper()
	*m.form.bindings[profileInput] = adHocPickLabel
	nm, _, rebuilt := m.reseedOnPickerMove()
	if !rebuilt {
		t.Fatal("moving the picker to the ad hoc entry did not open the form")
	}
	return nm.(Model)
}

func statePgxConnection(t *testing.T, m Model) Model {
	t.Helper()
	*m.form.bindings[adHocKubeField] = stagingPg
	*m.form.bindings[adHocSecretPrefix+"password"] = "kube:postgres-app/password"
	nm, _ := m.saveAdHocForm()
	return nm.(Model)
}

// keyThrough is one key through the model, with whatever the form asked the
// event loop to do next fed back in, as a session does.
func keyThrough(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	next, cmd := m.Update(key)
	return feedBack(t, next.(Model), cmd)
}

// feedBack feeds a command's messages back into the model, and theirs after them,
// to a depth no form here needs more than.
func feedBack(t *testing.T, nm Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; i < 6 && cmd != nil; i++ {
		var more []tea.Cmd
		for _, msg := range answers(cmd) {
			next, c := nm.Update(msg)
			nm = next.(Model)
			if c != nil {
				more = append(more, c)
			}
		}
		if len(more) == 0 {
			break
		}
		cmd = tea.Batch(more...)
	}
	return nm
}

func typeKeysThrough(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = keyThrough(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

// fetchAndLand is fetchFromCluster with the landing fed back as a session feeds
// it: the command the answer returns is what makes the widget offer it.
func fetchAndLand(t *testing.T, m Model) Model {
	t.Helper()
	saved := completeTimeout
	completeTimeout = 30 * time.Second
	t.Cleanup(func() { completeTimeout = saved })
	next, cmd := m.Update(tabKey)
	nm := next.(Model)
	if nm.flash != "completing…" || cmd == nil {
		t.Fatalf("tab did not start a fetch (flash %q)", nm.flash)
	}
	msg, ok := cmd().(completeMsg)
	if !ok {
		t.Fatal("the fetch did not produce a completeMsg")
	}
	next, landed := nm.Update(msg)
	return feedBack(t, next.(Model), landed)
}

// The picker is where a connection is chosen, and it ends in the entry that
// states one — wherever a forward has an input to fill, with or without a
// configured environment, since the person who needs it is the one with none.
func TestThePickerEndsInAnAdHocConnectionWhereAForwardCanFillTheCapability(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil), configOnlyPlugin())

	f := m.profilePicker(capNamed(t, m, "pgx.status"), "")
	if f == nil {
		t.Fatal("no picker for a capability a forward can fill, with nothing configured")
	}
	if got, want := strings.Join(f.Options, "|"), profileNoneLabel+"|"+adHocPickLabel; got != want {
		t.Errorf("options = %q, want %q", got, want)
	}
	if !strings.Contains(f.Help, "held until you quit") {
		t.Errorf("the picker does not say how long a stated connection lasts: %q", f.Help)
	}
	if got := m.profilePicker(capNamed(t, m, "cfgonly.go"), ""); got != nil {
		t.Errorf("a capability with no address to forward to was offered %v", got.Options)
	}
}

// Nothing to pick but the base configuration and the entry that asks means a
// form that opens where it always did: the picker is there, listed, and the
// cursor is past it. With an environment configured, the picker is the question
// and the cursor starts on it.
func TestAFormWithNothingToPickStartsPastThePicker(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm, cmd := openFormCmd(t, m, c)
	if _, ok := nm.form.bindings[profileInput]; !ok {
		t.Fatal("the picker is not in the form, so the entry cannot be found")
	}
	nm = feedBack(t, nm, cmd)
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs["host"]) {
		t.Error("the cursor did not start on the first box a person fills in")
	}

	withProfiles := adHocModelWith(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"pgx": conn(map[string]any{"host": "staging.internal"})}},
	}}, adHocPlugin(nil))
	nm, cmd = openFormCmd(t, withProfiles, capNamed(t, withProfiles, "pgx.status"))
	nm = feedBack(t, nm, cmd)
	if nm.form.form.GetFocusedField() == huh.Field(nm.form.inputs["host"]) {
		t.Error("an environment is configured and the cursor skipped the question of which one")
	}
}

// Choosing it asks, and leaving without stating anything is going back: the
// form it came from, on the environment it was on — whichever that was — with
// what was typed in it.
func TestLeavingTheAdHocFormGoesBackToTheEnvironmentItCameFrom(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"pgx": conn(map[string]any{"host": "staging.internal"})}},
	}}
	for name, tc := range map[string]struct {
		model func() Model
		pick  func(Model) string
	}{
		"the base configuration": {
			model: func() Model { return adHocModelWith(t, cfg, adHocPlugin(nil)) },
			pick:  func(Model) string { return profileNoneLabel },
		},
		"a configured environment": {
			model: func() Model { return adHocModelWith(t, cfg, adHocPlugin(nil)) },
			pick:  func(Model) string { return "staging" },
		},
		"a held connection": {
			model: func() Model {
				return holding(adHocModelWith(t, cfg, adHocPlugin(nil)), "pgx", config.Connection{Kube: stagingPg})
			},
			pick: func(m Model) string { return adHocLabel(m.adHoc["pgx"]) },
		},
	} {
		for _, byKey := range []bool{false, true} {
			m := tc.model()
			c := capNamed(t, m, "pgx.status")
			model, _ := m.startForm(c, map[string]any{profileInput: tc.pick(m)})
			nm := model.(Model)
			nm.form.form = startedForm(nm.form)
			*nm.form.bindings["user"] = "app"
			if got := *nm.form.bindings[profileInput]; got != tc.pick(m) {
				t.Fatalf("%s: the form did not open on it: %q", name, got)
			}

			nm = pickAdHoc(t, nm)
			if nm.form.adHocFrom == nil || nm.current.ID != "adhoc.connection" {
				t.Fatalf("%s: the form that asks was not opened (current %q)", name, nm.current.ID)
			}
			if byKey {
				nm = keyThrough(t, nm, keyEsc)
			} else {
				back, _ := nm.closeForm()
				nm = back.(Model)
			}
			if nm.form == nil || nm.form.adHocFrom != nil || nm.current.ID != c.ID {
				t.Fatalf("%s: esc did not return to the run form (current %q)", name, nm.current.ID)
			}
			if got := *nm.form.bindings[profileInput]; got != tc.pick(m) {
				t.Errorf("%s (key=%v): picker = %q, want %q", name, byKey, got, tc.pick(m))
			}
			if got := *nm.form.bindings["user"]; got != "app" {
				t.Errorf("%s: what was typed was lost: user = %q", name, got)
			}
		}
	}
}

// What the form states is what the run form is rebuilt on: the picker names it,
// the endpoint boxes show the coordinate as a display the forward answers, and
// the credential box says where its value comes from, by name.
func TestStatingTheConnectionRebuildsTheRunFormOnIt(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm := pickAdHoc(t, openFormOn(t, m, c))
	if _, ok := nm.form.bindings[adHocKubeField]; !ok {
		t.Fatal("the form does not ask for a coordinate")
	}
	if _, ok := nm.form.bindings[adHocSecretPrefix+"password"]; !ok {
		t.Fatal("the form does not ask for a reference to the credential the plugin declares")
	}
	nm = statePgxConnection(t, nm)

	if _, held := nm.adHoc["pgx"]; !held {
		t.Fatal("nothing was held for the plugin")
	}
	if nm.form == nil || nm.form.adHocFrom != nil {
		t.Fatal("the run form was not rebuilt")
	}
	wantLabel := "ad hoc · through " + stagingPg
	if got := *nm.form.bindings[profileInput]; got != wantLabel {
		t.Errorf("picker = %q, want %q", got, wantLabel)
	}
	if got := *nm.form.bindings["host"]; !strings.Contains(got, "staging/databases/svc/postgres") {
		t.Errorf("host box = %q, want the coordinate shown as the forward's display", got)
	}
	if !nm.form.derived["host"] {
		t.Error("the host box is an answer, not a display — it would pin the call over the forward")
	}
	var help string
	for _, f := range nm.form.fields {
		if f.Name == "password" {
			help = f.Help
		}
	}
	if !strings.Contains(help, "ad hoc fills it from kube:postgres-app/password") {
		t.Errorf("the credential box does not say where it comes from: %q", help)
	}
	if strings.Contains(help, "RTA_PROFILE") {
		t.Errorf("the credential box names a variable a profile owns: %q", help)
	}
}

// A run through it reaches the forward, and tells the handler the connection by
// its name — which no profile can have — and the forward it opened. The result
// says the same; and a box typed over the coordinate is a call straight to what
// was typed, which the result says too, rather than going on saying "through".
func TestARunThroughTheHeldConnectionReachesTheForwardAndSaysSo(t *testing.T) {
	var seen map[string]any
	port := fakeForward(t)
	m := adHocModel(t, adHocPlugin(&seen))
	c := capNamed(t, m, "pgx.status")
	nm := pickAdHoc(t, openFormOn(t, m, c))
	*nm.form.bindings[adHocKubeField] = stagingPg
	next, _ := nm.saveAdHocForm()
	nm = next.(Model)

	run := func(values map[string]any) resultMsg {
		t.Helper()
		name, filled, conn, verr := nm.resolveProfile(c, values)
		if verr != nil {
			t.Fatalf("resolve: %s", verr.Message)
		}
		if name != profile.AdHocName {
			t.Fatalf("resolved to %q, want the ad hoc name", name)
		}
		rm := runCmd(context.Background(), 1, c, withoutPicker(c, values), false, statedConfig{}, name, filled, conn,
			false)().(resultMsg)
		if rm.err != nil {
			t.Fatalf("run: %s", rm.err.Message)
		}
		return rm
	}
	show := func(rm resultMsg, values map[string]any) string {
		shown := nm
		shown.current, shown.mode, shown.result, shown.lastValues = c, modeResult, rm, values
		return plain(shown.resultView())
	}

	values := nm.form.values()
	rm := run(values)
	if got, want := fmt.Sprint(seen["host"], ":", seen["port"]), fmt.Sprintf("127.0.0.1:%d", port); got != want {
		t.Errorf("the run reached %s, want the forward %s", got, want)
	}
	if seen["profile"] != profile.AdHocName || fmt.Sprint(seen["tunnel"]) != "kube" {
		t.Errorf("the handler was told %v/%v, want ad hoc/kube", seen["profile"], seen["tunnel"])
	}
	if out := show(rm, values); !strings.Contains(out, "ad hoc · through "+stagingPg) {
		t.Errorf("the result does not say where it went:\n%s", out)
	}

	typed := map[string]any{"host": "db.direct.internal", profileInput: values[profileInput]}
	rm = run(typed)
	if seen["host"] != "db.direct.internal" || fmt.Sprint(seen["tunnel"]) != "" {
		t.Fatalf("a host typed over the coordinate reached %v through %q", seen["host"], seen["tunnel"])
	}
	out := show(rm, typed)
	if strings.Contains(out, "through") || !strings.Contains(out, "ad hoc · direct") {
		t.Errorf("the result says a call that went straight to a typed host went through the forward:\n%s", out)
	}

	typed = map[string]any{"port": 5433, profileInput: values[profileInput]}
	rm = run(typed)
	if out := show(rm, typed); strings.Contains(out, "through") {
		t.Errorf("one edited port box ended the forward and the result still says through:\n%s", out)
	}
}

// A variable exported for a profile called ad-hoc spells the name this one is
// called by. The connection reads none, so what it says is what it reaches.
func TestAnAdHocRunReadsNoVariableAProfileOwns(t *testing.T) {
	t.Setenv(plugin.ProfileEnvVar("ad-hoc", "password"), "the-other-profiles-password")
	var seen map[string]any
	fakeForward(t)
	m := adHocModel(t, adHocPlugin(&seen))
	c := capNamed(t, m, "pgx.status")
	nm := pickAdHoc(t, openFormOn(t, m, c))
	*nm.form.bindings[adHocKubeField] = stagingPg
	next, _ := nm.saveAdHocForm()
	nm = next.(Model)

	values := nm.form.values()
	name, filled, conn, verr := nm.resolveProfile(c, values)
	if verr != nil {
		t.Fatalf("resolve: %s", verr.Message)
	}
	if _, leaked := filled["password"]; leaked {
		t.Fatalf("the run was filled from a variable a profile owns: %v", filled)
	}
	if rm := runCmd(context.Background(), 1, c, withoutPicker(c, values), false, statedConfig{}, name, filled,
		conn, false)().(resultMsg); rm.err != nil {
		t.Fatalf("run: %s", rm.err.Message)
	}
	if seen["password"] != "" {
		t.Errorf("the handler was handed %q", seen["password"])
	}
	for _, f := range nm.form.fields {
		if f.Name == "password" && strings.Contains(f.Help, "RTA_PROFILE") {
			t.Errorf("the box teaches an export that would not be read: %q", f.Help)
		}
	}
}

// What the CLI refuses the form refuses, naming it in the form's own terms, and
// keeps the boxes: the person has a coordinate to correct and what they typed is
// in front of them.
func TestTheFormRefusesWhatAStoredConnectionWouldAndKeepsWhatWasTyped(t *testing.T) {
	for name, tc := range map[string]struct {
		kube, ref string
		want      string
	}{
		"nothing stated":                  {"", "", "nothing stated"},
		"a cluster reference, no cluster": {"", "kube:pg-creds/password", "fill the coordinate, or use kv:<entry>"},
	} {
		m := adHocModel(t, adHocPlugin(nil))
		c := capNamed(t, m, "pgx.status")
		nm := pickAdHoc(t, openFormOn(t, m, c))
		*nm.form.bindings[adHocKubeField] = tc.kube
		*nm.form.bindings[adHocSecretPrefix+"password"] = tc.ref

		next, _ := nm.saveAdHocForm()
		got := next.(Model)
		if len(got.adHoc) != 0 {
			t.Errorf("%s: a refused connection was held", name)
		}
		if got.form == nil || got.form.adHocFrom == nil {
			t.Errorf("%s: the form was not left in front of the person", name)
			continue
		}
		if !strings.Contains(got.flash, tc.want) {
			t.Errorf("%s: flash = %q, want it to say %q", name, got.flash, tc.want)
		}
		if v := *got.form.bindings[adHocSecretPrefix+"password"]; v != tc.ref {
			t.Errorf("%s: the reference was changed: %q", name, v)
		}
	}
}

// A refusal reopens on exactly what the boxes held. The held connection is what
// a first open edits; a box somebody emptied is not asking for it back, and
// pressing enter on the reopened form must not re-hold what they cleared.
func TestARefusalReopensOnExactlyWhatTheBoxesHeld(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm := statePgxConnection(t, pickAdHoc(t, openFormOn(t, m, c)))
	nm = pickAdHoc(t, nm)
	if got := *nm.form.bindings[adHocKubeField]; got != stagingPg {
		t.Fatalf("editing the held connection did not open on it: %q", got)
	}

	*nm.form.bindings[adHocKubeField] = ""
	next, _ := nm.saveAdHocForm()
	got := next.(Model)
	if got.form == nil || got.form.adHocFrom == nil {
		t.Fatal("a kube: reference with no coordinate was not refused")
	}
	if v := *got.form.bindings[adHocKubeField]; v != "" {
		t.Errorf("the emptied coordinate came back as %q", v)
	}
	if v := *got.form.bindings[adHocSecretPrefix+"password"]; v != "kube:postgres-app/password" {
		t.Errorf("the reference was lost: %q", v)
	}
}

// Emptying every box drops the connection: esc leaves it held, and a held
// connection is offered by everything it fits until the TUI quits, so this is
// the one way back. Nothing held and nothing stated is still a refusal.
func TestEmptyingEveryBoxDropsTheHeldConnection(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm := statePgxConnection(t, pickAdHoc(t, openFormOn(t, m, c)))
	label := *nm.form.bindings[profileInput]
	nm.current, nm.result, nm.lastValues = c, resultMsg{cap: c}, map[string]any{profileInput: label}

	nm = pickAdHoc(t, nm)
	*nm.form.bindings[adHocKubeField] = ""
	*nm.form.bindings[adHocSecretPrefix+"password"] = ""
	next, _ := nm.saveAdHocForm()
	nm = next.(Model)

	if len(nm.adHoc) != 0 {
		t.Errorf("the connection is still held: %v", nm.adHoc)
	}
	if nm.form == nil || nm.form.adHocFrom != nil {
		t.Fatal("the run form is not back")
	}
	if got := *nm.form.bindings[profileInput]; got != profileNoneLabel {
		t.Errorf("picker = %q, want the base configuration", got)
	}
	if !strings.Contains(nm.flash, "dropped") {
		t.Errorf("flash = %q, want it to say the connection was dropped", nm.flash)
	}
	if got := nm.lastValues[profileInput]; got != profileNoneLabel {
		t.Errorf("a re-run would still name the dropped connection: %v", got)
	}
	if opts := nm.adHocOptions(c); len(opts) != 1 || opts[0] != adHocPickLabel {
		t.Errorf("the dropped connection is still offered: %v", opts)
	}
}

// Replacing the connection moves what named the old one — the page on screen,
// the trail a row action came from, the last run's inputs — to the new one, so
// pressing esc or r afterwards reloads through what was just stated and not
// into an error page about a label that names nothing.
func TestReplacingTheHeldConnectionKeepsThePagesThatNamedTheOldOne(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm := statePgxConnection(t, pickAdHoc(t, openFormOn(t, m, c)))
	old := adHocLabel(nm.adHoc["pgx"])
	nm.result = resultMsg{cap: c}
	nm.lastValues = map[string]any{profileInput: old, "user": "app"}
	nm.trail = []runRef{{cap: c, values: map[string]any{profileInput: old}}}

	nm = pickAdHoc(t, nm)
	*nm.form.bindings[adHocKubeField] = "prod/databases/svc/postgres:5432"
	next, _ := nm.saveAdHocForm()
	nm = next.(Model)

	want := "ad hoc · through prod/databases/svc/postgres:5432"
	if nm.lastValues[profileInput] != want || nm.lastValues["user"] != "app" {
		t.Errorf("the last run's inputs = %v, want them on the new connection", nm.lastValues)
	}
	if nm.trail[0].values[profileInput] != want {
		t.Errorf("the trail still names the old connection: %v", nm.trail[0].values)
	}
	if name, conn, _, verr := nm.pickedConn(c, nm.lastValues); verr != nil || name != profile.AdHocName ||
		conn.Kube != "prod/databases/svc/postgres:5432" {
		t.Errorf("a re-run does not resolve to the new connection: %q %+v %v", name, conn, verr)
	}
}

// The value in place of a reference never reaches anything: refused at the box
// as the key is pressed, and by the whole connection if it gets that far, and
// never repeated.
func TestACredentialTypedInPlaceOfAReferenceIsRefusedAndNeverRepeated(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")

	nm := pickAdHoc(t, openFormOn(t, m, c))
	*nm.form.bindings[adHocKubeField] = stagingPg
	*nm.form.bindings[adHocSecretPrefix+"password"] = "hunter2"
	next, _ := nm.saveAdHocForm()
	got := next.(Model)
	if len(got.adHoc) != 0 || strings.Contains(got.flash, "hunter2") || !strings.Contains(got.flash, "names no source") {
		t.Errorf("the connection was held or the refusal repeated the value: %v %q", got.adHoc, got.flash)
	}

	nm = pickAdHoc(t, openFormOn(t, m, c))
	nm = typeKeysThrough(t, nm, stagingPg)
	nm = keyThrough(t, nm, keyEnter)
	nm = typeKeysThrough(t, nm, "hunter2")
	nm = keyThrough(t, nm, keyEnter)
	if len(nm.adHoc) != 0 {
		t.Fatalf("a plain value was held: %+v", nm.adHoc)
	}
	if nm.form == nil || nm.form.adHocFrom == nil {
		t.Fatal("the form did not stay in front of the person")
	}
	out := plain(nm.footerFor(modeForm))
	if !strings.Contains(out, "names its source") || strings.Contains(out, "hunter2") {
		t.Errorf("the footer does not say what is wrong, or repeats the value:\n%s", out)
	}
}

// A coordinate that does not parse stops enter at its own box.
func TestACoordinateThatDoesNotParseStopsAtItsBox(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	nm := pickAdHoc(t, openFormOn(t, m, capNamed(t, m, "pgx.status")))
	nm = typeKeysThrough(t, nm, "postgres")
	nm = keyThrough(t, nm, keyEnter)
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[adHocKubeField]) {
		t.Error("enter carried the person past a coordinate that cannot be forwarded to")
	}
	if out := plain(nm.footerFor(modeForm)); !strings.Contains(out, "✗") {
		t.Errorf("the footer says nothing about the coordinate:\n%s", out)
	}
}

// A held connection is offered to what it was stated for: the plugin, and the
// capabilities of it that can take its credential. A Service speaks one plugin's
// protocol, so a Postgres connection is not an environment for a Redis form
// that happens to declare a password too.
func TestAHeldConnectionIsOfferedOnlyToThePluginItWasStatedFor(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil), dbPlugin())
	m = holding(m, "pgx", config.Connection{
		Kube:    stagingPg,
		Secrets: map[string]string{"password": "kube:postgres-app/password"},
	})

	for _, id := range []string{"pgx.status", "pgx.list"} {
		opts := m.adHocOptions(capNamed(t, m, id))
		if len(opts) != 2 || opts[0] != adHocLabel(m.adHoc["pgx"]) || opts[1] != adHocPickLabel {
			t.Errorf("%s: options = %v, want the held connection then the entry that states one", id, opts)
		}
	}
	// db declares no password, and is another plugin: neither would take it.
	if opts := m.adHocOptions(capNamed(t, m, "db.status")); len(opts) != 1 || opts[0] != adHocPickLabel {
		t.Errorf("db.status was offered %v, want only the entry that states one", opts)
	}

	// Another plugin with a credential of the same name is still another plugin.
	other := adHocModel(t, twoCredentialPlugins()...)
	other = holding(other, "obj", config.Connection{Kube: stagingPg})
	if opts := other.adHocOptions(capNamed(t, other, "cache.ping")); len(opts) != 1 || opts[0] != adHocPickLabel {
		t.Errorf("a connection stated for obj was offered to cache: %v", opts)
	}
	if _, _, _, verr := other.pickedConn(capNamed(t, other, "cache.ping"),
		map[string]any{profileInput: adHocLabel(other.adHoc["obj"])}); verr == nil {
		t.Error("a form for cache ran through the connection stated for obj")
	}
}

// The credentials a form asks about are the plugin's own, sorted — the order the
// boxes are in.
func TestTheFormAsksAboutThePluginsOwnCredentialsInOrder(t *testing.T) {
	m := adHocModel(t, twoCredentialPlugins()...)
	if got := strings.Join(m.credentialInputs("obj"), ","); got != "access-key,secret-key" {
		t.Errorf("obj credentials = %q, want both of its own, sorted", got)
	}
	if got := strings.Join(m.credentialInputs("cache"), ","); got != "token" {
		t.Errorf("cache credentials = %q, want only its own", got)
	}
	nm := pickAdHoc(t, openFormOn(t, m, capNamed(t, m, "obj.ls")))
	var order []string
	for _, f := range nm.form.fields {
		order = append(order, fieldTitle(f.Name))
	}
	if got := strings.Join(order, ","); got != "kube,access-key,secret-key" {
		t.Errorf("boxes = %q, want the coordinate then each credential by its own name", got)
	}
}

// The picked connection is looked at by what it says, and never answers for an
// environment that is not it: with a profile and a held connection side by
// side, choosing the profile seeds from the profile.
func TestAHeldConnectionDoesNotAnswerForAnEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	c := capNamed(t, m, "db.status")
	m = holding(m, "db", config.Connection{Kube: stagingPg})
	if _, ok := m.heldFor(c); !ok {
		t.Fatal("the held connection does not fit, so this proves nothing")
	}

	name, seeded, _ := m.profileSeed(c, "staging")
	if name != "staging" || seeded["host"] != "staging.internal" {
		t.Errorf("choosing staging seeded %q %v, want staging's own", name, seeded)
	}
	if _, _, env := m.formSeed(c, nil, "staging"); env.name != "staging" {
		t.Errorf("the form is seeded from %q, want staging", env.name)
	}
	if name, _, _ := m.profileSeed(c, adHocLabel(m.adHoc["db"])); name != profile.AdHocName {
		t.Errorf("choosing the held connection seeded from %q", name)
	}
}

// A run form that names a connection no longer held is refused. Running it on
// the base configuration while the picker says otherwise is the failure the
// picker exists to prevent.
func TestAPickNamingAConnectionNoLongerHeldIsRefusedNotRunElsewhere(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	stale := adHocLabel(config.Connection{Kube: stagingPg})

	_, _, _, verr := m.pickedConn(c, map[string]any{profileInput: stale})
	if verr == nil || verr.Code != "core.adhoc.gone" {
		t.Fatalf("a connection nothing holds resolved: %v", verr)
	}

	m = holding(m, "pgx", config.Connection{Kube: "prod/databases/svc/postgres:5432"})
	if _, _, _, verr := m.pickedConn(c, map[string]any{profileInput: stale}); verr == nil {
		t.Error("a pick naming the replaced connection resolved to the new one")
	}
	held := m.adHoc["pgx"]
	if name, conn, _, verr := m.pickedConn(c, map[string]any{profileInput: adHocLabel(held)}); verr != nil ||
		name != profile.AdHocName || conn.Kube != held.Kube {
		t.Errorf("the held connection did not resolve: %q %+v %v", name, conn, verr)
	}
	// A credential this capability's plugin cannot take is refused at the run,
	// whatever label the form shows.
	m = holding(m, "pgx", config.Connection{Kube: stagingPg, Secrets: map[string]string{"nothing": "kv:entry"}})
	if _, _, _, verr := m.pickedConn(c, map[string]any{profileInput: adHocLabel(m.adHoc["pgx"])}); verr == nil ||
		!strings.HasPrefix(verr.Code, "core.adhoc.") {
		t.Errorf("a credential onto no input ran: %v", verr)
	}
}

// A cursor sent to the entry with End and a fast submit — which accepts every
// field at the cursor's value without the picker ever having moved — asks for
// the connection, and does not run a form whose environment is the entry.
func TestAFastSubmitWithTheCursorOnTheEntryAsksInsteadOfRunning(t *testing.T) {
	for _, submit := range []tea.KeyPressMsg{
		{Code: 's', Mod: tea.ModCtrl}, {Code: tea.KeyEnter, Mod: tea.ModAlt},
	} {
		m := adHocModel(t, adHocPlugin(nil))
		c := capNamed(t, m, "pgx.status")
		nm := openFormOn(t, m, c)
		nm.mode = modeForm
		nm = keyThrough(t, nm, tea.KeyPressMsg{Code: tea.KeyEnd})
		nm = keyThrough(t, nm, submit)

		if nm.form == nil || nm.form.adHocFrom == nil {
			t.Errorf("%v: the form did not ask (mode %v, result err %v)", submit, nm.mode, nm.result.err)
		}
		if nm.mode == modeResult || nm.result.err != nil {
			t.Errorf("%v: it ran, with %v", submit, nm.result.err)
		}
	}
}

// A form reopened with the cursor on the entry — what `e` did after the failed
// run above — starts on the base configuration, so moving off and back is the
// way to ask, and enter on the picker is not a dead key.
func TestAFormNeverOpensOnTheEntryThatAsks(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	model, _ := m.startForm(c, map[string]any{profileInput: adHocPickLabel})
	nm := model.(Model)
	if got := *nm.form.bindings[profileInput]; got != profileNoneLabel {
		t.Errorf("the form opened on %q", got)
	}
	if nm.form.builtOn != profileNoneLabel {
		t.Errorf("the form was built on %q, which no move can be measured from", nm.form.builtOn)
	}
}

// Tab on the coordinate walks the cluster, and on a `kube:` reference completes
// the Secrets of the namespace that coordinate names — with the scheme kept in
// the box, so what is offered extends what is typed, and the second tab takes
// what the first one brought.
func TestTabCompletesACredentialReferenceFromTheCoordinatesNamespace(t *testing.T) {
	log := clusterFake(t)
	m := adHocModel(t, adHocPlugin(nil))
	nm := pickAdHoc(t, openFormOn(t, m, capNamed(t, m, "pgx.status")))
	nm.form.form = startedForm(nm.form)
	// Typed, not assigned: a box that is left writes what it holds back to its
	// binding, and the widget holds what was typed into it.
	nm.form.form = typeInto(nm.form.form, "homelab/databases/svc/postgres:5432")

	if field, coord, ok := nm.completionTarget(); !ok || field != adHocKubeField || coord != "" {
		t.Fatalf("the coordinate box is not completed from the cluster: %q %q %v", field, coord, ok)
	}

	nm.form.form = settleForm(nm.form.form, keyEnter) // on to the reference
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[adHocSecretPrefix+"password"]) {
		t.Fatal("enter did not move to the credential's box")
	}
	if _, _, ok := nm.completionTarget(); ok {
		t.Error("an empty reference box completed from the cluster: it has not said which scheme yet")
	}
	nm.form.form = typeInto(nm.form.form, "kube:")
	nm = fetchAndLand(t, nm)

	if !strings.Contains(askedFake(t, log), "--context homelab --namespace databases get secrets -o name") {
		t.Errorf("the Secrets were not listed in the coordinate's namespace:\n%s", askedFake(t, log))
	}
	got := nm.form.suggested[adHocSecretPrefix+"password"]
	if len(got) != 2 || got[0] != "kube:api-token/" || got[1] != "kube:pg-creds/" {
		t.Errorf("offers = %v, want the Secrets behind the scheme the box already holds", got)
	}

	// The landing is fed back as a session feeds it, and the next tab accepts.
	nm = keyThrough(t, nm, tabKey)
	if got := *nm.form.bindings[adHocSecretPrefix+"password"]; got != "kube:api-token/" {
		t.Errorf("tab after a listing landed left the box at %q, want the first Secret", got)
	}

	// A local entry is the ordinary local tab, not a cluster read.
	*nm.form.bindings[adHocSecretPrefix+"password"] = "kv:"
	if _, _, ok := nm.completionTarget(); ok {
		t.Error("a kv: reference was completed from the cluster")
	}
	// And a kube: reference with no coordinate has no namespace to read.
	*nm.form.bindings[adHocSecretPrefix+"password"] = "kube:"
	*nm.form.bindings[adHocKubeField] = ""
	if _, _, ok := nm.completionTarget(); ok {
		t.Error("a kube: reference completed with no coordinate to read the namespace from")
	}
}

// What the box offers on its own is only what extends what is in it, so a
// starter the box already holds cannot lead the list and make tab a no-op.
func TestTheReferenceBoxOffersOnlyWhatExtendsIt(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	nm := pickAdHoc(t, openFormOn(t, m, capNamed(t, m, "pgx.status")))
	var suggest func(context.Context, plugin.Request) []string
	for _, f := range nm.form.fields {
		if f.Name == adHocSecretPrefix+"password" {
			suggest = f.Suggest
		}
	}
	if suggest == nil {
		t.Fatal("the credential box offers nothing")
	}
	ask := func(typed string) []string {
		return suggest(context.Background(),
			plugin.NewRequest(map[string]any{adHocSecretPrefix + "password": typed}, false, false))
	}
	if got := ask(""); len(got) == 0 || got[0] != "kube:" {
		t.Errorf("an empty box offers %v, want the kube: scheme among them", got)
	}
	if got := ask("kube:"); len(got) != 0 {
		t.Errorf("a box holding kube: offers %v, which would lead the list and kill tab", got)
	}
	if got := ask("kub"); len(got) != 1 || got[0] != "kube:" {
		t.Errorf("a box holding kub offers %v", got)
	}
}

// The box titles are the plugin's own words, not the form's field names.
func TestTheAdHocBoxesAreTitledByWhatTheyAskFor(t *testing.T) {
	for name, want := range map[string]string{
		adHocKubeField:                   "kube",
		adHocSecretPrefix + "password":   "password",
		adHocSecretPrefix + "access-key": "access-key",
	} {
		if got := fieldTitle(name); got != want {
			t.Errorf("fieldTitle(%q) = %q, want %q", name, got, want)
		}
	}
}

// The result's head keeps the marker where the title and the time would push it
// out: "ad hoc" survives any width the panel can draw, and the time and the
// coordinate come back as there is room for them.
func TestTheResultHeadKeepsTheMarkerWhereTheCoordinateDoesNotFit(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	long := "gke_acme-prod_europe-west1_main/databases/svc/postgres-rw:5432"
	m = holding(m, "pgx", config.Connection{Kube: long})
	m.current, m.mode = c, modeResult
	m.result = resultMsg{cap: c, elapsed: 12 * time.Millisecond, via: plugin.TunnelKube}
	m.lastValues = map[string]any{profileInput: adHocLabel(m.adHoc["pgx"])}

	for _, width := range []int{60, 80, 100, 140} {
		m.width = width
		first := strings.SplitN(plain(m.resultView()), "\n", 2)[0]
		if !strings.Contains(first, "ad hoc") {
			t.Errorf("width %d: the marker is gone from the head:\n%s", width, first)
		}
		if !strings.Contains(first, "pgx.status") {
			t.Errorf("width %d: the marker pushed the capability's id out:\n%s", width, first)
		}
	}
	m.width = 140
	if first := strings.SplitN(plain(m.resultView()), "\n", 2)[0]; !strings.Contains(first, "through "+long+" · 12ms") {
		t.Errorf("a wide head does not say where the call went beside the time:\n%s", first)
	}
	m.result.elapsed = 0
	first := strings.SplitN(plain(m.resultView()), "\n", 2)[0]
	if trimmed := strings.TrimRight(first, "─╮ "); strings.HasSuffix(trimmed, "·") {
		t.Errorf("the head ends in a separator with no time after it:\n%s", first)
	}
}

// Nothing is read from or written to the configuration by any of it.
func TestStatingAConnectionWritesNothing(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	path := os.Getenv("RTA_CONFIG")
	listing := func() string {
		entries, _ := os.ReadDir(filepath.Dir(path))
		var out []string
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(filepath.Dir(path), e.Name()))
			out = append(out, e.Name()+"\n"+string(b))
		}
		return strings.Join(out, "\n--\n")
	}
	before := listing()
	c := capNamed(t, m, "pgx.status")
	nm := statePgxConnection(t, pickAdHoc(t, openFormOn(t, m, c)))
	if _, held := nm.adHoc["pgx"]; !held {
		t.Fatal("nothing was held")
	}
	if after := listing(); before != after {
		t.Errorf("stating a connection changed the config directory:\n%s\nbecame\n%s", before, after)
	}
}

// The whole thing as a person does it: down to the entry, the coordinate and a
// reference typed into their boxes, enter to the end — and the form they came
// from is back, on the connection they stated, ready to run.
func TestStatingAConnectionWithTheKeysAPersonPresses(t *testing.T) {
	fakeForward(t)
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	nm := openFormOn(t, m, c)
	if nm.mode != modeForm {
		t.Fatalf("mode = %v, want the form", nm.mode)
	}

	nm = keyThrough(t, nm, keyDown)
	if nm.form == nil || nm.form.adHocFrom == nil {
		t.Fatal("down onto the entry did not ask for the connection")
	}

	nm = typeKeysThrough(t, nm, stagingPg)
	nm = keyThrough(t, nm, keyEnter)
	nm = typeKeysThrough(t, nm, "kube:postgres-app/password")
	nm = keyThrough(t, nm, keyEnter)

	held, ok := nm.adHoc["pgx"]
	if !ok {
		t.Fatalf("nothing was held after the last enter (flash %q)", nm.flash)
	}
	if held.Kube != stagingPg || held.Secrets["password"] != "kube:postgres-app/password" {
		t.Errorf("held %+v", held)
	}
	if nm.form == nil || nm.form.adHocFrom != nil || nm.current.ID != "pgx.status" {
		t.Fatalf("the run form is not back (current %q)", nm.current.ID)
	}
	if got := *nm.form.bindings[profileInput]; got != adHocLabel(held) {
		t.Errorf("picker = %q, want the connection just stated", got)
	}
}
