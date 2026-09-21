package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `+` on a catalogue row or a search match: the TUI's own way onto the
// dashboard, writing the entry `rta dashboard add` writes. The three
// surfaces were meant to be one feature, and this was the half that was
// missing — the catalogue is where a person is already looking at the
// capability they want to glance at.

// addModel is pinnedModel with a db plugin that also declines to run
// unasked in one capability — the shape every kube, pg and s3 capability
// has, and the tile the key exists for — plus a plugin taking no
// connection, and a mutation.
func addModel(t *testing.T, cfg config.Config, dash config.Dashboard) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	cfg.Dashboard = dash
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	db := dbPlugin()
	db.Capabilities = append(db.Capabilities,
		plugin.Capability{ID: "db.slow", Summary: "a costly read", Safety: plugin.Read, NoPreview: true,
			Inputs: db.Capabilities[0].Inputs, Run: run},
		plugin.Capability{ID: "db.drop", Summary: "drop", Safety: plugin.Destructive, Run: run})
	clock := plugin.Plugin{Name: "clock", Summary: "c", Capabilities: []plugin.Capability{
		{ID: "clock.now", Summary: "now", Safety: plugin.Read, NoPreview: true, Run: run},
		{ID: "clock.at", Summary: "at", Safety: plugin.Read, NoPreview: true,
			Inputs: []plugin.Field{{Name: "zone", Type: plugin.String, Required: true}}, Run: run},
	}}
	reg := registry.New()
	for _, p := range []plugin.Plugin{db, clock} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	m := New(reg, dash, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return sized.(Model)
}

// inCatalogue puts the catalogue's cursor on one capability.
func inCatalogue(t *testing.T, m Model, id string) Model {
	t.Helper()
	m.mode = modeBrowse
	for i, it := range m.list.Items() {
		if ci, ok := it.(capItem); ok && ci.c.ID == id {
			m.list.Select(i)
			return m
		}
	}
	t.Fatalf("no catalogue row for %s", id)
	return m
}

func plus(m Model) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	return next.(Model), cmd
}

func TestPlusOnACatalogueRowAsksWhichConnectionThenAddsTheTile(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), "db.slow")
	m, _ = plus(m)
	if m.mode != modeAddPick || m.addPick == nil || m.addPick.cap.ID != "db.slow" {
		t.Fatalf("+ on a capability taking a connection: mode %v, picker %+v", m.mode, m.addPick)
	}
	if m.addPick.value != "" {
		t.Errorf("seeded choice = %q, want following the switch first", m.addPick.value)
	}
	if m.addPick.returnTo != modeBrowse {
		t.Errorf("returnTo = %v, want the catalogue the key was pressed on", m.addPick.returnTo)
	}
	choices := m.pinChoices(m.addPick.cap)
	got := make([]string, 0, len(choices))
	for _, ch := range choices {
		got = append(got, ch.ref)
	}
	if strings.Join(got, " ") != "prod prod/analytics staging" {
		t.Errorf("choices = %v, want prod as a whole, its labelled instance, and staging", got)
	}

	m.addPick.value = "prod"
	next, cmd := m.confirmAddPick()
	m = next.(Model)
	if m.mode != modeDashboard {
		t.Fatalf("after confirming, mode = %v, want the dashboard", m.mode)
	}
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.slow@prod db.slow@prod/analytics" {
		t.Errorf("tiles = %q, want the new entry expanded into prod's connections", got)
	}
	if m.tiles[m.selected].entryKey() != "db.slow@prod" {
		t.Errorf("selected = %s, want the tile just added", m.tiles[m.selected].key())
	}
	if !strings.Contains(m.flash, "added db.slow@prod") || !strings.Contains(m.flash, "one panel per db connection") {
		t.Errorf("flash = %q", m.flash)
	}
	if add := savedConfig(t).Dashboard.Add; len(add) != 1 || add[0].ID != "db.slow" || add[0].Profile != "prod" {
		t.Errorf("saved add = %v, want the one entry", add)
	}
	m = land(t, m, cmd)
	if tc := m.connFor(m.tiles[3]); tc.pending || tc.err != nil || tc.filled["host"] != "analytics.prod.internal" {
		t.Errorf("the new panel runs against %+v, want it bound at once", tc)
	}
}

// A capability taking no connection has nothing to ask: the entry is
// written at once.
func TestPlusOnACapabilityTakingNoConnectionAddsAtOnce(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), "clock.now")
	m, _ = plus(m)
	if m.mode != modeDashboard || m.addPick != nil {
		t.Fatalf("mode = %v, picker %v, want the dashboard with nothing asked", m.mode, m.addPick)
	}
	if got := strings.Join(tileKeys(m.tiles), " "); !strings.HasSuffix(got, "clock.now") {
		t.Errorf("tiles = %q, want clock.now on the dashboard", got)
	}
	if add := savedConfig(t).Dashboard.Add; len(add) != 1 || add[0].ID != "clock.now" || add[0].Profile != "" {
		t.Errorf("saved add = %v", add)
	}
}

// What `rta dashboard add` refuses, `+` refuses in the same words, and
// writes nothing.
func TestPlusRefusesWhatCannotBeATile(t *testing.T) {
	for id, want := range map[string]string{
		"db.drop":  "is not a read",
		"clock.at": "needs zone",
	} {
		m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), id)
		m, _ = plus(m)
		if m.mode != modeBrowse || !strings.Contains(m.flash, want) {
			t.Errorf("%s: mode %v flash %q, want the catalogue kept and %q", id, m.mode, m.flash, want)
		}
		if add := savedConfig(t).Dashboard.Add; len(add) != 0 {
			t.Errorf("%s: something was written: %v", id, add)
		}
	}
}

