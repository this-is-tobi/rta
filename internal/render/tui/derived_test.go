package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// What a form hands back versus what it merely showed — the distinction
// capForm.derived carries, tested here at the two places it was learned the
// hard way: a run under a `kube:` connection, and the connection editor's
// save. And its sequel: what a form does not even ask, because the forward
// answers it (forwardFilled, startConnForm's endpoint-key hiding).

// An untouched run form does not outrun the forward.
//
// **This is the screenshot: `pg.database.list`, profile picked, error
// `nothing is listening on localhost:5432`.** The form displayed the
// plugin's declared defaults, handed them back as caller values, and Caller
// beats Profile by design (`--profile prod --host x` connects to x) — so the
// boxes nobody touched beat the forward the connection had just opened. The
// forward was raised, ignored, and torn down, and the call went to
// localhost. A displayed derivation must never outrank the layer it came
// from.
func TestAnUntouchedRunFormDoesNotOutrunTheForward(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"db": {Kube: "homelab/databases/svc/postgres:5432"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	var reached string
	c := plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
				Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			reached = fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))
			return view.Text{Body: "ok"}, nil
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40

	forwardPort := fakeForward(t)

	// Open the run form, touch nothing but the picker — exactly the session
	// in the screenshot.
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	*nm.form.bindings[profileInput] = "homelab"

	values := nm.form.values()
	if _, given := values["host"]; given {
		t.Fatalf("an untouched host box came back as a caller value: %v", values["host"])
	}

	name, filled, conn, verr := nm.resolveProfile(c, values)
	if verr != nil {
		t.Fatalf("resolve: %s", verr.Message)
	}
	msg := runCmd(context.Background(), 1, c, withoutPicker(c, values), false,
		nm.configFor(c), name, filled, conn)()
	if rm := msg.(resultMsg); rm.err != nil {
		t.Fatalf("run: %s", rm.err.Message)
	}
	want := fmt.Sprintf("127.0.0.1:%d", forwardPort)
	if reached != want {
		t.Errorf("the run reached %s, want %s — the displayed defaults outran the forward", reached, want)
	}
}

// The connection editor writes what was stated — by the file or by the
// person — and nothing else.
//
// It used to write every displayed declared default into `set:` on save:
// four phantom keys per connection out of one typed database name, seen in
// the field as `set:host localhost` beside a `kube:` coordinate — two
// statements about the destination, one of them nobody's. A `set:` full of
// defaults also pins today's defaults as if chosen, and turns `rta profile
// show` into a page that cannot tell the operator's intent from furniture.
func TestTheConnEditorWritesOnlyWhatWasStated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}}); err != nil {
		t.Fatal(err)
	}
	// A plugin with a defaulted text field AND a defaulted picker, because
	// they phantom through different holes: an untouched text box is caught
	// by displayed's declared-default fallback, while a picker always holds
	// a choice — only the editor's own marks know the file never made it.
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host"},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port"},
				{Name: "mode", Type: plugin.String, Default: "prefer",
					Options: []string{"disable", "prefer"}, Config: "mode"},
				{Name: "schema", Type: plugin.String, Config: "schema"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	m.profileOpen = "staging"

	model, _ := m.startConnForm("db")
	nm := model.(Model)
	*nm.form.bindings[profileSetPrefix+"schema"] = "public"
	model, _ = nm.saveConnForm()
	nm = model.(Model) // the saved model carries the refreshed rows

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	set := onDisk.Profiles["staging"].Plugins["db"].Set
	if set["schema"] != "public" {
		t.Fatalf("schema = %v, want the value that was typed (set: %v)", set["schema"], set)
	}
	for _, phantom := range []string{"host", "port", "mode"} {
		if v, has := set[phantom]; has {
			t.Errorf("set.%s = %v was written, and nobody stated it — a displayed "+
				"default became configuration", phantom, v)
		}
	}

	// And the file's own values round-trip: reopening and saving untouched
	// keeps them, because a kept value is an answer the file already gave.
	model, _ = nm.startConnForm("db")
	nm = model.(Model)
	nm.saveConnForm()
	onDisk, err = config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Set["schema"]; got != "public" {
		t.Errorf("schema = %v after an untouched re-save, want the file value kept", got)
	}
}

