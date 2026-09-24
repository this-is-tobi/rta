package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Adding a tile from inside the TUI: `+` on a catalogue row or a search
// match puts that capability on the dashboard, the way `rta dashboard add`
// does from a shell and an `add:` entry does from the file.
//
// The three surfaces write the same entry, and the catalogue is where a
// person is already looking at the capability they want to glance at: it
// was the one surface with no way to say so, and "quit, type the command,
// reopen" is the trip this closes. What the key asks is the one thing the
// entry needs that the row does not say — which connection, for a
// capability that takes one — through a picker of the profiles that cover
// its plugin, with "follows the switch" first, since that is what every
// automatic tile does. A capability that needs an input the row cannot
// supply — cert.expiry's hostname — is refused with the command that
// states it: a picker for inputs would be the run's own form, and a form's
// values are what was typed for one run, seeded from the connection on
// screen, not the standing inputs of a tile.

// addPickForm holds the connection choice for a tile being added.
type addPickForm struct {
	form  *huh.Form
	value string
	cap   plugin.Capability
	// returnTo is where cancelling goes back to: the catalogue or the
	// dashboard, whichever the key was pressed on. Confirming always lands
	// on the dashboard, with the new tile selected.
	returnTo mode
}

// pinChoice is one connection the picker offers: the reference a tile is
// pinned to, and how the row reads.
type pinChoice struct{ ref, label string }

func newAddPickForm(c plugin.Capability, active string, choices []pinChoice, returnTo mode) *addPickForm {
	ap := &addPickForm{cap: c, returnTo: returnTo}
	follows := "follows the switch"
	if active != "" {
		follows += " — now " + active
	}
	opts := make([]huh.Option[string], 0, 1+len(choices))
	opts = append(opts, huh.NewOption(follows, ""))
	for _, ch := range choices {
		opts = append(opts, huh.NewOption(ch.label, ch.ref))
	}
	ap.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("which connection is the " + c.ID + " tile about?").
			Options(opts...).
			Value(&ap.value),
	)).WithShowHelp(false).WithShowErrors(false)
	return ap
}

// offerAdd is `+` on c: refused when a tile of it could never run, written
// at once when it takes no connection, and otherwise asked which one.
func (m Model) offerAdd(c plugin.Capability, returnTo mode) (tea.Model, tea.Cmd) {
	if why := addRefusal(c, plugin.Profilable(c)); why != "" {
		m.flash = why
		return m, nil
	}
	choices := m.pinChoices(c)
	if len(choices) == 0 {
		return m.addTile(c, "")
	}
	m.addPick = newAddPickForm(c, m.active, choices, returnTo)
	m.mode = modeAddPick
	m.fitAddPick()
	return m, m.addPick.form.Init()
}

// addRefusal is why c cannot be a tile at all, in the words `rta dashboard
// add` uses, or "" when it can. pinned says whether a profile will fill
// its inputs.
func addRefusal(c plugin.Capability, pinned bool) string {
	if c.Safety != plugin.Read {
		return c.ID + " is not a read, and a tile runs on a timer with no confirmation"
	}
	if credential := Untileable(c); len(credential) > 0 {
		return c.ID + " reads " + strings.Join(credential, ", ") +
			", a credential a tile cannot be given — run `" +
			strings.Join(append([]string{"rta"}, c.Words()...), " ") + "` when you have one"
	}
	if missing := MissingInputs(c, nil, pinned); len(missing) > 0 {
		return c.ID + " needs " + strings.Join(missing, ", ") + " — `rta dashboard add " + c.ID +
			" --set " + missing[0] + "=…` states it"
	}
	return ""
}

// pinChoices lists the connections a tile of c could be pinned to: every
// trusted profile covering its plugin and, inside one holding several
// connections, the profile itself — one panel per connection — and each
// labelled instance. The default instance alone is not offered, since a
// bare name over several connections is the expansion, as it is for
// `rta dashboard add`.
func (m Model) pinChoices(c plugin.Capability) []pinChoice {
	if !plugin.Profilable(c) {
		return nil
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return nil
	}
	ns := plugin.Namespace(c.ID)
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []pinChoice
	for _, name := range names {
		p := cfg.Profiles[name]
		if !p.Trusted() || !p.Covers(ns) {
			continue
		}
		refs := profile.InstanceRefs(p, name, ns)
		if len(refs) > 1 {
			out = append(out, pinChoice{ref: name,
				label: fmt.Sprintf("%s — every %s connection it holds, %d panels", name, ns, len(refs))})
		}
		for _, ref := range refs {
			if ref != name || len(refs) == 1 {
				out = append(out, pinChoice{ref: ref, label: ref})
			}
		}
	}
	return out
}

