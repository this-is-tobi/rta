package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// profileModel builds a model over dbPlugin with the given config on disk.
func profileModel(t *testing.T, cfg config.Config) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(dbPlugin()); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	return m
}

// applySwitch runs the switch's binding the way the update loop does: syncActive
// hands back a command, the command resolves off the loop, and the message it
// returns is what installs the values. Driven by hand here so a test asserts
// against the state a real session actually reaches.
func applySwitch(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.syncActive()
	if cmd == nil {
		return
	}
	msg, ok := cmd().(boundMsg)
	if !ok {
		t.Fatalf("binding a switch produced %T, want boundMsg", cmd())
	}
	m.bound = msg.bound
	m.active = msg.name
}

// conn is one plugin entry, the shape almost every fixture here needs.
func conn(set map[string]any) config.Connection { return config.Connection{Set: set} }

func twoProfileConfig() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"staging": {
			Note:    "shared",
			Plugins: map[string]config.Connection{"db": conn(map[string]any{"host": "staging.internal"})},
		},
		"prod": {
			Plugins: map[string]config.Connection{"db": conn(map[string]any{"host": "prod.internal"})},
		},
	}}
}

// The pane lists what is configured, which plugins each one covers, and which
// one is switched on.
func TestProfilesPaneListsEnvironments(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if len(m.profiles) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.profiles))
	}
	m.mode = modeProfiles
	out := m.profilesView()
	for _, want := range []string{"STAGING", "PROD", "db", "shared"} {
		if !strings.Contains(out, want) {
			t.Errorf("pane does not show %q:\n%s", want, out)
		}
	}
}

// One environment spans several plugins, and the pane says so rather than
// picking one to name.
func TestAnEnvironmentShowsEveryPluginItCovers(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"proj1-staging": {Plugins: map[string]config.Connection{
			"db":  conn(map[string]any{"host": "staging.internal"}),
			"db2": conn(map[string]any{"host": "other.internal"}),
		}},
	}})
	row := m.profiles[0]
	if got := len(row.conns); got != 2 {
		t.Fatalf("conns = %d, want 2", got)
	}
	if got := profileCovers(row); !strings.Contains(got, "db") || !strings.Contains(got, "db2") {
		t.Errorf("covers line = %q, want both plugins", got)
	}
}

// `u` switches this machine to an environment, and pressing it again switches
// off.
//
// A toggle rather than a one-way switch: the pane is where somebody looks to
// find out what they are in, so it has to be where they can leave — and leaving
// is the fast path that matters, because while an environment is on, agents may
// reach that one and nothing else.
func TestUseSwitchesOnAndOff(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	// Rows are sorted, so 0 is "prod".
	if got := m.profiles[0].name; got != "prod" {
		t.Fatalf("first row = %q, want prod", got)
	}
	if flash := m.useSelectedProfile(); !strings.Contains(flash, "switched to prod") {
		t.Errorf("flash = %q", flash)
	}
	if got := profile.Active(); got != "prod" {
		t.Errorf("active = %q, want prod", got)
	}
	if flash := m.useSelectedProfile(); !strings.Contains(flash, "switched off") {
		t.Errorf("second press did not switch off: %q", flash)
	}
	if got := profile.Active(); got != "" {
		t.Errorf("active = %q, want nothing switched on", got)
	}
}

