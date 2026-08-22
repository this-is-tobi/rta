package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
)

// The app's key vocabulary, declared once.
//
// Coherence is the feature. A key that means one thing on the dashboard and
// another in the catalogue costs a person a trip to the documentation for
// something they should never have had to look up, and every screen that
// spells the same idea differently is a small tax on the same attention. So
// the keys, the words for them and the footer that teaches them all come
// from this table rather than being written out per view — which is how
// "↑↓←→", "↑↓" and "↑/↓" came to mean the same thing in three places, and
// how h/j/k/l, tab, ":", "<", ">" and "x" came to work without any screen
// ever saying so.
//
// A binding declares every key it answers to, not just the pretty one, so
// "which keys does this screen actually handle" is a question the code can
// answer — and a test can check against what the screen claims.
type binding struct {
	// display is what the footer shows: a glyph cluster ("↑↓←→"), a pair
	// ("[ ]"), or the key itself.
	display string
	// keys are every key press this binding answers to. The extras are
	// deliberate aliases for people who already have the habit — vim
	// motions, tab, the shifted forms of the bracket keys.
	keys []string
	// label is the verb, in the same words everywhere.
	label string
	// rank decides what survives a narrow footer. Lower stays longer. It is
	// deliberately not the display order: `q quit` is shown last because it
	// is where the eye expects it, and dropped late because being unable to
	// leave is worse than being unable to reorder.
	rank int
}

// Ranks, lowest survives longest.
const (
	rankAction  = iota // context-specific: learnable on this screen and nowhere else
	rankPrimary        // the thing this screen is for, and how to move around it
	rankExit           // how to leave
	rankExtra          // everything else the screen can also do
)

// The vocabulary. One entry per idea, not per screen.
var (
	bindQuit   = binding{display: "q", keys: []string{"q", "ctrl+c"}, label: "quit", rank: rankExit}
	bindBack   = binding{display: "esc", keys: []string{"esc"}, label: "back", rank: rankExit}
	bindOpen   = binding{display: "enter", keys: []string{"enter"}, label: "open", rank: rankPrimary}
	bindRerun  = binding{display: "r", keys: []string{"r"}, label: "re-run", rank: rankPrimary}
	bindEdit   = binding{display: "e", keys: []string{"e"}, label: "edit inputs", rank: rankExtra}
	bindCopy   = binding{display: "y", keys: []string{"y"}, label: "copy json", rank: rankExtra}
	bindMove   = binding{display: "[ ]", keys: []string{"[", "]", "<", ">"}, label: "move", rank: rankExtra}
	bindHide   = binding{display: "H", keys: []string{"H"}, label: "hide", rank: rankExtra}
	bindPlugin = binding{display: "p", keys: []string{"p"}, label: "plugins", rank: rankExtra}
	bindTheme  = binding{display: "t", keys: []string{"t"}, label: "theme", rank: rankExtra}
	bindConfig = binding{display: "c", keys: []string{"c"}, label: "configure", rank: rankAction}
	// shift+enter accepts whatever is currently bound across every
	// remaining field, exactly as pressing enter on each in turn would —
	// the shortcut for a form where the defaults are already fine and the
	// rest is not worth tabbing through. Every huh field type answers to
	// plain "enter" for its own Next/Submit key (charm.land/huh/v2's
	// default keymap), and bubbletea v2 requests basic key disambiguation
	// from the terminal by default — the doc for tea.KeyboardEnhancements
	// names "shift+enter" as one of the keys this makes reachable with no
	// further setup. Where the terminal does not support it, this key
	// simply never arrives — plain enter, field by field, still works
	// exactly as it does today.
	//
	// alt+enter is a deliberate second way in, not a display alias — VS
	// Code's integrated terminal needs its own setting for real
	// shift+enter disambiguation (terminal.integrated.enableKittyKeyboard-
	// Protocol), and even with that on, a shift+enter keybinding installed
	// by Claude Code's own /terminal-setup (workbench.action.terminal.
	// sendSequence, sending literal ESC+CR) claims the key first regardless
	// — a real, reported conflict (PROJECT.md D76). ESC immediately
	// followed by one more byte is the older, universal "meta sends
	// escape" convention every terminal already speaks with zero protocol
	// negotiation, and github.com/charmbracelet/ultraviolet (bubbletea's
	// own input decoder) already parses that shape as alt+<key> — ESC+CR
	// decodes to alt+enter (`KeyEnter = rune(ansi.CR)`, decoder.go's
	// two-byte ESC-prefix case), a real, distinct key event, not two. That
	// is also exactly what "Option as Meta" produces on a terminal a
	// person enables it on by hand. Recognizing it here means the fallback
	// this feature already had — plain enter, field by field — has one
	// more terminal that needs no setup at all on rta's side, at the cost
	// of nothing: no plain "enter" ever carries ModAlt, so this can never
	// fire from an ordinary keypress.
	bindFastSubmit = binding{display: "⇧enter", keys: []string{"shift+enter", "alt+enter"}, label: "submit", rank: rankExtra}
	bindBrowse     = binding{display: "b", keys: []string{"b", ":"}, label: "browse", rank: rankExtra}
	bindSearch     = binding{display: "/", keys: []string{"/"}, label: "search", rank: rankExtra}
	bindToggle     = binding{display: "space", keys: []string{" ", "space", "x"}, label: "show/hide", rank: rankPrimary}

	// One notation for moving a cursor, everywhere. The dashboard is a grid
	// and the others are columns, but "the arrow keys move the thing you are
	// looking at" is one idea and reads worse as three.
	bindSelect = binding{
		display: "↑↓←→",
		keys:    []string{"up", "down", "left", "right", "k", "j", "h", "l", "tab"},
		label:   "select", rank: rankPrimary,
	}
	bindScroll = binding{
		display: "↑↓",
		keys:    []string{"up", "down", "k", "j"},
		label:   "scroll", rank: rankPrimary,
	}
	bindColumn = binding{
		display: "↑↓",
		keys:    []string{"up", "down", "k", "j"},
		label:   "select", rank: rankPrimary,
	}
)

