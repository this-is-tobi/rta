package tui

import (
	"charm.land/lipgloss/v2"
	"slices"

	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The plugins pane: what is installed, and what it puts on the dashboard.
//
// It exists because hiding was one-way. `H` took a tile off the dashboard and
// the only way back was to find the config file and guess a capability ID —
// which is exactly the friction the in-dashboard arranging was meant to
// delete. A hide you cannot undo is not a preference, it is a mistake waiting
// to happen.
//
// It answers the other question in the same breath: which plugins do I
// actually have? A tile grid cannot say, because plugins with nothing
// glanceable have no tile — so this is the one screen where `cert` and `http`
// are as visible as `note`, and it says why they are not on the dashboard
// rather than leaving you to wonder.
//
// It also says where each plugin came from, which it could not before
// external plugins existed. plugin.Plugin carries a name, a summary and its
// capabilities and nothing about its origin, so `kv` and a binary somebody
// dropped on $PATH rendered as the same kind of thing — on the one screen
// whose whole job is "what do I actually have". Every security property in
// pluginhost is built on binding a plugin to its artifact, and
// this is where a person gets to see that binding.

// pluginRow is one installed plugin and its relationship to the dashboard.
type pluginRow struct {
	plugin plugin.Plugin
	// origin is the artifact an external plugin was loaded from, as the
	// registry recorded it at registration. Its zero value means built in:
	// compiled into the rta binary the user already chose to run, which is
	// why it needs no path and no digest.
	origin registry.Origin
	// lock is what rta.lock records about this artifact, or nil.
	//
	// **Matched by digest and never by name**, which is the same rule every
	// authorisation in rta follows. A row named `pg` and a lock entry named
	// `pg` are not evidence of anything on their own — an upgrade that did not
	// finish, or a local build placed over the store, leaves exactly that
	// shape — so a version and an index attached on the strength of the name
	// would be rta reporting provenance for bytes it did not recognise.
	// Nothing here is nil because a plugin is unmanaged; nil means "no record
	// of *these* bytes", and the row says the smaller true thing instead.
	lock *plugindist.LockEntry
	// tile is the capability that represents it on the dashboard, empty when
	// the plugin has nothing it can show unprompted.
	tile string
	// shown is false when a tile exists but the user hid it.
	shown bool
	// waiting marks an artifact discovery found on $PATH and refused to
	// launch, because nothing has trusted it.
	//
	// **It is a row rather than an absence, and that is the whole point.** A
	// trust gate's failure mode is silence: a plugin installed, present and
	// doing nothing looks exactly like one that was never installed. Every
	// other inventory rta has says so — `rta plugin list` carries the row,
	// `rta doctor` warns, and startup prints a line — and this pane, whose
	// stated job is "which plugins do I actually have?", was the one that did
	// not, while also printing a confident count that left them out.
	waiting bool
	// decided records a trust decision taken in this session, which is a
	// third state and not a fourth flag: the file has changed and the
	// process has not, so the row must say something neither "trusted" nor
	// "waiting" covers. Empty until somebody presses the key.
	decided string
	// ungranted is what this plugin declares it needs to read and has not
	// been allowed.
	//
	// **A loaded plugin that cannot do anything looks exactly like one that
	// works until it is called.** A plugin whose every call needs a kubeconfig
	// fails with whatever its own tool says about a file it cannot open, which
	// reads as a broken install rather than as a decision nobody has made. The
	// pane whose job is "which plugins do I actually have" is the pane that
	// should say so — the same argument `waiting` above is written for, one
	// permission further along.
	ungranted []plugin.Need
	// granted is what this plugin declares it needs and *has* been allowed.
	//
	// **The counterpart to ungranted, and its absence made the pane able to
	// report a permission only while it was missing.** A plugin allowed to read
	// a kubeconfig rendered identically to one that never asked for anything:
	// ungranted was empty in both cases, so the line fell through to the plain
	// summary. That is the same silence `waiting` and `ungranted` were both
	// written to break, pointed at the state somebody would actually want to
	// audit — "what have I handed this binary?" is a question about what was
	// granted, and the pane could only ever answer what was not.
	//
	// It is also the prerequisite for taking a grant back from here. A control
	// that revokes something the screen never shows is a control with no
	// referent: you cannot point at what you are removing.
	granted []plugin.Need
}

// The two decisions a row can carry from this session.
const (
	decidedTrust   = "trusted"
	decidedUntrust = "untrusted"
)

// pluginGroup is where a plugin's bytes came from, which is the fact that
// changes how every other fact on its row reads: "13 capabilities, one of them
// destructive" means one thing about code compiled into the binary this
// operator chose to run, and something else about a file that appeared on
// their $PATH.
//
// **There is deliberately no "official" group, and that is a finding rather
// than an omission.** The obvious third band would be "installed from rta's
// own index", and nothing rta records can honestly say that. internal/plugindist
// attaches no default index at all — `rta plugin index add <name> <repository>`
// is the whole story, and the name is the operator's to choose — so
// LockEntry.Index holds whatever they typed. `rta plugin index add official
// https://not-us/` produces a row indistinguishable from the real thing, and a
// band drawn from it would be rta asserting a provenance property out of a
// string anyone can pick. Trust here binds to a digest and never to a name,
// which is the same rule that keeps a manifest's `version` from resolving
// anything.
//
// What rta genuinely knows is narrower and more useful: whether it placed
// these bytes itself. A managed artifact has an rta.lock row naming the index,
// the version claimed at install time and what the signature check found; one
// found on $PATH has none of that, and the difference is exactly the one an
// operator auditing their machine is looking for.
type pluginGroup int

const (
	groupBuiltin pluginGroup = iota
	groupManaged
	groupPath
	groupWaiting
)

// title and caption are the two lines a group rule occupies. The caption says
// what the provenance *means*, once, instead of every row spending characters
// re-implying it.
func (g pluginGroup) title() string {
	switch g {
	case groupBuiltin:
		return "built in"
	case groupManaged:
		return "installed by rta"
	case groupWaiting:
		return "not run"
	}
	return "found on $PATH"
}

func (g pluginGroup) caption() string {
	switch g {
	case groupBuiltin:
		return "compiled into the rta binary you chose to run, which is why they need no digest"
	case groupManaged:
		return "rta placed these bytes and recorded where from — the version is the index's claim, not a guarantee"
	case groupWaiting:
		return "discovered and never launched — approving one is a decision about that exact artifact, not its name"
	}
	return "binaries rta did not place and holds no record of"
}

// group answers which band this row belongs under.
//
// Waiting first, because an artifact rta refused to launch is a decision
// rather than an inventory row — it is the same reason `rta plugin list` puts
// those last — and its provenance is the less interesting half of what it is.
func (r pluginRow) group() pluginGroup {
	switch {
	case r.waiting:
		return groupWaiting
	case !r.external():
		return groupBuiltin
	case underStore(r.origin.Path):
		return groupManaged
	}
	return groupPath
}

// underStore reports whether an artifact sits inside the directory rta
// installs into.
//
// The path, and not a name in rta.lock, because pluginhost.Identify resolves
// symlinks before hashing — the store serves through a bin/ directory of
// links, so a managed plugin's origin is already its real path under the
// store. It is also the question `rta doctor` asks to spot a $PATH copy
// shadowing a managed one, asked the same way here so the two screens cannot
// disagree about which artifact is which.
func underStore(path string) bool {
	return path != "" && strings.HasPrefix(path, plugindist.StoreDir()+string(filepath.Separator))
}

// needList names the locations a row is short of, in the plugin's own words.
func needList(ns []plugin.Need) string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = string(n)
	}
	return strings.Join(out, ", ")
}

