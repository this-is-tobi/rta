package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/render/theme/themetest"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One of each kind of grade, named so that no cell is mistaken for another.
var tileGrades = []struct {
	cell string
	kind theme.StatusKind
}{
	{"Ready", theme.StatusGood},
	{"Terminating", theme.StatusWarn},
	{"NotReady", theme.StatusBad},
	{"disabled", theme.StatusMuted},
	{"zzz", theme.StatusNeutral},
}

// A tile draws its status cells through the table the command line draws, and the
// ones too wide to be a grid in the box through its own lines, and both are held
// to the setting through the real render function, border and all.
func TestATilesCellsAreDrawnAsTheSettingSays(t *testing.T) {
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		cells := make([]string, len(tileGrades))
		for i, g := range tileGrades {
			cells[i] = g.cell
		}
		t.Run("a grid in the box", func(t *testing.T) {
			panel := renderTile(nodesTile(cells...), 60, 12, false, false)
			if !strings.Contains(plain(panel), "┬") {
				t.Fatalf("not drawn as a grid:\n%s", plain(panel))
			}
			for _, g := range tileGrades {
				if got, want := themetest.ColourBefore(t, panel, g.cell), themetest.Drawn(mode, g.kind); got != want {
					t.Errorf("%q is drawn in %q under status: %s, want %q", g.cell, got, mode, want)
				}
			}
		})
		t.Run("lines, for a table too wide for the box", func(t *testing.T) {
			wide := view.Table{Columns: []view.Column{
				{Name: "node"}, {Name: "state", Kind: view.KindStatus}, {Name: "disk", Kind: view.KindUsage},
				{Name: "detail"},
			}}
			usage := []string{"31%", "84%", "96%", "17%", "42%"}
			for i, g := range tileGrades {
				wide.Rows = append(wide.Rows, []string{fmt.Sprintf("node-%d", i), g.cell, usage[i],
					"a long sentence about why this node is the way it is, past any box"})
			}
			panel := renderTile(tile{cap: plugin.Capability{ID: "kube.node.list"}, view: wide}, 44, 12, false, false)
			if strings.Contains(plain(panel), "┬") {
				t.Fatalf("still a grid in a box that cannot hold one:\n%s", plain(panel))
			}
			for _, g := range tileGrades {
				if got, want := themetest.ColourBefore(t, panel, g.cell), themetest.Drawn(mode, g.kind); got != want {
					t.Errorf("%q is drawn in %q under status: %s, want %q", g.cell, got, mode, want)
				}
			}
			for cell, kind := range map[string]theme.StatusKind{
				"31%": theme.StatusGood, "84%": theme.StatusWarn, "96%": theme.StatusBad,
			} {
				if got, want := themetest.ColourBefore(t, panel, cell), themetest.Drawn(mode, kind); got != want {
					t.Errorf("%q is drawn in %q under status: %s, want %q", cell, got, mode, want)
				}
			}
		})
		t.Run("a page of facts", func(t *testing.T) {
			page := tile{cap: plugin.Capability{ID: "docker.overview"}, view: view.KeyValue{Pairs: []view.Pair{
				{Key: "daemon", Value: "did not answer — refused"},
				{Key: "nodes", Value: "could not be read — forbidden"},
				{Key: "api", Value: "ready"},
				{Key: "name", Value: "lab"},
			}}}
			panel := renderTile(page, 60, 10, false, false)
			for value, kind := range map[string]theme.StatusKind{
				"did not answer — refused": theme.StatusBad, "could not be read — forbidden": theme.StatusWarn,
				"ready": theme.StatusGood, "lab": theme.StatusNeutral,
			} {
				if got, want := themetest.ColourBefore(t, panel, value), themetest.Drawn(mode, kind); got != want {
					t.Errorf("%q is drawn in %q under status: %s, want %q", value, got, mode, want)
				}
			}
		})
	})
}

