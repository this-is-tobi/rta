package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// An environment's screen written in its entry is shown by the profiles pane,
// and the editors that rewrite the entry carry it through: the form collects a
// name, a note, a deadline and a colour, and a screen is none of them.

func ownScreenConfig() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"staging": {
			Plugins: map[string]config.Connection{"db": conn(map[string]any{"host": "staging.internal"})},
			Dashboard: config.ProfileDashboard{Inline: &config.View{
				Add:    []config.Tile{{ID: "db.status"}, {ID: "db.status", Profile: "staging"}},
				Hidden: []string{"sys.overview"},
			}},
		},
		"prod": {
			Plugins:   map[string]config.Connection{"db": conn(map[string]any{"host": "prod.internal"})},
			Dashboard: config.ProfileDashboard{Names: []string{"ops"}},
		},
	}}
}

func TestTheProfilesPaneSaysWhatScreenAnEnvironmentShows(t *testing.T) {
	m := profileModel(t, ownScreenConfig())
	lines := map[string]string{}
	for _, row := range m.profiles {
		lines[row.name] = profileCovers(row)
	}
	if got := lines["staging"]; !strings.Contains(got, "own screen, 2 tiles") {
		t.Errorf("staging's line = %q", got)
	}
	if got := lines["prod"]; !strings.Contains(got, "screen ops") || strings.Contains(got, "own screen") {
		t.Errorf("prod's line = %q", got)
	}
	if got := profileCovers(profileRow{name: "x", p: config.Profile{
		Plugins: map[string]config.Connection{"db": {}}}}); strings.Contains(got, "screen") {
		t.Errorf("an environment with no screen says one: %q", got)
	}
}

// The line is cut at the edge of the pane, and what an environment's connections
// are called, with their pins, is as long as the environment has connections: the
// screen came after them and was the part that fell off, on the environment that
// needed it said most. It comes first, so it is there whatever the width.
func TestTheScreenIsOnTheLineBeforeTheConnectionsThatMayCutItOff(t *testing.T) {
	m := profileModel(t, ownScreenConfig())
	var staging profileRow
	for _, row := range m.profiles {
		if row.name == "staging" {
			staging = row
		}
	}
	line := plain(profileCovers(staging))
	if screen, conn := strings.Index(line, "own screen"), strings.Index(line, "db"); screen < 0 || conn < 0 || screen > conn {
		t.Errorf("the screen is not ahead of the connections: %q", line)
	}
}

// Editing the environment must not drop the screen it carries: it is written one
// level down from where the form's fields are, and a form that rebuilt the entry
// from them would empty it in a keystroke.
func TestEditingAProfileKeepsItsOwnScreen(t *testing.T) {
	m := profileModel(t, ownScreenConfig())
	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileNoteField] = "the shared one"
	_, _ = nm.saveProfileForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	p := onDisk.Profiles["staging"]
	if p.Note != "the shared one" {
		t.Errorf("note = %q", p.Note)
	}
	if p.Dashboard.Inline == nil || len(p.Dashboard.Inline.Add) != 2 || len(p.Dashboard.Inline.Hidden) != 1 {
		t.Errorf("the form dropped the screen: %+v", p.Dashboard)
	}
	if onDisk.Profiles["prod"].Dashboard.Describe() != "ops" {
		t.Errorf("another environment's view was lost: %+v", onDisk.Profiles["prod"].Dashboard)
	}
}

// The same for the form one level in, which rewrites a connection of the entry.
func TestEditingAConnectionKeepsTheScreenOfItsEnvironment(t *testing.T) {
	m := profileModel(t, ownScreenConfig())
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	*nm.form.bindings[profileSetPrefix+"host"] = "moved.internal"
	_, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	p := onDisk.Profiles["staging"]
	if p.Plugins["db"].Set["host"] != "moved.internal" {
		t.Errorf("host = %v", p.Plugins["db"].Set["host"])
	}
	if p.Dashboard.Inline == nil || len(p.Dashboard.Inline.Add) != 2 {
		t.Errorf("the connection form dropped the screen: %+v", p.Dashboard)
	}
}

// A rename moves the entry, so the screen written in it moves too — and with it
// the name of the view, which is the profile's.
func TestRenamingAProfileCarriesItsOwnScreenAndItsViewName(t *testing.T) {
	m := profileModel(t, ownScreenConfig())
	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileNameField] = "staging-2"
	_, _ = nm.saveProfileForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if block, ok := onDisk.Block("staging-2"); !ok || len(block.Add) != 2 || !onDisk.InProfile("staging-2") {
		t.Errorf("the renamed entry's screen = %+v, %v", block, ok)
	}
	if onDisk.HasView("staging") {
		t.Errorf("the old name still addresses a screen: %v", onDisk.ViewNames())
	}
}