// canTile reports whether this plugin could be on the dashboard at all.
func (r pluginRow) canTile() bool { return r.tile != "" }

// usable reports whether this row is a plugin rta can actually do anything
// with. An untrusted artifact has no capabilities to browse, no config to
// edit and nothing to put on a dashboard, because it was never asked.
func (r pluginRow) usable() bool { return !r.waiting }

// external reports whether this plugin came from a binary on $PATH.
func (r pluginRow) external() bool { return r.origin.External() }

// pinnedName is how a profile or a plugins: section key must name this plugin:
// bare for a built-in, pinned to the installed artifact otherwise.
//
// One function because two callers building the string themselves is how a
// digest ends up written one way in a completion and another in a validator,
// and a pin that does not match is a profile that refuses to resolve.
func (r pluginRow) pinnedName() string {
	if r.external() {
		return r.plugin.Name + "@" + r.origin.Short()
	}
	return r.plugin.Name
}

// reach is the worst a plugin can do, in the safety vocabulary the rest of
// the system uses. It is the column worth having next to provenance: "this is
// third-party code" and "this can destroy things" are the two facts you want
// in the same glance, and neither is much use alone.
func (r pluginRow) reach() plugin.Safety {
	worst := plugin.Read
	for _, c := range r.plugin.Capabilities {
		switch c.Safety {
		case plugin.Destructive:
			return plugin.Destructive
		case plugin.Write:
			worst = plugin.Write
		}
	}
	return worst
}