// The border says how bad the tile's answer is, in the two colours of the grades
// that ask for something, and is grey under "off". The grade itself does not
// move: a tile that is bad stays the one the header counts, and the cells still
// say it. A tile that is fine has never had a coloured border, so "alerts" draws
// the borders exactly as "all" does.
func TestATileBorderFollowsTheSettingAndKeepsWhatIsNotAGrade(t *testing.T) {
	grey := colourOf(t, theme.Border)
	primary := colourOf(t, theme.Key)
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		coloured := func(kind theme.StatusKind) string {
			if c := themetest.Drawn(mode, kind); c != "" {
				return c
			}
			return grey
		}
		for name, tc := range map[string]struct {
			tile tile
			want string
		}{
			"a bad tile":      {nodesTile("Ready", "NotReady"), coloured(theme.StatusBad)},
			"a warning tile":  {nodesTile("Ready", "Terminating"), coloured(theme.StatusWarn)},
			"bad beats warn":  {nodesTile("Terminating", "NotReady"), coloured(theme.StatusBad)},
			"a fine tile":     {nodesTile("Ready", "Ready"), grey},
			"nothing graded":  {tile{cap: plugin.Capability{ID: "x.facts"}, view: view.KeyValue{Pairs: []view.Pair{{Key: "a", Value: "b"}}}}, grey},
			"a failed run":    {tile{cap: plugin.Capability{ID: "db.status"}, err: view.Errorf("db.conn.refused", "nothing is listening")}, coloured(theme.StatusBad)},
			"calls waiting":   {waitingTile("2 waiting"), colourOf(t, theme.WarnText)},
			"bad and waiting": {waitingTile("2 waiting", view.Pair{Key: "daemon", Value: "did not answer — refused"}), mixedBorder(mode)},
		} {
			if got := borderOf(t, renderTile(tc.tile, 40, 8, false, false)); got != tc.want {
				t.Errorf("%s: the border is %s under status: %s, want %s", name, got, mode, tc.want)
			}
		}
		// Focus is the selection and not a grade.
		if got := borderOf(t, renderTile(nodesTile("NotReady"), 40, 8, true, false)); got != primary {
			t.Errorf("the selected tile's border is %s under status: %s, want the primary %s", got, mode, primary)
		}
		// The grade is the same under every setting, and so is what is drawn of it.
		if g, _ := tileGrade(nodesTile("Ready", "NotReady")); g != gradeBad {
			t.Errorf("the tile's grade is %v under status: %s, want bad: the setting colours a grade and does not change it", g, mode)
		}
	})
}

// waitingTile is a tile whose answer says calls are parked for a person, with
// any more facts beside it.
func waitingTile(waiting string, more ...view.Pair) tile {
	pairs := append([]view.Pair{{Key: waitingKey, Value: waiting}}, more...)
	return tile{cap: plugin.Capability{ID: "agent.overview"}, view: view.KeyValue{Pairs: pairs}}
}

// mixedBorder is what a tile that is bad and has calls waiting is drawn in: the
// bad colour while the setting draws it, and the waiting colour when it does not,
// because a parked consent request is a person's to answer and not a grade.
func mixedBorder(mode theme.Grades) string {
	if mode == theme.GradesOff {
		return themetest.ColourOf(theme.WarnText)
	}
	return themetest.ColourOf(theme.BadText)
}

// Folding hides detail and never a problem, and a folded header says the worst of
// its tiles in the grade's colour: amber and red under "alerts", and the same
// words in no colour under "off". The ok is the one that goes under "alerts".
func TestAFoldedHeaderIsDrawnAsTheSettingSays(t *testing.T) {
	headerOf := func(t *testing.T, tiles ...string) string {
		var configured []config.Tile
		for _, id := range tiles {
			configured = append(configured, config.Tile{ID: id + ".status", Section: "group"})
		}
		m := sectioned(t, config.Dashboard{
			Tiles:    configured,
			Sections: map[string]config.DashboardSection{"group": {Collapsed: true}},
		}, 120, 40)
		for _, line := range strings.Split(m.View().Content, "\n") {
			if strings.Contains(plain(line), "▸ group") {
				return line
			}
		}
		t.Fatalf("no header in\n%s", plain(m.View().Content))
		return ""
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for _, tc := range []struct {
			name  string
			tiles []string
			text  string
			kind  theme.StatusKind
		}{
			{"a bad tile", []string{"alpha", "beta", "gamma"}, "● 1 bad", theme.StatusBad},
			{"a warning tile", []string{"alpha", "beta"}, "● 1 warn", theme.StatusWarn},
			{"all fine", []string{"alpha", "epsilon", "zeta"}, "ok", theme.StatusGood},
		} {
			header := headerOf(t, tc.tiles...)
			if got, want := themetest.ColourBefore(t, header, tc.text), themetest.Drawn(mode, tc.kind); got != want {
				t.Errorf("%s: %q is drawn in %q under status: %s, want %q", tc.name, tc.text, got, mode, want)
			}
		}
	})
}

// The profiles pane writes the same words `rta profile list` does, ok, warn and
// invalid, and they are status words wherever they are drawn.
func TestTheProfilePanesStatusWordsFollowTheSetting(t *testing.T) {
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for word, tc := range map[string]struct {
			row  profileRow
			kind theme.StatusKind
		}{
			"ok":      {profileRow{}, theme.StatusGood},
			"warn":    {profileRow{note: "reads a store this machine has not made"}, theme.StatusWarn},
			"invalid": {profileRow{problem: "no such plugin"}, theme.StatusBad},
		} {
			if got, want := themetest.ColourBefore(t, profileStatus(tc.row), word), themetest.Drawn(mode, tc.kind); got != want {
				t.Errorf("profile status %q is drawn in %q under status: %s, want %q", word, got, mode, want)
			}
		}
		bands := connBands(profileRow{conns: []connRow{{key: "pg"}}})
		if len(bands) != 1 || len(bands[0].right) != 1 {
			t.Fatalf("bands = %+v", bands)
		}
		if got, want := themetest.ColourBefore(t, bands[0].right[0], "ok"), themetest.Drawn(mode, theme.StatusGood); got != want {
			t.Errorf("a connection's ok is drawn in %q under status: %s, want %q", got, mode, want)
		}
	})
}