// A profile carrying `ttl:` brings its own deadline to every switch.
//
// The deadline lives with the profile because the profile is what knows how
// dangerous it is: a deadline that has to be remembered is one that gets
// forgotten exactly when it matters.
func TestAProfileTTLGivesTheSwitchADeadline(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"prod": {TTL: "1h", Plugins: map[string]config.Connection{
			"db": conn(map[string]any{"host": "prod.internal"}),
		}},
	}})
	if flash := m.useSelectedProfile(); !strings.Contains(flash, "for 1h") {
		t.Errorf("flash = %q, want the window named", flash)
	}
	sel := profile.LoadSelection()
	if sel.Until == nil {
		t.Fatal("a profile with ttl was switched on with no deadline")
	}
	if left, _ := sel.Left(time.Now()); left < 50*time.Minute || left > time.Hour {
		t.Errorf("left = %v, want about an hour", left)
	}

	// And it lapses on its own, without anything running.
	past := time.Now().Add(-time.Minute)
	if verr := profile.SaveSelection(profile.Selection{Active: "prod", Until: &past}); verr != nil {
		t.Fatal(verr)
	}
	if got := profile.Active(); got != "" {
		t.Errorf("active = %q after the deadline passed, want nothing", got)
	}
}

// An environment that cannot resolve cannot be switched to.
//
// Switching to one would leave every later command failing with a message about
// a connection the operator believes they chose deliberately.
func TestAnInvalidProfileCannotBeSwitchedTo(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"broken": {Plugins: map[string]config.Connection{
			"nosuchplugin": conn(map[string]any{"host": "x"}),
		}},
	}})
	if m.profiles[0].valid() {
		t.Fatal("a profile naming an unregistered plugin was reported valid")
	}
	if flash := m.useSelectedProfile(); !strings.Contains(flash, "cannot be used") {
		t.Errorf("flash = %q, want a refusal", flash)
	}
	if got := profile.Active(); got != "" {
		t.Errorf("active = %q, want nothing switched on", got)
	}
}

// Editing an environment writes back its own keys and nothing else in the file.
func TestEditingAProfileKeepsTheRestOfTheFile(t *testing.T) {
	cfg := twoProfileConfig()
	cfg.Output = "yaml"
	cfg.Plugins = map[string]map[string]any{"db": {"host": "base.internal"}}
	m := profileModel(t, cfg)

	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileNoteField] = "edited note"
	next, _ = nm.saveProfileForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Note; got != "edited note" {
		t.Errorf("note = %q, want the edited value", got)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Set["host"]; got != "staging.internal" {
		t.Error("editing the note emptied the plugins block")
	}
	if _, still := onDisk.Profiles["prod"]; !still {
		t.Error("editing one profile deleted another")
	}
	if onDisk.Output != "yaml" || onDisk.Plugins["db"]["host"] != "base.internal" {
		t.Errorf("editing a profile rewrote the rest of the file: %+v", onDisk)
	}
}

// Editing one plugin's connection must not silently drop its credential.
//
// The two are edited by different forms on purpose, so the form that does not
// collect `secrets:` has to carry it through — otherwise changing a host
// repoints the connection and unsets its password in one keystroke.
func TestEditingAConnectionKeepsItsCredentialMapping(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db": {
				Set:     map[string]any{"host": "staging.internal"},
				Secrets: map[string]string{"password": "kv:staging-db"},
			},
		}},
	}})
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	*nm.form.bindings[profileSetPrefix+"host"] = "moved.internal"
	next, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	got := onDisk.Profiles["staging"].Plugins["db"]
	if got.Secrets["password"] != "kv:staging-db" {
		t.Errorf("secrets = %v, want the mapping carried through", got.Secrets)
	}
	if got.Set["host"] != "moved.internal" {
		t.Errorf("host = %v, want the edited value", got.Set["host"])
	}
}

// Renaming moves the row and takes the switch with it.
//
// The switch, because it also bounds agents: a selection naming a profile that
// no longer exists would refuse every agent call against a name nothing can
// look up.
func TestRenamingAProfileMovesTheSwitch(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileNameField] = "staging-2"
	next, _ = nm.saveProfileForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, old := onDisk.Profiles["staging"]; old {
		t.Error("the old name is still in the file after a rename")
	}
	renamed, ok := onDisk.Profiles["staging-2"]
	if !ok {
		t.Fatalf("the renamed profile is missing: %+v", onDisk.Profiles)
	}
	if renamed.Plugins["db"].Set["host"] != "staging.internal" {
		t.Errorf("a rename emptied the plugins block: %+v", renamed)
	}
	if got := profile.Active(); got != "staging-2" {
		t.Errorf("active = %q — a rename left the switch naming a profile that is gone", got)
	}
}