// pluginRows lists every registered plugin with its dashboard state, followed
// by the artifacts discovery refused to launch.
//
// Grouped by provenance rather than left in the registry's alphabetical order,
// which put the two or three artifacts an operator did not compile themselves
// among a dozen that came with the binary. This pane's stated job is "which
// plugins do I actually have", and the answer somebody is scanning for is
// nearly always the third-party half; a sort by name hid it in plain sight.
//
// Untrusted still last, because they are a smaller list and an outstanding
// decision rather than an inventory — the same order `rta plugin list` puts
// them in, and the group ordering is chosen to preserve it.
func pluginRows(reg *registry.Registry, dash config.Dashboard, untrusted []pluginhost.Untrusted) []pluginRow {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	// Read once for the pane rather than per row per frame: these are files,
	// and the plugin list is redrawn on every keystroke.
	allowed := plugintrust.Load()
	locked := map[string]plugindist.LockEntry{}
	for _, e := range plugindist.ReadLock() {
		locked[e.Digest] = e
	}
	rows := make([]pluginRow, 0, len(reg.Plugins())+len(untrusted))
	for _, p := range reg.Plugins() {
		origin, _ := reg.Origin(p.Name)
		row := pluginRow{plugin: p, origin: origin}
		if e, ok := locked[origin.Digest]; ok {
			row.lock = &e
		}
		for _, n := range p.Needs {
			if slices.Contains(allowed.Allowed(origin.Digest), string(n)) {
				row.granted = append(row.granted, n)
			} else {
				row.ungranted = append(row.ungranted, n)
			}
		}
		if t, ok := pluginTile(reg, p); ok {
			row.tile = t.cap.ID
			row.shown = !hidden[t.cap.ID]
		}
		rows = append(rows, row)
	}
	for _, u := range untrusted {
		// The name and the artifact are all discovery learned: it hashed the
		// file and stopped, so there is no summary and no capability list to
		// show, because asking for them is the thing that runs the code.
		row := pluginRow{
			plugin:  plugin.Plugin{Name: u.Name},
			origin:  registry.Origin{Path: u.Path, Digest: u.Digest},
			waiting: true,
		}
		if e, ok := locked[u.Digest]; ok {
			row.lock = &e
		}
		rows = append(rows, row)
	}
	// Stable within a group, by name, so the pane an operator learned the
	// shape of yesterday has the same shape today.
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := rows[i].group(), rows[j].group(); a != b {
			return a < b
		}
		return rows[i].plugin.Name < rows[j].plugin.Name
	})
	return rows
}

// pluginGroups counts the distinct bands the current list falls into.
//
// One group means no grouping happened, and a lone rule over every row on the
// pane is a heading that separates nothing — the same discipline a profile
// colour follows, where only a marked environment announces itself. The count
// is also what the scroll arithmetic subtracts, so both halves ask one
// function rather than two that can drift by a line.
func (m Model) pluginGroups() int {
	seen := map[pluginGroup]bool{}
	for _, row := range m.plugins {
		seen[row.group()] = true
	}
	if len(seen) < 2 {
		return 0
	}
	return len(seen)
}