// The safety class a result's header names is drawn in the status colours, read
// green, write amber and destructive red, and it is no grade: it says what the call
// may do, so it keeps its colour under every setting, and its word is still there.
func TestTheSafetyClassInAResultHeaderKeepsItsColourUnderEverySetting(t *testing.T) {
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for safety, kind := range map[plugin.Safety]theme.StatusKind{
			plugin.Read: theme.StatusGood, plugin.Write: theme.StatusWarn, plugin.Destructive: theme.StatusBad,
		} {
			m := New(registry.New(), config.Dashboard{}, nil)
			m.current = plugin.Capability{ID: "x.y", Safety: safety}
			m.result = resultMsg{cap: m.current, view: view.Text{Body: "done"}}
			meta := m.resultMeta()
			if got, want := themetest.ColourBefore(t, meta, string(safety)), themetest.Drawn(theme.GradesAll, kind); got != want {
				t.Errorf("%s is drawn in %q in the result header under status: %s, want %q", safety, got, mode, want)
			}
		}
	})
}

// The catalogue's and the search tile's permission column and the plugin inventory's
// reach label name the same three classes as the header does, and keep their colour
// under every setting for the same reason. Read is muted there, as it always was.
func TestTheSafetyClassOfTheCatalogueAndTheReachKeepsItsColourUnderEverySetting(t *testing.T) {
	want := map[plugin.Safety]string{
		plugin.Read:        themetest.ColourOf(theme.Subtle),
		plugin.Write:       themetest.Drawn(theme.GradesAll, theme.StatusWarn),
		plugin.Destructive: themetest.Drawn(theme.GradesAll, theme.StatusBad),
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for safety, colour := range want {
			c := plugin.Capability{ID: "x.y", Safety: safety}
			if got := themetest.ColourBefore(t, permissionLabel(c), string(safety)); got != colour {
				t.Errorf("the permission column draws %s in %q under status: %s, want %q", safety, got, mode, colour)
			}
			if got := themetest.ColourBefore(t, permissionCell(c, 40), string(safety)); got != colour {
				t.Errorf("the catalogue's cell draws %s in %q under status: %s, want %q", safety, got, mode, colour)
			}
			row := pluginRow{plugin: plugin.Plugin{Name: "x", Capabilities: []plugin.Capability{c}}}
			if got := themetest.ColourBefore(t, reachLabel(row, string(safety)), string(safety)); got != colour {
				t.Errorf("the reach label draws %s in %q under status: %s, want %q", safety, got, mode, colour)
			}
		}
	})
}

// The plugin inventory's words for whether code can be relied on, and its marker for a
// tile that is on, are no grades: approved, untrusted, failed to start and not run say
// whether rta runs an artifact, so they keep their colour under every setting.
func TestThePluginsPaneKeepsItsTrustAndStateWordsInColourUnderEverySetting(t *testing.T) {
	m := failedModel(t)
	for i := range m.plugins {
		switch m.plugins[i].plugin.Name {
		case "inside":
			m.plugins[i].tile, m.plugins[i].shown = "inside.go", true
		case "managed":
			m.plugins[i].decided = decidedTrust
		case "stray":
			m.plugins[i].decided = decidedUntrust
		}
	}
	words := map[string]theme.StatusKind{
		"[on]": theme.StatusGood, "approved": theme.StatusGood, "untrusted": theme.StatusWarn,
		"failed to start": theme.StatusBad, "not run": theme.StatusWarn,
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		pane := m.pluginsView()
		for word, kind := range words {
			if got, want := themetest.ColourBefore(t, pane, word), themetest.Drawn(theme.GradesAll, kind); got != want {
				t.Errorf("%q is drawn in %q in the plugins pane under status: %s, want %q", word, got, mode, want)
			}
		}
	})
}

// An edit of `status:` in the file reaches a dashboard that is already open, on
// the next tick, and an unchanged file repaints nothing.
func TestAnEditedStatusReachesAnOpenDashboard(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	if m.syncPalette(readStamps()); theme.Grading() != theme.GradesAll {
		t.Fatalf("a configuration that states none draws %s", theme.Grading())
	}
	write := func(mode string) {
		t.Helper()
		if err := config.Mutate(func(c config.Config) (config.Config, bool) {
			c.Theme = map[string]string{theme.GradesKey: mode}
			return c, true
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("alerts")
	if !m.syncPalette(readStamps()) || theme.Grading() != theme.GradesAlerts {
		t.Fatalf("status: alerts was not painted: %s", theme.Grading())
	}
	if m.syncPalette(readStamps()) {
		t.Error("an unchanged status was painted again")
	}
	write("off")
	if !m.syncPalette(readStamps()) || theme.Grading() != theme.GradesOff {
		t.Fatalf("status: off was not painted: %s", theme.Grading())
	}
	if err := config.Mutate(func(c config.Config) (config.Config, bool) {
		c.Theme = nil
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	if !m.syncPalette(readStamps()) || theme.Grading() != theme.GradesAll {
		t.Fatalf("taking status out of the file left %s in force", theme.Grading())
	}
}
