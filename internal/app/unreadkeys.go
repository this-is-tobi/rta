package app

import (
	"maps"
	"slices"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// unreadKey is one key the operator's configuration states for a plugin that
// a build of it does not read: where it is stated, the key, and the key the
// build reads in its place, "" when no rename says.
type unreadKey struct {
	where, key, instead string
}

// unreadKeysFor is every key cfg states for plugin ns that a build reading
// readers does not read: a plugins: section under any heading, and the set:
// and only: of each profile's connections to it, in that order and sorted.
// renamed is what the build reads in place of an old key (NearestKey).
//
// Each one stops the plugin, or the profile, until it is fixed
// (pluginconf.Resolve, profile.Lookup). This is for the two moments the
// operator is already reading what changed for the plugin, a move between
// builds and a repin, so the key and what to write instead are named there
// rather than at the next call.
func unreadKeysFor(cfg config.Config, ns string, readers map[string][]plugin.Field, renamed map[string]string) []unreadKey {
	var out []unreadKey
	add := func(where, key string) {
		if len(readers[key]) > 0 {
			return
		}
		out = append(out, unreadKey{where: where, key: key,
			instead: pluginconf.NearestKey(renamed, readers, key).Renamed})
	}
	for _, heading := range slices.Sorted(maps.Keys(cfg.Plugins)) {
		if config.PluginNamespace(heading) != ns {
			continue
		}
		flat, _ := config.FlatSection(cfg.Plugins[heading])
		for _, key := range slices.Sorted(maps.Keys(flat)) {
			add("`plugins."+heading+"`", key)
		}
	}
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		for _, entry := range p.PluginKeys() {
			if config.PluginNamespace(entry) != ns {
				continue
			}
			conn := p.Plugins[entry]
			for _, key := range slices.Sorted(maps.Keys(conn.Set)) {
				add("profile `"+name+"`'s `set:` under "+entry, key)
			}
			for _, key := range slices.Sorted(maps.Keys(conn.Only)) {
				add("profile `"+name+"`'s `only:` under "+entry, key)
			}
		}
	}
	return out
}

// unreadKeyPairs is a move's receipt line for each key the operator's
// configuration states that the build moved to does not read. Both builds'
// renames are asked: the one moved to says what an old key became, and the
// one moved off, read backwards, what a key it introduced was before, so a
// move back from `tls` is told `sslmode`.
func unreadKeyPairs(up plugindist.Upgraded, dryRun bool) []view.Pair {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	renamed := maps.Clone(up.Declared.Renamed)
	if renamed == nil {
		renamed = map[string]string{}
	}
	for old, now := range up.FromDeclared.Renamed {
		if _, said := renamed[now]; !said {
			renamed[now] = old
		}
	}
	verb := "does not read"
	if dryRun {
		verb = "will not read"
	}
	var pairs []view.Pair
	for _, u := range unreadKeysFor(cfg, up.Name, pluginconf.ReadersOf(up.Declared), renamed) {
		pairs = append(pairs, view.Pair{Key: "not read", Value: u.sentence(up.Name+" "+up.Version, verb)})
	}
	return pairs
}

// sentence is u as a receipt says it of build, which verb does not read.
func (u unreadKey) sentence(build, verb string) string {
	s := u.where + " states `" + u.key + "`, which " + build + " " + verb
	if u.instead != "" {
		s += "; it reads `" + u.instead + "` in its place"
	}
	return s
}
