package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// dbPlugin mirrors plugins/pg's own shape: every capability appends the same
// connFields, so a config editor over the plugin has to dedup rather than
// ask "host" once per capability.
func dbPlugin() plugin.Plugin {
	// The endpoint roles are what make this a plugin a profile's forward can
	// fill, and they were missing. Every real database plugin declares the
	// pair — plugins/pg, mysql and mariadb all do — and without them this
	// fixture could not tell a plugin a coordinate means something for from
	// one it does not, so no test here modelled the difference and the
	// connection editor offered the coordinate box to both.
	shared := []plugin.Field{
		{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
			Local: true, Endpoint: plugin.EndpointHost},
		{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
			Local: true, Endpoint: plugin.EndpointPort},
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "db", Summary: "a database plugin",
		Capabilities: []plugin.Capability{
			{ID: "db.status", Summary: "status", Safety: plugin.Read, Inputs: shared, Run: run},
			{ID: "db.list", Summary: "list", Safety: plugin.Read,
				Inputs: append(append([]plugin.Field{}, shared...),
					plugin.Field{Name: "schema", Type: plugin.String, Config: "schema"}), Run: run},
		},
	}
}

// otherDBPlugin is a second plugin a forward can fill, so a rebuild between
// two of them can be told apart from a rebuild onto one that takes no
// coordinate at all.
//
// Addressed as one string rather than as a host and a port — the shape
// plugins/etcd and plugins/qdrant use — so it shares no config key with
// dbPlugin and a key dropped by the rebuild is visible as a key dropped.
func otherDBPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "db2", Summary: "another database plugin",
		Capabilities: []plugin.Capability{
			{ID: "db2.status", Summary: "status", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					{Name: "addr", Type: plugin.String, Config: "addr", Local: true,
						Endpoint: plugin.EndpointAddress, Help: "host[:port]"},
				}},
		},
	}
}

func plainPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "plain", Summary: "nothing to configure",
		Capabilities: []plugin.Capability{
			{ID: "plain.go", Summary: "go", Safety: plugin.Read, Run: run},
		},
	}
}

func TestConfigFieldsDedupsAcrossCapabilities(t *testing.T) {
	fields := configFields(dbPlugin())
	if len(fields) != 3 {
		t.Fatalf("configFields = %v, want 3 (host, port, schema deduplicated)", fields)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Name] {
			t.Errorf("%q appears twice", f.Name)
		}
		seen[f.Name] = true
	}
	for _, want := range []string{"host", "port", "schema"} {
		if !seen[want] {
			t.Errorf("configFields is missing %q", want)
		}
	}
}

// vaultShapedPlugin mirrors plugins/vault: one input NAME declared twice
// against two different config KEYS. An input's name and its config key are
// not the same thing, and this is the plugin in the tree that proves it.
func vaultShapedPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "vlt", Summary: "a secrets plugin",
		Capabilities: []plugin.Capability{
			{ID: "vlt.kv.list", Summary: "kv", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					{Name: "address", Type: plugin.String, Config: "address"},
					{Name: "mount", Type: plugin.String, Default: "secret", Config: "kv-mount"},
				}},
			{ID: "vlt.transit.list", Summary: "transit", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					{Name: "address", Type: plugin.String, Config: "address"},
					{Name: "mount", Type: plugin.String, Default: "transit", Config: "transit-mount"},
				}},
		},
	}
}

// The config editor edits config KEYS, and two inputs sharing a name are two
// keys.
//
// Deduplicating by Field.Name collapsed vault's two mounts into one box, and
// capForm then saved that box under the shared name `mount:` — a key
// plugin.Resolve never reads, because lookupConfig walks Field.Config. So
// this editor wrote a key nothing consumes, silently dropped the other mount,
// and `rta doctor` reported "nothing in vault reads mount" about a value rta
// had just written itself.
func TestConfigFieldsKeysByConfigKeyNotByInputName(t *testing.T) {
	fields := configFields(vaultShapedPlugin())
	got := map[string]bool{}
	for _, f := range fields {
		got[f.Name] = true
	}
	for _, want := range []string{"address", "kv-mount", "transit-mount"} {
		if !got[want] {
			t.Errorf("configFields does not offer %q; got %v", want, got)
		}
	}
	if got["mount"] {
		t.Error("configFields offers the input name `mount`, which no capability reads from config")
	}
	if len(fields) != 3 {
		t.Errorf("configFields = %d fields, want 3 (address, kv-mount, transit-mount)", len(fields))
	}
	// The declared default has to survive the rename, or the form seeds the
	// two mounts with each other's value.
	for _, f := range fields {
		switch f.Name {
		case "kv-mount":
			if f.Default != "secret" {
				t.Errorf("kv-mount default = %v, want secret", f.Default)
			}
		case "transit-mount":
			if f.Default != "transit" {
				t.Errorf("transit-mount default = %v, want transit", f.Default)
			}
		}
	}
}

