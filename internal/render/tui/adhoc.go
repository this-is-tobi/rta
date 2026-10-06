package tui

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/tunnel"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A connection stated from the run form, for the session and for nothing longer.
//
// `rta pg replication --kube staging/databases/svc/postgres:5432 --secret
// password=kube:postgres-app/password` is the same thing as a form: the
// environment picker at the top of a run form ends in "ad hoc connection…", and
// choosing it asks for the coordinate and for a reference to each credential
// before the form it came from is rebuilt on them. The connection is then held
// until the TUI quits and offered by the picker of every capability of that
// plugin it fits, because the look at a database is rarely one call: the
// replication, then the activity, then a table.
//
// **A person's act, as it is on the command line, and it writes nothing.** The
// form exists in the TUI and the flags on the CLI, and the connection exists on
// no tool an agent is offered, so an agent cannot name a cluster, a Service or
// a Secret: over MCP a connection is a profile, named by something a person
// consented to. Nothing here reaches the config file, a profile, the store or
// the recent-values file, and nothing outlives the session; `rta profile set`
// is the way to keep one that turns out to be worth keeping.
//
// **Held per plugin.** A coordinate names one Service, and a Service speaks one
// plugin's protocol: offered to another plugin's form, the connection would
// point a Redis client at a Postgres Service and read the Postgres Secret into a
// Redis password, which the picker's label alone would not have told anyone. So
// each plugin has its own, and stating one for pg leaves redis's alone.
//
// **Held to what a stored connection is held to**, by the same call the CLI
// makes (profile.AdHoc): the coordinate parses, a credential is a reference
// naming its source and never the credential, a `kube:` reference says which
// cluster, and the plugin has an input a forward can fill. The form says what
// is wrong at the box where it can be fixed and keeps what was typed.
//
// **Credentials are references, in a plain box.** A masked box would be the
// place a credential is typed, and the whole point of this form is that it is
// not: what goes in it is `kube:<secret>/<key>` or `kv:<entry>`, which is a name
// and safe to show, and a value typed there is refused as it is on the command
// line. Only the Secret inputs are asked about; the rest of the connection — a
// user, a database — is the run form's own boxes, as it is the capability's own
// flags on the command line.

const (
	// adHocPickLabel is the picker's entry that asks for a connection. It never
	// stays selected: choosing it opens the form, which returns to the run form
	// on whatever it stated, or on what was picked before.
	adHocPickLabel = "— ad hoc connection… —"

	adHocKubeField    = "adhoc-kube"
	adHocSecretPrefix = "adhoc-secret-"

	// adHocClusterScheme is the reference scheme whose `<secret>/<key>` the form
	// completes from the cluster the coordinate names.
	adHocClusterScheme = "kube:"
)

// adHocOffered reports whether a run form for c may state a connection: only
// where a forward has an input to fill, for the reason --profile and --kube are
// per capability on the command line — an entry that does nothing teaches that
// entries are decoration. A capability that reaches its cluster through the
// kubeconfig has no address a coordinate could replace.
func adHocOffered(c plugin.Capability) bool {
	return plugin.Profilable(c) && plugin.Tunnellable([]plugin.Capability{c})
}

// adHocLabel is what the picker calls a held connection, which is also the
// picker's answer when it is chosen. It carries a space, which no profile
// reference can (config.ValidRef), so it can never be mistaken for one.
func adHocLabel(conn config.Connection) string {
	return profile.AdHocName + " · " + profile.AdHocWords(conn.Kube, conn.SecretsFrom, len(conn.Secrets))
}

// isAdHocPick reports whether a picker answer is one of the two entries this
// file adds, and not an environment.
func isAdHocPick(s string) bool {
	return s == adHocPickLabel || strings.HasPrefix(s, profile.AdHocName+" ·")
}

// heldFor is the connection held for c's plugin when c can run through it: held
// to what a stored connection is held to as the plugin declares it. A connection
// stated with a credential this plugin has no input for is not offered, because
// an entry that can only fail is worse than an absent one.
func (m Model) heldFor(c plugin.Capability) (config.Connection, bool) {
	conn, held := m.adHoc[plugin.Namespace(c.ID)]
	if !held || !adHocOffered(c) {
		return config.Connection{}, false
	}
	if verr := profile.AdHoc(c, conn, m.installed()); verr != nil {
		return config.Connection{}, false
	}
	return conn, true
}

// heldPick is the held connection when s names it, for the paths that resolve a
// picker answer without reading the file.
func (m Model) heldPick(c plugin.Capability, s string) (config.Connection, bool) {
	conn, ok := m.heldFor(c)
	if !ok || s != adHocLabel(conn) {
		return config.Connection{}, false
	}
	return conn, true
}

