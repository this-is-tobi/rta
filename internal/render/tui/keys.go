package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
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
	// The profiles pane and its actions. `f` rather than `p`, which the
	// plugins pane already holds — and a profile is a connection, not a
	// plugin, so overloading one key for both would be a lie about what they
	// are.
	bindProfile = binding{display: "f", keys: []string{"f"}, label: "profiles", rank: rankExtra}
	bindUse     = binding{display: "u", keys: []string{"u"}, label: "use", rank: rankPrimary}
	bindNew     = binding{display: "n", keys: []string{"n"}, label: "new", rank: rankAction}
	bindRemove  = binding{display: "d", keys: []string{"d"}, label: "delete", rank: rankAction}
	bindSecret  = binding{display: "s", keys: []string{"s"}, label: "credential", rank: rankAction}
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
	// — a real, reported conflict. ESC immediately
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
	// "approve" rather than "trust", because the footer has to say what the
	// key does and not what the subsystem is called: somebody looking at a
	// row marked "not run" is deciding whether to allow it, and that is the
	// word for it.
	bindTrust = binding{display: "t", keys: []string{"t"}, label: "approve", rank: rankAction}
	// "allow" and not "grant": the file, the command (`rta plugin allow`) and
	// the row's own warning all say allow, and a footer inventing a fourth
	// word for the thing three other surfaces already name is how somebody
	// comes to look for a `grant` subcommand that does something else.
	bindAllow = binding{display: "a", keys: []string{"a"}, label: "allow", rank: rankAction}

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
//
// keys is what this entry teaches, which is not always what it displays: "↑↓"
// stands for six keys and "c" for one. Carrying it is what lets a test ask the
// only question that matters about a footer — does this screen advertise every
// key it answers to — without parsing a rendered bar back into intentions.
type hintItem struct {
	display, label string
	rank           int
	keys           []string
	// style renders the whole entry when it is not a key hint. Only the flash
	// uses it: a confirmation is an answer rather than something to press, and
	// it has read green since it existed.
	style *lipgloss.Style
}

func item(b binding) hintItem {
	return hintItem{display: b.display, label: b.label, rank: b.rank, keys: b.keys}
}

// action is a per-capability shortcut — `a add` on the note tile. It outranks
// everything: it is the one thing on the screen that cannot be guessed from
// any other screen, so it is the last thing that should be dropped.
func action(key, label string) hintItem {
	return hintItem{display: key, label: label, rank: rankAction, keys: []string{key}}
}

// labelled overrides a binding's word for one screen, for the cases where the
// same key genuinely does a more specific thing worth naming.
func labelled(b binding, label string) hintItem {
	return hintItem{display: b.display, label: label, rank: b.rank, keys: b.keys}
}

