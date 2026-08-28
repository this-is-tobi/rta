package plugindist

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
)

// orphanedConfig lists the config locations that still name a plugin after
// it is gone: `plugins.<ns>@<digest>` sections and profile entries. Named,
// never removed — the config file is the operator's, may be shared across
// machines, and `rta doctor` already reports orphans as problems; what an
// uninstall owes is telling them at the moment they can still connect the
// dots (ADR 0017's own walkthrough finding: "nothing offered to clean it up
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
// declaration lives in the manifest at all (ADR 0017 §2).
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
// must contain. Invalid manifests are reported apart by the caller via
// Manifests; here they are simply not rows.
func Search(term, safety string) []SearchRow {
	var rows []SearchRow
	for _, ix := range Indexes() {
		listed, _ := Manifests(ix)
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
	return rows
}

func claimsSafety(m Manifest, safety string) bool {
	for _, c := range m.Capabilities {
		if c.Safety == safety {
			return true
		}
	}
	return false
}
