package tui

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The profiles panes: the environments an operator has, which one is switched
// on, what each covers, and where each credential comes from.
//
// Two levels, because a profile is two things. The outer pane is the list of
// environments — the thing somebody switches between, several times a day, and
// the thing a grant is issued against. The inner pane is one environment's
// plugins, which is where the values actually live and where nobody goes except
// to change something.
//
// It exists for the reason the plugins pane exists: the alternative was
// hand-editing YAML and guessing. A profile is the unit an agent's permission is
// granted against and the bound on what agents may reach, so "which
// environments do I have, what does each reach, and which one am I in" is a
// question somebody has to be able to answer *before* issuing consent — and
// answering it by opening a config file is exactly the friction that makes
// people not bother.

// The picker's field name and the entry meaning "no profile".
//
// `profile` is reserved on any capability a profile can fill (Capability
// validate), so a synthetic field of that name can never collide with an
// input a plugin declared — which is what makes taking it back out of the
// form's values safe rather than a guess about names.
const (
	profileInput     = "profile"
	profileNoneLabel = "— base configuration —"
)

// profilePicker is the field that chooses an environment at the moment somebody
// runs something, or nil when there is nothing to choose.
//
// A picker rather than a text box, for the same reason every closed-set input
// in rta is one: the answers are known and typing one is a chance to get it
// wrong. Invalid profiles are left out — an entry that can only fail is worse
// than an absent one — and "base configuration" is always first, because a
// list where the ordinary case is not offered makes the ordinary case feel
// like a mistake.
func (m Model) profilePicker(c plugin.Capability, on string) *plugin.Field {
	if !plugin.Profilable(c) {
		return nil
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return nil
	}
	ns := plugin.Namespace(c.ID)
	bad := map[string]bool{}
	for _, p := range profile.Check(cfg, m.reg) {
		bad[p.Name] = true
	}
	options := []string{profileNoneLabel}
	for _, name := range cfg.ProfilesFor(ns) {
		if bad[name] {
			continue
		}
		// The refs a call would accept: an environment holding several
		// connections to this plugin lists as staging/analytics beside
		// staging, because a bare name over several labeled entries is
		// exactly what Lookup refuses — a picker option that can only fail
		// is worse than an absent one.
		options = append(options, profile.InstanceRefs(cfg.Profiles[name], name, ns)...)
	}
	if len(options) == 1 {
		// Nothing configured for this plugin: a picker with one entry is a
		// question with one answer, which is not a question.
		return nil
	}
	// Membership, not name-validity: the options already exclude broken
	// profiles and already spell out which refs are answerable, so a
	// remembered choice that is no longer among them — renamed, broken, or
	// a bare name that has since grown labeled instances — falls back to
	// the base configuration rather than seeding a Select with an answer
	// it does not offer.
	if !slices.Contains(options, on) {
		on = profileNoneLabel
	}
	return &plugin.Field{
		Name: profileInput, Type: plugin.String, Options: options, Default: on,
		Help: "which configured environment to run against",
	}
}

// withoutPicker is values as a handler must see them: without the environment
// question, which is rta's and not the capability's.
//
// **Only where the name is rta's to take.** `profile` is reserved on a
// capability a profile can fill and nowhere else (Capability.validate), because
// builtin/grant legitimately declares it as *data* — `grant revoke pg
// --profile staging` names the grant to take back. Stripping it unconditionally
// handed runRevoke an empty profile, which skips its own narrowing filter and
// revokes every grant on pg across every connection: the widest possible
// reading of the narrowest possible request, from a form that showed the
// narrow one. The guard is Profilable, the same question profilePicker asks
// before offering the field at all.
//
// A copy rather than a delete, because the map it is given is the one the shell
// keeps for `r` and `e` — see startRun.
func withoutPicker(c plugin.Capability, values map[string]any) map[string]any {
	if !plugin.Profilable(c) {
		return values
	}
	if _, ok := values[profileInput]; !ok {
		return values
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		if k == profileInput {
			continue
		}
		out[k] = v
	}
	return out
}

