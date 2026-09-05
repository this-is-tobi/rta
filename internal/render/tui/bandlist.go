package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
)

// A band list: a rule carrying a name on the left and the fact you scan for on
// the right, with the detail indented under it.
//
// The plugins pane invented this grammar and it earned its keep — four facts
// about provenance, reach, size and dashboard state on two unseparated lines,
// repeated twelve times, is a wall where every line looks like every other one
// and the eye has nothing to catch on. Two panes now want it (environments, and
// the plugins inside one), so it lives here rather than being copied: two
// copies of a layout drift into two layouts, and the whole argument for the
// grammar was that a person learns to read it once.
type band struct {
	// name goes in the rule, upper-cased and cleaned by the renderer.
	//
	// **Plain text, and the renderer enforces it.** Every name on these lists
	// is a string somebody else wrote — a profile's name and a plugin key come
	// out of a config file, a plugin's name out of an artifact on $PATH — and
	// a value carrying `ESC [ 2 J` clears the screen from inside a pane rta
	// drew. internal/textclean exists for this and was reaching the strings a
	// plugin returns and not the ones a file holds; here is the other half.
	name string
	// right is the pre-rendered status cluster, joined with the muted middot.
	right []string
	// detail is the indented lines under the rule, already styled. Every band
	// contributes the same number, so a pane's scroll arithmetic is a
	// multiplication rather than a running total.
	detail []string
}

// bandHeight is the lines one band occupies: the rule plus its detail.
const bandHeight = 3

// renderBands paints the visible slice of a band list.
//
// Truncation is per line and marked, never silent: a detail line that ran off
// the edge says so with an ellipsis, because the values on these lines are the
// ones somebody is checking before they let an agent near them.
func renderBands(bands []band, sel, scroll, visible, inner int) string {
	var b strings.Builder
	for i := scroll; i < len(bands) && i < scroll+visible; i++ {
		row := bands[i]

		mark := " "
		plainName := strings.ToUpper(textclean.Terminal(row.name))
		name := theme.Key.Render(" " + plainName + " ")
		if i == sel {
			mark = theme.AccentTxt.Render("❯")
			name = theme.Title.Render(" " + plainName + " ")
		}
		right := ""
		if len(row.right) > 0 {
			right = " " + strings.Join(row.right, theme.Subtle.Render(" · ")) + " "
		}
		rule := max(inner-lipgloss.Width(" "+plainName+" ")-lipgloss.Width(right)-3, 0)
		b.WriteString(theme.Subtle.Render("─") + mark + name +
			theme.Subtle.Render(strings.Repeat("─", rule)) +
			right + theme.Subtle.Render("─") + "\n")

		indent := strings.Repeat(" ", pluginTextAt)
		for j := 0; j < bandHeight-1; j++ {
			line := ""
			if j < len(row.detail) {
				line = row.detail[j]
			}
			b.WriteString(ansi.Truncate(indent+line, inner, "…") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// visibleBands is how many fit in a body of this height, never fewer than one:
// a pane that shows nothing because the terminal is short is worse than one
// that shows a single entry badly.
func visibleBands(bodyHeight int) int { return max(bodyHeight/bandHeight, 1) }

// clampBand keeps a selection inside its own scroll window.
func clampBand(sel int, scroll *int, count, visible int) {
	*scroll = min(*scroll, max(0, count-visible))
	if sel < *scroll {
		*scroll = sel
	}
	if sel >= *scroll+visible {
		*scroll = sel - visible + 1
	}
	*scroll = max(*scroll, 0)
}