// Saving one field must not delete the rest of the section.
//
// `cfg.Plugins[heading] = values` replaced the whole section with only what
// the form collected, so a key this build does not declare — written by hand,
// or belonging to a capability the installed binary happens not to offer
// right now — was deleted by the act of saving an unrelated field. The
// operator gets no warning: the value is simply gone the next time anything
// reads it.
func TestSaveConfigFormKeepsKeysTheFormNeverShowed(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	installed := registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "1a2b3c4d5e6f"}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"db@1a2b3c4d5e6f": {
			"host":          "old.example",
			"from-a-future": "keep me",
		},
	}}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: installed})
	nm := next.(Model)
	*nm.form.bindings["host"] = "new.example"
	next, _ = nm.saveConfigForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	section := onDisk.Plugins["db@1a2b3c4d5e6f"]
	if section["host"] != "new.example" {
		t.Errorf("host = %v, want the edited value", section["host"])
	}
	if section["from-a-future"] != "keep me" {
		t.Errorf("saving one field deleted a key the form never showed; section = %v", section)
	}
}

// …and a field the form DID show, cleared, stays cleared. The obvious fix for
// the test above — overlay the form's values onto what was on disk — makes a
// value impossible to delete, because capForm.values() omits an empty text
// field with no declared default rather than emitting an empty string.
func TestSaveConfigFormLetsAShownFieldBeCleared(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	installed := registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "1a2b3c4d5e6f"}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"db@1a2b3c4d5e6f": {"host": "old.example", "schema": "public"},
	}}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: installed})
	nm := next.(Model)
	// schema declares no default, so emptying it is the operator saying
	// "remove this".
	*nm.form.bindings["schema"] = ""
	next, _ = nm.saveConfigForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if v, still := onDisk.Plugins["db@1a2b3c4d5e6f"]["schema"]; still {
		t.Errorf("schema = %v after being cleared; a shown field must be deletable", v)
	}
}

func TestConfigurableIsFalseForAPluginWithNoConfigFields(t *testing.T) {
	if configurable(plainPlugin()) {
		t.Error("a plugin with no Config-bearing input was reported configurable")
	}
	if !configurable(dbPlugin()) {
		t.Error("dbPlugin declares Config fields and was reported not configurable")
	}
}

func TestStartConfigFormFlashesForANonConfigurablePlugin(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	if err := reg.Register(plainPlugin()); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: plainPlugin()})
	nm := next.(Model)
	if nm.form != nil {
		t.Error("a form opened for a plugin with nothing to configure")
	}
	if nm.flash == "" {
		t.Error("no flash explaining why nothing opened")
	}
}

// The property RawSection exists for: fixing a stale pin means confirming
// the values that were already there, not retyping them from a blank form.
func TestStartConfigFormSeedsFromAStaleSectionRatherThanBlank(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	installed := registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "1a2b3c4d5e6f"}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"db@000000000000": {"host": "stale.example", "port": int64(9999)}, // a different, stale digest
	}}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: installed})
	nm := next.(Model)
	if nm.form == nil {
		t.Fatal("no form opened")
	}
	if got := *nm.form.bindings["host"]; got != "stale.example" {
		t.Errorf("host = %q, want the stale section's own value, not the declared default", got)
	}
	if got := *nm.form.bindings["port"]; got != "9999" {
		t.Errorf("port = %q, want the stale section's own value", got)
	}
}