// alias records that an entry also answers to extra keys it does not have room
// to display — `enter` beside `c configure`, where showing both would spend a
// second slot in the bar to teach the same verb twice.
//
// It is not a licence to hide keys: an alias is a key somebody would try, not
// one they would have to be told. Anything that does something a person could
// not guess from the displayed key belongs in its own entry.
func alias(it hintItem, extra ...string) hintItem {
	it.keys = append(append([]string{}, it.keys...), extra...)
	return it
}

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
		if it.style != nil {
			parts = append(parts, it.style.Render(it.display+" "+it.label))
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

// footerItems is what the screen currently on show advertises — every screen,
// in one table.
//
// It used to be seven expressions written at the seven places a bar was
// painted, and the profiles pane is what that cost: `f` opened it from the day
// it was written and the dashboard's footer never learned to say so. A key
// nobody can see is a key nobody has, however well it works.
//
// One table also makes the guarantee testable. Every entry carries the keys it
// teaches, so a test can put the model in each screen, press everything, and
// fail on anything that does something without appearing here — which is the
// only way this stops drifting again the next time a pane gains an action.
func (m Model) footerItems(screen mode) []hintItem {
	switch screen {
	case modeDashboard:
		return m.dashFooterItems()
	case modeResult:
		return m.resultFooterItems()
	case modePlugins:
		return []hintItem{
			item(bindColumn), item(bindToggle), item(bindTrust), item(bindAllow), item(bindConfig),
			labelled(bindOpen, "capabilities"),
			// The key that opened the pane closes it — the habit every pane
			// with a letter shortcut trains, and one nobody needs told.
			alias(item(bindBack), "p"), item(bindQuit),
		}
	case modeProfiles:
		return m.profileFooterItems()
	case modeProfilePlugins:
		return []hintItem{
			item(bindScroll), alias(item(bindConfig), "enter", "e"), item(bindNew),
			item(bindSecret), item(bindRemove),
			alias(item(bindBack), "left", "h"), item(bindQuit),
		}
	case modeBrowse:
		return []hintItem{
			item(bindColumn), labelled(bindOpen, "run"), labelled(bindSearch, "filter"),
			item(bindBack), item(bindQuit),
		}
	case modeForm:
		return []hintItem{
			labelled(bindOpen, "next"), item(bindFastSubmit), labelled(bindBack, "cancel"),
		}
	case modeTheme:
		return []hintItem{
			labelled(bindOpen, "next/save"), item(bindFastSubmit), labelled(bindBack, "cancel"),
		}
	case modeCopyPick:
		return []hintItem{
			labelled(bindOpen, "copy"), item(bindFastSubmit), labelled(bindBack, "cancel"),
		}
	case modeRunning:
		return []hintItem{
			alias(labelled(bindBack, "leave it running"), "q"),
			action("ctrl+c", "quit"),
		}
	}
	return nil
}

// footerFor renders one screen's bar, with the flash beside it.
//
// It takes the screen rather than reading m.mode so that a view function is
// self-contained: pluginsView asks for the plugin pane's keys and gets them,
// whatever state the model happens to be in. Reading m.mode here would make
// every view's footer depend on a field none of them otherwise touch, which is
// the kind of coupling that makes a test render the right pane under the wrong
// bar and still pass.
//
// The flash lives here because it is the answer to the key just pressed, and
// the bar is where the key was advertised — putting the confirmation anywhere
// else makes somebody look for it.
func (m Model) footerFor(screen mode) string {
	// An armed delete replaces the whole bar rather than joining it. While
	// the removal is one key away every other hint is a distraction, and `y`
	// must not keep the meaning this pane's own vocabulary gives it — in
	// modeProfiles it copies export lines. The bar is also what makes the
	// gate honest: it names exactly what `y` would remove, in the place the
	// key that armed it was advertised. Gated on m.mode as well as the armed
	// string so a view rendered for another screen under this state — which
	// no key sequence reaches, but tests do — keeps its own bar.
	if m.armedDelete != "" && screen == m.mode {
		label := "delete profile " + m.armedDelete + " and revoke the grants naming it"
		if screen == modeProfilePlugins {
			label = "remove " + m.armedDelete + " from " + m.profileOpen
		}
		if m.width > 0 {
			label = ansi.Truncate(label, max(m.width-3, 8), "…")
		}
		bad := theme.BadText
		return fitHintBar(m.width, footerMaxLines,
			hintItem{display: "y", label: label, rank: rankAction, keys: []string{"y"}},
			hintItem{display: "✗", label: "any other key cancels", rank: rankAction, style: &bad},
		)
	}
	items := m.footerItems(screen)
	if m.flash != "" {
		// Packed with everything else rather than appended after it, which is
		// the whole fix: it used to be glued on past the width fitHintBar had
		// just packed to, so a long confirmation ran off the edge or soft
		// wrapped — and dashRowsVisible budgets the tile grid from
		// lipgloss.Height(dashFooter()), which then reported one line while
		// the terminal drew two. A saved-profile message stole a row the grid
		// had already given to a tile and scrolled the header off the top.
		//
		// rankAction, so it is the last thing dropped: it is the answer to the
		// key just pressed, which is more use in that moment than any hint
		// beside it.
		good := theme.GoodText
		// Truncated as well as packed. Every other entry is two words and is
		// never split — "a half-rendered key is worse than a missing one" —
		// but a confirmation is a sentence, so the one that does not fit has
		// to give ground rather than run off the edge.
		flash := m.flash
		if m.width > 0 {
			flash = ansi.Truncate(flash, max(m.width-3, 8), "…")
		}
		items = append(items, hintItem{display: "✓", label: flash, rank: rankAction, style: &good})
	}
	return fitHintBar(m.width, footerMaxLines, items...)
}
