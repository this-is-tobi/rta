package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func withFailedPlugins(t *testing.T, fs ...pluginhost.Failed) {
	t.Helper()
	prev := failedPluginsFound
	t.Cleanup(func() { failedPluginsFound = prev })
	SetFailedPlugins(fs)
}

var helloFailed = pluginhost.Failed{
	Name: "hello", Path: "/usr/local/bin/rta-plugin-hello", Digest: strings.Repeat("a", 64),
	Reason: "exited before its handshake: exit status 1 — the plugin wrote: hello stand-in",
	Remedy: "`rta plugin untrust hello` stops rta launching it",
}

// An approved plugin that cannot start was reported at startup and then
// missing from the inventory, where it looked like one that was never
// installed. It has its row, with the reason and the way out beside it, and
// the status is one the palette draws as an error.
func TestAPluginThatFailedToStartHasARowInTheInventory(t *testing.T) {
	withFailedPlugins(t, helloFailed)
	out, errOut, err := run(t, registry.New(), "plugin", "list", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	var tab struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &tab); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(tab.Rows) != 1 {
		t.Fatalf("rows = %v, want the one plugin", tab.Rows)
	}
	row := tab.Rows[0]
	if row[0] != "hello" || row[2] != "failed to start" {
		t.Errorf("row = %v, want hello and failed to start", row)
	}
	if row[4] != "found on $PATH" {
		t.Errorf("source = %q, want where the artifact is: /usr/local/bin is not rta's store", row[4])
	}
	for _, want := range []string{"exited before its handshake: exit status 1", "hello stand-in", "`rta plugin untrust hello`"} {
		if !strings.Contains(row[3], want) {
			t.Errorf("detail = %q, want %q", row[3], want)
		}
	}
}

// A plugin the system root provides has no command the operator can type, so
// its row says why it failed and offers none.
func TestAFailedPluginWithNoWayOutOffersNone(t *testing.T) {
	f := helloFailed
	f.Remedy = ""
	if got := failedPluginDetail(f); got != f.Reason {
		t.Errorf("detail = %q, want the reason alone", got)
	}
}

func TestDoctorKeepsARowForAPluginThatFailedToStart(t *testing.T) {
	withFailedPlugins(t, helloFailed)
	var got [][3]string
	doctorFailedPlugins(func(check, status, detail string) { got = append(got, [3]string{check, status, detail}) })
	if len(got) != 1 {
		t.Fatalf("rows = %v, want one", got)
	}
	check, status, detail := got[0][0], got[0][1], got[0][2]
	if check != "plugin hello" || status != "warn" {
		t.Errorf("row = %q %q, want `plugin hello` as a warning", check, status)
	}
	for _, want := range []string{"/usr/local/bin/rta-plugin-hello", "aaaaaaaaaaaa", "failed to start", "hello stand-in",
		"`rta plugin untrust hello`"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	}
}

// "Which of these did I install" is answered from the table. The twenty plugins
// compiled into the binary, the ones rta installed and the ones it found on
// $PATH were rows of one kind, so after `rta plugin install pg` the pg row was
// one of twenty-one, while `rta init` said none was installed and the TUI's
// inventory had banded them all along. The Source column says it in the TUI's
// words, for the plugins that are not running too.
func TestThePluginListSaysWhereEachPluginCameFrom(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	system := t.TempDir()
	t.Setenv("RTA_SYSTEM_DIR", system)
	const digest = "1a2b3c4d5e6f7890aabbccddeeff00112233445566778899aabbccddeeff0011"
	inStore := filepath.Join(plugindist.StoreDir(), "pg", digest, "rta-plugin-pg")
	// Where Identify says an image's plugin is: its real path, which on a
	// machine whose temporary directory is a link is not the spelling the root
	// was named with.
	real, err := filepath.EvalSymlinks(system)
	if err != nil {
		t.Fatal(err)
	}
	inRoot := filepath.Join(real, "plugins", "store", "kube", digest, "rta-plugin-kube")

	reg := testRegistry(t)
	for name, path := range map[string]string{
		"managed": inStore, "imaged": inRoot, "stray": "/usr/local/bin/rta-plugin-stray",
	} {
		if err := reg.RegisterFrom(plugin.Plugin{Name: name, Summary: name,
			Capabilities: []plugin.Capability{{ID: name + ".status", Summary: "status", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }}}},
			registry.Origin{Path: path, Digest: digest}); err != nil {
			t.Fatal(err)
		}
	}
	failed := helloFailed
	failed.Path = inStore
	withFailedPlugins(t, failed)
	prev := untrustedPluginsFound
	t.Cleanup(func() { untrustedPluginsFound = prev })
	SetUntrustedPlugins([]pluginhost.Untrusted{{Name: "waiting", Path: "/opt/tools/rta-plugin-waiting", Digest: digest}})

	table, ok := pluginsView(reg).(view.Table)
	if !ok || table.Columns[len(table.Columns)-1].Name != "Source" {
		t.Fatalf("the inventory has no Source column: %+v", table.Columns)
	}
	got := map[string]string{}
	for _, row := range table.Rows {
		got[row[0]] = row[len(row)-1]
	}
	for name, want := range map[string]string{
		"demo":    "built in",
		"managed": "installed by rta",
		"imaged":  "in the system root",
		"stray":   "found on $PATH",
		"hello":   "installed by rta",
		"waiting": "found on $PATH",
	} {
		if got[name] != want {
			t.Errorf("%s: source = %q, want %q (every row: %v)", name, got[name], want, got)
		}
	}
}

// A stock install is built in all the way down, and a column that says so on
// every row separates nothing: the TUI draws no band for it either. The table
// is then the one it always was, and when something is not built in the column
// comes last, so what a script reads by position stays where it was.
func TestThePluginListLeavesOutASourceThatTellsNothingApart(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	names := func(reg *registry.Registry) []string {
		table, ok := pluginsView(reg).(view.Table)
		if !ok {
			t.Fatal("the inventory is not a table")
		}
		var out []string
		for _, c := range table.Columns {
			out = append(out, c.Name)
		}
		return out
	}

	if got, want := strings.Join(names(testRegistry(t)), ","), "Plugin,Capabilities,Can,Summary"; got != want {
		t.Errorf("a list of built-ins has columns %s, want %s", got, want)
	}

	reg := testRegistry(t)
	if err := reg.RegisterFrom(plugin.Plugin{Name: "stray", Summary: "stray",
		Capabilities: []plugin.Capability{{ID: "stray.status", Summary: "status", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }}}},
		registry.Origin{Path: "/usr/local/bin/rta-plugin-stray", Digest: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(names(reg), ","), "Plugin,Capabilities,Can,Summary,Source"; got != want {
		t.Errorf("a list with a plugin from $PATH has columns %s, want %s", got, want)
	}
}

// Nothing failed, nothing said: the table is the one it always was.
func TestAPluginListWithNothingFailedHasNoSuchRow(t *testing.T) {
	withFailedPlugins(t)
	out, _, err := run(t, registry.New(), "plugin", "list", "-o", "json")
	if err != nil || strings.Contains(out, "failed to start") {
		t.Errorf("out = %q (%v)", out, err)
	}
}
