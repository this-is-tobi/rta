package tui

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/clipboard"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/tunnel"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Creating and editing environments, the plugins inside them, where each
// credential comes from, and the one-key actions beside them.
//
// The plugin editor is capForm — the same form the plugin config editor reuses,
// for a sharper reason than convenience. A connection's `set:` block is keyed by
// Field.Config, which is exactly what configFields already collects and exactly
// what that form already renders with each input's declared type, Options
// picker, bounds and help. Building a second form over the same fields would be
// two things to keep in step, and the first time they drifted the difference
// would be which values an operator can state.

// The form field names. Prefixed and constant so nothing here depends on a
// plugin not happening to declare a config key with the same name.
const (
	profileNameField   = "profile-name"
	profileNoteField   = "profile-note"
	profileTTLField    = "profile-ttl"
	profilePluginField = "profile-plugin"
	profileKubeField   = "profile-kube"
	profileSSHField    = "profile-ssh"
	profileTLSField    = "profile-tls"
	profileSetPrefix   = "set."

	profileTTLNone = "no deadline"
)

// profileTTLOptions are the windows a switch can carry.
//
// A closed set rather than a free-text duration, because this is the field
// where a typo is expensive in the quiet direction: `2hh` does not parse, and a
// production environment that silently has no deadline is exactly the outcome
// the field exists to prevent. `rta use --for` still takes any duration for the
// one-off case.
var profileTTLOptions = []string{profileTTLNone, "15m", "1h", "4h", "8h", "12h", "24h"}

// startProfileForm opens the environment editor: what it is called, why it
// exists, and how long a switch to it lasts. An empty name creates a new one.
//
// The plugins are edited one level in, not here. A form that collected a name, a
// note and three plugins' worth of connection values would be a form nobody
// finishes, and the two halves change on completely different rhythms — a name
// is written once, a host changes whenever the environment moves.
func (m Model) startProfileForm(name string) (tea.Model, tea.Cmd) {
	cfg, err := config.LoadFile()
	if err != nil {
		m.flash = "config not read: " + err.Error()
		return m, nil
	}
	p := cfg.Profiles[name]

	ttl := profileTTLNone
	if p.TTL != "" {
		ttl = p.TTL
	}
	options := profileTTLOptions
	if !containsString(options, ttl) {
		// Whatever is in the file stays offered, so opening the editor on a
		// hand-written `ttl: 90m` and pressing enter does not quietly round it
		// to something rta happened to list.
		options = append(append([]string{}, options...), ttl)
	}

	fields := []plugin.Field{
		{Name: profileNameField, Type: plugin.String, Required: true,
			Help: "what to call this environment: lowercase letters, digits and dashes"},
		{Name: profileNoteField, Type: plugin.String,
			Help: "why this environment exists — shown wherever it is listed"},
		{Name: profileTTLField, Type: plugin.String, Options: options, Default: ttl,
			Help: "how long switching to it lasts before it lapses on its own"},
	}
	synth := plugin.Capability{
		ID:      "profile.edit",
		Summary: "a named environment",
		Safety:  plugin.Write,
		Inputs:  fields,
	}
	m.current = synth
	m.trail = nil
	m.form = newCapForm(synth, fields, map[string]any{
		profileNameField: name,
		profileNoteField: p.Note,
		profileTTLField:  ttl,
	}, true, nil)
	m.form.profileTarget = name
	m.form.profileEditing = true
	m.origin = modeProfiles
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// saveProfileForm writes the submitted environment back to the config file.
//
// Load-modify-write against the file on disk, never against a copy this
// session read earlier: the same discipline the dashboard arrangement and the
// plugin config editor both hold, because a profile is one part of a file and
// editing it must not rewrite anything else.
func (m Model) saveProfileForm() (tea.Model, tea.Cmd) {
	was := m.form.profileTarget
	values := m.form.values()
	m.form = nil

	name := strings.TrimSpace(str(values[profileNameField]))
	if !config.ValidName(name) {
		m.flash = "not a valid profile name: lowercase letters, digits and dashes"
		return m.closeToOrigin()
	}

	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]config.Profile{}
		}
		// Keep what this form does not collect. The `plugins:` block is
		// written one level in, and losing it by renaming an environment would
		// empty it in a single keystroke.
		p := cfg.Profiles[was]
		p.Note = strings.TrimSpace(str(values[profileNoteField]))
		p.TTL = ""
		if ttl := strings.TrimSpace(str(values[profileTTLField])); ttl != "" && ttl != profileTTLNone {
			p.TTL = ttl
		}

		// A rename moves the row rather than leaving two. The switch follows,
		// or this machine would be switched on to a profile that no longer
		// exists — and, because the selection also bounds agents, every agent
		// call would be refused against a name nothing can look up.
		if was != "" && was != name {
			delete(cfg.Profiles, was)
			if sel := profile.LoadSelection(); sel.Active == was {
				sel.Active = name
				_ = profile.SaveSelection(sel)
			}
		}
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		m.flash = "config not saved: " + err.Error()
		return m.closeToOrigin()
	}
	m.profiles = m.profileRows()
	if m.profileOpen == was {
		m.profileOpen = name
	}
	m.flash = "saved profile " + name
	return m.closeToOrigin()
}