// `+` on the automatic tile somebody hid is the ask to see it again, not
// a second copy of it.
func TestPlusOnTheHiddenAutomaticTileShowsItAgain(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{Hidden: []string{"db.status"}}), "db.status")
	if got := strings.Join(tileKeys(m.tiles), " "); strings.Contains(got, "db.status") {
		t.Fatalf("tiles = %q, want db.status hidden to begin with", got)
	}
	m, _ = plus(m)
	if m.mode != modeAddPick {
		t.Fatalf("mode = %v, want the picker, since db.status takes a connection", m.mode)
	}
	next, _ := m.confirmAddPick()
	m = next.(Model)
	if !strings.Contains(m.flash, "showing db.status again") {
		t.Errorf("flash = %q", m.flash)
	}
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Hidden) != 0 || len(cfg.Dashboard.Add) != 0 {
		t.Errorf("hidden = %v add = %v, want the hide undone and nothing added", cfg.Dashboard.Hidden, cfg.Dashboard.Add)
	}
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status" {
		t.Errorf("tiles = %q", got)
	}
}

// From the search bar, `+` takes the highlighted match; cancelling the
// picker goes back to the dashboard it was pressed on.
func TestPlusFromTheSearchBarAddsTheHighlightedMatch(t *testing.T) {
	m := addModel(t, twoInstanceConfig(), config.Dashboard{})
	m.searchEditing, m.query = true, "db.slow"
	m, _ = plus(m)
	if m.mode != modeAddPick || m.addPick.cap.ID != "db.slow" || m.addPick.returnTo != modeDashboard {
		t.Fatalf("mode %v picker %+v", m.mode, m.addPick)
	}
	if m.searchEditing || m.query != "" {
		t.Error("the query survived the key that spent it")
	}
	gen := m.tickGen
	next, cmd := m.closeAddPick()
	m = next.(Model)
	if m.mode != modeDashboard || m.tickGen == gen || cmd == nil {
		t.Errorf("cancelling: mode %v, refresh restarted %v", m.mode, m.tickGen != gen)
	}
	if add := savedConfig(t).Dashboard.Add; len(add) != 0 {
		t.Errorf("cancelling wrote %v", add)
	}
}

// On the dashboard itself, `+` is where a person looks for "add a tile":
// it opens the catalogue to pick from, and says so.
func TestPlusOnTheDashboardOpensTheCatalogue(t *testing.T) {
	m := addModel(t, twoInstanceConfig(), config.Dashboard{})
	m, _ = plus(m)
	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want the catalogue", m.mode)
	}
	if !strings.Contains(m.flash, "+ on its row") {
		t.Errorf("flash = %q, want it to say what + does there", m.flash)
	}
	if add := savedConfig(t).Dashboard.Add; len(add) != 0 {
		t.Errorf("opening the catalogue wrote %v", add)
	}
}

// A tile already there — added, stated, or the automatic one on screen —
// is not written twice, and the key says which it is.
func TestPlusOnATileAlreadyThereWritesNothing(t *testing.T) {
	cases := map[string]struct {
		dash config.Dashboard
		id   string
		pick string
		want string
	}{
		"added": {config.Dashboard{Add: []config.Tile{{ID: "db.slow", Profile: "prod"}}},
			"db.slow", "prod", "db.slow@prod is already on the dashboard"},
		"stated": {config.Dashboard{Tiles: []config.Tile{{ID: "clock.now"}}},
			"clock.now", "", "clock.now is already on the dashboard"},
		"automatic": {config.Dashboard{},
			"db.status", "", "db.status is already on the automatic dashboard"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := inCatalogue(t, addModel(t, twoInstanceConfig(), tc.dash), tc.id)
			before := savedConfig(t).Dashboard
			m, _ = plus(m)
			if m.mode == modeAddPick {
				m.addPick.value = tc.pick
				next, _ := m.confirmAddPick()
				m = next.(Model)
			}
			if m.mode != modeDashboard || m.flash != tc.want {
				t.Errorf("mode %v flash %q, want the dashboard and %q", m.mode, m.flash, tc.want)
			}
			if after := savedConfig(t).Dashboard; dashStamp(after) != dashStamp(before) {
				t.Errorf("the file changed: %+v -> %+v", before, after)
			}
		})
	}
}

// Resizing while the picker is open must not panic, and must keep it:
// fitAddPick is the same shape as fitCopyPick, refitted from the same
// place.
func TestResizingWhileTheAddPickerIsOpenKeepsIt(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), "db.slow")
	m, _ = plus(m)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if rm := resized.(Model); rm.mode != modeAddPick || rm.addPick == nil {
		t.Errorf("after a resize: mode %v, picker %v", rm.mode, rm.addPick)
	}
}

// shift+enter on the picker accepts the highlighted connection — the
// switch, seeded first — so the shortcut means the same thing on every
// form-shaped screen.
func TestShiftEnterOnTheAddPickerAcceptsTheHighlightedChoice(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), "db.slow")
	m, _ = plus(m)
	next, _ := m.Update(shiftEnter)
	m = next.(Model)
	if m.mode != modeDashboard || !strings.Contains(m.flash, "added db.slow") {
		t.Fatalf("mode %v flash %q, want the tile added following the switch", m.mode, m.flash)
	}
	if add := savedConfig(t).Dashboard.Add; len(add) != 1 || add[0].ID != "db.slow" || add[0].Profile != "" {
		t.Errorf("saved add = %v", add)
	}
}
