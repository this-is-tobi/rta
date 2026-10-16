package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/tui"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// What a configuration says about sections that does nothing, or that cannot be
// what it was meant to be: the one place `rta config check` and `rta doctor` ask.
//
// **A warning, and not a refusal, for all of it.** A section that is mentioned
// where no tile is in it is drawn as nothing, and a column count below zero is
// read as 0: the screen is the one it would have been without the line, and
// refusing the file over a cosmetic key would be the worse outage — the reasoning
// that reports a view nothing states instead of refusing the profile that names
// it (danglingViews). What the decoder can refuse it already does, with the line,
// for a section's `columns:` as for the dashboard's own: one that is not a number
// does not load. A negative one loads, on the dashboard's own key too, and is
// named here for all of them.

// sectionProblem is one thing in a block that states sections that does not do
// what it says, under the dotted path a person would look for it by.
type sectionProblem struct{ key, text string }

// sectionProblems holds every dashboard block own states — the `dashboard:` block,
// each view, and each profile's own screen — to what sections can say. whole is
// the configuration every file states together, which is where a profile is looked
// for: the profile whose connections name a group may be in another file than the
// view that states how it starts.
func sectionProblems(reg *registry.Registry, own, whole config.Config) []sectionProblem {
	type block struct {
		prefix string
		d      config.Dashboard
	}
	blocks := []block{{"dashboard", own.Dashboard}}
	for _, name := range slices.Sorted(maps.Keys(own.Dashboard.Views)) {
		blocks = append(blocks, block{"dashboard.views." + name, own.Dashboard.Views[name].Dashboard()})
	}
	for _, name := range own.ProfileNames() {
		if inline := own.Profiles[name].Dashboard.Inline; inline != nil {
			blocks = append(blocks, block{"profiles." + name + ".dashboard", inline.Dashboard()})
		}
	}
	out := make([]sectionProblem, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockSectionProblems(reg, b.prefix, b.d, whole)...)
	}
	return out
}

func blockSectionProblems(reg *registry.Registry, prefix string, d config.Dashboard, whole config.Config) []sectionProblem {
	var out []sectionProblem
	if d.Columns < 0 {
		out = append(out, sectionProblem{prefix + ".columns", notColumns(d.Columns)})
	}
	out = append(out, refreshProblems(reg, prefix, d)...)
	for _, list := range []struct {
		key   string
		tiles []config.Tile
	}{{"tiles", d.Tiles}, {"add", d.Add}} {
		for i, t := range list.tiles {
			if t.Section != "" && !config.ValidSectionName(t.Section) {
				out = append(out, sectionProblem{fmt.Sprintf("%s.%s[%d].section", prefix, list.key, i), notAName(t.Section)})
			}
		}
	}
	names := tui.SectionNames(reg, d, whole)
	for _, name := range slices.Sorted(maps.Keys(d.Sections)) {
		key := prefix + ".sections." + name
		switch {
		case !config.ValidSectionName(name):
			out = append(out, sectionProblem{key, notAName(name)})
		case !names[name]:
			out = append(out, sectionProblem{key, namesNoTile(name, names)})
		}
		if n := d.Sections[name].Columns; n < 0 {
			out = append(out, sectionProblem{key + ".columns", notColumns(n)})
		}
	}
	return out
}

func notAName(name string) string {
	return fmt.Sprintf("%q is not a name a section can have: at most %d characters, no control characters, "+
		"no space at either end", name, config.MaxSectionName)
}

func notColumns(n int) string {
	return fmt.Sprintf("columns %d is not a number of columns, and is read as 0: it takes a whole number from 0, "+
		"and 0 is the grid the rest of the screen is on", n)
}

// namesNoTile says that a `sections:` entry has nothing to state, and what the
// section names are on this screen, which is the fastest way to a typo.
func namesNoTile(name string, names map[string]bool) string {
	text := fmt.Sprintf("no tile of this screen is in a section called %q, so it states nothing", name)
	if len(names) == 0 {
		return text + " — `section:` on a tile entry, or `rta dashboard add --section`, puts one in it"
	}
	return text + " (the sections here are " + strings.Join(slices.Sorted(maps.Keys(names)), ", ") + ")"
}

// refreshProblems is every `refresh:` of one screen that cannot be what it says.
//
// The same three questions at every level, since the key is meant to work the
// same wherever it is written: can it be read, and, on a tile, can the capability
// run that often. A section's or the screen's value is a default for the tiles
// under it and is never reported for being faster than one capability's declared
// pace — that tile keeps its own, which is what a default is — while a tile's own
// is a demand, and a demand that cannot be met is said, with the pace it will get.
func refreshProblems(reg *registry.Registry, prefix string, d config.Dashboard) []sectionProblem {
	var out []sectionProblem
	readable := func(key, text string) {
		if _, err := config.ParseRefresh(text); err != nil {
			out = append(out, sectionProblem{key, "refresh " + err.Error()})
		}
	}
	readable(prefix+".refresh", d.Refresh)
	for _, name := range slices.Sorted(maps.Keys(d.Sections)) {
		readable(prefix+".sections."+name+".refresh", d.Sections[name].Refresh)
	}
	for _, list := range []struct {
		key   string
		tiles []config.Tile
	}{{"tiles", d.Tiles}, {"add", d.Add}} {
		for i, t := range list.tiles {
			key := fmt.Sprintf("%s.%s[%d].refresh", prefix, list.key, i)
			want, err := config.ParseRefresh(t.Refresh)
			if err != nil {
				out = append(out, sectionProblem{key, "refresh " + err.Error()})
				continue
			}
			c, ok := reg.Capability(t.ID)
			if !ok || want == 0 {
				continue
			}
			if floor, cut := plugin.ClampPace(c, want); cut {
				out = append(out, sectionProblem{key, fmt.Sprintf(
					"refresh %s is faster than %s runs: it re-runs no more often than every %s, which is what the tile does — "+
						"a configuration can make a pace slower than a capability's and never faster", t.Refresh, t.ID, config.FormatRefresh(floor))})
			}
		}
	}
	return out
}