// startConnForm opens the editor for one plugin inside the open environment. An
// empty key adds a new one.
//
// The plugin field is completed from what is actually installed, already pinned:
// a digest is not something anybody should be asked to type or to look up, and
// typing one wrong is the failure the pin exists to prevent — so the field that
// carries it is exactly where the answer should be offered rather than demanded.
func (m Model) startConnForm(key string) (tea.Model, tea.Cmd) {
	chosen := key
	if key == "" && m.pluginSel < len(m.plugins) {
		// A new entry inherits the plugin under the cursor in the plugins pane
		// when there is one, so the common path — look at what is installed,
		// add it to this environment — needs no typing of a digest. Not an
		// untrusted one: it would seed the form with an entry that cannot
		// resolve.
		if row := m.plugins[m.pluginSel]; row.usable() {
			chosen = row.pinnedName()
		}
	}
	return m.connForm(key, chosen, nil)
}

// connForm builds the editor for one entry. key is the entry as the file holds
// it — empty for a new one — and chosen is the plugin the form is *about*,
// which is the same thing on an existing entry and the seeded or newly typed
// one otherwise.
//
// **Splitting the two is what lets a new entry be configured on the way in.**
// The config boxes come from whichever plugin is named, and the name used to be
// read off the file's key, so a new entry had none: you picked the plugin,
// submitted, reopened the entry you had just made, and filled it in. Nothing
// about that was a decision — the plugin simply was not known at the moment the
// form was built. It is known now, at both moments it can change: seeded from
// the plugins pane when the form opens, and re-read when the operator completes
// the field into a different one (reseedOnConnPluginChange).
//
// seed carries the operator's current answers across a rebuild; nil on a fresh
// open, where the file's own values are the seed.
func (m Model) connForm(key, chosen string, seed map[string]any) (tea.Model, tea.Cmd) {
	row, ok := m.openProfile()
	if !ok {
		return m, nil
	}
	conn := row.p.Plugins[key]
	// A rebuild onto a different plugin keeps the coordinate and drops the
	// `set:` block: those keys belonged to the plugin that is no longer named,
	// and carrying them over would offer one plugin's configuration under
	// another's name.
	if chosen != key {
		conn.Set = nil
	}

	// The chosen plugin's own declaration, when the picker names one that is
	// installed. Everything below depends on it: which boxes exist at all,
	// and which of them a forward would shadow.
	decl, known := m.declarationOf(chosen)
	// **The coordinate boxes are offered only to a plugin a forward can
	// reach.** They used to be offered to everything, which is how a
	// port-forward came to be saved against a plugin that talks to its cluster
	// through kubectl and declares no endpoint input: the box was the most
	// prominent thing on the screen, it completed against the real cluster, so
	// it read as *the* way to point the plugin somewhere — and every run of
	// that profile was then refused by the resolver. Offering a box the
	// runtime will not honour is the page-versus-run drift this codebase keeps
	// removing, in the direction where the operator does the work twice.
	//
	// Offered while the plugin is unknown, because the picker completes to
	// `name@digest` and every prefix on the way there names nothing. The save
	// refuses what this cannot yet judge.
	forwardable := !known || plugin.Tunnellable(decl.Capabilities)

	fields := []plugin.Field{
		{Name: profilePluginField, Type: plugin.String, Required: true,
			Suggest: func(context.Context, plugin.Request) []string { return m.installedPlugins() },
			Help:    "which plugin, pinned to its artifact — press tab"},
	}
	coordinates := []plugin.Field{
		// Above the set. fields, because it decides where the call goes and
		// they are subordinate to it: a `set.host` beside a coordinate is two
		// statements about the destination, and the forward is the one that
		// wins. Reading them in the other order would suggest the opposite.
		{Name: profileKubeField, Type: plugin.String,
			// Short on purpose: help wraps at the form's width and every
			// wrapped line pushes a field off a small screen. No example —
			// tab completes each segment from the cluster, so the field
			// produces real ones. The either/or and the "or neither" now live
			// in the heading above both boxes, where they are said once
			// instead of half in each.
			Help: "a port-forward: context/namespace/kind/name:port — tab completes each segment"},
		{Name: profileSSHField, Type: plugin.String,
			// The suggestions are the aliases in the operator's own ssh config
			// — a local file, so per-keystroke is fine — because
			// an alias is the case this is best at: one word carrying the
			// user, port, key and ProxyJump the config already states.
			Suggest: func(context.Context, plugin.Request) []string { return tunnel.SSHHosts() },
			Help:    "an ssh host: [user@]host[:port]/desthost:destport — tab suggests your ssh aliases"},
		// Below both boxes, not beside either: it is a fact about whichever one
		// is filled, not a third destination. Named apart from a plugin's own
		// `set.tls`/`set.sslmode` box below (etcd, pg, mysql, mariadb, qdrant,
		// s3 all declare one) on purpose — config.Connection.TunnelTLS's doc
		// comment has the reasoning, and this screen can show both at once
		// without either explaining the other.
		{Name: profileTLSField, Type: plugin.Bool,
			Help: "the far side of the forward above speaks TLS on its own — fill it as https, not http"},
	}
	if forwardable {
		fields = append(fields, coordinates...)
	}
	// Every config key the plugin declares, when one is already chosen. On a
	// new entry there is nothing to offer yet, and the operator submits once to
	// choose the plugin and reopens to fill it in.
	if known {
		tunnelled := strings.TrimSpace(conn.Kube) != "" || strings.TrimSpace(conn.SSH) != ""
		for _, f := range configFields(decl) {
			// Under a coordinate the endpoint keys are the forward's to
			// fill — Dial lays them over `set:` per call, and checkSet
			// refuses the pair — so the editor does not offer them, even
			// when the file states one. A first cut kept a stated key's
			// box "so it could be cleared", which assumed clearing is
			// something every widget can do: a select always holds one of
			// its Options and a toggle always holds a bool, so a stated
			// `sslmode` or `tls` was a box whose one legal action the
			// widget could not perform — and the save guard then refused
			// every save of the entry. The save is the repair instead:
			// it drops the file's dead endpoint keys and the flash names
			// them (saveConnForm). Decided at open, like the
			// reopen-to-fill flow above: clearing the coordinate reveals
			// the boxes on reopen.
			//
			// A TLSAdjacent field — pg's sslrootcert — is offered no box
			// here for the same reason, one step removed: it carries no
			// Endpoint role itself, but it depends on the EndpointTLS field
			// this same tunnel already forces off, so a value typed here
			// would do nothing the moment it left the box.
			if tunnelled && (f.Endpoint != plugin.EndpointNone || f.TLSAdjacent) {
				continue
			}
			// Prefixed so a plugin's own key can never collide with the
			// field above: a plugin declaring a config key called "plugin"
			// would otherwise silently become the entry's key.
			f.Name = profileSetPrefix + f.Name
			f.Required = false
			fields = append(fields, f)
		}
	}

	defaults := map[string]any{profilePluginField: chosen,
		profileKubeField: conn.Kube, profileSSHField: conn.SSH, profileTLSField: conn.TunnelTLS}
	for k, v := range conn.Set {
		defaults[profileSetPrefix+k] = v
	}
	// What the operator has already typed wins over the file, so a rebuild is
	// invisible except for the boxes it added.
	for k, v := range seed {
		if s, isString := v.(string); isString && strings.TrimSpace(s) == "" {
			continue
		}
		defaults[k] = v
	}

	synth := plugin.Capability{
		ID:      "profile.plugin",
		Summary: "what " + row.name + " changes about one plugin",
		Safety:  plugin.Write,
		Inputs:  fields,
	}
	m.current = synth
	m.trail = nil
	// At most two headings, and never one over the first field: the panel
	// above already says what the form is. The ones that earn their line are
	// the boundaries — "how to reach it" makes the two coordinate boxes read
	// as one either/or question, and the `set.` block below it stops looking
	// like two more of the same. A heading with nothing under it is worse than
	// none, so the first goes when the boxes do.
	var opts []formOption
	if forwardable {
		opts = append(opts, withSection(profileKubeField,
			"how to reach it — one of these, or neither to connect directly"))
	}
	if firstSet := firstSetField(fields); firstSet != "" {
		// The absent coordinate is said here rather than left to be noticed.
		// Every other plugin in the profile has that box, so its absence reads
		// as a missing feature unless the heading names it as a fact about
		// this one — the same reason a column that appears only when a row can
		// fill it still has to announce itself.
		title := "what " + row.name + " changes about it"
		if !forwardable {
			title += " — " + decl.Name + " needs no forward"
		}
		opts = append(opts, withSection(firstSet, title))
	}
	m.form = newCapForm(synth, fields, defaults, true, nil, opts...)
	// The set. fields the file does not state are displays of the plugin's
	// own defaults, not statements to keep: submitting them unchanged must
	// not write `set: host: localhost` into an environment whose operator
	// only ever typed a database name — which is what it did, four phantom
	// keys per save, one of them the value that decides where a call goes.
	m.form.derived = map[string]bool{}
	for _, f := range fields {
		if name, isSet := strings.CutPrefix(f.Name, profileSetPrefix); isSet {
			if _, inFile := conn.Set[name]; !inFile {
				m.form.derived[f.Name] = true
			}
		}
	}
	m.form.profileTarget = key
	m.form.connEditing = true
	m.form.builtOn = chosen
	m.origin = modeProfilePlugins
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// declarationOf is the installed plugin a profile key names, and whether it
// names one at all.
//
// The key is `name@digest` and the picker completes toward it one keystroke
// at a time, so "not installed" is the ordinary state while somebody is still
// typing — the caller decides what an unknown plugin means rather than being
// handed a zero value that looks like an answer.
func (m Model) declarationOf(key string) (plugin.Plugin, bool) {
	ns := config.PluginNamespace(key)
	if ns == "" {
		return plugin.Plugin{}, false
	}
	for _, prow := range m.plugins {
		if prow.plugin.Name == ns {
			return prow.plugin, true
		}
	}
	return plugin.Plugin{}, false
}

// reseedOnConnPluginChange rebuilds the connection editor when the plugin it
// is about has changed under the cursor — the same move the run form makes
// when its environment picker moves (reseedOnPickerMove), for the same reason:
// the boxes below depend on the answer above, so they have to be rebuilt when
// it changes rather than describe the plugin that was named a keystroke ago.
//
// Only on a *registered* plugin, and that is the whole guard. The field
// completes to `name@digest`, so every intermediate prefix somebody types is a
// name that resolves to nothing — rebuilding on those would throw the form away
// on almost every keystroke. Waiting until the value names something installed
// means the rebuild happens exactly once, on the press that finishes the name.
func (m Model) reseedOnConnPluginChange() (tea.Model, tea.Cmd, bool) {
	cf := m.form
	if cf == nil || !cf.connEditing || cf.form.State != huh.StateNormal {
		return m, nil, false
	}
	b, bound := cf.bindings[profilePluginField]
	if !bound {
		return m, nil, false
	}
	chosen := strings.TrimSpace(*b)
	if chosen == cf.builtOn {
		return m, nil, false
	}
	ns := config.PluginNamespace(chosen)
	if ns == "" || !m.registered(ns) {
		return m, nil, false
	}
	target := cf.profileTarget
	nm, cmd := m.connForm(target, chosen, cf.values())
	return nm, cmd, true
}

// registered reports whether a namespace is one of the plugins loaded now.
func (m Model) registered(ns string) bool {
	for _, row := range m.plugins {
		if row.plugin.Name == ns && row.usable() {
			return true
		}
	}
	return false
}

// saveConnForm writes one plugin entry back into the open environment.
func (m Model) saveConnForm() (tea.Model, tea.Cmd) {
	was := m.form.profileTarget
	values := m.form.values()
	m.form = nil

	key := strings.TrimSpace(str(values[profilePluginField]))
	if key == "" {
		m.flash = "nothing saved: no plugin named"
		return m.closeToOrigin()
	}

	var (
		gone    bool
		refusal string
		freed   []string
		unread  []string
	)
	// The whole load-mutate-write under one lock, like every other writer of
	// this file: the gap between reading it and writing it back is where a
	// concurrent save is lost, and this form is one of nine places that had
	// that gap. A decision to refuse mid-edit returns false rather than
	// returning early, so nothing is written and the lock is released on the
	// ordinary path.
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		p, ok := cfg.Profiles[m.profileOpen]
		if !ok {
			gone = true
			return cfg, false
		}
		if p.Plugins == nil {
			p.Plugins = map[string]config.Connection{}
		}
		// Keep what this form does not collect. A `secrets:` mapping is written by
		// the credential action rather than here, and losing it by editing a host
		// would repoint the connection and silently drop its credential in one
		// keystroke.
		conn := p.Plugins[was]
		decl, known := m.declarationOf(key)
		// Refused here rather than written and reported later. This is the one
		// screen where somebody is typing a coordinate, so it is where a typo
		// should be caught — `rta doctor` finding it afterwards means the profile
		// was saved broken and the operator has moved on.
		kube := strings.TrimSpace(str(values[profileKubeField]))
		ssh := strings.TrimSpace(str(values[profileSSHField]))
		// A refusal, unlike the endpoint-key repair below, because it is honest
		// here: both boxes are plain text, so "empty one of them" is an action
		// the widget can always perform — the select trap below cannot occur on
		// these two plain-text fields.
		if kube != "" && ssh != "" {
			refusal = "one tunnel: keep `kube:` or `ssh:`, and empty the other"
			return cfg, false
		}
		if kube != "" {
			if verr := tunnel.CheckKube(kube); verr != nil {
				refusal = verr.Message
				return cfg, false
			}
		}
		if ssh != "" {
			if verr := tunnel.CheckSSH(ssh); verr != nil {
				refusal = verr.Message
				return cfg, false
			}
		}
		// And whether this plugin can use a forward at all. connForm does not
		// offer the boxes to one that cannot, so this catches the two ways a
		// value arrives anyway: typed before the picker finished resolving the
		// name, and already in the file from an artifact since rebuilt without
		// its endpoint roles. Refused rather than repaired — unlike a shadowed
		// endpoint key there is nothing to salvage, and writing it would save a
		// profile every run then refuses.
		if (kube != "" || ssh != "") && known && !plugin.Tunnellable(decl.Capabilities) {
			refusal = decl.Name + " declares no input a tunnel can fill — empty the coordinate"
			return cfg, false
		}
		conn.Kube = kube
		conn.SSH = ssh
		tunnelled := kube != "" || ssh != ""
		// Repaired rather than refused, the same reason the endpoint-key
		// removal below is: a Bool toggle has no empty to choose, so an
		// operator who clears the coordinate while a previously-saved TLS
		// toggle still reads on (this screen does not react live to a
		// sibling box the way a rebuild between opens would) would find the
		// form unsaveable until they also flipped a switch nothing drew
		// their eye to. internal/profile.checkTunnel would refuse this
		// combination outright if it reached a file, so cleared is the only
		// value worth writing — named in the flash so it is not a mystery.
		tlsOn, _ := values[profileTLSField].(bool)
		conn.TunnelTLS = tlsOn && tunnelled
		if tlsOn && !tunnelled {
			unread = append(unread, "tunnelTLS")
		}
		// Remembered before the rebuild, so the endpoint-key repair below can
		// name what the file loses even when no box carried it: under a
		// coordinate startConnForm offers no endpoint boxes at all, so a stated
		// key vanishes through this rebuild — silently, without this.
		prior := conn.Set
		conn.Set = map[string]any{}
		for k, v := range values {
			name, isSet := strings.CutPrefix(k, profileSetPrefix)
			if !isSet {
				continue
			}
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
				continue
			}
			conn.Set[name] = v
		}
		// An endpoint key beside a coordinate is removed here, and the flash says
		// so — repaired at save for CheckKube's reason: this is the one screen
		// that knows both halves, and writing the pair would save a profile
		// checkSet then refuses everywhere. Removed rather than refused, which a
		// first cut tried: refusal assumed the operator could empty the box, and
		// an Options or Bool endpoint input has no empty to choose — the entry
		// became unsaveable while the coordinate stood. The keys reach here from
		// the file (startConnForm offers no endpoint boxes under a coordinate)
		// or from boxes visible while the coordinate was being typed; either
		// way every possible value is one the forward overrides, so the only
		// honest file is one without the key — and the flash is the receipt.
		if known {
			declared := map[string]bool{}
			for _, f := range configFields(decl) {
				declared[f.Name] = true
				// Endpoint-role fields are overridden directly; a
				// TLSAdjacent one depends on an EndpointTLS field the same
				// tunnel forces off (plugin.Field.TLSAdjacent's own doc
				// comment has the full reasoning) — both are exactly as
				// dead, so both get the same repair.
				if !tunnelled || (f.Endpoint == plugin.EndpointNone && !f.TLSAdjacent) {
					continue
				}
				_, stated := conn.Set[f.Name]
				_, had := prior[f.Name]
				if stated || had {
					delete(conn.Set, f.Name)
					freed = append(freed, "set."+f.Name)
				}
			}
			// A prior key no declared field offered a box for vanishes through
			// the rebuild whatever this save was about — it is a key nothing
			// reads (checkSet refuses the profile over it), usually left behind
			// by a plugin rebuilt without it. Removing it is the same
			// repair-at-save the endpoint keys get, and the receipt below is
			// what keeps it from being a mystery.
			stale := make([]string, 0, len(prior))
			for k := range prior {
				if !declared[k] {
					stale = append(stale, k)
				}
			}
			sort.Strings(stale)
			for _, k := range stale {
				unread = append(unread, "set."+k)
			}
			if tunnelled {
				// And the `secrets:` mappings aimed at the same inputs, which the
				// forward shadows identically (checkSecretRefs) — keyed by input
				// name, so walked off the raw declarations rather than
				// configFields' config-key renames. This form keeps Secrets
				// untouched otherwise, so the file's map is what is repaired.
				seen := map[string]bool{}
				for _, c := range decl.Capabilities {
					for _, f := range c.Inputs {
						if f.Endpoint == plugin.EndpointNone || !plugin.ProfileFillable(c, f) ||
							seen[f.Name] {
							continue
						}
						seen[f.Name] = true
						if _, mapped := conn.Secrets[f.Name]; mapped {
							delete(conn.Secrets, f.Name)
							freed = append(freed, "secrets."+f.Name)
						}
					}
				}
			}
		}
		if !known {
			// The plugin is not installed, so this form offered no `set.` boxes
			// and nothing here can tell a statement from a leftover. A form must
			// not rewrite what it could not show: the file's own block stands,
			// exactly as Secrets always has.
			conn.Set = prior
		}
		if len(conn.Set) == 0 {
			conn.Set = nil
		}
		if was != "" && was != key {
			delete(p.Plugins, was)
		}
		p.Plugins[key] = conn
		cfg.Profiles[m.profileOpen] = p
		return cfg, true
	}); err != nil {
		m.flash = "config not saved: " + err.Error()
		return m.closeToOrigin()
	}
	if gone {
		m.flash = "profile " + m.profileOpen + " is gone"
		return m.closeToOrigin()
	}
	if refusal != "" {
		// Stays on the form, as it did before: the operator has a coordinate
		// to correct and the boxes are still in front of them.
		m.flash = refusal
		return m, nil
	}
	m.profiles = m.profileRows()
	m.flash = "saved " + key + " in " + m.profileOpen
	if len(freed) > 0 {
		what := "it"
		if len(freed) > 1 {
			what = "these"
		}
		m.flash += " — removed " + strings.Join(freed, ", ") + ": the forward fills " + what
	}
	if len(unread) > 0 {
		what := "nothing reads it"
		if len(unread) > 1 {
			what = "nothing reads them"
		}
		m.flash += " — dropped " + strings.Join(unread, ", ") + ": " + what
	}
	if was == "" {
		if nm, cmd, asked := m.credentialAfterAdding(key); asked {
			return nm, cmd
		}
	}
	return m.closeToOrigin()
}

