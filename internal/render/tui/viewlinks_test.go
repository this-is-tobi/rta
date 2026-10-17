package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
)

// A profile links the views that belong to its environment, and V walks those
// and no others while it is switched on: an SRE on one environment does not
// page through the screens of every other, and `v` goes back to the one before
// like `cd -`.

func linkedModel(t *testing.T, profiles map[string]config.Profile, switchedOn string) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	cfg := config.Config{Profiles: profiles}
	for _, name := range []string{"databases", "ops", "ops-apps", "ops-remote", "stage"} {
		cfg.SetBlock(name, config.Dashboard{Tiles: []config.Tile{{ID: "alpha.info"}}})
	}
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: switchedOn}); verr != nil {
		t.Fatal(verr)
	}
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	out := sized.(Model)
	out.selected = 1
	// The screen the environment draws on arrival, as syncActive lands on it.
	out.active = switchedOn
	want := cfg.ViewFor(switchedOn)
	out.view, out.dash = want, cfg.TrustedDashboardFor(want)
	out.rebuildTiles()
	return out
}

func linking(names ...string) config.Profile {
	return config.Profile{
		Plugins:   map[string]config.Connection{"db": {}},
		Dashboard: config.ProfileDashboard{Names: names},
	}
}

func pressV(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	return next.(Model)
}

// walk presses V n times and returns the screens it drew.
func walk(t *testing.T, m Model, n int) []string {
	t.Helper()
	var seen []string
	for range n {
		m = pressV(t, m, "V")
		seen = append(seen, m.view)
	}
	return seen
}

func TestVWalksOnlyTheScreensTheSwitchedOnProfileLinks(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{
		"prod":  linking("ops", "ops-apps", "ops-remote"),
		"other": linking("stage"),
	}, "prod")
	if m.view != "ops" {
		t.Fatalf("the profile's first view is not the one drawn: %q", m.view)
	}
	got := walk(t, m, 4)
	want := []string{"ops-apps", "ops-remote", "ops", "ops-apps"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("V walked %v, want %v: nothing of the other environment's screens", got, want)
	}
}

func TestWithNoProfileOnOrNoneLinkedVStillWalksEveryScreen(t *testing.T) {
	for name, profiles := range map[string]map[string]config.Profile{
		"nothing switched on":     {"prod": linking("ops")},
		"a profile linking none":  {"loose": linking()},
		"a profile not in config": {"prod": linking("ops")},
	} {
		on := ""
		if name == "a profile linking none" {
			on = "loose"
		}
		m := linkedModel(t, profiles, on)
		got := walk(t, m, 6)
		want := []string{"databases", "ops", "ops-apps", "ops-remote", "stage", ""}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: V walked %v, want default and every view %v", name, got, want)
		}
	}
}

func TestSwitchingEnvironmentFollowsTheLinksAndForgetsThePick(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{
		"prod":  linking("ops", "ops-apps"),
		"other": linking("stage", "databases"),
	}, "prod")
	m = pressV(t, m, "V")
	if m.view != "ops-apps" || !m.hasLast {
		t.Fatalf("view=%q hasLast=%v after V", m.view, m.hasLast)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "other"}); verr != nil {
		t.Fatal(verr)
	}
	m.syncActive()
	if m.view != "stage" || m.hasLast {
		t.Errorf("after the switch the screen is %q (last remembered: %v), want the new environment's first and no memory", m.view, m.hasLast)
	}
	if got := walk(t, m, 2); strings.Join(got, ",") != "databases,stage" {
		t.Errorf("V after the switch walked %v, want the new environment's screens", got)
	}
}

func TestAProfileLinkingOneScreenSaysHowToLinkAnother(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops")}, "prod")
	m = pressV(t, m, "V")
	if m.view != "ops" || m.flashGuide == "" {
		t.Fatalf("view=%q guide=%q, want the screen kept and a pointer", m.view, m.flashGuide)
	}
	for _, want := range []string{"prod", "links one screen", "rta profile set prod --dashboard ops --dashboard <view>"} {
		if !strings.Contains(m.flashGuide, want) {
			t.Errorf("the guidance %q lacks %q", m.flashGuide, want)
		}
	}
}