// pickedProfile is the environment a run form should open on: whichever one the
// last run of this capability was aimed at, or else whatever is switched on.
//
// Reading base rather than only the switch is what makes `e` — edit the inputs
// of the result you are looking at — stay on the connection that produced it.
// Re-seeding from the switch instead would take somebody who deliberately ran
// against prod, and put their next edit somewhere else without saying so.
func (m Model) pickedProfile(c plugin.Capability, base map[string]any) string {
	if !plugin.Profilable(c) {
		return ""
	}
	if raw, ok := base[profileInput]; ok {
		if s, isStr := raw.(string); isStr {
			if s == profileNoneLabel {
				return ""
			}
			return s
		}
	}
	// Nothing picked, so the answer is whatever is switched on *now* — read
	// from the selection rather than from m.active, which is only as fresh as
	// the last refresh tick and the tick does not run while a form is open.
	// That minute is exactly where a deadline lapses: without this the boxes
	// went on showing production's host while the run correctly went
	// unprofiled, which is the screen and the call disagreeing again, in the
	// safe direction and still a lie.
	return profile.Active()
}

// withPicker appends the environment picker to a run form's fields, defaulted
// to on.
//
// Applied only where a form is about to *run* something, and only to real
// capabilities: the plugin-config and profile editors reuse capForm over
// synthetic capabilities whose fields carry Config keys, so an unguarded rule
// would offer to pick an environment while editing the environment itself.
func (m Model) withPicker(c plugin.Capability, fs []plugin.Field, on string, forwarded []string) []plugin.Field {
	picker := m.profilePicker(c, on)
	if picker == nil {
		return fs
	}
	// The environment's port-forward fills fields this form therefore does not
	// ask about (forwardFilled), and the picker is where that is said: it is
	// the field that made it true, and one help line here costs less screen
	// than the boxes it explains away did.
	if len(forwarded) > 0 {
		picker.Help = "runs through a tunnel, which fills " + strings.Join(forwarded, ", ")
	}
	// First, because it changes what every other answer means: a host typed
	// under one environment is not the same value under another, and a picker
	// at the bottom of the form is one you notice after filling it in.
	return append([]plugin.Field{*picker}, fs...)
}

// environmentNotes says, under the box, when the environment the picker names
// is the thing that answers it.
//
// Two kinds of box open empty under an environment that has an answer for
// them, for two unrelated reasons, and on screen they are the same box: a
// credential, which profileSeed drops on purpose rather than paint a
// passphrase's length in dots, and any input a `secrets:` mapping fills,
// which is empty because Bind is pure — a reference is not resolved until the
// run, and a form seed may neither unlock a store nor reach a cluster to find
// out. The operator's question at either one is "do I have to type this", and
// until this the form could not answer it: an empty password box under
// staging looked identical whether staging supplied the password or nobody
// did.
//
// **The reference is named, the value never is.** `kv:prod-db-password` is
// the name of an entry — configuration this operator wrote, already legible
// one pane over — while the value behind it is the thing the store exists to
// keep off screens. Nothing here could fetch one by mistake even if it tried:
// Fill is the only half that resolves a reference, and it is unreachable from
// a form seed by construction.
//
// Silence when the environment has no answer, which is most boxes on most
// screens. A "not set" under every unconfigured credential is what would make
// the one line that does say something get skipped.
func environmentNotes(c plugin.Capability, fs []plugin.Field, seed map[string]any,
	name string, conn config.Connection,
) []plugin.Field {
	if name == "" {
		return fs
	}
	refs := map[string]config.SecretRef{}
	for _, r := range conn.SecretRefs() {
		refs[r.Input] = r
	}
	// A copy rather than a rewrite in place. On the untunnelled path fs is
	// the capability's own declared inputs, which the registry hands out by
	// reference and runForm keeps to rebuild this form on every picker move —
	// annotating those would stamp one environment's answer onto the
	// declaration and then onto every later form built from it.
	out := make([]plugin.Field, 0, len(fs))
	for _, f := range fs {
		if note := environmentNote(c, f, seed, name, refs); note != "" {
			if f.Help == "" {
				f.Help = note
			} else {
				f.Help += " — " + note
			}
		}
		out = append(out, f)
	}
	return out
}

