package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
)

// panelHead is what goes in a panel's top border.
//
// Plain text, not styled strings, and that is the point. Every caller used to
// render its own title, and every caller independently reached for theme.Key
// or theme.Title — the same Primary the content inside the panel uses for its
// keys — so a tile announced itself in exactly the colour of its own contents.
// A convention that four places have to remember is a convention that gets
// broken by the fifth; panel decides now, and there is nowhere left to decide
// it differently.
// Plain text also means text a terminal will not act on: a title is often a
// name out of a config file or off an artifact on $PATH, and panel cleans all
// three fields for the same reason renderBands cleans a band's name.
type panelHead struct {
	Title string // the pane's name, in theme.PanelTitle
	Note  string // muted, beside the title: a summary
	// NoteColor paints the note in a colour of its own instead of muted:
	// a pinned tile's profile name in that profile's colour, the one thing
	// the profile doc lets a colour touch. "" keeps the note muted, and so
	// does anything that is not a #rrggbb colour.
	NoteColor string
	Right     string // muted, right-aligned in the top border: a cost, a count
	// Attention says the panel is asking for something: its border and its
	// Right text are drawn in the warning colour instead of the quiet ones. The
	// text carries the meaning, so a terminal that shows no colour loses the
	// emphasis and not the message; focus still paints the border in the
	// primary colour, because the selection has to stay visible on the panel
	// somebody is looking at.
	Attention bool
	// Heavy draws a focused panel in heavy lines. Focus is otherwise the
	// border's primary colour and nothing else, so on a terminal that shows no
	// colour (NO_COLOR, TERM=dumb) the selected tile of the dashboard was
	// indistinguishable from its neighbours and the arrow keys moved a
	// selection nobody could see. The size is the same: the grid and the
	// mouse's hit-testing count on every panel being width by height cells.
	Heavy bool
}

// boxGlyphs are the characters a panel's border is drawn in.
type boxGlyphs struct {
	topLeft, topRight, bottomLeft, bottomRight, across, down string
}

// roundedBox is every panel's border; heavyBox replaces it on the focused one
// where colour cannot carry the difference.
var (
	roundedBox = boxGlyphs{"╭", "╮", "╰", "╯", "─", "│"}
	heavyBox   = boxGlyphs{"┏", "┓", "┗", "┛", "━", "┃"}
)

// panel draws a bordered pane with its title embedded in the top border —
// the shared visual grammar of dashboard tiles and the result pane:
//
//	╭─ title  note ────────── right ─╮
//	│ body                           │
//	╰────────────────────────────────╯
//
// width is the total rendered width and is guaranteed exactly: body lines
// are ANSI-aware truncated and padded, so grid math (and mouse hit-testing)
// can rely on every panel being width × height cells. height > 0 fixes the
// total height by padding or clipping body lines; 0 means natural height.
// focus switches the border to the primary accent.
func panel(h panelHead, body string, width, height int, focus bool) string {
	if width < 10 {
		return body // degenerate terminal: give the content back unframed
	}
	h.Title, h.Note, h.Right = textclean.Terminal(h.Title),
		textclean.Terminal(h.Note), textclean.Terminal(h.Right)
	border := theme.Border
	if h.Attention {
		border = lipgloss.NewStyle().Foreground(theme.Warn)
	}
	box := roundedBox
	if focus {
		border = lipgloss.NewStyle().Foreground(theme.Primary)
		if h.Heavy {
			box = heavyBox
		}
	}
	inner := width - 4 // "│ " + content + " │"

	// Top border: "╭─ " title " " ─fill─ " right " "─╮" — the fixed chrome is
	// 6 cells. Drop right when it would starve the title, then truncate.
	rightSeg := ""
	if h.Right != "" {
		right := theme.Subtle
		if h.Attention {
			right = theme.WarnText
		}
		rightSeg = " " + right.Render(h.Right) + " "
	}
	if rightSeg != "" && width-lipgloss.Width(rightSeg)-7 < 8 {
		rightSeg = ""
	}
	title := theme.PanelTitle.Render(h.Title)
	if h.Note != "" {
		note := theme.Subtle
		if theme.HexColor.MatchString(h.NoteColor) {
			note = lipgloss.NewStyle().Foreground(lipgloss.Color(h.NoteColor))
		}
		title += note.Render("  " + h.Note)
	}
	title = ansi.Truncate(title, width-lipgloss.Width(rightSeg)-7, "…")
	fill := width - lipgloss.Width(title) - lipgloss.Width(rightSeg) - 6
	top := border.Render(box.topLeft+box.across+" ") + title + " " +
		border.Render(strings.Repeat(box.across, max(fill, 0))) +
		rightSeg + border.Render(box.across+box.topRight)

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if height > 0 {
		want := height - 2
		if len(lines) > want {
			lines = lines[:max(want, 0)]
		}
		for len(lines) < want {
			lines = append(lines, "")
		}
	}
	side := border.Render(box.down)
	var b strings.Builder
	b.WriteString(top)
	for _, line := range lines {
		line = ansi.Truncate(line, inner, "…")
		pad := strings.Repeat(" ", max(inner-lipgloss.Width(line), 0))
		b.WriteString("\n" + side + " " + line + pad + " " + side)
	}
	b.WriteString("\n" + border.Render(box.bottomLeft+strings.Repeat(box.across, width-2)+box.bottomRight))
	return b.String()
}

// hint renders one footer key guide entry: the key in accent, its label
// muted — the visual grammar of every well-loved TUI footer.
func hint(key, label string) string {
	return lipgloss.NewStyle().Foreground(theme.Primary).Render(key) + " " + theme.Subtle.Render(label)
}