// credentialAfterAdding opens the credential editor on an entry that has just
// been added and cannot be used without one.
//
// **Only after adding, and only when nothing supplies it.** An environment's
// plugin entry that needs a credential and has none is not a half-finished
// thing the operator can come back to — it is the state the pane reports as
// "1 not set" and every call through it refuses. Making that the next screen
// costs no keystroke and removes three: close the form, find the entry again,
// press `s`. Editing an existing entry does not do this, because there the
// operator came to change a host and being taken somewhere else is a different
// experience from being taken further.
//
// Esc is still the answer: the entry is already written, so leaving is
// declining to finish rather than losing the work.
func (m Model) credentialAfterAdding(key string) (tea.Model, tea.Cmd, bool) {
	row, ok := m.openProfile()
	if !ok {
		return m, nil, false
	}
	for i, c := range row.conns {
		if c.key != key {
			continue
		}
		unset := false
		for _, cr := range c.credentials {
			if !cr.satisfied() {
				unset = true
			}
		}
		if !unset {
			return m, nil, false
		}
		m.connSel = i
		m.mode = modeProfilePlugins
		m.flash += " — it needs a credential"
		nm, cmd := m.startCredentialForm()
		return nm, cmd, true
	}
	return m, nil, false
}

// installedPlugins is every registered plugin in the form a profile must name
// it: bare for a built-in, pinned to the installed artifact otherwise. The
// summary rides along as the completion's description, so a list of digests
// reads as a list of plugins.
func (m Model) installedPlugins() []string {
	out := make([]string, 0, len(m.plugins))
	for _, row := range m.plugins {
		// An untrusted artifact is not offered. A profile naming one cannot
		// resolve — the plugin is not registered, so `rta use` refuses the
		// whole environment — and completing to it would hand the operator a
		// broken entry from rta's own suggestion list.
		if !row.usable() {
			continue
		}
		out = append(out, row.pinnedName()+"\t"+row.plugin.Summary)
	}
	sort.Strings(out)
	return out
}