// Under a cluster connection, the endpoint boxes are not asked at all: the
// forward fills host, port and TLS per call (profile.Dial), so a form that
// asked would be collecting answers with nowhere to go. The picker — the
// field that made it true — carries the one line saying what fills them.
//
// This is the display half of the screenshot, one step further than the
// first fix: `host: localhost` under a badge saying homelab became `host:`
// with a hint, and an empty box is still a question — three of them, each an
// invitation to answer. A value the person actually gave keeps its field,
// because Resolve gives the caller precedence and a winning answer must be
// on screen to be seen and edited.
func TestEndpointFieldsUnderAClusterConnectionAreNotAsked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"db": {Kube: "homelab/databases/svc/postgres:5432"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	c := plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
				Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
			{Name: "sslmode", Type: plugin.String, Default: "prefer", Config: "sslmode",
				Local: true, Endpoint: plugin.EndpointTLS,
				Options: []string{"disable", "prefer"}, Help: "tls"},
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	switchOn(t, &m, "homelab")

	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	for _, name := range []string{"host", "port", "sslmode"} {
		if _, asked := nm.form.bindings[name]; asked {
			t.Errorf("%s has a box under a cluster connection — the forward fills it, "+
				"and an empty box is a question", name)
		}
	}
	picker := nm.form.fields[0]
	if picker.Name != profileInput {
		t.Fatalf("fields[0] = %s, want the picker", picker.Name)
	}
	if !strings.Contains(picker.Help, "tunnel, which fills host, port, sslmode") {
		t.Errorf("picker help = %q — nothing on screen says where the endpoint went", picker.Help)
	}
	// None of it comes back as an answer.
	values := nm.form.values()
	for _, name := range []string{"host", "port", "sslmode"} {
		if v, given := values[name]; given {
			t.Errorf("%s = %v came back as a caller value from a form that never asked", name, v)
		}
	}
	// A value the person gave keeps its field and its value; the rest stay
	// dropped, and the help names only what the forward still fills.
	model, _ = m.startForm(c, map[string]any{"host": "db.internal"})
	nm = model.(Model)
	if got := *nm.form.bindings["host"]; got != "db.internal" {
		t.Errorf("host box = %q, want the value the person gave kept on screen", got)
	}
	if _, asked := nm.form.bindings["port"]; asked {
		t.Error("port is asked about while only host was given")
	}
	if help := nm.form.fields[0].Help; !strings.Contains(help, "fills port, sslmode") ||
		strings.Contains(help, "host") {
		t.Errorf("picker help = %q, want it to name port and sslmode but not the given host", help)
	}
}

// endpointEditorModel is a conn-editor model whose plugin declares endpoint
// roles — the combination the editor's hiding is about, and the one dbPlugin
// deliberately lacks so the ordinary editor tests stay about statements.
func endpointEditorModel(t *testing.T, conn config.Connection) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": conn}},
	}}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
					Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
					Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
				// An Options endpoint input, because it is the one a person
				// cannot repair by hand: a select always holds a choice, so
				// "empty the box" is an instruction the widget cannot follow.
				{Name: "sslmode", Type: plugin.String, Default: "prefer", Config: "sslmode",
					Local: true, Endpoint: plugin.EndpointTLS,
					Options: []string{"disable", "prefer"}, Help: "tls"},
				{Name: "schema", Type: plugin.String, Config: "schema", Help: "schema"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true,
					Help: "password"},
			},
		}}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	m.profileOpen = "staging"
	return m
}