// Deleting an environment revokes the grants that named it.
//
// A grant for a profile that no longer exists authorizes nothing — Lookup
// refuses the name — so leaving it is a row in `rta grant list` that reads
// like access and is not.
func TestDeletingAProfileRevokesItsGrants(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "db", Profile: "prod", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "db", Profile: "staging", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}
	flash := m.deleteSelectedProfile() // row 0 is prod
	if !strings.Contains(flash, "deleted profile prod") {
		t.Errorf("flash = %q", flash)
	}
	if !strings.Contains(flash, "revoked 1 grant") {
		t.Errorf("flash does not mention the revoked grant: %q", flash)
	}
	left, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(left) != 1 || left[0].Profile != "staging" {
		t.Errorf("grants left = %+v, want only staging's", left)
	}
}

// The picker offers the environments that cover this plugin, plus the base
// configuration — and offers nothing when there is nothing to choose.
func TestThePickerOffersConfiguredEnvironments(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	c := dbPlugin().Capabilities[0]

	f := m.profilePicker(c, "")
	if f == nil {
		t.Fatal("no picker offered for a capability with two configured environments")
	}
	if f.Options[0] != profileNoneLabel {
		t.Errorf("options = %v, want the base configuration first", f.Options)
	}
	if len(f.Options) != 3 {
		t.Errorf("options = %v, want base + two profiles", f.Options)
	}

	// Nothing configured: a picker with one entry is a question with one
	// answer, which is not a question.
	empty := profileModel(t, config.Config{})
	if f := empty.profilePicker(c, ""); f != nil {
		t.Errorf("a picker was offered with nothing to pick: %v", f.Options)
	}
}

// An invalid environment is left out of the picker: an entry that can only fail
// is worse than an absent one.
func TestThePickerLeavesOutBrokenEnvironments(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"good":   {Plugins: map[string]config.Connection{"db": conn(map[string]any{"host": "a"})}},
		"broken": {Plugins: map[string]config.Connection{"db": conn(map[string]any{"nosuchkey": "b"})}},
	}})
	f := m.profilePicker(dbPlugin().Capabilities[0], "")
	if f == nil {
		t.Fatal("no picker offered")
	}
	for _, o := range f.Options {
		if o == "broken" {
			t.Errorf("a profile that cannot resolve was offered: %v", f.Options)
		}
	}
}

// The dashboard header says where this session is, because every tile below it
// is showing that environment and every command run from it lands there.
func TestTheDashboardHeaderNamesTheSwitchedOnEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if got := m.activeBadge(); got != "" {
		t.Errorf("badge = %q with nothing switched on", got)
	}
	m.useSelectedProfile() // prod
	applySwitch(t, &m)
	if got := m.activeBadge(); !strings.Contains(got, "prod") {
		t.Errorf("badge = %q, want it to name prod", got)
	}
	if !strings.Contains(plain(m.dashboardView()), "prod") {
		t.Error("the dashboard header does not say which environment is switched on")
	}
}

// The switched-on environment reaches the tiles, or the dashboard is quietly
// answering a question about somewhere else.
func TestTilesRunAgainstTheSwitchedOnEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	c := dbPlugin().Capabilities[0]
	if name, _, _ := m.profileFor(c); name != "" {
		t.Errorf("a tile was bound to %q with nothing switched on", name)
	}
	m.useSelectedProfile() // prod
	applySwitch(t, &m)
	name, filled, _ := m.profileFor(c)
	if name != "prod" {
		t.Fatalf("tile profile = %q, want prod", name)
	}
	if filled["host"] != "prod.internal" {
		t.Errorf("tile values = %v, want the environment's host", filled)
	}
}

