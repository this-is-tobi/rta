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
// page through the screens of every other. v walks the same list the other way
// round, as shift+tab does tab's, so one screen too far is one key back.

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

// walk presses key n times, V or v, and returns the screens it drew.
func walk(t *testing.T, m Model, key string, n int) []string {
	t.Helper()
	var seen []string
	for range n {
		m = pressV(t, m, key)
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
	got := walk(t, m, "V", 4)
	want := []string{"ops-apps", "ops-remote", "ops", "ops-apps"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("V walked %v, want %v: nothing of the other environment's screens", got, want)
	}
}

func TestLowerVWalksTheSameScreensTheOtherWayRoundAndWrapsAtTheTop(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{
		"prod":  linking("ops", "ops-apps", "ops-remote"),
		"other": linking("stage"),
	}, "prod")
	got := walk(t, m, "v", 4)
	want := []string{"ops-remote", "ops-apps", "ops", "ops-remote"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("v walked %v, want %v: the list V walks, reversed, from the first screen round to the last", got, want)
	}
}

func TestWithNoProfileOnOrNoneLinkedBothKeysStillWalkEveryScreen(t *testing.T) {
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
		forward := walk(t, m, "V", 6)
		wantForward := []string{"databases", "ops", "ops-apps", "ops-remote", "stage", ""}
		if strings.Join(forward, ",") != strings.Join(wantForward, ",") {
			t.Errorf("%s: V walked %v, want default and every view %v", name, forward, wantForward)
		}
		back := walk(t, m, "v", 6)
		wantBack := []string{"stage", "ops-remote", "ops-apps", "ops", "databases", ""}
		if strings.Join(back, ",") != strings.Join(wantBack, ",") {
			t.Errorf("%s: v walked %v, want the same screens the other way %v", name, back, wantBack)
		}
	}
}

// Wherever the walk starts, one key undoes the other: that is what makes one
// screen too far a single key to repair, and it is the property a pair of
// keys like these is for.
func TestVAndLowerVUndoEachOtherFromEveryScreen(t *testing.T) {
	for name, tc := range map[string]struct {
		profiles map[string]config.Profile
		on       string
		screens  int
	}{
		"three linked":     {map[string]config.Profile{"prod": linking("ops", "ops-apps", "ops-remote")}, "prod", 3},
		"two linked":       {map[string]config.Profile{"prod": linking("ops", "databases")}, "prod", 2},
		"every screen":     {map[string]config.Profile{}, "", 6},
		"linked, reversed": {map[string]config.Profile{"prod": linking("ops-remote", "ops-apps", "ops")}, "prod", 3},
	} {
		m := linkedModel(t, tc.profiles, tc.on)
		for range tc.screens {
			start := m.view
			if got := pressV(t, pressV(t, m, "V"), "v").view; got != start {
				t.Errorf("%s: V then v from %q drew %q", name, start, got)
			}
			if got := pressV(t, pressV(t, m, "v"), "V").view; got != start {
				t.Errorf("%s: v then V from %q drew %q", name, start, got)
			}
			m = pressV(t, m, "V")
		}
	}
}

func TestSwitchingEnvironmentFollowsTheLinksAndForgetsThePick(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{
		"prod":  linking("ops", "ops-apps"),
		"other": linking("stage", "databases"),
	}, "prod")
	m = pressV(t, m, "V")
	if m.view != "ops-apps" {
		t.Fatalf("view=%q after V", m.view)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "other"}); verr != nil {
		t.Fatal(verr)
	}
	m.syncActive()
	if m.view != "stage" {
		t.Errorf("after the switch the screen is %q, want the new environment's first", m.view)
	}
	if got := walk(t, m, "V", 2); strings.Join(got, ",") != "databases,stage" {
		t.Errorf("V after the switch walked %v, want the new environment's screens", got)
	}
	if got := walk(t, m, "v", 2); strings.Join(got, ",") != "databases,stage" {
		t.Errorf("v after the switch walked %v, want the new environment's screens", got)
	}
}

func TestAProfileLinkingOneScreenSaysHowToLinkAnother(t *testing.T) {
	for _, key := range []string{"V", "v"} {
		m := linkedModel(t, map[string]config.Profile{"prod": linking("ops")}, "prod")
		m = pressV(t, m, key)
		if m.view != "ops" || m.flashGuide == "" {
			t.Fatalf("%s: view=%q guide=%q, want the screen kept and a pointer", key, m.view, m.flashGuide)
		}
		for _, want := range []string{"prod", "links one screen", "rta profile set prod --dashboard ops --dashboard <view>"} {
			if !strings.Contains(m.flashGuide, want) {
				t.Errorf("%s: the guidance %q lacks %q", key, m.flashGuide, want)
			}
		}
	}
}