// useSelectedProfile switches this machine to the selected environment, or off
// it when it is already on.
//
// A toggle rather than a one-way switch: the pane is where somebody looks to
// find out what they are in, so it has to be where they can leave. And leaving
// is the fast path that matters — while an environment is on, agents may reach
// that one and nothing else, so switching off is how somebody takes every
// profiled reach away from every agent in one keystroke.
func (m *Model) useSelectedProfile() string {
	if m.profileSel >= len(m.profiles) {
		return ""
	}
	row := m.profiles[m.profileSel]
	if profile.LoadSelection().Name(time.Now()) == row.name {
		if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
			return "not saved: " + verr.Error()
		}
		return "switched off " + row.name + " — commands run against the base configuration"
	}
	if !row.valid() {
		// Switching to an environment that cannot resolve would leave every
		// later command failing with a message about a connection the operator
		// believes they chose deliberately.
		return row.name + " cannot be used: " + row.problem
	}
	sel := profile.Selection{Active: row.name}
	note := "switched to " + row.name
	if d, has := row.p.Window(); has {
		until := time.Now().Add(d)
		sel.Until = &until
		note += " for " + row.p.TTL
	}
	if verr := profile.SaveSelection(sel); verr != nil {
		return "not saved: " + verr.Error()
	}
	return note + " — covers " + strings.Join(row.p.Namespaces(), ", ")
}