// A command started before the binding lands still reaches the environment.
//
// The bind runs off the update loop so switching does not freeze the app, and
// a person can start a command inside that window. The quiet answer — "nothing
// bound, so no profile" — is the wrong one: the call would reach the plugin's
// base connection while the header says staging, which is the whole failure
// profiles exist to prevent.
func TestACommandInsideTheBindWindowStillReachesTheEnvironment(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	m.useSelectedProfile() // prod
	m.active, m.bound = "prod", nil

	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, map[string]any{})
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "prod" {
		t.Fatalf("profile = %q, want prod — the call would have gone to the base connection", name)
	}
	if filled["host"] != "prod.internal" {
		t.Errorf("values = %v, want the environment's host", filled)
	}
}

// A binding that lands after the environment has moved on is dropped.
//
// Two ways it can move, and the second is the one that used to get through.
// Flipping twice in a second must not leave the first environment's values
// installed under the second one's name — and *editing* the environment
// already switched on must not let the bind started before the edit win over
// the one started after it, which name comparison could never have caught
// because both binds carry the same name.
func TestALateBindingForAnOldEnvironmentIsDropped(t *testing.T) {
	stale := map[string]envBind{"db.list": {values: map[string]any{"host": "prod.internal"}}}

	t.Run("another environment", func(t *testing.T) {
		m := profileModel(t, twoProfileConfig())
		m.active, m.boundStamp = "staging", environmentStamp("staging")
		next, _ := m.Update(boundMsg{name: "prod", stamp: environmentStamp("prod"), bound: stale})
		if got := next.(Model).bound; got != nil {
			t.Errorf("bound = %v — a stale bind was installed under the current name", got)
		}
	})

	t.Run("the same environment, edited", func(t *testing.T) {
		m := profileModel(t, twoProfileConfig())
		before := environmentStamp("prod")
		m.active, m.boundStamp = "prod", before

		// The edit, through the same writer the profiles pane uses.
		cfg, err := config.LoadFile()
		if err != nil {
			t.Fatal(err)
		}
		conn := cfg.Profiles["prod"].Plugins["db"]
		conn.Set = map[string]any{"host": "prod-2.internal"}
		cfg.Profiles["prod"].Plugins["db"] = conn
		if err := config.Write(cfg); err != nil {
			t.Fatal(err)
		}
		m.boundStamp = environmentStamp("prod")
		if m.boundStamp == before {
			t.Fatal("editing the environment did not change its stamp, so nothing here can be proven")
		}
		next, _ := m.Update(boundMsg{name: "prod", stamp: before, bound: stale})
		if got := next.(Model).bound; got != nil {
			t.Errorf("bound = %v — a bind from before the edit was installed after it", got)
		}
	})
}

// The plugin editor is built from the inventory, so the inventory has to be
// loaded before somebody can reach the editor.
//
// Opening the profiles pane without passing through the plugins pane first used
// to reach an editor with no fields and nothing to complete.
func TestOpeningTheProfilesPaneLoadsThePluginInventory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(twoProfileConfig()); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(dbPlugin()); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	if len(m.plugins) != 0 {
		t.Fatalf("the inventory was already loaded, so this proves nothing")
	}
	opened := press(t, m, "f")
	if len(opened.plugins) == 0 {
		t.Fatal("the profiles pane opened without the inventory its editor is built from")
	}
	if len(opened.installedPlugins()) == 0 {
		t.Error("the plugin field has nothing to complete from")
	}
}