// environmentNote is one box's clause, or "".
func environmentNote(c plugin.Capability, f plugin.Field, seed map[string]any,
	name string, refs map[string]config.SecretRef,
) string {
	// A box already showing something needs no note about where a value would
	// come from: whatever is in the seed is what won, and Fill applies that
	// same precedence when the call is finally made.
	//
	// Credentials are the exception, and by construction rather than by
	// choice: profileSeed strips them, so a masked box is empty whether or
	// not the environment fills it — the ambiguity this whole function
	// exists to remove. One does reach the seed, from a plugin's own
	// configuration, and it is the case that most needs the line: the box
	// shows that value in dots while a `secrets:` reference at the profile
	// layer is what the run will actually use.
	if _, shown := seed[f.Name]; shown && !f.Type.Sensitive() {
		return ""
	}
	if !plugin.ProfileFillable(c, f) {
		return ""
	}
	row := credentialRow{input: f.Name, ref: refs[f.Name]}
	// No environment channel for a labeled instance — see Bind, and
	// credentialRows, which reads the same emptiness the same way.
	if config.RefInstance(name) == "" {
		row.env = plugin.ProfileEnvVar(name, f.Name)
		_, row.exported = os.LookupEnv(row.env)
	}
	return row.formNote(name)
}

// profileRow is one configured environment and everything the outer pane shows
// about it.
type profileRow struct {
	name string
	p    config.Profile
	// problem is the first reason this environment cannot be used, or "".
	problem string
	// active is true when this is the one switched on right now.
	active bool
	// until is when the switch lapses, nil for no deadline. Only meaningful
	// while active.
	until *time.Time
	// conns is one entry per plugin this environment configures.
	conns []connRow
}

func (r profileRow) valid() bool { return r.problem == "" }

// connRow is one plugin inside an environment: what it sets and where its
// credentials come from.
type connRow struct {
	key     string
	conn    config.Connection
	problem string
	// credentials is one line per Secret input the plugin declares, saying
	// where this environment's value for it comes from.
	credentials []credentialRow
}

func (r connRow) valid() bool { return r.problem == "" }

// credentialRow is where one Secret input's value comes from, if anywhere.
//
// The whole point of the row is that "nowhere" is a legitimate and common
// answer that must not look like an error: an environment pointed at a database
// that trusts the local socket needs no password at all.
type credentialRow struct {
	input string
	// env is the RTA_PROFILE_<NAME>_<INPUT> name — the answer to "how do I
	// give this thing its password", readable whether or not it happens to
	// be set. Empty for a labeled instance, which has no environment
	// channel at all (see Bind); source() and the export-line copy both
	// read the emptiness as "steer at `secrets:` instead".
	env      string
	exported bool
	// ref is the `secrets:` mapping, empty when there is none.
	ref config.SecretRef
}

// winner names the channel this environment actually reads the credential
// from, and the one it beats — both empty when nothing supplies it.
//
// The decision, split from the wording, because two screens ask it and phrase
// the answer differently: a table cell in this pane, and a clause under the
// box on a run form. Written out twice, the day a third credential channel
// arrives is the day one of them goes on naming a channel that no longer
// wins — and a form whose help disagrees with what the run does is the exact
// failure the picker and the seed were made to agree about.
func (c credentialRow) winner() (won, beaten string) {
	ref := ""
	if c.ref.Scheme != "" {
		ref = c.ref.Scheme + ":" + c.ref.Ref
	}
	switch {
	case c.exported && ref != "":
		return "$" + c.env, ref
	case c.exported:
		return "$" + c.env, ""
	case ref != "":
		return ref, ""
	}
	return "", ""
}

// source names where the value actually comes from, in resolution order.
func (c credentialRow) source() string {
	won, beaten := c.winner()
	switch {
	case beaten != "":
		return won + " (set — wins over " + beaten + ")"
	case c.exported:
		return won + " (set)"
	case won != "":
		return won
	case c.env == "":
		return "not set — a labeled instance takes a `secrets:` reference"
	default:
		return "not set"
	}
}