// The connection editor does not offer what the forward fills, and its save
// repairs what the file still states.
//
// A `set.host` box beside a coordinate is two statements about the
// destination, and a box for the one that loses is an invitation to make it
// — checkSet refuses the pair outright. A first cut kept a file-stated
// key's box "so it could be cleared", which assumed clearing is something
// every widget can do: a stated `sslmode` is a select, a select always
// holds one of its Options, and the save guard then refused every save of
// the entry — an editor with a mandatory chore its own widgets cannot
// perform. So the boxes are gone regardless, the save drops the dead keys,
// and the flash is the receipt.
func TestTheConnEditorDoesNotOfferWhatTheForwardFills(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		Kube: "homelab/databases/svc/postgres:5432",
		Set:  map[string]any{"host": "stale.internal", "sslmode": "prefer"},
	})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	for _, name := range []string{"host", "port", "sslmode"} {
		if _, offered := nm.form.bindings[profileSetPrefix+name]; offered {
			t.Errorf("set.%s has a box beside a coordinate — an invitation to state the "+
				"losing destination", name)
		}
	}
	if _, offered := nm.form.bindings[profileSetPrefix+"schema"]; !offered {
		t.Error("set.schema lost its box — only the endpoint keys are the forward's to fill")
	}
	// An untouched save is the repair: the coordinate stays, the dead keys
	// go, and the flash names what happened to them.
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "removed set.host, set.sslmode") {
		t.Errorf("flash = %q, want the removal named — a save that silently drops a "+
			"statement is a save that lies", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if saved.Kube != "homelab/databases/svc/postgres:5432" {
		t.Errorf("kube = %q, want the coordinate kept", saved.Kube)
	}
	for _, name := range []string{"host", "sslmode"} {
		if v, still := saved.Set[name]; still {
			t.Errorf("set.%s = %v survived the save — the file still names two destinations", name, v)
		}
	}
}

// Saving a coordinate over kept endpoint keys removes them, with the flash
// as receipt — repaired at save for CheckKube's reason: this is the one
// screen that knows both halves, and writing the pair would save a profile
// checkSet then refuses everywhere. Removal rather than refusal, because
// refusal assumed the operator could empty the boxes, and the stated
// sslmode select has no empty to choose — the entry was unsaveable while
// the coordinate stood.
func TestSavingACoordinateRemovesTheKeptEndpointKeys(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		Set: map[string]any{"host": "stale.internal", "sslmode": "prefer"},
		// A mapping aimed at the same input the forward fills, and one aimed
		// at a credential: the first dies with the coordinate
		// (checkSecretRefs), the second is the feature working as intended.
		Secrets: map[string]string{"host": "kv:db-host", "password": "kv:db-pass"},
	})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	// No coordinate yet, so every key has its box — including the stated
	// host, and the stated sslmode nobody could clear by hand.
	if _, offered := nm.form.bindings[profileSetPrefix+"host"]; !offered {
		t.Fatal("a direct connection's set.host has no box")
	}
	*nm.form.bindings[profileKubeField] = "homelab/databases/svc/postgres:5432"
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "removed set.host, set.sslmode") ||
		!strings.Contains(nm.flash, "secrets.host") {
		t.Errorf("flash = %q, want every removal named", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if saved.Kube != "homelab/databases/svc/postgres:5432" {
		t.Errorf("kube = %q, want the coordinate the person typed", saved.Kube)
	}
	for _, name := range []string{"host", "sslmode"} {
		if v, still := saved.Set[name]; still {
			t.Errorf("set.%s = %v was written beside the coordinate — the pair every "+
				"surface refuses", name, v)
		}
	}
	if v, still := saved.Secrets["host"]; still {
		t.Errorf("secrets.host = %v was written beside the coordinate — fetched and "+
			"discarded on every call", v)
	}
	if saved.Secrets["password"] != "kv:db-pass" {
		t.Errorf("secrets.password = %v, want the credential mapping kept — it is the "+
			"feature, not the conflict", saved.Secrets["password"])
	}
}

// The credential editor does not aim a mapping at what the forward fills.
//
// Under a coordinate a mapping onto `host` is fetched and discarded on
// every call, and checkSecretRefs refuses the profile that states it — so
// offering the target invites creating a file the next screen refuses. On a
// direct connection `host` from a local entry is an ordinary configuration
// and stays offered.
func TestTheCredentialEditorDoesNotAimAtForwardFilledInputs(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		Kube: "homelab/databases/svc/postgres:5432",
	})
	model, _ := m.startCredentialForm()
	nm := model.(Model)
	opts := optionsOf(nm.form, credInputField)
	for _, name := range []string{"host", "port", "sslmode"} {
		if slices.Contains(opts, name) {
			t.Errorf("%q is offered as a mapping target under a coordinate — the forward "+
				"fills it (offered: %v)", name, opts)
		}
	}
	for _, name := range []string{"password", "schema"} {
		if !slices.Contains(opts, name) {
			t.Errorf("%q is not offered, and the forward does not fill it (offered: %v)", name, opts)
		}
	}

	direct := endpointEditorModel(t, config.Connection{})
	model, _ = direct.startCredentialForm()
	nm = model.(Model)
	opts = optionsOf(nm.form, credInputField)
	if !slices.Contains(opts, "host") {
		t.Errorf("host lost its place on a direct connection, where a mapping onto it is "+
			"ordinary (offered: %v)", opts)
	}
	// port is never offered anywhere: a mapping delivers text, the request
	// readers do not coerce, and checkSecretRefs refuses the pair — an Int
	// target is a zero delivered with confidence.
	if slices.Contains(opts, "port") {
		t.Errorf("port is offered as a mapping target (offered: %v)", opts)
	}
}