// toggleShown flips whether a plugin's tile is on the dashboard, and persists
// it. Returns the flash line describing what happened.
func (m *Model) toggleShown(idx int) string {
	if idx < 0 || idx >= len(m.plugins) {
		return ""
	}
	row := &m.plugins[idx]
	if !row.usable() {
		return untrustedNote(*row)
	}
	if !row.canTile() {
		// Saying why beats a key that silently does nothing — and saying the
		// *right* why matters more here than in the inventory line, because
		// this fires on a keypress somebody made expecting something to
		// happen. Through NoTileReason rather than a sentence of its own: this
		// was the third copy of "needs to be told what to look at", which is
		// wrong for every plugin that simply declines to run unasked.
		return row.plugin.Name + " has no dashboard tile: " + NoTileReason(row.plugin)
	}
	row.shown = !row.shown

	if row.shown {
		m.dash.Hidden = withoutID(m.dash.Hidden, row.tile)
	} else if !containsID(m.dash.Hidden, row.tile) {
		m.dash.Hidden = append(m.dash.Hidden, row.tile)
		// An explicit tile list has no notion of "hidden": edit it in place,
		// or the tile reappears next run and the hide looks broken.
		m.dash.Tiles = dropTile(m.dash.Tiles, row.tile)
	}
	// Rebuild from the registry so the dashboard behind this pane is already
	// correct when it is closed — and so showing a tile puts it back where the
	// arrangement says it goes, not at the end.
	m.tiles = buildTiles(m.reg, m.dash)
	m.selected = min(m.selected, len(m.tiles)-1)
	m.clampScroll()

	verb := "hid"
	if row.shown {
		verb = "showing"
	}
	note := verb + " " + row.tile
	if err := m.save(); err != nil {
		return note + " (this session only: " + err.Error() + ")"
	}
	return note
}

func containsID(list []string, id string) bool {
	for _, s := range list {
		if s == id {
			return true
		}
	}
	return false
}

func withoutID(list []string, id string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != id {
			out = append(out, s)
		}
	}
	return out
}

// untrustedNote is the one sentence every key that cannot act on an untrusted
// row answers with. One function, because a row that browses, configures and
// profiles differently is a row where two of the three explanations drift.
func untrustedNote(row pluginRow) string {
	return row.plugin.Name + " has not been run: `rta plugin trust " + row.plugin.Name +
		"` approves this artifact, and the next rta loads it"
}

// pluginRowHeight is how many lines one plugin occupies: a band naming it,
// then what it is, then where it came from.
const pluginRowHeight = 3

// The two text columns under a band start at the same cell, and there is one
// constant saying where. They were computed independently and landed one
// apart — the summary at 9, the detail at 8 — which is the kind of thing that
// does not look like a bug so much as like the app being slightly out of
// focus, repeated once per plugin down the whole pane.
// pluginTextAt is the column both text lines start at, and it is the column
// the plugin's name starts at in the band above them: "─" + the selection
// marker + one space. One left edge for the name, what it is, and where it
// came from — so the eye tracks a single vertical line down the pane instead
// of three that nearly agree.
//
// They were computed independently before this and landed at three different
// columns: the name at 3, the summary at 9, the detail at 8. That does not
// read as a bug so much as as the app being slightly out of focus, once per
// plugin, all the way down.
const pluginTextAt = 3

// visiblePlugins is how many whole plugins fit in the body of the pane.
func (m Model) visiblePlugins(bodyHeight int) int {
	return max((bodyHeight-pluginGroupHeight*m.pluginGroups())/pluginRowHeight, 1)
}

// pluginGroupHeight is the lines a group rule and its caption occupy.
//
// Every group is budgeted for whether or not its rule lands inside the window,
// which under-fills the pane by a line or two when the view is scrolled into
// the middle of a group. That is the deliberate half of the trade: the pane's
// rows are a fixed height precisely so the scroll arithmetic is a
// multiplication rather than a running total, and one function answering
// slightly small for both the clamp and the view is safe in a way that two
// functions agreeing most of the time is not — the failure mode there is the
// selection sitting one line under the bottom edge, which reads as the app
// being broken.
const pluginGroupHeight = 2