// formNote is the same answer as a clause under a form's box: what fills this
// input, named rather than shown, or "" when nothing does.
func (c credentialRow) formNote(name string) string {
	won, beaten := c.winner()
	if won == "" {
		return ""
	}
	note := name + " fills it from " + won
	if beaten != "" {
		// Named too, and named as the loser. An operator reading their own
		// `secrets:` line expects it to be the answer, so the box saying only
		// "$RTA_PROFILE_PROD_PASSWORD" would leave them to work out for
		// themselves that a variable in this shell is quietly outranking the
		// file — which is the whole reason the pane spells this precedence
		// out as well.
		note += ", not " + beaten
	}
	return note
}

func (c credentialRow) satisfied() bool {
	won, _ := c.winner()
	return won != ""
}

// profileRows builds the outer pane's contents from the config on disk.
func (m Model) profileRows() []profileRow {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil
	}
	whole := map[string]string{}
	perPlugin := map[string]map[string]string{}
	for _, p := range profile.Check(cfg, m.reg) {
		if p.Plugin == "" {
			if _, already := whole[p.Name]; !already {
				whole[p.Name] = p.Reason
			}
			continue
		}
		if perPlugin[p.Name] == nil {
			perPlugin[p.Name] = map[string]string{}
		}
		if _, already := perPlugin[p.Name][p.Plugin]; !already {
			perPlugin[p.Name][p.Plugin] = p.Reason
		}
	}
	sel := profile.LoadSelection()
	active := sel.Name(time.Now())

	rows := make([]profileRow, 0, len(cfg.Profiles))
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		problem := whole[name]
		conns := make([]connRow, 0, len(p.Plugins))
		for _, key := range p.PluginKeys() {
			conn := p.Plugins[key]
			conns = append(conns, connRow{
				key: key, conn: conn, problem: perPlugin[name][key],
				credentials: m.credentialRows(name, key, conn),
			})
			if problem == "" && perPlugin[name][key] != "" {
				problem = key + ": " + perPlugin[name][key]
			}
		}
		row := profileRow{name: name, p: p, problem: problem, active: name == active, conns: conns}
		if row.active {
			row.until = sel.Until
		}
		rows = append(rows, row)
	}
	return rows
}

// credentialRows lists every Secret input this plugin declares, and where this
// environment gets it.
//
// Driven off the declaration rather than off what the profile happens to set,
// because the useful thing to show is the credential the connection *needs* —
// an environment with none is the case somebody has to see.
func (m Model) credentialRows(name, key string, conn config.Connection) []credentialRow {
	refs := map[string]config.SecretRef{}
	for _, r := range conn.SecretRefs() {
		refs[r.Input] = r
	}
	ns := config.PluginNamespace(key)
	var out []credentialRow
	seen := map[string]bool{}
	for _, c := range m.reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Type != plugin.Secret || !plugin.ProfileFillable(c, f) || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			// A labeled instance has no environment channel — see Bind: a
			// variable for `staging/analytics` would be forgeable by naming
			// a profile carefully, so none exists, and showing one here
			// would teach an export that fills the default instead. The
			// empty env is what tells source() and the export-line copy to
			// steer at `secrets:` references.
			env, exported := "", false
			if _, instance, _ := config.SplitKey(key); instance == "" {
				env = plugin.ProfileEnvVar(name, f.Name)
				_, exported = os.LookupEnv(env)
			}
			out = append(out, credentialRow{
				input: f.Name, env: env, exported: exported, ref: refs[f.Name],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].input < out[j].input })
	return out
}

