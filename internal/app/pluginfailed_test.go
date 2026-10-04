package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
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

// Nothing failed, nothing said: the table is the one it always was.
func TestAPluginListWithNothingFailedHasNoSuchRow(t *testing.T) {
	withFailedPlugins(t)
	out, _, err := run(t, registry.New(), "plugin", "list", "-o", "json")
	if err != nil || strings.Contains(out, "failed to start") {
		t.Errorf("out = %q (%v)", out, err)
	}
}