// One bind opens the store once per entry, not once per capability.
//
// Fill fetches per capability and `pg` has six of them, while every fetch
// unlocks the store — measured at ~0.5s of scrypt on this machine. Without the
// memo, switching to an environment with one mapped credential cost three
// seconds of key derivation to learn the same six-character string six times.
// Failures are memoised too: a store that cannot be opened will not open on the
// fourth try, and retrying costs another derivation to find that out.
func TestOneBindOpensTheStoreOncePerEntry(t *testing.T) {
	calls := map[string]int{}
	read := memoRead(func(ref string) (string, *view.Error) {
		calls[ref]++
		if ref == "missing" {
			return "", view.Errorf("kv.notfound", "no entry")
		}
		return "value-of-" + ref, nil
	})
	for range 6 {
		if got, _ := read("db-password"); got != "value-of-db-password" {
			t.Fatalf("memoised reader answered %q", got)
		}
		if _, verr := read("missing"); verr == nil {
			t.Fatal("a memoised failure came back as a success")
		}
	}
	if calls["db-password"] != 1 || calls["missing"] != 1 {
		t.Errorf("opened the store %d/%d times, want once each", calls["db-password"], calls["missing"])
	}
}

// Both panes open from the dashboard and come back, through the real loop.
func TestTheProfilesPanesOpenFromTheDashboard(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(twoProfileConfig()); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(dbPlugin()); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}, nil),
		teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	waitFor(t, tm, "STAGING")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "host=prod.internal")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t)
}

// The reported bug, end to end: an environment edited while it is switched on
// has to reach both the screen and the next command, without quitting.
//
// The cache key used to be the environment's *name*, which is the one thing
// about an environment that does not change when its meaning does. So
// switching to an environment resolved it once and nothing resolved it again:
// a form opened afterwards showed the host, endpoint or region as it stood at
// the switch, and — the half that had no symptom — the command ran against it
// too. Relaunching was the only way out.
func TestEditingTheSwitchedOnEnvironmentTakesEffectWithoutRestarting(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, &m)

	c := dbPlugin().Capabilities[0]
	if _, filled, _, _ := m.resolveProfile(c, map[string]any{}); filled["host"] != "prod.internal" {
		t.Fatalf("the switch did not take: %v", filled)
	}

	// The edit, exactly as the profiles pane makes it: load, change, write.
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["prod"].Plugins["db"] = conn(map[string]any{"host": "prod-2.internal"})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}

	// One refresh tick later — no keystroke, no relaunch, nothing that knows
	// an edit happened.
	applySwitch(t, &m)

	name, filled, _, verr := m.resolveProfile(c, map[string]any{})
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "prod" {
		t.Fatalf("profile = %q, want prod", name)
	}
	if filled["host"] != "prod-2.internal" {
		t.Errorf("the command would run against %v — the edit did not reach it", filled)
	}
	// And the form seeded from it, which is the half the operator could see.
	_, seeded, _ := m.profileSeed(c, "prod")
	if seeded["host"] != "prod-2.internal" {
		t.Errorf("the form would open on %v — the screen and the call disagree", seeded)
	}
}

// Only when something actually moved. The stamp is read on every refresh tick,
// and a tick that re-bound an unchanged environment would spend a second of
// scrypt every five for nothing.
func TestAnUnchangedEnvironmentDoesNotRebind(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, &m)

	for i := 0; i < 3; i++ {
		if cmd := m.syncActive(); cmd != nil {
			t.Fatalf("tick %d re-bound an environment nothing had changed", i)
		}
	}
	// A write that leaves the environment saying the same thing is not a
	// change either: the stamp describes what it states, not when it was
	// written.
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	if cmd := m.syncActive(); cmd != nil {
		t.Error("rewriting the file unchanged re-bound the environment")
	}
}

// A run started between two refresh ticks is exact, not merely five seconds
// out of date. Editing an environment and pressing enter a second later is an
// ordinary sequence, and the five seconds between refreshes were five seconds
// of the previous connection.
func TestARunBetweenTicksSeesTheEdit(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, &m)

	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["prod"].Plugins["db"] = conn(map[string]any{"host": "prod-2.internal"})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}

	// No tick, no screen transition, nothing that knows an edit happened —
	// the state a session is in one second after saving.
	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, map[string]any{})
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "prod" {
		t.Fatalf("profile = %q, want prod", name)
	}
	if filled["host"] != "prod-2.internal" {
		t.Errorf("the command ran against %v — a cached bind outlived what it described", filled)
	}
	if seeded := mustSeed(t, m, c); seeded["host"] != "prod-2.internal" {
		t.Errorf("the form would open on %v", seeded)
	}
}

