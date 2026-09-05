package plugindist

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/view"
)

// orphanedConfig lists the config locations that still name a plugin after
// it is gone: `plugins.<ns>@<digest>` sections and profile entries. Named,
// never removed — the config file is the operator's, may be shared across
// machines, and `rta doctor` already reports orphans as problems; what an
// uninstall owes is telling them at the moment they can still connect the
// dots (found while walking the install path end to end: "nothing offered to clean it up
// because nothing knew an uninstall had happened").
func orphanedConfig(name string) []string {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil
	}
	var out []string
	for key := range cfg.Plugins {
		if config.PluginNamespace(key) == name {
			out = append(out, "plugins."+key)
		}
	}
	for pname, p := range cfg.Profiles {
		for _, key := range p.PluginKeys() {
			if config.PluginNamespace(key) == name {
				out = append(out, "profiles."+pname+"."+key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// SearchRow is one line of `rta plugin search`: claims, labelled by the index
// making them, answerable without downloading anything — the reason the
// declaration lives in the manifest at all.
type SearchRow struct {
	Name    string
	Index   string
	Version string
	Summary string
	Safety  string
	// Installed is the locked digest when this plugin is managed, short form.
	Installed string
}

// Search lists every claim across the attached indexes, optionally filtered
// by a substring of the name or summary and by a safety class the plugin
// must contain, and returns what it could not read beside what it could.
//
// The problems used to be dropped here, on the reasoning that Manifests
// reports them to `plugin index list` anyway. That made every index rta cannot
// read indistinguishable from an index carrying nothing that matches, and a
// search is where somebody actually asks: an attached repository that is not
// an index answered "nothing matches" for every term, forever, and the count
// in a table nobody had a reason to open was the only place that said
// otherwise. A caller may still ignore them; it can no longer do so by
// accident.
func Search(term, safety string) ([]SearchRow, []*view.Error) {
	var rows []SearchRow
	var bad []*view.Error
	for _, ix := range Indexes() {
		listed, problems := Manifests(ix)
		bad = append(bad, problems...)
		for _, l := range listed {
			m := l.Manifest
			if term != "" && !strings.Contains(m.Name, term) &&
				!strings.Contains(strings.ToLower(m.Summary), strings.ToLower(term)) {
				continue
			}
			if safety != "" && !claimsSafety(m, safety) {
				continue
			}
			row := SearchRow{Name: m.Name, Index: l.Index, Version: m.Version,
				Summary: m.Summary, Safety: m.SafetyLine()}
			if e, ok := LockedFor(m.Name); ok && len(e.Digest) >= 12 {
				row.Installed = e.Digest[:12]
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Index < rows[j].Index
	})
	return rows, bad
}

func claimsSafety(m Manifest, safety string) bool {
	for _, c := range m.Capabilities {
		if c.Safety == safety {
			return true
		}
	}
	return false
}