// Editing an entry whose plugin is not installed keeps its statements.
//
// With no declarations to build boxes from, the editor shows only the plugin
// and coordinate fields — and the save used to rebuild `set:` from the boxes
// it had, which was none, silently emptying the block. A form must not
// rewrite what it could not show: the `set:` block now stands exactly as
// `secrets:` always has.
func TestEditingAnUninstalledPluginsEntryKeepsItsStatements(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"ghost@abc123": {
				Set:     map[string]any{"host": "ghost.internal", "region": "eu"},
				Secrets: map[string]string{"password": "kv:ghost-pass"},
			},
		}},
	}})
	m.profileOpen = "staging"
	model, _ := m.startConnForm("ghost@abc123")
	nm := model.(Model)
	if _, offered := nm.form.bindings[profileSetPrefix+"host"]; offered {
		t.Fatal("a set.host box exists for a plugin whose declarations are not installed")
	}
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if strings.Contains(nm.flash, "removed") || strings.Contains(nm.flash, "dropped") {
		t.Errorf("flash = %q reports removals from an entry the form could not see into", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["ghost@abc123"]
	if saved.Set["host"] != "ghost.internal" || saved.Set["region"] != "eu" {
		t.Errorf("set = %v after an untouched save, want the file's statements kept — the "+
			"editor rewrote a block it had no boxes for", saved.Set)
	}
	if saved.Secrets["password"] != "kv:ghost-pass" {
		t.Errorf("secrets = %v, want kept", saved.Secrets)
	}
}

// A `set:` key nothing reads is dropped at save, with the receipt saying so.
//
// Such a key is usually left behind by a plugin rebuilt without it, it makes
// checkSet refuse the whole profile, and no box exists to clear it — the
// same shape as the endpoint keys under a coordinate, repaired the same way.
// Silently was how it went before the receipt: the rebuild dropped it and
// nothing said so.
func TestSavingDropsWhatNothingReadsWithAReceipt(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		Set: map[string]any{"schema": "public", "ancient": "left-behind"},
	})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	if _, offered := nm.form.bindings[profileSetPrefix+"ancient"]; offered {
		t.Fatal("a box exists for a key no declaration backs")
	}
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "dropped set.ancient") ||
		!strings.Contains(nm.flash, "nothing reads it") {
		t.Errorf("flash = %q, want the drop named with its reason", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if _, still := saved.Set["ancient"]; still {
		t.Error("the key nothing reads survived the save, and the profile stays refused over it")
	}
	if saved.Set["schema"] != "public" {
		t.Errorf("set.schema = %v, want the declared statement kept", saved.Set["schema"])
	}
}

// Moving the picker re-seeds the form on the environment it now names.
//
// The picker sits first because it changes what every other answer means —
// and until this, changing it changed nothing on screen: boxes kept the old
// environment's seeds and the derived marks were the only thing keeping the
// stale display from becoming the destination. One walk crosses every
// display rule at once: a typed answer survives each move, the endpoint box
// disappears under a coordinate and returns under a direct connection
// showing that environment's own host, and none of it comes back as a
// caller value.
func TestMovingThePickerReseedsTheForm(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"db": {Kube: "homelab/databases/svc/postgres:5432"},
		}},
		"staging": {Plugins: map[string]config.Connection{
			"db": {Set: map[string]any{"host": "staging.internal"}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	c := plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
				Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
			{Name: "database", Type: plugin.String, Config: "database", Help: "database"},
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db",
		Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40

	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	if got := *nm.form.bindings["host"]; got != "localhost" {
		t.Fatalf("host box = %q under the base configuration, want the declared default", got)
	}
	*nm.form.bindings["database"] = "mydb" // an answer the person typed

	// Down on the focused picker: — base configuration — becomes homelab,
	// and the same keypress rebuilds the form on it.
	model, _ = nm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	nm = model.(Model)
	if _, asked := nm.form.bindings["host"]; asked {
		t.Error("host still has a box under homelab's coordinate — the rebuild did not re-decide " +
			"what is asked")
	}
	if help := nm.form.fields[0].Help; !strings.Contains(help, "tunnel, which fills host") {
		t.Errorf("picker help = %q, want it naming what the forward fills", help)
	}
	if got := *nm.form.bindings["database"]; got != "mydb" {
		t.Fatalf("database box = %q after the move, want the typed answer kept", got)
	}
	if v, given := nm.form.values()["host"]; given {
		t.Errorf("host = %v came back as a caller value from a box that is not on screen", v)
	}

	// Down again: staging connects directly, so the box returns — showing
	// staging's own host, as a display rather than an answer.
	nm.form.form = startedForm(nm.form)
	model, _ = nm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	nm = model.(Model)
	host, asked := nm.form.bindings["host"]
	if !asked {
		t.Fatal("host has no box under a direct connection")
	}
	if *host != "staging.internal" {
		t.Errorf("host box = %q, want staging's own value seeded in", *host)
	}
	if v, given := nm.form.values()["host"]; given {
		t.Errorf("host = %v came back as a caller value — a re-seeded display outran the "+
			"environment it displays", v)
	}
	if got := *nm.form.bindings["database"]; got != "mydb" {
		t.Errorf("database box = %q after two moves, want the typed answer still kept", got)
	}
}