// fillableInputs is every input a `secrets:` mapping may target for this
// plugin, sorted, credentials first.
//
// Wider than credentialRows, deliberately. That list is what a page *about
// credentials* should show — the Secret inputs, the ones with nowhere else to
// come from. What a mapping may actually fill is whatever
// plugin.ProfileFillable admits, which is the same rule internal/profile.Fill
// applies: `user` from a cluster Secret's `username` key is an ordinary thing
// to want, and offering only the Secrets made it unreachable from this screen
// while working perfectly in the file.
//
// Credentials first because they are why somebody opens this, and because a
// picker whose first entry is `database` invites filling in the wrong one.
//
// tunnelled narrows the list to what a mapping can still reach: under a
// `kube:` coordinate the endpoint-role inputs are the forward's — Dial lays
// the endpoint over everything Fill resolved, and checkSecretRefs refuses
// the pair — so offering `host` there is offering a mapping that is fetched
// and discarded on every call.
func (m Model) fillableInputs(key string, tunnelled bool) []string {
	ns := config.PluginNamespace(key)
	creds, others := []string{}, []string{}
	seen := map[string]bool{}
	for _, c := range m.reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if !plugin.ProfileFillable(c, f) || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			if tunnelled && f.Endpoint != plugin.EndpointNone {
				continue
			}
			// A mapping delivers text, and the request readers do not coerce
			// — checkSecretRefs refuses a number or bool target, so offering
			// one invites a file the next screen refuses.
			if f.Type == plugin.Int || f.Type == plugin.Bool || f.Type == plugin.Float {
				continue
			}
			if f.Type.Sensitive() {
				creds = append(creds, f.Name)
			} else {
				others = append(others, f.Name)
			}
		}
	}
	sort.Strings(creds)
	sort.Strings(others)
	return append(creds, others...)
}

// missing counts the credentials this environment needs and does not have.
func (r profileRow) missing() (needed, unset int) {
	for _, c := range r.conns {
		for _, cr := range c.credentials {
			needed++
			if !cr.satisfied() {
				unset++
			}
		}
	}
	return needed, unset
}

// openProfile is the row the inner pane is showing, or the zero value.
func (m Model) openProfile() (profileRow, bool) {
	for _, r := range m.profiles {
		if r.name == m.profileOpen {
			return r, true
		}
	}
	return profileRow{}, false
}

func (m Model) profileFooter() string { return m.footerFor(modeProfiles) }

// profileFooterItems is the profiles pane's bar.
//
// **`y export lines` was the one hint in the app that named an artifact
// instead of an action.** Every other entry reads as a verb — `u use`, `n
// new`, `d delete` — and `y` is the copy key on every screen that has one, so
// a label that dropped the verb read as though the key exported something,
// or as a stray line of output.
//
// It is also conditional now, on the same reasoning the panes already apply to
// a column: the key copies `export VAR=…` for credentials nothing has set, so
// on an environment where nothing is unset it advertised an action whose whole
// answer was "nothing to export". Offered only when there is something to
// copy, its presence is the news — and it lands directly under the band that
// has just said "2 credentials · 1 not set".
func (m Model) profileFooterItems() []hintItem {
	items := []hintItem{
		item(bindScroll), item(bindUse), alias(labelled(bindOpen, "plugins"), "right", "l"),
		alias(item(bindConfig), "e"), item(bindNew), item(bindRemove),
	}
	if m.selectedProfileHasUnsetCredential() {
		items = append(items, labelled(bindCopy, "copy export lines"))
	}
	return append(items, alias(item(bindBack), "f"), item(bindQuit))
}

// selectedProfileHasUnsetCredential reports whether the environment under the
// cursor needs a credential that neither the environment nor a mapping
// supplies — which is exactly what `y` writes a line for.
func (m Model) selectedProfileHasUnsetCredential() bool {
	if m.profileSel >= len(m.profiles) {
		return false
	}
	row := m.profiles[m.profileSel]
	if !row.valid() {
		return false
	}
	for _, c := range row.conns {
		for _, cr := range c.credentials {
			// The same rule copyExportLine applies: a labeled instance's
			// credential has no export line to offer, so it must not be
			// what makes the key appear.
			if !cr.satisfied() && cr.env != "" {
				return true
			}
		}
	}
	return false
}

func (m Model) profileBodyHeight() int {
	return max(m.height-1-lipgloss.Height(m.profileFooter())-2, bandHeight)
}

func (m *Model) clampProfileScroll(bodyHeight int) {
	clampBand(m.profileSel, &m.profileScroll, len(m.profiles), visibleBands(bodyHeight))
}

func (m *Model) clampConnScroll(bodyHeight int) {
	row, _ := m.openProfile()
	clampBand(m.connSel, &m.connScroll, len(row.conns), visibleBands(bodyHeight))
}