func TestAProfileThatWritesItsOwnScreenSaysToMoveItToWalkSeveral(t *testing.T) {
	own := config.Profile{
		Plugins:   map[string]config.Connection{"db": {}},
		Dashboard: config.ProfileDashboard{Inline: &config.View{Tiles: []config.Tile{{ID: "alpha.info"}}}},
	}
	for _, key := range []string{"V", "v"} {
		m := linkedModel(t, map[string]config.Profile{"prod": own}, "prod")
		m = pressV(t, m, key)
		if !strings.Contains(m.flashGuide, "writes its own screen") || !strings.Contains(m.flashGuide, "dashboard: views:") {
			t.Errorf("%s: guidance = %q, want the way to several screens", key, m.flashGuide)
		}
	}
}

func TestAProfileWhoseLinksAllDangleRefusesAndNamesTheCheck(t *testing.T) {
	for _, key := range []string{"V", "v"} {
		m := linkedModel(t, map[string]config.Profile{"prod": linking("ghost", "phantom")}, "prod")
		m = pressV(t, m, key)
		if m.flashBad == "" || !strings.Contains(m.flashBad, "nothing states") || !strings.Contains(m.flashBad, "rta doctor") {
			t.Errorf("%s: flashBad = %q, want the dangling links named and the check", key, m.flashBad)
		}
	}
}

// The links can be edited under a running session. The screen on is then not in
// the list, and a position counted from it would be made up: V enters the list
// at its first screen and v at its last, each from its own end.
func TestLinksEditedUnderASessionAreEnteredFromTheirOwnEnd(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	cfg := savedConfig(t)
	p := cfg.Profiles["prod"]
	p.Dashboard = config.ProfileDashboard{Names: []string{"ops-apps", "stage", "databases"}}
	cfg.Profiles["prod"] = p
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	if got := pressV(t, m, "V").view; got != "ops-apps" {
		t.Errorf("V from a screen the list no longer holds drew %q, want its first", got)
	}
	if got := pressV(t, m, "v").view; got != "databases" {
		t.Errorf("v from a screen the list no longer holds drew %q, want its last", got)
	}
}

func TestWalkFromGoesRoundBothEndsAndEntersAnUnlistedScreenFromItsOwnEnd(t *testing.T) {
	order := []string{"a", "b", "c"}
	for _, tc := range []struct {
		current string
		step    int
		want    string
	}{
		{"a", 1, "b"}, {"b", 1, "c"}, {"c", 1, "a"},
		{"c", -1, "b"}, {"b", -1, "a"}, {"a", -1, "c"},
		{"gone", 1, "a"}, {"gone", -1, "c"},
	} {
		if got := walkFrom(order, tc.current, tc.step); got != tc.want {
			t.Errorf("walkFrom(%v, %q, %d) = %q, want %q", order, tc.current, tc.step, got, tc.want)
		}
	}
	for _, step := range []int{1, -1} {
		if got := walkFrom([]string{"only"}, "only", step); got != "only" {
			t.Errorf("a list of one went to %q on step %d", got, step)
		}
	}
}

func TestVAndLowerVWriteNothingAndShareOneHintOnTheBar(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps")}, "prod")
	before := savedConfig(t)
	for _, key := range []string{"", "V", "v"} {
		if key != "" {
			m = pressV(t, m, key)
		}
		bar := plainFooter(m)
		if !strings.Contains(bar, "V/v view") {
			t.Errorf("after %q the bar lacks the V/v hint:\n%s", key, bar)
		}
		if strings.Contains(bar, "last view") {
			t.Errorf("after %q the bar still speaks of a last view, which v no longer goes to:\n%s", key, bar)
		}
	}
	if after := savedConfig(t); dashStamp("", after.Dashboard) != dashStamp("", before.Dashboard) ||
		after.Profiles["prod"].Dashboard.Describe() != before.Profiles["prod"].Dashboard.Describe() {
		t.Error("V or v wrote the config")
	}
}

// V and v are commands from the first frame, where the search line holds the
// selection: they used to be typed into the query there.
func TestVAndLowerVWorkFromTheColdSearchLine(t *testing.T) {
	m := linkedModel(t, map[string]config.Profile{"prod": linking("ops", "ops-apps", "ops-remote")}, "prod")
	m.selected = 0
	m = pressV(t, m, "V")
	if m.view != "ops-apps" || m.searchEditing || m.query != "" {
		t.Errorf("V on the search line: view=%q editing=%v query=%q", m.view, m.searchEditing, m.query)
	}
	m = pressV(t, m, "v")
	if m.view != "ops" || m.searchEditing || m.query != "" {
		t.Errorf("v on the search line: view=%q editing=%v query=%q", m.view, m.searchEditing, m.query)
	}
	m = pressV(t, m, "v")
	if m.view != "ops-remote" || m.searchEditing || m.query != "" {
		t.Errorf("v again on the search line: view=%q editing=%v query=%q, want the last screen", m.view, m.searchEditing, m.query)
	}
}
