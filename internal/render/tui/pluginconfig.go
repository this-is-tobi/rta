package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/pluginconf"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The plugin config editor: `c` on a row in the plugins pane opens a form
// over every Config-bearing input its capabilities declare, seeded from
// whatever is already on disk for it, written back through the same
// load-modify-write discipline every other in-app save uses.
//
// Reuses capForm rather than a purpose-built type — unlike the theme editor,
// these fields genuinely are plugin.Field values collected from real
// capabilities, with real Type/Options/Min/Max/Help already declared, which
// is exactly the shape capForm exists for.

// configFields merges the Config-bearing inputs of every capability a plugin
// declares into one list, deduplicated by config key — and renamed to it.
//
// pg's five capabilities all declare the same host/port/user/database/
// sslmode — cap() appends connFields() to each one so no two can disagree
// about a default (plugins/pg/main.go) — so without dedup this form would
// ask "host" five times over.
//
// **By Field.Config, not by Field.Name, and the returned field is renamed to
// its key.** An input's name and its config key are not the same thing and
// nothing says they must be: plugins/vault declares an input called `mount`
// twice, once keyed `kv-mount` and once `transit-mount` (kv.go, transit.go).
// Keyed by name, the two collapsed into one box; keyed by name, capForm then
// wrote what that box held under `mount:` — which plugin.Resolve never reads,
// because it looks up f.Config. So this editor wrote a key nothing consumes
// and dropped the other mount entirely, and `rta doctor` then reported
// "nothing in vault reads mount" about a value rta's own editor had just
// written.
//
// Renaming rather than carrying both is what makes the rest fall out: the
// form binds, seeds and saves by f.Name throughout, so a field whose name IS
// its config key seeds correctly from the raw on-disk section (also keyed by
// config key) and saves back under the same key. The label an operator sees
// becomes the key they would type by hand, which is the right label for a
// config editor and the only one that can tell vault's two mounts apart.
func configFields(p plugin.Plugin) []plugin.Field {
	seen := map[string]bool{}
	var out []plugin.Field
	for _, c := range p.Capabilities {
		for _, f := range c.Inputs {
			if f.Config == "" || seen[f.Config] {
				continue
			}
			seen[f.Config] = true
			f.Name = f.Config
			out = append(out, f)
		}
	}
	return out
}

// configurable reports whether a plugin has anything an operator could put
// in config at all.
func configurable(p plugin.Plugin) bool { return len(configFields(p)) > 0 }

// startConfigForm opens the editor for row: every Config-bearing input
// across its capabilities, seeded from whatever is already on disk for it —
// including a section left behind by a stale pin, so fixing one after an
// upgrade means confirming the values that were already there rather than
// retyping them from a blank form. pluginconf.RawSection is what makes that
// possible: unlike the resolved config every capability actually runs with,
// it does not refuse a mismatched pin.
func (m Model) startConfigForm(row pluginRow) (tea.Model, tea.Cmd) {
	fields := configFields(row.plugin)
	if len(fields) == 0 {
		m.flash = row.plugin.Name + " has nothing to configure"
		return m, nil
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		m.flash = "config not read: " + err.Error()
		return m, nil
	}
	_, raw, _ := pluginconf.RawSection(onDisk, row.plugin.Name)

	synth := plugin.Capability{
		ID:      row.plugin.Name + ".configure",
		Summary: "configuration for " + row.plugin.Name,
		Safety:  plugin.Write,
		Inputs:  fields,
	}
	m.current = synth
	m.trail = nil
	// raw, not m.formSeed(synth, nil): the resolved config a capability
	// would actually run with is nil for exactly the stale-pin case this
	// screen exists to fix, and formSeed would seed the form with the
	// declared defaults instead of the values sitting on disk.
	m.form = newCapForm(synth, fields, raw, true, nil)
	m.form.configTarget = row.plugin.Name
	m.form.configOrigin = row.origin
	m.origin = modePlugins
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// saveConfigForm writes the submitted values under the heading an operator
// would have to write by hand: bare for a built-in, pinned to the currently
// installed artifact for anything on $PATH — never the heading RawSection
// seeded the form from, which may be stale by exactly the amount this screen
// exists to fix.
func (m Model) saveConfigForm() (tea.Model, tea.Cmd) {
	namespace := m.form.configTarget
	origin := m.form.configOrigin
	values := m.form.values()
	// The keys this form actually showed, read before the form is dropped.
	// Taken from the form rather than recomputed from the registry so the set
	// is exactly what the operator was editing, even if the catalogue has
	// changed underneath the screen.
	declared := map[string]bool{}
	for _, f := range m.form.fields {
		declared[f.Name] = true
	}
	m.form = nil

	heading := namespace
	if origin.External() {
		heading = namespace + "@" + origin.Short()
	}

	cfg, err := config.LoadFile()
	if err != nil {
		m.flash = "config not saved: " + err.Error()
		return m.closeToOrigin()
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]map[string]any{}
	}
	// Migrate rather than accumulate: any other heading already naming this
	// namespace — most often the stale pin this form was seeded from — is
	// replaced by the one just confirmed, so saving does not leave an old
	// pin and a new one both making a claim about the same plugin, which
	// `rta doctor` would then report as if the operator meant both.
	for section := range cfg.Plugins {
		if ns, _, _ := strings.Cut(section, "@"); ns == namespace && section != heading {
			delete(cfg.Plugins, section)
		}
	}
	// The form owns the keys it showed, and nothing else. Assigning `values`
	// over the whole section replaced it, so every key this editor did not
	// collect was deleted by the act of saving an unrelated field — a key for
	// a capability the installed binary happens not to declare right now, or
	// one written by hand for a version this build does not know about.
	//
	// Not a plain merge, which would make a field impossible to clear:
	// capForm.values() omits a text field left empty with no default, so an
	// overlay would keep resurrecting the value the operator just deleted.
	// Declared keys are therefore taken from the form even when absent —
	// absent means cleared — and only undeclared ones are carried forward.
	merged := map[string]any{}
	for k, v := range cfg.Plugins[heading] {
		if !declared[k] {
			merged[k] = v
		}
	}
	for k, v := range values {
		merged[k] = v
	}
	cfg.Plugins[heading] = merged
	if err := config.Write(cfg); err != nil {
		m.flash = "config not saved: " + err.Error()
		return m.closeToOrigin()
	}

	// Live effect within this session: rebuild the resolver from what was
	// just written, so the very next dashboard refresh sees it without
	// leaving and restarting rta.
	resolver, _ := pluginconf.Resolve(cfg, m.reg.Origin)
	m.pluginCfg = resolver.For
	m.plugins = pluginRows(m.reg, m.dash)

	m.flash = "saved plugins." + heading
	return m.closeToOrigin()
}
