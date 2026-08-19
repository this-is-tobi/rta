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

// pluginRow is one installed plugin and its relationship to the dashboard.
type pluginRow struct {
	plugin plugin.Plugin
	// tile is the capability that represents it on the dashboard, empty when
	// the plugin has nothing it can show unprompted.
	tile string
	// shown is false when a tile exists but the user hid it.
	shown bool
}

// canTile reports whether this plugin could be on the dashboard at all.
func (r pluginRow) canTile() bool { return r.tile != "" }

// pluginRows lists every registered plugin with its dashboard state.
func pluginRows(reg *registry.Registry, dash config.Dashboard) []pluginRow {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	rows := make([]pluginRow, 0, len(reg.Plugins()))
	for _, p := range reg.Plugins() {
		row := pluginRow{plugin: p}
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

// pluginsView renders the inventory: one line per plugin, its dashboard state
// on the left where the eye scans for it.
func (m Model) pluginsView() string {
	header := theme.Title.Render(" rta") + theme.Subtle.Render("  plugins")
	footer := fitHintBar(m.width, footerMaxLines,
		item(bindColumn), item(bindToggle), labelled(bindOpen, "capabilities"),
		item(bindBack), item(bindQuit),
	)
	if m.flash != "" {
		footer += theme.GoodText.Render("  ✓ " + m.flash)
	}

	width := m.width
	if width <= 0 {
		width = 80
	}
	var b strings.Builder
	for i, row := range m.plugins {
		marker := "  "
		name := theme.Key.Render(row.plugin.Name)
		if i == m.pluginSel {
			marker = theme.AccentTxt.Render("❯ ")
			name = theme.Title.Render(row.plugin.Name)
		}
		// The state column is fixed-width and first, so a glance down the left
		// edge answers "what is on my dashboard?" without reading anything.
		state := theme.Subtle.Render("  —   ")
		switch {
		case row.canTile() && row.shown:
			state = theme.GoodText.Render(" [on] ")
		case row.canTile():
			state = theme.Subtle.Render(" [off]")
		}
		line := marker + state + " " + pad(name, row.plugin.Name, 8) +
			theme.Subtle.Render(row.plugin.Summary)
		b.WriteString(ansi.Truncate(line, width-4, "…") + "\n")

		detail := "    " + theme.Subtle.Render(pluginDetail(row))
		b.WriteString(ansi.Truncate(detail, width-4, "…") + "\n")
	}
	body := panel(theme.Key.Render("plugins"),
		theme.Subtle.Render(fmt.Sprintf("%d installed", len(m.plugins))),
		strings.TrimRight(b.String(), "\n"), width, m.height-1-lipgloss.Height(footer), true)
	return header + "\n" + body + "\n" + footer
}

// pluginDetail is the second line: how much the plugin offers, and — when it
// is not on the dashboard — why not.
func pluginDetail(row pluginRow) string {
	detail := fmt.Sprintf("%d capabilities", len(row.plugin.Capabilities))
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

// pad right-pads a styled string to width, measuring the plain text.
func pad(styled, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled + " "
}
