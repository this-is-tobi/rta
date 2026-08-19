package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
)

// panel draws a bordered pane with its title embedded in the top border —
// the shared visual grammar of dashboard tiles and the result pane:
//
//	╭─ title ──────────────── right ─╮
//	│ body                           │
//	╰────────────────────────────────╯
//
// width is the total rendered width and is guaranteed exactly: body lines
// are ANSI-aware truncated and padded, so grid math (and mouse hit-testing)
// can rely on every panel being width × height cells. height > 0 fixes the
// total height by padding or clipping body lines; 0 means natural height.
// focus switches the border to the primary accent.
func panel(title, right, body string, width, height int, focus bool) string {
	if width < 10 {
		return body // degenerate terminal: give the content back unframed
	}
	border := theme.Border
	if focus {
		border = lipgloss.NewStyle().Foreground(theme.Primary)
	}
	inner := width - 4 // "│ " + content + " │"

	// Top border: "╭─ " title " " ─fill─ " right " "─╮" — the fixed chrome is
	// 6 cells. Drop right when it would starve the title, then truncate.
	rightSeg := ""
	if right != "" {
		rightSeg = " " + right + " "
	}
	if rightSeg != "" && width-lipgloss.Width(rightSeg)-7 < 8 {
		rightSeg = ""
	}
	title = ansi.Truncate(title, width-lipgloss.Width(rightSeg)-7, "…")
	fill := width - lipgloss.Width(title) - lipgloss.Width(rightSeg) - 6
	top := border.Render("╭─ ") + title + " " + border.Render(strings.Repeat("─", max(fill, 0))) +
		rightSeg + border.Render("─╮")

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
	side := border.Render("│")
	var b strings.Builder
	b.WriteString(top)
	for _, line := range lines {
		line = ansi.Truncate(line, inner, "…")
		pad := strings.Repeat(" ", max(inner-lipgloss.Width(line), 0))
		b.WriteString("\n" + side + " " + line + pad + " " + side)
	}
	b.WriteString("\n" + border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return b.String()
}

// hint renders one footer key guide entry: the key in accent, its label
// muted — the visual grammar of every well-loved TUI footer.
func hint(key, label string) string {
	return lipgloss.NewStyle().Foreground(theme.Primary).Render(key) + " " + theme.Subtle.Render(label)
}

// hintBar joins hints with muted separators into a footer line.
func hintBar(items ...string) string {
	return " " + strings.Join(items, theme.Subtle.Render(" · "))
}
