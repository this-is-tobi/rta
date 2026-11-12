package pluginconf

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/near"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Nearest is what a key nothing in a namespace reads was likely meant as. At
// most one of its answers is set.
type Nearest struct {
	// Renamed is the key that replaced it, by the plugin's own Plugin.Renamed,
	// and Help the replacing key's help.
	Renamed, Help string
	// Guess is a key one slip away from it.
	Guess string
	// Qualified is the key spelled with each capability that reads it.
	Qualified []string
}

// NearestKey is what key, read by nothing in a namespace, was likely meant as.
// The plugin's own rename comes first: a key an older file still carries is
// no typo, and only the plugin knows what it became. An entry of renamed is
// used only while key is read by no input and its replacement by some, so a
// stale entry misleads nobody. Then a key one slip away, then the key spelled
// with each capability that reads it.
//
// The one ladder for every block keyed by config key: a plugins: section,
// `rta config set`, and a profile's `set:` and `only:`. Each had its own copy,
// and only some of the copies knew to say which capability a key belongs to.
func NearestKey(renamed map[string]string, readers map[string][]plugin.Field, key string) Nearest {
	if to, ok := renamed[key]; ok && len(readers[key]) == 0 && len(readers[to]) > 0 {
		return Nearest{Renamed: to, Help: readers[to][0].Help}
	}
	keys := slices.Sorted(maps.Keys(readers))
	if guess := near.Word(key, keys); guess != "" {
		return Nearest{Guess: guess}
	}
	return Nearest{Qualified: near.Qualified(key, keys)}
}

// Hint words n for a reader who writes a key as spell spells it, naming ns as
// the plugin that reads the replacement. "" when n names nothing, for the
// caller's own last resort.
func (n Nearest) Hint(ns string, spell func(key string) string) string {
	switch {
	case n.Renamed != "":
		hint := ns + " reads " + spell(n.Renamed) + " in its place"
		if n.Help != "" {
			hint += ": " + n.Help
		}
		return hint
	case n.Guess != "":
		return "did you mean " + spell(n.Guess) + "?"
	case len(n.Qualified) > 0:
		spelled := make([]string, len(n.Qualified))
		for i, q := range n.Qualified {
			spelled[i] = spell(q)
		}
		return "it is spelled with the capability that reads it: " + strings.Join(spelled, " or ")
	}
	return ""
}

// Quoted spells a key as a quoted string, escaped, the way a section's report
// names one: the key is what a file says, and may hold anything.
func Quoted(key string) string { return strconv.Quote(key) }

// RenamedIn is the Renamed table of the plugin declared under ns, nil when
// none is.
func RenamedIn(plugins []plugin.Plugin, ns string) map[string]string {
	for _, p := range plugins {
		if p.Name == ns {
			return p.Renamed
		}
	}
	return nil
}

// TileWith is the refusal of a dashboard tile whose `with:` names an input c
// does not take, nil when every name is one it takes.
//
// Such a value was dropped on every refresh, and the tile ran without it: a
// tile written `with: {sslmode: verify-full}` against a plugin that renamed
// the input read at the plugin's default mode, under a panel that said
// nothing. It is refused in the panel instead, naming what the input was
// likely meant as: the input that reads the key the plugin renamed it to,
// when its name was that key, then one a slip away.
func TileWith(c plugin.Capability, renamed map[string]string, with map[string]any) *view.Error {
	declared := map[string]bool{}
	byKey := map[string]string{}
	names := make([]string, 0, len(c.Inputs))
	for _, f := range c.Inputs {
		declared[f.Name] = true
		names = append(names, f.Name)
		if f.Config != "" {
			byKey[f.Config] = f.Name
		}
	}
	sort.Strings(names)
	for _, name := range slices.Sorted(maps.Keys(with)) {
		if declared[name] {
			continue
		}
		verr := view.Errorf("core.dashboard.input.unknown",
			"the tile's `with:` gives %s %s, an input it does not take", c.ID, Quoted(name))
		switch to := byKey[renamed[name]]; {
		case to != "":
			return verr.WithHint(plugin.Namespace(c.ID) + " takes " + Quoted(to) + " in its place")
		case near.Word(name, names) != "":
			return verr.WithHint("did you mean " + Quoted(near.Word(name, names)) + "?")
		}
		return verr.WithHint("`rta explain " + c.ID + "` lists the inputs it takes")
	}
	return nil
}