func TestAProfileThatWritesItsOwnScreenSaysToMoveItToWalkSeveral(t *testing.T) {
	own := config.Profile{
		Plugins:   map[string]config.Connection{"db": {}},
		Dashboard: config.ProfileDashboard{Inline: &config.View{Tiles: []config.Tile{{ID: "alpha.info"}}}},
	}
	m := linkedModel(t, map[string]config.Profile{"prod": own}, "prod")
	m = pressV(t, m, "V")
	if !strings.Contains(m.flashGuide, "writes its own screen") || !strings.Contains(m.flashGuide, "dashboard: views:") {
		t.Errorf("guidance = %q, want the way to several screens", m.flashGuide)
	}
}

func TestAProfileWhoseLinksAllDangleRefusesAndNamesTheCheck(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ghost", "phantom")}, "prod")
	m = pressV(t, m, "V")
	if m.flashBad == "" || !strings.Contains(m.flashBad, "nothing states") || !strings.Contains(m.flashBad, "rta doctor") {
		t.Errorf("flashBad = %q, want the dangling links named and the check", m.flashBad)
	}
}

func TestVGoesBackAndForthBetweenTwoScreensLikeCdDash(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps", "ops-remote")}, "prod")
	m = pressV(t, m, "V") // ops-apps, last ops
	m = pressV(t, m, "V") // ops-remote, last ops-apps
	steps := []string{"ops-apps", "ops-remote", "ops-apps", "ops-remote"}
	for i, want := range steps {
		m = pressV(t, m, "v")
		if m.view != want {
			t.Fatalf("v #%d drew %q, want %q", i+1, m.view, want)
		}
	}
}

func TestVOnTheDefaultScreenGoesBackToItToo(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{}, "")
	m = pressV(t, m, "V") // databases, last default
	if m.view != "databases" {
		t.Fatalf("V drew %q", m.view)
	}
	m = pressV(t, m, "v")
	if m.view != "" {
		t.Errorf("v drew %q, want the dashboard: block", m.view)
	}
	m = pressV(t, m, "v")
	if m.view != "databases" {
		t.Errorf("v again drew %q, want the toggle back", m.view)
	}
}

func TestVWithNoEarlierScreenRefusesAndChangesNothing(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	before := fingerprint(m)
	after := pressV(t, m, "v")
	if after.flashBad == "" || !strings.Contains(after.flashBad, "no earlier screen") {
		t.Errorf("flashBad = %q, want a refusal that points at V", after.flashBad)
	}
	after.flash, after.flashBad = m.flash, m.flashBad
	if fingerprint(after) != before || after.view != m.view {
		t.Errorf("a refused v changed the screen or the model")
	}
}

func TestTheLastViewIsGoneWhenTheProfileNoLongerLinksIt(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	m = pressV(t, m, "V") // ops-apps, last ops
	cfg := savedConfig(t)
	p := cfg.Profiles["prod"]
	p.Dashboard = config.ProfileDashboard{Names: []string{"ops-apps", "databases"}}
	cfg.Profiles["prod"] = p
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	m = pressV(t, m, "v")
	if !strings.Contains(m.flashBad, "gone") || m.hasLast {
		t.Errorf("flashBad=%q hasLast=%v, want the memory dropped and said", m.flashBad, m.hasLast)
	}
	if m.view != "ops-apps" {
		t.Errorf("the screen moved to %q on a refused v", m.view)
	}
}

func TestVWritesNothingAndTheBarOffersLastViewOnlyOnceThereIsOne(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	before := savedConfig(t)
	if bar := plainFooter(m); strings.Contains(bar, "last view") {
		t.Errorf("the bar offers v before any screen was left:\n%s", bar)
	}
	m = pressV(t, m, "V")
	if bar := plainFooter(m); !strings.Contains(bar, "v last view") {
		t.Errorf("the bar does not offer v once a screen was left:\n%s", bar)
	}
	if after := savedConfig(t); dashStamp("", after.Dashboard) != dashStamp("", before.Dashboard) ||
		after.Profiles["prod"].Dashboard.Describe() != before.Profiles["prod"].Dashboard.Describe() {
		t.Error("V wrote the config")
	}
}

// V and v are commands from the first frame, where the search line holds the
// selection: they used to be typed into the query there.
func TestVAndLowerVWorkFromTheColdSearchLine(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	m.selected = 0
	m = pressV(t, m, "V")
	if m.view != "ops-apps" || m.searchEditing || m.query != "" {
		t.Errorf("V on the search line: view=%q editing=%v query=%q", m.view, m.searchEditing, m.query)
	}
	m = pressV(t, m, "v")
	if m.view != "ops" || m.searchEditing || m.query != "" {
		t.Errorf("v on the search line: view=%q editing=%v query=%q", m.view, m.searchEditing, m.query)
	}
}