// profilesView is the outer pane: one band per environment.
func (m Model) profilesView() string {
	header := theme.Title.Render(" rta") + theme.Subtle.Render("  profiles")
	footer := m.profileFooter()

	width := m.width
	if width <= 0 {
		width = 80
	}

	if len(m.profiles) == 0 {
		// The empty state carries the whole idea, because this is where
		// somebody arrives who has never made one. Two sentences and the key
		// that starts it beats a blank pane and a footer.
		body := panel(panelHead{Title: "profiles", Right: "none yet"},
			// The newlines stay outside the styled string: lipgloss pads a
			// multi-line block to its widest line, so a trailing "\n\n"
			// inside Render carries 29 cells of padding into whatever is
			// concatenated after it — the paragraph below used to start a
			// third of the way across the pane.
			theme.Subtle.Render("  No environments configured.")+"\n\n"+
				"  A profile is one environment across every plugin that has\n"+
				"  something in it: "+theme.Key.Render("rta use proj1-staging")+" points pg, s3\n"+
				"  and vault at staging at once, and\n  "+
				theme.Key.Render("rta grant allow pg --profile proj1-staging --ttl 1h")+"\n"+
				"  lets an agent reach that one for an hour.\n\n  Press "+
				theme.AccentTxt.Render("n")+" to make one.",
			width, m.height-1-lipgloss.Height(footer), true)
		return header + "\n" + body + "\n" + footer
	}

	visible := visibleBands(m.profileBodyHeight())
	scroll := min(max(m.profileScroll, 0), max(0, len(m.profiles)-visible))

	bands := make([]band, 0, len(m.profiles))
	for _, row := range m.profiles {
		bands = append(bands, band{
			name:   row.name,
			right:  []string{profileState(row), profileStatus(row)},
			detail: []string{profileCovers(row), profileDetail(row)},
		})
	}

	right := fmt.Sprintf("%d configured", len(m.profiles))
	if len(m.profiles) > visible {
		right = fmt.Sprintf("%d-%d of %d", scroll+1,
			min(scroll+visible, len(m.profiles)), len(m.profiles))
	}
	body := panel(panelHead{Title: "profiles", Right: right},
		renderBands(bands, m.profileSel, scroll, visible, width-4),
		width, m.height-1-lipgloss.Height(footer), true)
	return header + "\n" + body + "\n" + footer
}

// profileState is the "am I in production" fact, and the one somebody scans
// for. A deadline is shown as time remaining rather than a timestamp, because
// "23m left" is the form of the answer to the question being asked.
func profileState(row profileRow) string {
	if !row.active {
		return theme.Subtle.Render("—")
	}
	if row.until == nil {
		return theme.GoodText.Render("[on]")
	}
	left := time.Until(*row.until)
	if left <= 0 {
		return theme.Subtle.Render("—")
	}
	return theme.GoodText.Render("[on · " + profile.ShortDuration(left) + " left]")
}

func profileStatus(row profileRow) string {
	if !row.valid() {
		return theme.BadText.Render("invalid")
	}
	return theme.GoodText.Render("ok")
}

// profileCovers is the second line: which plugins this environment reaches.
func profileCovers(row profileRow) string {
	if len(row.p.Plugins) == 0 {
		return theme.WarnText.Render("covers nothing — no plugins configured")
	}
	// **Styled per segment, never as one assembled string.** A style applied
	// over text that already carries ANSI ends at the first reset inside it:
	// the separator emits its own colour and then `ESC[m`, so wrapping the
	// joined string left the first plugin faded and every one after it in the
	// terminal's default — `pg@…` dim, `s3@…` and `vault@…` bright white,
	// which read as a status difference between plugins that does not exist.
	//
	// Cleaned per segment for the same reason it is styled per segment: a
	// plugin key and a note are whatever the config file holds, and a band's
	// detail line is documented as already styled, so the renderer cannot
	// clean it afterwards without stripping the colour this function just
	// applied. The data is cleaned; the styling is added after.
	keys := row.p.PluginKeys()
	styled := make([]string, 0, len(keys))
	for _, key := range keys {
		styled = append(styled, theme.Faded.Render(textclean.Terminal(key)))
	}
	detail := strings.Join(styled, theme.Subtle.Render(" · "))
	if row.p.Note != "" {
		detail += theme.Subtle.Render(" · " + textclean.Terminal(row.p.Note))
	}
	return detail
}