// The migration: submitting writes under the installed digest and removes
// the stale heading, so the file never claims two artifacts at once for one
// namespace.
func TestSaveConfigFormMigratesAStalePinToTheInstalledDigest(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	installed := registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "1a2b3c4d5e6f"}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"db@000000000000": {"host": "stale.example", "port": int64(9999)},
	}}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: installed})
	nm := next.(Model)

	*nm.form.bindings["host"] = "fixed.example"
	next, _ = nm.saveConfigForm()
	nm = next.(Model)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := onDisk.Plugins["db@000000000000"]; stale {
		t.Error("the stale heading is still in the file after saving")
	}
	section, ok := onDisk.Plugins["db@1a2b3c4d5e6f"]
	if !ok {
		t.Fatalf("no section under the installed digest; plugins = %v", onDisk.Plugins)
	}
	if section["host"] != "fixed.example" {
		t.Errorf("host = %v, want the edited value", section["host"])
	}
	if nm.flash == "" {
		t.Error("no flash confirming the save")
	}
}

// …and the keys the form never showed come with it. The carry-forward read
// the heading being written, which on a migration is a section that does not
// exist yet — so the one job this screen exists for, fixing a stale pin,
// silently dropped every neighbouring key while moving the values the operator
// had just confirmed. The file that results looks deliberate, which is what
// makes it worse than leaving the stale heading in place.
func TestSaveConfigFormCarriesUnshownKeysAcrossThePinMigration(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	installed := registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "1a2b3c4d5e6f"}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"db@000000000000": {"host": "stale.example", "from-a-future": "keep me"},
	}}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: installed})
	nm := next.(Model)
	*nm.form.bindings["host"] = "fixed.example"
	next, _ = nm.saveConfigForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	section := onDisk.Plugins["db@1a2b3c4d5e6f"]
	if section["from-a-future"] != "keep me" {
		t.Errorf("the migration dropped a key the form never showed; section = %v", section)
	}
	if section["host"] != "fixed.example" {
		t.Errorf("host = %v, want the edited value", section["host"])
	}
}

// The plugin config editor reuses capForm (it is the same modeForm, driven
// by the same afterFormUpdate), so shift+enter should already reach it —
// but nothing had ever driven it end to end through the real event loop to
// confirm that, unlike the capability-run and theme-form cases.
func TestShiftEnterSavesThePluginConfigForm(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	if err := reg.Register(dbPlugin()); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'p', Text: "p"})
	waitFor(t, tm, "db")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitFor(t, tm, "host")

	// Clear the displayed default and state a host, so the save has one real
	// answer and one untouched display to tell apart.
	tm.Send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	for _, r := range "db.internal" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm.Send(shiftEnter)
	waitFor(t, tm, "saved") // flash text is "saved plugins.db", the tail can get clipped at 100 cols

	quit(t, tm)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	section, ok := onDisk.Plugins["db"]
	if !ok {
		t.Fatalf("no db section written; plugins = %v", onDisk.Plugins)
	}
	if section["host"] != "db.internal" {
		t.Errorf("host = %v, want the value typed before shift+enter", section["host"])
	}
	// The untouched port box was a display of the declared default, not a
	// statement: writing it would pin today's default into the file as if the
	// operator had chosen it.
	if v, has := section["port"]; has {
		t.Errorf("port = %v was written, and nobody stated a port", v)
	}
}

// A built-in has no artifact of its own, so its section is written bare —
// the same rule pluginconf.Resolve enforces on the read side.
func TestSaveConfigFormLeavesABuiltInSectionBare(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := registry.New()
	m := New(reg, config.Dashboard{}, nil)
	// registry.Origin{} is the built-in zero value — External() is false.
	next, _ := m.startConfigForm(pluginRow{plugin: dbPlugin(), origin: registry.Origin{}})
	nm := next.(Model)
	if nm.form == nil {
		t.Fatal("no form opened")
	}

	next, _ = nm.saveConfigForm()
	nm = next.(Model)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := onDisk.Plugins["db"]; !ok {
		t.Errorf("no bare 'db' section; plugins = %v", onDisk.Plugins)
	}
}