// addTile writes the entry and lands on the dashboard with the new tile
// selected. A capability the automatic dashboard already shows is not
// written twice: hidden, it is shown again, which is what the key meant;
// otherwise the key did nothing, and says so.
func (m Model) addTile(c plugin.Capability, ref string) (tea.Model, tea.Cmd) {
	entry := config.Tile{ID: c.ID, Profile: ref}
	key := entry.Key()
	ns := plugin.Namespace(c.ID)
	changed := true
	onDashboard := func(t config.Tile) bool { return t.Key() == key }
	switch {
	// A stated list counts too: an entry over a `tiles:` key would take
	// that tile's place (joinTiles) and change nothing on screen, since
	// the key carries no width or input the stated entry lacks.
	case slices.ContainsFunc(m.dash.Add, onDashboard) || slices.ContainsFunc(m.dash.Tiles, onDashboard):
		m.flash, changed = key+" is already on the dashboard", false
	case ref == "" && m.automaticKey(key):
		if slices.Contains(m.dash.Hidden, c.ID) {
			m.dash.Hidden = withoutID(m.dash.Hidden, c.ID)
			m.flash = "showing " + c.ID + " again"
		} else {
			m.flash, changed = c.ID+" is already on the automatic dashboard", false
		}
	default:
		if why := addRefusal(c, ref != ""); why != "" {
			m.flash, changed = why, false
			break
		}
		m.dash.Add = append(m.dash.Add, entry)
		m.flash = "added " + key + " — H takes it down"
		if inst := m.instances(); inst != nil {
			if n := len(inst(ref, ns)); n > 1 {
				m.flash = fmt.Sprintf("added %s — one panel per %s connection, %d of them; H takes one down", key, ns, n)
			}
		}
	}
	if changed {
		if err := m.save(); err != nil {
			m.flash += " (this session only: " + err.Error() + ")"
		}
		m.rebuildTiles()
	}
	for i, t := range m.tiles {
		if t.entryKey() == key {
			m.selected = i
			break
		}
	}
	m.clampScroll()
	m.mode = modeDashboard
	m.tickGen++
	return m, tea.Batch(m.syncTiles(), refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor))
}

// fitAddPick sizes the embedded huh form to the panel frame, mirroring
// fitCopyPick.
func (m *Model) fitAddPick() {
	if m.addPick == nil {
		return
	}
	if m.width > 0 {
		m.addPick.form = m.addPick.form.WithWidth(formWidth(m.width))
	}
	if m.height > 0 {
		m.addPick.form = m.addPick.form.WithHeight(max(m.height-5, 6))
	}
}

// updateAddPick drives the embedded huh form and hands off to
// afterAddPickUpdate, the drive-then-dispatch shape every form-driven mode
// shares.
func (m Model) updateAddPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.addPick == nil {
		m.mode = modeDashboard
		return m, nil
	}
	model, cmd := m.addPick.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.addPick.form = f
	}
	return m.afterAddPickUpdate(cmd)
}

func (m Model) afterAddPickUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.addPick.form.State {
	case huh.StateCompleted:
		return m.confirmAddPick()
	case huh.StateAborted:
		return m.closeAddPick()
	}
	return m, cmd
}

// fastSubmitAddPick accepts the highlighted connection: shift+enter means
// the same as enter on a single-field form, wired so the shortcut means
// one thing on every form-shaped screen.
func (m Model) fastSubmitAddPick() (tea.Model, tea.Cmd) {
	if m.addPick == nil {
		return m, nil
	}
	m.addPick.form = advanceFormBySyntheticEnter(m.addPick.form)
	return m.afterAddPickUpdate(nil)
}

// confirmAddPick writes the tile against whichever connection the form
// landed on.
func (m Model) confirmAddPick() (tea.Model, tea.Cmd) {
	c, ref := m.addPick.cap, m.addPick.value
	m.addPick = nil
	return m.addTile(c, ref)
}

// closeAddPick dismisses the picker without writing anything, back to
// where `+` was pressed — restarting tile refresh when that is the
// dashboard, as closeCopyPick does.
func (m Model) closeAddPick() (tea.Model, tea.Cmd) {
	returnTo := m.addPick.returnTo
	m.addPick = nil
	m.mode = returnTo
	if returnTo == modeDashboard {
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor)
	}
	return m, nil
}

// addPickView frames the picker under the capability's own head, so
// choosing its connection reads as part of the capability rather than a
// screen of its own.
func (m Model) addPickView() string {
	if m.addPick == nil {
		return ""
	}
	footer := m.footerFor(modeAddPick)
	return panel(capHead(m.addPick.cap), "\n"+m.addPick.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}