func mustSeed(t *testing.T, m Model, c plugin.Capability) map[string]any {
	t.Helper()
	_, seeded, _ := m.profileSeed(c, "prod")
	return seeded
}

// An environment that is no longer switched on stops supplying its
// connection, whether the deadline lapsed or somebody switched off elsewhere.
//
// The stamp answers "does this binding still describe what the environment
// says" and cannot answer "is that environment still in force": both of those
// change the *selection* and touch no profile. Selection.Until documents
// itself as enforced on every read, and a command deciding which server it
// reaches is a read — so without this a production activation went on
// handing out its credentials after it expired, for as long as the session
// stayed off the dashboard, which is where the tick that would have noticed
// lives.
func TestALapsedEnvironmentStopsSupplyingItsConnection(t *testing.T) {
	c := dbPlugin().Capabilities[0]

	t.Run("the deadline lapsed", func(t *testing.T) {
		m := profileModel(t, twoProfileConfig())
		if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
			t.Fatal(verr)
		}
		applySwitch(t, &m)
		if _, filled, _, _ := m.resolveProfile(c, map[string]any{}); filled["host"] != "prod.internal" {
			t.Fatalf("the switch did not take: %v", filled)
		}

		past := time.Now().Add(-time.Minute)
		if verr := profile.SaveSelection(profile.Selection{Active: "prod", Until: &past}); verr != nil {
			t.Fatal(verr)
		}
		if got := profile.Active(); got != "" {
			t.Fatalf("the fixture did not lapse: profile.Active() = %q", got)
		}
		name, filled, _, verr := m.resolveProfile(c, map[string]any{})
		if verr != nil {
			t.Fatal(verr)
		}
		if name != "" || filled != nil {
			t.Errorf("an expired activation still supplied %q %v", name, filled)
		}
	})

	t.Run("switched off from another terminal", func(t *testing.T) {
		m := profileModel(t, twoProfileConfig())
		if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
			t.Fatal(verr)
		}
		applySwitch(t, &m)
		if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
			t.Fatal(verr)
		}
		if name, filled, _, _ := m.resolveProfile(c, map[string]any{}); name != "" || filled != nil {
			t.Errorf("`rta use --off` elsewhere still supplied %q %v", name, filled)
		}
		// And the form agrees, or the screen and the call part company in the
		// other direction — boxes showing production's host over a run that
		// correctly went nowhere near it. Through pickedProfile, because that
		// is what decides a form's environment when nobody picked one; an
		// explicit pick is a different question and still wins.
		on := m.pickedProfile(c, nil)
		if on != "" {
			t.Errorf("a form would open on %q after it was switched off", on)
		}
		if _, seeded, _ := m.profileSeed(c, on); seeded != nil {
			t.Errorf("a form still opened on a switched-off environment: %v", seeded)
		}
	})
}

// A command must never be sent to the plugin's base connection because a
// cache lookup happened twice and the file moved in between. Running silently
// somewhere else is the failure this whole path exists to prevent.
func TestAConfigWriteMidResolveDoesNotUnprofileTheCall(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, &m)

	// The shape of the race, made deterministic: the environment's text
	// changes between what would have been the two lookups.
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["prod"].Plugins["db"] = conn(map[string]any{"host": "prod-2.internal"})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}

	c := dbPlugin().Capabilities[0]
	name, filled, _, verr := m.resolveProfile(c, map[string]any{})
	if verr != nil {
		t.Fatal(verr)
	}
	if name != "prod" {
		t.Fatalf("profile = %q — the call went out unprofiled", name)
	}
	if filled["host"] != "prod-2.internal" {
		t.Errorf("values = %v, want the environment as it now stands", filled)
	}
}