// deleteSelectedProfile removes the selected environment from the config file.
//
// It also drops any grant naming it, and says so. A grant for a profile that
// no longer exists authorizes nothing — Lookup refuses the name — so leaving
// it would be a row in `rta grant list` that reads like access and is not.
func (m *Model) deleteSelectedProfile() string {
	if m.profileSel >= len(m.profiles) {
		return ""
	}
	row := m.profiles[m.profileSel]
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		delete(cfg.Profiles, row.name)
		return cfg, true
	}); err != nil {
		return "not deleted: " + err.Error()
	}
	if sel := profile.LoadSelection(); sel.Active == row.name {
		_ = profile.SaveSelection(profile.Selection{})
	}
	note := "deleted profile " + row.name
	if n := grant.RevokeProfile(row.name, time.Now()); n > 0 {
		note += ", and revoked " + plural(n, "grant") + " naming it"
	}
	return note
}

// deleteSelectedConn removes one plugin from the open environment.
func (m *Model) deleteSelectedConn() string {
	row, ok := m.openProfile()
	if !ok || m.connSel >= len(row.conns) {
		return ""
	}
	key := row.conns[m.connSel].key
	gone := false
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		p, exists := cfg.Profiles[m.profileOpen]
		if !exists {
			gone = true
			return cfg, false
		}
		delete(p.Plugins, key)
		cfg.Profiles[m.profileOpen] = p
		return cfg, true
	}); err != nil {
		return "not removed: " + err.Error()
	}
	if gone {
		return "profile " + m.profileOpen + " is gone"
	}
	return "removed " + key + " from " + m.profileOpen
}