// clampPluginScroll keeps the selected plugin on screen.
//
// The pane had no scroll at all and simply clipped, which at 80x24 — the
// default terminal since forever — hid `note` entirely while `j` still
// selected it. Toggling a tile you cannot see is the kind of thing that reads
// as the app being broken rather than as a pane being short.
func (m *Model) clampPluginScroll(bodyHeight int) {
	visible := m.visiblePlugins(bodyHeight)
	m.pluginScroll = min(m.pluginScroll, max(0, len(m.plugins)-visible))
	if m.pluginSel < m.pluginScroll {
		m.pluginScroll = m.pluginSel
	}
	if m.pluginSel >= m.pluginScroll+visible {
		m.pluginScroll = m.pluginSel - visible + 1
	}
	m.pluginScroll = max(m.pluginScroll, 0)
}

// pluginsView renders the inventory, one band per plugin.
//
// The band is browse.go's grammar, not a new one: a rule carrying the name on
// the left and the fact you scan for on the right. There it is the capability
// count; here it is the worst thing the plugin can do, because "which of
// these can change my machine?" is the question this screen exists to answer
// second, after "what do I have".
//
// Separating with a band rather than a blank line is what makes the two
// content lines readable at all. Four facts about provenance, reach, size and
// dashboard state on two unseparated lines, repeated twelve times, is a wall —
// every line looks like every other line and the eye has nothing to catch on.
// pluginFooter is built in one place because both the view and the scroll
// arithmetic need its height, and two constructions that drift by a line put
// the selection just off the bottom of the pane.
func (m Model) pluginFooter() string { return m.footerFor(modePlugins) }

// pluginBodyHeight is the space inside the panel, in lines.
func (m Model) pluginBodyHeight() int {
	return max(m.height-1-lipgloss.Height(m.pluginFooter())-2, pluginRowHeight)
}

func (m Model) pluginsView() string {
	header := theme.Title.Render(" rta") + theme.Subtle.Render("  plugins")
	footer := m.pluginFooter()

	width := m.width
	if width <= 0 {
		width = 80
	}
	bodyHeight := m.pluginBodyHeight()
	visible := m.visiblePlugins(bodyHeight)
	scroll := min(max(m.pluginScroll, 0), max(0, len(m.plugins)-visible))

	inner := width - 4
	grouped := m.pluginGroups() > 0
	var b strings.Builder
	for i := scroll; i < len(m.plugins) && i < scroll+visible; i++ {
		row := m.plugins[i]
		selected := i == m.pluginSel

		// The group rule, before the first row that belongs under it.
		//
		// Compared against the row above in the whole list rather than the one
		// above on screen, so scrolling into the middle of a group does not
		// re-announce it — a heading that reappears every time you scroll is a
		// heading that stops meaning "this is where it starts".
		if grouped && (i == 0 || m.plugins[i-1].group() != row.group()) {
			g := row.group()
			label := " " + strings.ToUpper(g.title()) + " "
			rule := max(inner-lipgloss.Width(label)-2, 0)
			b.WriteString(theme.Subtle.Render("──"+label+strings.Repeat("─", rule)) + "\n")
			b.WriteString(ansi.Truncate(
				strings.Repeat(" ", pluginTextAt)+theme.Faded.Render(g.caption()), inner, "…") + "\n")
		}

		// The band: "─❯ KV ───────────── [on] · destructive ─".
		//
		// The selection marker lives here rather than on the line below,
		// because the band is where the name is and the name is what a person
		// is selecting. It was on the content line first, with the band
		// styled Title when selected and Key when not — which is no styling
		// at all: both are Primary+Bold, so the band said nothing about
		// selection and the only cue was an arrow two lines from the name.
		//
		// Dashboard state sits on the right beside reach rather than in a
		// left-hand column. A fixed column on the left scans well and costs
		// the thing worth more: with it there, the text lines cannot begin
		// where the name begins, and the pane has two competing left edges.
		mark := " "
		if selected {
			mark = theme.AccentTxt.Render("❯")
		}
		label := " " + strings.ToUpper(row.plugin.Name) + " "

		state := theme.Subtle.Render("—")
		switch {
		case row.canTile() && row.shown:
			state = theme.GoodText.Render("[on]")
		case row.canTile():
			state = theme.Subtle.Render("[off]")
		}
		right := " " + state + theme.Subtle.Render(" · ") + reachLabel(row, string(row.reach())) + " "
		if row.waiting {
			// Not a reach: rta has never asked this artifact what it can do,
			// and printing "read" for something it declined to run would be
			// stating a fact it does not have.
			right = " " + theme.WarnText.Render("not run") + " "
		}
		if row.decided == decidedTrust {
			right = " " + theme.GoodText.Render("approved") + " "
		}
		if row.decided == decidedUntrust {
			right = " " + theme.WarnText.Render("untrusted") + " "
		}

		rule := max(inner-lipgloss.Width(label)-lipgloss.Width(right)-3, 0)
		name := theme.Key.Render(label)
		if selected {
			name = theme.Title.Render(label)
		}
		b.WriteString(theme.Subtle.Render("─") + mark + name +
			theme.Subtle.Render(strings.Repeat("─", rule)) +
			right + theme.Subtle.Render("─") + "\n")

		indent := strings.Repeat(" ", pluginTextAt)
		summary := theme.Subtle.Render(row.plugin.Summary)
		if len(row.ungranted) > 0 {
			summary = theme.WarnText.Render("needs " + needList(row.ungranted) +
				" and has not been allowed — `rta plugin allow " + row.plugin.Name + "`")
		}
		if row.waiting {
			summary = theme.WarnText.Render("installed and not run — nothing has trusted this artifact")
		}
		switch row.decided {
		case decidedTrust:
			summary = theme.GoodText.Render("approved just now — this artifact runs from the next rta onwards")
		case decidedUntrust:
			summary = theme.WarnText.Render("approval taken back — it stays loaded until rta exits, and will not run again")
		}
		b.WriteString(ansi.Truncate(indent+summary, inner, "…") + "\n")
		b.WriteString(ansi.Truncate(indent+theme.Faded.Render(pluginDetail(row)), inner, "…") + "\n")
	}

	right := fmt.Sprintf("%d installed", len(m.plugins))
	if len(m.plugins) > visible {
		right = fmt.Sprintf("%d-%d of %d", scroll+1, min(scroll+visible, len(m.plugins)), len(m.plugins))
	}
	body := panel(panelHead{Title: "plugins", Right: right},
		strings.TrimRight(b.String(), "\n"), width, m.height-1-lipgloss.Height(footer), true)
	return header + "\n" + body + "\n" + footer
}