// matches reports whether a key press triggers this binding.
func (b binding) matches(key string) bool {
	for _, k := range b.keys {
		if k == key {
			return true
		}
	}
	return false
}

// hint renders the binding for the footer.
func (b binding) hint() string { return hint(b.display, b.label) }

// hintItem is one entry in a footer: a binding, or an ad-hoc pair for the
// per-capability actions, whose keys and words come from the capability
// rather than from the vocabulary.
type hintItem struct {
	display, label string
	rank           int
}

func item(b binding) hintItem { return hintItem{b.display, b.label, b.rank} }

// action is a per-capability shortcut — `a add` on the todo tile. It outranks
// everything: it is the one thing on the screen that cannot be guessed from
// any other screen, so it is the last thing that should be dropped.
func action(key, label string) hintItem { return hintItem{key, label, rankAction} }

// labelled overrides a binding's word for one screen, for the cases where the
// same key genuinely does a more specific thing worth naming.
func labelled(b binding, label string) hintItem { return hintItem{b.display, label, b.rank} }

// fitHintBar lays the hints out in at most maxLines lines of the given
// width, dropping the least important only when even that will not hold.
//
// The previous behaviour was all-or-nothing: measure the whole bar on one
// line, and if it did not fit, swap in a hardcoded three-hint minimum. So
// selecting a tile that had actions of its own silently cost the reader five
// navigation hints at once, on a terminal with thirty-six columns to spare.
// That is what made "[ ] move" appear on some tiles and not others — nothing
// about moving changed, the bar just gave up. Wrapping to a second line is
// what lets every screen offer the same navigation regardless of how much
// else it has to say, which is the point: a key that is there should be
// advertised, and advertised in the same place every time.
//
// A bar that still had to drop something ends in "…", because a footer that
// quietly shows less than the truth is how a key becomes undiscoverable.
func fitHintBar(width, maxLines int, items ...hintItem) string {
	if len(items) == 0 {
		return ""
	}
	if maxLines < 1 {
		maxLines = 1
	}
	keep := make([]bool, len(items))
	for i := range keep {
		keep[i] = true
	}
	for {
		bar, lines := layout(items, keep, width)
		if width <= 0 || lines <= maxLines {
			return bar
		}
		// Highest rank goes first, and within a rank the rightmost — so the
		// bar shortens from its tail, which is where the eye stops looking.
		victim, worst := -1, -1
		for i, k := range keep {
			if k && items[i].rank >= worst {
				victim, worst = i, items[i].rank
			}
		}
		if victim < 0 {
			return bar // nothing left to drop; a long bar beats no bar
		}
		keep[victim] = false
	}
}

// layout packs the kept hints into lines, greedily, and reports how many it
// took. Hints are never split: a half-rendered key is worse than a missing
// one. The ellipsis is packed like any other part, so a bar that had to drop
// something cannot overflow by announcing it.
func layout(items []hintItem, keep []bool, width int) (string, int) {
	parts := make([]string, 0, len(items)+1)
	dropped := false
	for i, it := range items {
		if !keep[i] {
			dropped = true
			continue
		}
		parts = append(parts, hint(it.display, it.label))
	}
	if len(parts) == 0 {
		return "", 0
	}
	if dropped {
		parts = append(parts, theme.Subtle.Render("…"))
	}

	sep := theme.Subtle.Render(" · ")
	sepW := lipgloss.Width(sep)
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, p := range parts {
		pw := lipgloss.Width(p)
		switch {
		case curW == 0:
			cur.WriteString(" " + p)
			curW = 1 + pw
		case width > 0 && curW+sepW+pw > width:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(" " + p)
			curW = 1 + pw
		default:
			cur.WriteString(sep + p)
			curW += sepW + pw
		}
	}
	lines = append(lines, cur.String())
	return strings.Join(lines, "\n"), len(lines)
}

// footerMaxLines bounds how tall a hint bar may grow. Two lines is enough
// for every screen in the app at any width people actually use, and a third
// would start eating the content the footer exists to explain.
const footerMaxLines = 2