// profileDetail is the third line: why this environment does not work, or how
// much of it is ready.
//
// The problem wins when there is one. An environment that cannot resolve has
// nothing useful to say about its credentials, and showing both invites
// somebody to fix the wrong thing.
func profileDetail(row profileRow) string {
	if !row.valid() {
		return theme.BadText.Render(row.problem)
	}
	needed, unset := row.missing()
	switch {
	case needed == 0:
		return theme.Faded.Render("no credential needed")
	case unset == 0:
		return theme.Faded.Render(plural(needed, "credential") + " resolved")
	default:
		return theme.WarnText.Render(fmt.Sprintf("%s · %d not set",
			plural(needed, "credential"), unset))
	}
}

// connsView is the inner pane: one band per plugin in an environment.
func (m Model) connsView() string {
	row, ok := m.openProfile()
	if !ok {
		return m.profilesView()
	}
	header := theme.Title.Render(" rta") +
		theme.Subtle.Render("  profiles · ") + theme.Key.Render(textclean.Terminal(row.name))
	footer := m.footerFor(modeProfilePlugins)

	width := m.width
	if width <= 0 {
		width = 80
	}

	if len(row.conns) == 0 {
		body := panel(panelHead{Title: row.name, Right: "no plugins"},
			theme.Subtle.Render("  This environment configures no plugin yet.")+"\n\n"+
				"  Press "+theme.AccentTxt.Render("n")+" to add one — pick the plugin, then\n"+
				"  fill in what this environment changes about it.",
			width, m.height-1-lipgloss.Height(footer), true)
		return header + "\n" + body + "\n" + footer
	}

	visible := visibleBands(m.profileBodyHeight())
	scroll := min(max(m.connScroll, 0), max(0, len(row.conns)-visible))

	bands := make([]band, 0, len(row.conns))
	for _, c := range row.conns {
		status := theme.GoodText.Render("ok")
		if !c.valid() {
			status = theme.BadText.Render("invalid")
		}
		bands = append(bands, band{
			name:   c.key,
			right:  []string{status},
			detail: []string{connSummary(c), connDetail(c)},
		})
	}

	right := fmt.Sprintf("%d configured", len(row.conns))
	if len(row.conns) > visible {
		right = fmt.Sprintf("%d-%d of %d", scroll+1,
			min(scroll+visible, len(row.conns)), len(row.conns))
	}
	body := panel(panelHead{Title: row.name, Right: right},
		renderBands(bands, m.connSel, scroll, visible, width-4),
		width, m.height-1-lipgloss.Height(footer), true)
	return header + "\n" + body + "\n" + footer
}

// connSummary renders the overlay compactly: the values, not the key names,
// because "host=prod.db.internal" is what tells somebody which connection this
// is and "host, port, database" tells them nothing they did not already know.
func connSummary(c connRow) string {
	keys := make([]string, 0, len(c.conn.Set))
	for k := range c.conn.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return theme.Subtle.Render("sets nothing — the plugin's own configuration applies")
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, textclean.Terminal(fmt.Sprintf("%s=%v", k, c.conn.Set[k])))
	}
	return theme.Faded.Render(strings.Join(parts, " "))
}

func connDetail(c connRow) string {
	if !c.valid() {
		return theme.BadText.Render(c.problem)
	}
	if len(c.credentials) == 0 {
		return theme.Faded.Render("no credential needed")
	}
	parts := make([]string, 0, len(c.credentials))
	for _, cr := range c.credentials {
		text := cr.input + ": " + cr.source()
		if !cr.satisfied() {
			parts = append(parts, theme.WarnText.Render(text))
			continue
		}
		parts = append(parts, theme.Faded.Render(text))
	}
	return strings.Join(parts, theme.Subtle.Render(" · "))
}