// pluginDetail is the second line: where the plugin came from, what it can do
// to the machine, how much it offers, and — when it is not on the dashboard —
// why not.
//
// Provenance leads, because it is the fact that changes how you read every
// other fact on the line. "13 capabilities, one of them destructive" means one
// thing about kv, which ships inside the binary the user chose to run, and
// something else entirely about a binary that appeared on $PATH.
//
// The digest is shown short. It is the identity every authorisation in rta is
// bound to, so it is worth being able to see it here and compare it against
// what a grant was issued against, without running doctor.
// pluginOrigin is the provenance half of the detail line: the artifact, and —
// for one rta placed itself — what it recorded about placing it.
//
// The lock's three facts are here rather than only in `rta doctor` because
// this is the pane somebody opens to ask what a plugin *is*, and until now the
// answer for an installed one was the same "$PATH: …" a stray binary got. The
// version is the index's claim at install time and nothing resolves through
// it; the signature line is the outcome of a check rta records and never
// requires, which is why it is stated in full rather than reduced to a tick.
//
// A managed artifact with no matching lock row says so plainly rather than
// borrowing the row that shares its name. That state is real — an upgrade that
// did not finish, or a local build copied into the store — and `rta doctor`
// investigates it properly; the pane's job is to not claim a provenance it
// cannot support.
func pluginOrigin(row pluginRow) string {
	if !row.external() {
		return "built in"
	}
	if row.group() == groupManaged {
		if row.lock == nil {
			return "in rta's store · " + row.origin.Short() +
				" — rta.lock has no record of these bytes; `rta doctor` says what drifted"
		}
		// The same three facts `rta doctor` prints for a managed plugin, in
		// the same words. Two screens describing one artifact differently is
		// how an operator ends up unsure which of them to believe.
		return fmt.Sprintf("installed by rta · %s from index %q · signature: %s · %s",
			row.lock.Version, row.lock.Index, row.lock.Signature, row.origin.Short())
	}
	return "$PATH: " + row.origin.Path + " · " + row.origin.Short()
}