// adHocOptions are the entries the picker ends with: the held connection when c
// can run through it, then the entry that states a new one. Nothing where c has
// no address a forward could fill.
func (m Model) adHocOptions(c plugin.Capability) []string {
	if !adHocOffered(c) {
		return nil
	}
	var out []string
	if conn, ok := m.heldFor(c); ok {
		out = append(out, adHocLabel(conn))
	}
	return append(out, adHocPickLabel)
}

// adHocPicked is pickedConn's answer for a picker that names the ad hoc
// connection: what it states, as the CLI's flags would, or why a run cannot go
// through it. A form open on a connection that has since been replaced or
// dropped is refused here and not run against the base configuration — silently
// reaching somewhere other than what the picker says is the failure the picker
// exists to prevent.
func (m Model) adHocPicked(c plugin.Capability, label string) (string, config.Connection, map[string]any, *view.Error) {
	var none config.Connection
	conn, held := m.adHoc[plugin.Namespace(c.ID)]
	if !held || label != adHocLabel(conn) {
		return "", none, nil, view.Errorf("core.adhoc.gone", "that ad hoc connection is not held any more").
			WithHint("pick the environment again — \"ad hoc connection…\" states one")
	}
	if verr := profile.AdHoc(c, conn, m.installed()); verr != nil {
		return "", none, nil, verr
	}
	return profile.AdHocName, conn, nil, nil
}

// adHocNote is where the run on screen went, in the words of its picker, or ""
// when it did not go through a connection stated here. The result pane says so
// beside the time because an answer from a Service somebody named a minute ago
// and an answer from the base configuration look the same, and the difference is
// the one thing the reader has to know.
//
// What the run did, not what the picker said: a box typed over beside a
// coordinate is a call straight to what was typed (profile.Dial), with the
// connection's credentials still read, and a header that went on saying
// "through staging/…" would be false exactly when the destination is not that
// one. The forward the run opened is on the result, and the note follows it.
func (m Model) adHocNote() string {
	c := m.result.cap
	conn, held := m.adHoc[plugin.Namespace(c.ID)]
	picked, _ := m.lastValues[profileInput].(string)
	if !held || picked != adHocLabel(conn) {
		return ""
	}
	if conn.Tunnelled() && m.result.via == plugin.TunnelNone {
		if m.result.err != nil {
			// Nothing reached anywhere: a refusal before the dial says nothing
			// about where the call would have gone.
			return ""
		}
		return profile.AdHocName + " · direct, no forward"
	}
	return picked
}

// markedRight is a result head's right-hand text with the ad hoc marker in it,
// shortened until the panel will draw it. The panel drops its whole right
// segment when what is left for the title would be under eight cells, and the
// marker is the segment's reason to exist: a result that went through somebody's
// cluster and one that did not would look the same on any terminal narrower than
// the coordinate. "ad hoc" always fits, and is what is left when nothing longer
// does.
func (m Model) markedRight(head panelHead, where, elapsed string) string {
	join := func(parts ...string) string {
		return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " · ")
	}
	// The same arithmetic panel() makes: two cells of padding around the
	// segment, seven of chrome, and the capability's id kept whole.
	fits := func(s string) bool {
		return m.width-lipgloss.Width(s)-2-7 >= lipgloss.Width(head.Title)
	}
	for _, candidate := range []string{
		join(where, elapsed), where, join(profile.AdHocName, elapsed),
	} {
		if fits(candidate) {
			return candidate
		}
	}
	return profile.AdHocName
}

