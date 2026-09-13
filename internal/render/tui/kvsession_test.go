package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// warmStore creates a store, then opens it from the TUI surface the way a
// submitted unlock form does — which is the only thing that starts a store
// session. The creating write opens nothing, so it is done first and
// elsewhere, exactly as a store made by `rta kv init` would have been.
func warmStore(t *testing.T, m Model) {
	t.Helper()
	set, ok := m.reg.Capability("kv.set")
	if !ok {
		t.Fatal("kv.set is not registered")
	}
	create := plugin.NewRequest(map[string]any{"key": "db-password", "value": "hunter2", "passphrase": "correct horse battery staple"}, false, false)
	if _, err := set.Run(context.Background(), create); err != nil {
		t.Fatal(err)
	}
	show, ok := m.reg.Capability("kv.show")
	if !ok {
		t.Fatal("kv.show is not registered")
	}
	open := plugin.NewRequest(map[string]any{"key": "db-password", "passphrase": "correct horse battery staple"}, false, false).
		WithSurface(plugin.SurfaceTUI)
	if _, err := show.Run(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if _, ok := kv.SessionPassphrase(); !ok {
		t.Fatal("a TUI unlock did not start a store session")
	}
}

func kvModel(t *testing.T) (Model, view.Table) {
	t.Helper()
	// The session is process state, which is the point of it — and what
	// makes one test's warm store the next test's stale premise.
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	reg := realRegistry(t)
	list, ok := reg.Capability("kv.list")
	if !ok {
		t.Fatal("kv.list is not registered")
	}
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.current = list
	m.lastValues = map[string]any{}
	m.row = 1
	tbl := view.Table{
		Columns: []view.Column{{Name: "Key"}, {Name: "Kind"}},
		Rows:    [][]string{{"api-token", "token"}, {"db-password", "password"}},
	}
	return m, tbl
}

// Once the store is unlocked in this TUI, walking into an entry does not ask
// for the passphrase again: the unlock pair is filled from the session and
// the action runs.
func TestAWarmSessionSkipsTheUnlockFormForANonDisclosingAction(t *testing.T) {
	m, tbl := kvModel(t)
	warmStore(t, m)
	show, _ := m.reg.Capability("kv.show")

	model, _ := m.runAction(capAction{key: "enter", label: "show", cap: show, src: srcRow}, tbl)
	next := model.(Model)
	if next.form != nil {
		asked := make([]string, 0, len(next.form.fields))
		for _, f := range next.form.fields {
			asked = append(asked, f.Name)
		}
		t.Fatalf("kv.show opened a form asking %v with the store already unlocked in this TUI", asked)
	}
	if got := next.lastValues["key"]; got != "db-password" {
		t.Errorf("key = %v, want the row under the cursor", got)
	}
}

// The unlock is what makes a reveal deliberate (kvreveal_test.go owns that
// argument), so a warm session does not buy a one-keystroke reveal: the
// capabilities whose result *is* the value still open the unlock form.
func TestADisclosingActionStillAsksForTheUnlockOnAWarmSession(t *testing.T) {
	m, tbl := kvModel(t)
	warmStore(t, m)
	get, _ := m.reg.Capability("kv.get")

	model, _ := m.runAction(capAction{key: "v", label: "reveal", cap: get, src: srcRow}, tbl)
	next := model.(Model)
	if next.form == nil {
		t.Fatal("a warm session turned `v` into a one-keystroke reveal")
	}
	if _, asked := next.form.bindings["passphrase"]; !asked {
		t.Error("the reveal's form no longer asks for the passphrase")
	}
}

// The three capabilities that hand the value to whoever is at the keyboard —
// on screen, on the clipboard, as shell exports — and nothing else. Pinned
// so a new kv capability that discloses is a conscious addition here.
func TestTheDisclosingCapabilitiesAreExactlyTheOnesThatHandOutTheValue(t *testing.T) {
	want := map[string]bool{"kv.get": true, "kv.copy": true, "kv.env": true}
	if len(discloses) != len(want) {
		t.Fatalf("discloses = %v, want exactly %v", discloses, want)
	}
	for id := range want {
		if !discloses[id] {
			t.Errorf("%s is missing from discloses", id)
		}
	}
}

// A store that is open in this process is a fact worth a glance: the header
// says so, with how long it stays that way, wherever the environment badge
// already lives.
func TestTheHeaderSaysWhileTheStoreIsUnlocked(t *testing.T) {
	m, _ := kvModel(t)
	if strings.Contains(m.dashboardView(), "unlocked") {
		t.Fatal("the header claims the store is unlocked before anything unlocked it")
	}
	warmStore(t, m)
	if header := m.dashboardView(); !strings.Contains(header, "store unlocked") {
		t.Errorf("the header does not say the store is unlocked:\n%s", header)
	}
}