func pluginDetail(row pluginRow) string {
	origin := pluginOrigin(row)
	if row.decided == decidedTrust {
		return origin + " · approved — it loads when rta restarts"
	}
	if row.waiting {
		// **This used to send people to a shell, and the reasoning was
		// wrong in an interesting way.** It said a control that changes a
		// file and loads nothing appears to work and does not, so the
		// command was safer than a key. But `rta plugin trust` does exactly
		// the same thing — it writes the approval and tells you the plugin
		// loads on your next command — and nobody reads that as broken.
		// What made the TUI version sound broken was the missing sentence,
		// not the missing restart.
		//
		// And the key is the better moment for the decision, not merely the
		// shorter one: the digest and the artifact path are on the screen
		// while it is being made, where the command shows them only after.
		return origin + " · press t to approve it — it loads when rta restarts"
	}
	detail := origin + " · " + fmt.Sprintf("%d %s", len(row.plugin.Capabilities),
		pluralNoun(len(row.plugin.Capabilities), "capability"))
	// What this binary has actually been handed, named on the row rather than
	// implied by the absence of a warning. A plugin reading a kubeconfig and
	// one reading nothing at all were previously the same line.
	//
	// Placed before the tile state deliberately: a granted location is a
	// standing permission on this machine and the tile is a display preference,
	// and the more consequential fact should not be the one that gets truncated
	// off the end of a narrow pane.
	if len(row.granted) > 0 {
		detail += " · allowed to read " + needList(row.granted)
	}
	switch {
	case !row.canTile():
		detail += " · no dashboard tile: " + NoTileReason(row.plugin)
	case row.shown:
		detail += " · dashboard tile: " + row.tile
	default:
		detail += " · tile hidden: " + row.tile
	}
	return detail
}

// reachLabel styles the worst thing a plugin can do, in the same colours the
// rest of the app uses for the same three words. Read is deliberately muted:
// it is the majority and the safe one, and a screen where everything is
// coloured says nothing.
func reachLabel(row pluginRow, text string) string {
	switch row.reach() {
	case plugin.Destructive:
		return theme.BadText.Render(text)
	case plugin.Write:
		return theme.WarnText.Render(text)
	default:
		return theme.Subtle.Render(text)
	}
}

// pad right-pads a styled string to width, measuring the plain text.
func pad(styled, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled + " "
}

// trustSelected takes the trust decision the pane is showing, without leaving
// it.
//
// Both directions from one key, because the row already says which one it is
// and a second key for the reverse would be a key that does nothing on most
// rows. The asymmetry that matters is handled by what happens rather than by
// what is pressed: approving is the decision with consequences and it is the
// one the row spells out first, while taking approval back only ever narrows.
//
// Neither takes effect on the running process. That is stated in the flash
// and in the row rather than worked around, for the reason `rta plugin trust`
// states it too: trust is read once, before anything is launched.
func (m *Model) trustSelected() string {
	if m.pluginSel >= len(m.plugins) {
		return ""
	}
	row := m.plugins[m.pluginSel]
	if !row.external() && !row.waiting {
		return row.plugin.Name + " is built into rta — there is no artifact to approve"
	}
	if row.waiting {
		if verr := plugintrust.Add(row.origin.Digest, row.plugin.Name, row.origin.Path); verr != nil {
			return "not approved: " + verr.Message
		}
		m.plugins[m.pluginSel].decided = decidedTrust
		return "approved " + row.plugin.Name + " (" + row.origin.Short() +
			") — it loads when rta restarts"
	}
	n, verr := plugintrust.Remove(row.plugin.Name)
	if verr != nil {
		return "not withdrawn: " + verr.Message
	}
	if n == 0 {
		return row.plugin.Name + " was not in the trusted list"
	}
	m.plugins[m.pluginSel].decided = decidedUntrust
	return "took approval back from " + row.plugin.Name +
		" — it stays loaded until rta exits, and will not run again"
}