// copyExportLine puts the exports for the open environment's unset credentials
// on the clipboard.
//
// rta cannot set a variable in the operator's shell — a child process does not
// reach its parent's environment — so the honest thing is to hand over the
// lines to paste. The values are deliberately left as placeholders: this is a
// screen, and a credential belongs on it no more than in a config file.
//
// **An environment rta has already called invalid does not get to write a
// command.** The variable name is derived from the profile's name, and a name
// that never passed ValidName can be anything a config file holds — so this
// pane once printed "not a valid profile name" in red on the band and then,
// on the very next keypress, put a line built out of that name on the
// clipboard under "fill in the value and run it". envToken now makes the name
// an identifier whatever it was given, and this refuses on top of it: two
// independent reasons the paste is safe, because the one that was relied on
// lived in another package.
func (m *Model) copyExportLine() string {
	if m.profileSel >= len(m.profiles) {
		return ""
	}
	row := m.profiles[m.profileSel]
	if !row.valid() {
		return row.name + " is not usable — " + row.problem
	}
	var missing []string
	for _, c := range row.conns {
		for _, cr := range c.credentials {
			if !cr.satisfied() {
				missing = append(missing, "export "+cr.env+"=…")
			}
		}
	}
	if len(missing) == 0 {
		if needed, _ := row.missing(); needed == 0 {
			return row.name + " needs no credential"
		}
		return row.name + "'s credentials are already resolved — nothing to export"
	}
	sort.Strings(missing)
	if ok, _, _ := clipboard.Copy([]byte(strings.Join(missing, "\n") + "\n")); !ok {
		return "no clipboard here — " + strings.Join(missing, "; ")
	}
	return "copied " + plural(len(missing), "export line") + " — fill in the value and run it"
}

