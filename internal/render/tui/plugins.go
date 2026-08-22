package tui

import (
	"charm.land/lipgloss/v2"

	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
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
// are as visible as `todo`, and it says why they are not on the dashboard
// rather than leaving you to wonder.
//
// It also says where each plugin came from, which it could not before
// external plugins existed. plugin.Plugin carries a name, a summary and its
// capabilities and nothing about its origin, so `kv` and a binary somebody
// dropped on $PATH rendered as the same kind of thing — on the one screen
// whose whole job is "what do I actually have". Every security property in
// pluginhost is built on binding a plugin to its artifact (ADR 0015), and
// this is where a person gets to see that binding.

// pluginRow is one installed plugin and its relationship to the dashboard.
type pluginRow struct {
	plugin plugin.Plugin
	// origin is the artifact an external plugin was loaded from, as the
	// registry recorded it at registration. Its zero value means built in:
	// compiled into the rta binary the user already chose to run, which is
	// why it needs no path and no digest.
	origin registry.Origin
	// tile is the capability that represents it on the dashboard, empty when
	// the plugin has nothing it can show unprompted.
	tile string
	// shown is false when a tile exists but the user hid it.
	shown bool
}

// canTile reports whether this plugin could be on the dashboard at all.
func (r pluginRow) canTile() bool { return r.tile != "" }

// external reports whether this plugin came from a binary on $PATH.
func (r pluginRow) external() bool { return r.origin.External() }

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

// pluginRows lists every registered plugin with its dashboard state.
func pluginRows(reg *registry.Registry, dash config.Dashboard) []pluginRow {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	rows := make([]pluginRow, 0, len(reg.Plugins()))
	for _, p := range reg.Plugins() {
		origin, _ := reg.Origin(p.Name)
		row := pluginRow{plugin: p, origin: origin}
		if t, ok := pluginTile(reg, p); ok {
			row.tile = t.cap.ID
			row.shown = !hidden[t.cap.ID]
		}
		rows = append(rows, row)
	}
	return rows
}

// toggleShown flips whether a plugin's tile is on the dashboard, and persists
// it. Returns the flash line describing what happened.
func (m *Model) toggleShown(idx int) string {
	if idx < 0 || idx >= len(m.plugins) {
		return ""
	}
	row := &m.plugins[idx]
	if !row.canTile() {
		// Saying why beats a key that silently does nothing.
		return row.plugin.Name + " has nothing to show without being told what to look at"
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
	return max(bodyHeight/pluginRowHeight, 1)
}

// clampPluginScroll keeps the selected plugin on screen.
//
// The pane had no scroll at all and simply clipped, which at 80x24 — the
// default terminal since forever — hid `todo` entirely while `j` still
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
func (m Model) pluginFooter() string {
	footer := fitHintBar(m.width, footerMaxLines,
		item(bindColumn), item(bindToggle), item(bindConfig), labelled(bindOpen, "capabilities"),
		item(bindBack), item(bindQuit),
	)
	if m.flash != "" {
		footer += theme.GoodText.Render("  ✓ " + m.flash)
	}
	return footer
}

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
	var b strings.Builder
	for i := scroll; i < len(m.plugins) && i < scroll+visible; i++ {
		row := m.plugins[i]
		selected := i == m.pluginSel

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

		rule := max(inner-lipgloss.Width(label)-lipgloss.Width(right)-3, 0)
		name := theme.Key.Render(label)
		if selected {
			name = theme.Title.Render(label)
		}
		b.WriteString(theme.Subtle.Render("─") + mark + name +
			theme.Subtle.Render(strings.Repeat("─", rule)) +
			right + theme.Subtle.Render("─") + "\n")

		indent := strings.Repeat(" ", pluginTextAt)
		b.WriteString(ansi.Truncate(indent+theme.Subtle.Render(row.plugin.Summary), inner, "…") + "\n")
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
// bound to (ADR 0015), so it is worth being able to see it here and compare it
// against what `--allow-destructive` was pinned to, without running doctor.
func pluginDetail(row pluginRow) string {
	origin := "built in"
	if row.external() {
		origin = "$PATH: " + row.origin.Path + " · " + row.origin.Short()
	}
	detail := origin + " · " + fmt.Sprintf("%d capabilities", len(row.plugin.Capabilities))
	switch {
	case !row.canTile():
		detail += " · no dashboard tile: needs to be told what to look at"
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