// startAdHocForm asks for the connection. from is the run form the picker was
// on, kept so that completing the form and leaving it both return to it, and
// seed is what to open the boxes on: nil on the first open, where the held
// connection is what is edited, and exactly what the boxes held when a refusal
// sends the person back to fix it — blanks included, since a box somebody
// emptied is not one that asks for the held value back.
func (m Model) startAdHocForm(from *capForm, seed map[string]any) (tea.Model, tea.Cmd) {
	c := from.cap
	ns := plugin.Namespace(c.ID)
	kubeBox := plugin.Field{
		Name: adHocKubeField, Type: plugin.String,
		// Short on purpose: help wraps at the form's width and every wrapped
		// line pushes a field off a small screen.
		Help: "a Service or Pod: context/namespace/kind/name:port — tab completes each segment",
	}
	inputs := m.credentialInputs(ns)
	fields := make([]plugin.Field, 0, 1+len(inputs))
	fields = append(fields, kubeBox)
	// What a reference may start with, offered locally on tab. The entries are
	// read once, as the credential form reads them: a form is short-lived and
	// the store is not a thing to open per keystroke.
	entries := kv.Names()
	starters := make([]string, 0, 1+len(entries))
	starters = append(starters, adHocClusterScheme)
	for _, name := range entries {
		starters = append(starters, "kv:"+name)
	}
	for _, input := range inputs {
		box := adHocSecretPrefix + input
		fields = append(fields, plugin.Field{
			Name: box, Type: plugin.String,
			// Only what strictly extends the box. A starter equal to what is
			// typed — "kube:" once it has been — would lead the offers, and the
			// widget accepting it changes nothing: the box's tab would be dead
			// the moment a listing had landed behind it.
			Suggest: func(_ context.Context, req plugin.Request) []string {
				typed := strings.ToLower(strings.TrimSpace(req.String(box)))
				return slices.DeleteFunc(slices.Clone(starters), func(s string) bool {
					return len(s) <= len(typed) || !strings.HasPrefix(strings.ToLower(s), typed)
				})
			},
			// The listing of a Secret's keys is a real read of it, which is why
			// it is on tab and never on typing; the profile form says the same.
			Help: "kube:<secret>/<key> or kv:<entry> — tab completes, and listing keys reads the Secret; blank when none",
		})
	}

	defaults := map[string]any{}
	if seed == nil {
		if held, ok := m.adHoc[ns]; ok {
			defaults[adHocKubeField] = held.Kube
			for input, ref := range held.Secrets {
				defaults[adHocSecretPrefix+input] = ref
			}
		}
	}
	for k, v := range seed {
		defaults[k] = v
	}

	synth := plugin.Capability{
		ID:      "adhoc.connection",
		Summary: "where " + c.ID + " goes — held until you quit, nothing is written",
		Safety:  plugin.Read, Inputs: fields,
	}
	m.current = synth
	opts := make([]formOption, 0, 1+len(inputs))
	opts = append(opts, withCheck(adHocKubeField, func(v string) error {
		if verr := tunnel.CheckKube(v); verr != nil {
			return errors.New(verr.Message)
		}
		return nil
	}))
	for _, input := range inputs {
		opts = append(opts, withCheck(adHocSecretPrefix+input, func(v string) error {
			probe := config.Connection{Secrets: map[string]string{input: v}}
			if len(probe.BadSecretRefs()) > 0 {
				return errors.New("a reference names its source: kube:<secret>/<key> or kv:<entry> — " +
					"never the value itself")
			}
			return nil
		}))
	}
	m.form = newCapForm(synth, fields, defaults, true, nil, opts...)
	m.form.adHocFrom = from
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// saveAdHocForm holds what the form states and returns to the run form on it.
//
// Refused here, on the form, for what only the whole connection can say: a
// `kube:` reference with no coordinate does not say which cluster holds the
// Secret. What the boxes could check alone they already did, as it was typed.
//
// **Emptying every box drops the connection**, which is the only way to take
// one back: esc leaves it held, and a held connection is offered by the picker of
// everything it fits until the TUI quits.
func (m Model) saveAdHocForm() (tea.Model, tea.Cmd) {
	cf := m.form
	from := cf.adHocFrom
	ns := plugin.Namespace(from.cap.ID)
	values := cf.values()
	if values == nil {
		values = map[string]any{}
	}

	conn := config.Connection{Kube: strings.TrimSpace(str(values[adHocKubeField]))}
	for _, input := range m.credentialInputs(ns) {
		if ref := strings.TrimSpace(str(values[adHocSecretPrefix+input])); ref != "" {
			if conn.Secrets == nil {
				conn.Secrets = map[string]string{}
			}
			conn.Secrets[input] = ref
		}
	}
	held, wasHeld := m.adHoc[ns]
	if conn.Kube == "" && len(conn.Secrets) == 0 {
		if !wasHeld {
			m.refuse("nothing stated: give a coordinate, or a reference to a credential — esc goes back")
			return m.startAdHocForm(from, values)
		}
		m.holdAdHoc(ns, nil, adHocLabel(held), profileNoneLabel)
		m.flash = "dropped the ad hoc connection"
		return m.backToRunForm(from, profileNoneLabel)
	}
	if conn.Kube == "" {
		for _, ref := range conn.Secrets {
			if strings.HasPrefix(ref, adHocClusterScheme) {
				m.refuse("a kube: reference reads its Secret from the coordinate's namespace — " +
					"fill the coordinate, or use kv:<entry>")
				return m.startAdHocForm(from, values)
			}
		}
	}
	if verr := profile.AdHoc(from.cap, conn, m.installed()); verr != nil {
		msg := verr.Message
		if verr.Hint != "" {
			msg += " — " + verr.Hint
		}
		m.refuse(msg)
		return m.startAdHocForm(from, values)
	}
	label := adHocLabel(conn)
	old := ""
	if wasHeld {
		old = adHocLabel(held)
	}
	m.holdAdHoc(ns, &conn, old, label)
	m.flash = "held until you quit — nothing is written"
	return m.backToRunForm(from, label)
}

// holdAdHoc replaces (or, with nil, drops) the connection held for a plugin, and
// moves what named the old one to the new: a listing on screen, the page a row
// action came from and the last run's inputs all carry the picker's answer, and
// a label that stopped naming anything turned the page into an error the moment
// somebody pressed esc.
func (m *Model) holdAdHoc(ns string, conn *config.Connection, oldLabel, newLabel string) {
	held := maps.Clone(m.adHoc)
	if held == nil {
		held = map[string]config.Connection{}
	}
	if conn == nil {
		delete(held, ns)
	} else {
		held[ns] = *conn
	}
	m.adHoc = held
	if oldLabel == "" || oldLabel == newLabel {
		return
	}
	relabel := func(values map[string]any) map[string]any {
		if s, _ := values[profileInput].(string); s != oldLabel {
			return values
		}
		out := maps.Clone(values)
		out[profileInput] = newLabel
		return out
	}
	if plugin.Namespace(m.result.cap.ID) == ns {
		m.lastValues = relabel(m.lastValues)
	}
	trail := slices.Clone(m.trail)
	for i := range trail {
		if plugin.Namespace(trail[i].cap.ID) == ns {
			trail[i].values = relabel(trail[i].values)
		}
	}
	m.trail = trail
}

// leaveAdHocForm goes back to the run form without stating anything, on the
// environment it was on before the picker moved to the entry that asked.
func (m Model) leaveAdHocForm() (tea.Model, tea.Cmd) {
	from := m.form.adHocFrom
	return m.backToRunForm(from, from.builtOn)
}

// backToRunForm rebuilds the run form the ad hoc form was opened from, with its
// picker on pick and everything the person had typed in it kept — the move
// reseedOnPickerMove makes for any other answer.
func (m Model) backToRunForm(from *capForm, pick string) (tea.Model, tea.Cmd) {
	values := from.values()
	values[profileInput] = pick
	m.current = from.cap
	m.form = m.runForm(from.cap, from.offered, withoutPicker(from.cap, values), values)
	m.fitForm()
	return m, m.form.form.Init()
}

// firstFocus is what a run form that has just been opened does next: starts, and
// — when the picker has nothing to choose but the base configuration and the
// entry that states a connection — moves past it.
//
// The picker is first because it changes what every other answer means, and
// with an environment configured that is the question to be asked first. With
// none, it is a field every form of a plugin a forward can fill has gained for
// somebody who never asked for it: the first key they press would land in a
// picker that reads "j" as down, and the form they have used for a year would
// want an enter before the host box. It stays in the form, listed, one up-arrow
// away — which is how it is found — and the cursor starts where it always did.
func (m Model) firstFocus() tea.Cmd {
	init := m.form.form.Init()
	if len(m.form.fields) > 0 {
		f := m.form.fields[0]
		if f.Name == profileInput && len(f.Options) == 2 && f.Options[1] == adHocPickLabel {
			return tea.Batch(init, huh.NextField)
		}
	}
	return init
}

// adHocCompletionTarget is the box a tab would cluster-complete in the ad hoc
// form right now: the coordinate, or a credential's `kube:` reference once there
// is a coordinate to read the Secret's namespace from. A reference that starts
// with `kv:` completes from the local entries, which is the ordinary local tab.
func (m Model) adHocCompletionTarget() (field, coord string, ok bool) {
	cf := m.form
	name, focused := m.focusedInput()
	if !focused {
		return "", "", false
	}
	if name == adHocKubeField {
		return name, "", true
	}
	if !strings.HasPrefix(name, adHocSecretPrefix) {
		return "", "", false
	}
	typed := strings.TrimSpace(*cf.bindings[name])
	coordinate := strings.TrimSpace(*cf.bindings[adHocKubeField])
	if !strings.HasPrefix(typed, adHocClusterScheme) || coordinate == "" {
		return "", "", false
	}
	return name, coordinate, true
}