// firstSetField names the first config box, which is where the second heading
// goes. Empty when the plugin has nothing to configure, or when none is chosen
// yet — in both cases there is no block to head.
func firstSetField(fields []plugin.Field) string {
	for _, f := range fields {
		if strings.HasPrefix(f.Name, profileSetPrefix) {
			return f.Name
		}
	}
	return ""
}

// str renders a form value as the string a config key should hold.
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The credential form's field names.
const (
	credInputField  = "credential-input"
	credSourceField = "credential-source"
	credEntryField  = "credential-entry"
	credValueField  = "credential-value"
	credKubeField   = "credential-kube"

	credSourceRef   = "reference an entry already in the store"
	credSourceStore = "store a new value and reference it"
	credSourceKube  = "read it from a Secret in the cluster"
	credSourceEnv   = "use an environment variable instead"
)

// startCredentialForm maps one of the selected plugin's credentials onto the
// store, or onto a new entry in it.
//
// rta cannot set a variable in somebody's shell — a child process does not
// reach its parent's environment — so "export this line" is as far as the
// environment path can go. The store is the path that can be finished here,
// which is why it is offered: a credential an operator can attach without
// leaving the screen is one they will actually attach.
func (m Model) startCredentialForm() (tea.Model, tea.Cmd) {
	row, ok := m.openProfile()
	if !ok || m.connSel >= len(row.conns) {
		return m, nil
	}
	conn := row.conns[m.connSel]
	if len(conn.credentials) == 0 {
		m.flash = conn.key + " needs no credential"
		return m, nil
	}

	// Every input a mapping may fill, not only the credentials. `user` from a
	// cluster Secret's `username` key works in the file and was unreachable
	// from here — see fillableInputs. Minus the endpoint-role inputs when the
	// connection names a coordinate, for the same reason the conn editor
	// hides their `set.*` boxes there: the forward fills them.
	inputs := m.fillableInputs(conn.key, conn.conn.Tunnelled())
	if len(inputs) == 0 {
		m.flash = conn.key + " has no input a connection can fill"
		return m, nil
	}
	sources := []string{credSourceRef, credSourceStore, credSourceEnv}
	// Referencing needs something to reference. Offering the option against an
	// empty store is offering a choice that dead-ends.
	entries := kv.Names()
	if len(entries) == 0 {
		sources = []string{credSourceStore, credSourceEnv}
	}
	// The cluster, and only when this connection names one. A Secret is read
	// from the namespace the coordinate already gives, so without a coordinate
	// there is nowhere to read from and the option would dead-end the same way
	// referencing an empty store does.
	if conn.conn.Kube != "" {
		sources = append(sources, credSourceKube)
	}

	var (
		fields []plugin.Field
		base   map[string]any
	)
	// Asked only when there is something to choose. Most plugins declare one
	// credential, and a picker with one entry is a question with one answer —
	// the same reason the environment picker does not appear when nothing is
	// configured for the plugin.
	if len(inputs) > 1 {
		fields = append(fields, plugin.Field{Name: credInputField, Type: plugin.String,
			Options: inputs, Default: inputs[0],
			Help: "which input this fills"})
	} else {
		base = map[string]any{credInputField: inputs[0]}
	}
	fields = append(fields, plugin.Field{Name: credSourceField, Type: plugin.String,
		Options: sources, Default: sources[0],
		Help: "where its value comes from"})
	if len(entries) > 0 {
		fields = append(fields, plugin.Field{
			Name: credEntryField, Type: plugin.String, Options: entries, Default: entries[0],
			Help: "which stored entry to point it at",
		})
	}
	if conn.conn.Kube != "" {
		fields = append(fields, plugin.Field{
			Name: credKubeField, Type: plugin.String,
			// <secret>/<key>, not a whole coordinate: the namespace comes from
			// this connection's own `kube:` line. Letting a credential name a
			// different namespace would turn a coordinate for one service into
			// a general-purpose cluster reader.
			//
			// The help says out loud that key completion reads the Secret,
			// because it does: there is no keys-only read in the API, so the
			// listing is a real `get secret` in the cluster's audit log —
			// which is why it happens on tab and never as typing.
			// The values stay in kubectl's process either way.
			Help: "which Secret and key, as <secret>/<key> — tab completes; " +
				"listing keys reads the Secret from the coordinate's namespace",
		})
	}
	fields = append(fields, plugin.Field{
		// Secret, so huh masks it and no credential is ever painted on a
		// screen somebody may be sharing.
		Name: credValueField, Type: plugin.Secret,
		Help: "kept in rta's encrypted store — the config file only ever holds the reference",
	})

	what := conn.key + " credential"
	if len(inputs) == 1 {
		what = conn.key + "'s " + inputs[0]
	}
	synth := plugin.Capability{
		ID: "profile.credential", Summary: "where " + row.name + "'s " + what + " comes from",
		Safety: plugin.Write, Inputs: fields,
	}
	m.current = synth
	m.trail = nil
	// Each follow-up appears only for the answer that needs it, so nobody is
	// shown a box that will be ignored — and picking the environment variable
	// asks nothing further, because there is nothing further rta can do.
	m.form = newCapForm(synth, fields, nil, true, base,
		hideUnless(credEntryField, sourceIs(credSourceRef)),
		hideUnless(credValueField, sourceIs(credSourceStore)),
		hideUnless(credKubeField, sourceIs(credSourceKube)))
	m.form.kubeCoord = conn.conn.Kube
	m.form.profileTarget = conn.key
	m.form.credentialEditing = true
	m.origin = modeProfilePlugins
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// sourceIs holds while the credential form's source field reads want.
func sourceIs(want string) func(*capForm) bool {
	return func(cf *capForm) bool {
		bound, ok := cf.bindings[credSourceField]
		return ok && *bound == want
	}
}

// saveCredentialForm writes the chosen mapping back to the config file.
func (m Model) saveCredentialForm() (tea.Model, tea.Cmd) {
	key := m.form.profileTarget
	name := m.profileOpen
	values := m.form.values()
	m.form = nil

	input := str(values[credInputField])
	source := str(values[credSourceField])
	entry := strings.TrimSpace(str(values[credEntryField]))
	secret := str(values[credValueField])
	// ref is what gets written under `secrets:`, scheme included. Built here
	// rather than at the write below, so the two sources that produce one —
	// the store and the cluster — cannot disagree about the grammar.
	ref := ""

	if source == credSourceKube {
		kubeRef := strings.TrimSpace(str(values[credKubeField]))
		// Checked here because this is where somebody is typing it. The same
		// shape internal/profile.kubeSecrets refuses at resolution time, said
		// at the keystroke instead of at the call.
		name, k, ok := strings.Cut(kubeRef, "/")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(k) == "" {
			m.flash = "write it as <secret>/<key>, e.g. pg-creds/password"
			return m, nil
		}
		ref = "kube:" + kubeRef
	}

	if source == credSourceEnv {
		// Nothing to write: the environment path is the operator's shell, and
		// the honest end of it is the line to paste.
		m.flash = "press y on the environment to copy the export line for " + input
		return m.closeToOrigin()
	}

	if source == credSourceStore {
		if secret == "" {
			m.flash = "nothing stored: no value given"
			return m.closeToOrigin()
		}
		// The entry is named after the environment, the plugin and the input
		// rather than asked for. One less field, and a name that says what it is
		// wherever it is later seen in `rta kv list`.
		entry = name + "-" + config.PluginNamespace(key) + "-" + input
		if verr := kv.Store(entry, secret, "credential for profile "+name, "profile:"+name); verr != nil {
			m.flash = "not stored: " + verr.Message
			return m.closeToOrigin()
		}
	}
	if ref == "" {
		if entry == "" {
			m.flash = "nothing changed: no entry chosen"
			return m.closeToOrigin()
		}
		ref = "kv:" + entry
	}

	missing := ""
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		p, ok := cfg.Profiles[name]
		if !ok {
			missing = "profile " + name + " is gone"
			return cfg, false
		}
		conn, has := p.Plugins[key]
		if !has {
			missing = key + " is no longer in " + name
			return cfg, false
		}
		if conn.Secrets == nil {
			conn.Secrets = map[string]string{}
		}
		conn.Secrets[input] = ref
		p.Plugins[key] = conn
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		m.flash = "config not saved: " + err.Error()
		return m.closeToOrigin()
	}
	if missing != "" {
		// The entry was stored before this point and stays stored: it is a
		// real credential under a name `rta kv list` shows, and deleting it
		// because the profile moved would be destroying the thing the
		// operator just typed.
		m.flash = missing
		return m.closeToOrigin()
	}
	m.profiles = m.profileRows()
	m.flash = input + " now comes from " + ref
	return m.closeToOrigin()
}
