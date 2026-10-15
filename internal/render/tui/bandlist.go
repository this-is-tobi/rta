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
	// detail is the indented lines under the rule, already styled: two for every
	// band, which is what the rows of a pane have in common.
	detail []string
	// note is a sentence about the entry that has to be read whole, as plain text
	// the renderer cleans and styles: the reason a row is `warn`, which is the only
	// thing that says why. It is wrapped to the band's width and drawn in the
	// warning colour under the first detail line, so a band is as tall as its note
	// is long and no taller when it has none. A detail line is cut at the edge of
	// the pane and marked, which is right for a value somebody is checking and
	// wrong for a sentence that is cut where it turns actionable.
	note string
}

// bandHeight is the lines one band occupies when it has nothing more to say: the
// rule plus its two detail lines. A band with a note is taller by the note's own
// lines (height), which is why the arithmetic of a pane's scroll walks its bands
// and no longer multiplies.
const bandHeight = 3

// noteLines is the band's note wrapped to what is left of inner after the indent,
// a word that is longer than that broken and not cut, since cutting is what the
// note exists to avoid.
func (b band) noteLines(inner int) []string {
	if b.note == "" {
		return nil
	}
	wrapped := ansi.Wrap(textclean.Terminal(b.note), max(inner-pluginTextAt, 1), "")
	return strings.Split(wrapped, "\n")
}

// height is the lines the band takes drawn inner cells wide: its rule, the detail
// lines every band has, and its note.
func (b band) height(inner int) int {
	return 1 + max(len(b.detail), bandHeight-1) + len(b.noteLines(inner))
}

// renderBands paints the visible slice of a band list.
//
// Truncation is per line and marked, never silent: a detail line that ran off
// the edge says so with an ellipsis, because the values on these lines are the
// ones somebody is checking before they let an agent near them. A note is the
// one thing that is wrapped and not cut, since it is a sentence and not a value.
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
		for j := 0; j < max(len(row.detail), bandHeight-1); j++ {
			line := ""
			if j < len(row.detail) {
				line = row.detail[j]
			}
			b.WriteString(ansi.Truncate(indent+line, inner, "…") + "\n")
			// Under the first line, which is what it qualifies: the second line a
			// band has always had was taken by the note where it was one, and
			// the credentials follow it.
			if j == 0 {
				for _, note := range row.noteLines(inner) {
					b.WriteString(indent + theme.WarnText.Render(note) + "\n")
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// bandsFit is how many bands, from the one at from, fit in a body of this height
// drawn inner cells wide, never fewer than one: a pane that shows nothing because
// the terminal is short is worse than one that shows a single entry badly.
func bandsFit(bands []band, from, bodyHeight, inner int) int {
	count, used := 0, 0
	for i := from; i < len(bands); i++ {
		used += bands[i].height(inner)
		if used > bodyHeight {
			break
		}
		count++
	}
	return max(count, 1)
}

// maxBandScroll is the furthest the window may start: the first band from which
// everything to the end fits, so that scrolling to the end fills the body and
// does not leave the last band alone above dead space. Bands have their own
// heights, so it is a walk back from the end.
func maxBandScroll(bands []band, bodyHeight, inner int) int {
	used := 0
	for s := len(bands) - 1; s >= 0; s-- {
		used += bands[s].height(inner)
		if used > bodyHeight {
			return min(s+1, len(bands)-1)
		}
	}
	return 0
}

// clampBand keeps a selection inside its own scroll window.
func clampBand(sel int, scroll *int, bands []band, bodyHeight, inner int) {
	*scroll = min(*scroll, maxBandScroll(bands, bodyHeight, inner))
	if sel < *scroll {
		*scroll = sel
	}
	for sel >= *scroll+bandsFit(bands, *scroll, bodyHeight, inner) {
		*scroll++
	}
	*scroll = max(*scroll, 0)
}
