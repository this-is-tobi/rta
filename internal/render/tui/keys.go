package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
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

// Ranks, lowest survives longest. They are ordered by what a person cannot do
// without. A confirmation or a refusal comes first because it answers the key
// just pressed, and a bar that dropped it said nothing about a save that had
// happened. How to leave comes next, with "? help": at forty columns a pane
// whose own actions and navigation filled both lines dropped esc and q along
// with the rest of the tail, which left a screen that did not say how to get
// out of it, and a "…" without "? help" points at keys nobody can find. The
// three hints cost about twenty cells. The zero value stays rankAction so a
// hint built without a rank is the common case.
const (
	rankNotice  = iota - 2 // the answer to the key just pressed
	rankLeave              // how to leave a screen, and how to list what the bar dropped
	rankAction             // context-specific: learnable on this screen and nowhere else
	rankPrimary            // the thing this screen is for, and how to move around it
	rankExtra              // everything else the screen can also do
)

// The vocabulary. One entry per idea, not per screen.
var (
	bindQuit = binding{display: "q", keys: []string{"q", "ctrl+c"}, label: "quit", rank: rankLeave}
	// Last on the bar, so of the hints that share its rank it is the first
	// to go when even those do not fit.
	bindHelp  = binding{display: "?", keys: []string{"?"}, label: "help", rank: rankLeave}
	bindBack  = binding{display: "esc", keys: []string{"esc"}, label: "back", rank: rankLeave}
	bindOpen  = binding{display: "enter", keys: []string{"enter"}, label: "open", rank: rankPrimary}
	bindRerun = binding{display: "r", keys: []string{"r"}, label: "re-run", rank: rankPrimary}
	bindEdit  = binding{display: "e", keys: []string{"e"}, label: "edit inputs", rank: rankExtra}
	bindCopy  = binding{display: "y", keys: []string{"y"}, label: "copy json", rank: rankExtra}
	bindMove  = binding{display: "[ ]", keys: []string{"[", "]", "<", ">"}, label: "move", rank: rankExtra}
	bindHide  = binding{display: "H", keys: []string{"H"}, label: "hide", rank: rankExtra}
	// The dashboard's screens: V walks them forward and v the other way round.
	// One hint for the pair, as [ ] is for moving a tile either way, since the
	// two answer in exactly the same situations and a bar that spent two
	// entries on them would lose both sooner on a narrow terminal.
	bindView = binding{display: "V/v", keys: []string{"V", "v"}, label: "view", rank: rankExtra}
	// Unlocks the encrypted store for this session, and locks it again.
	bindUnlock = binding{display: "U", keys: []string{"U"}, label: "unlock store", rank: rankExtra}
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
	// The run form's environment picker sits above its first box, and the arrows
	// belong to the box, so shift+tab is the way up to it.
	bindEnvironment = binding{display: "⇧tab", keys: []string{"shift+tab"}, label: "environment", rank: rankAction}
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
	//
	// ctrl+s is the third way in and the only one every terminal can send:
	// shift+enter needs the keyboard protocol and alt+enter needs Option as
	// Meta, and Terminal.app has neither out of the box, so for the people
	// using it "submit" was a word in the footer for a key that never arrived.
	// A control byte needs nothing negotiated. Bubble Tea puts the terminal in
	// raw mode, which is what stops ctrl+s being the XOFF it is at a cooked
	// prompt, and no huh or bubbles field binds it, so it cannot be a letter of
	// anything typed. All three are shown, because a hint that names only the
	// key that works for you is a hint that hides the one that works for
	// someone else.
	bindFastSubmit = binding{
		display: "⇧enter/alt+enter/ctrl+s",
		keys:    []string{"shift+enter", "alt+enter", "ctrl+s"},
		label:   "submit", rank: rankExtra,
	}
	bindBrowse = binding{display: "b", keys: []string{"b", ":"}, label: "browse", rank: rankExtra}
	// The catalogue's and the search bar's way onto the dashboard: what
	// `rta dashboard add` does from a shell; on the dashboard itself it
	// opens the catalogue to pick from. `+` rather than `a`, which the
	// plugin pane already spends on allow. rankExtra like H and [ ]: the
	// arrangement keys give way to running things when the bar is short.
	bindAdd = binding{display: "+", keys: []string{"+"}, label: "add tile", rank: rankExtra}
	// The same key inside the open search box, where it adds the highlighted
	// match and does not open the catalogue: two actions, so two words.
	bindAddResult = binding{display: "+", keys: []string{"+"}, label: "add as tile", rank: rankExtra}
	bindSearch    = binding{display: "/", keys: []string{"/"}, label: "search", rank: rankExtra}
	bindToggle    = binding{display: "space", keys: []string{" ", "space", "x"}, label: "show/hide", rank: rankPrimary}
	// "approve" rather than "trust", because the footer has to say what the
	// key does and not what the subsystem is called: somebody looking at a
	// row marked "not run" is deciding whether to allow it, and that is the
	// word for it.
	bindTrust = binding{display: "t", keys: []string{"t"}, label: "approve", rank: rankAction}
	// "access" and not "grant" or "allow": the command (`rta plugin access add`)
	// and the row's own warning both say access, and a footer inventing another
	// word for the thing two other surfaces already name is how somebody comes
	// to look for a `grant` subcommand that does something else. Allow is the
	// standing grant to an agent, which is another act.
	bindAllow = binding{display: "a", keys: []string{"a"}, label: "access", rank: rankAction}

	// z folds the section of the selected tile or header and Z every section, on a
	// dashboard that has some. Letters rather than enter alone, which opens a
	// tile, and rank extra like H and V: what the arrangement looks like gives way
	// to running things when the bar is short.
	bindFold    = binding{display: "z", keys: []string{"z"}, label: "fold", rank: rankExtra}
	bindFoldAll = binding{display: "Z", keys: []string{"Z"}, label: "fold all", rank: rankExtra}

	// One notation for moving a cursor, everywhere. The dashboard is a grid
	// and the others are columns, but "the arrow keys move the thing you are
	// looking at" is one idea and reads worse as three.
	bindSelect = binding{
		display: "↑↓←→",
		keys:    []string{"up", "down", "left", "right", "k", "j", "h", "l", "tab", "shift+tab", "home", "end"},
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
	// The catalogue's way through a list that is longer than the screen. The
	// bubbles list answers to these, vim's page and end keys among them, and rta
	// never wrote them, so for as long as nothing named them they were on no bar
	// and in no overlay: eight pages and the only way to the last was down.
	bindPage = binding{
		display: "pgup/pgdn",
		keys:    []string{"pgup", "pgdown", "left", "right", "h", "l", "b", "u", "f", "d"},
		label:   "page", rank: rankExtra,
	}
	bindEnds = binding{
		display: "home/end",
		keys:    []string{"home", "end", "g", "G"},
		label:   "first/last", rank: rankExtra,
	}
	// The result pane's way through an answer taller than it. The viewport under
	// it is a pager and answers to a pager's keys (a page on pgup and pgdn, the
	// space bar, f and b; half a page on u and d and their ctrl forms), which rta
	// never wrote down, so they were on no bar and in no overlay. Not bindPage:
	// that one is the catalogue's list, which turns its pages on the arrows and
	// on h and l as well, and a result's arrows scroll a line.
	bindPageView = binding{
		display: "pgup/pgdn",
		keys:    []string{"pgup", "pgdown", "space", " ", "f", "b", "u", "d", "ctrl+u", "ctrl+d"},
		label:   "page", rank: rankExtra,
	}
)

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
	// typed says the entry teaches typing: every printable key the screen has
	// not claimed for something else is a letter of a text. No list of keys can
	// say that, and a test that held a footer to its keys needs to be told.
	typed bool
	// style renders the whole entry when it is not a key hint. Only the flash
	// uses it: a confirmation is an answer rather than something to press, and
	// it has read green since it existed.
	style *lipgloss.Style
	// wrap says the label is a sentence and not a pair of words: a form's
	// refusal, which ends in what to do about it. It is wrapped across the rows
	// the bar has, in place of being cut at the width, and is never the entry
	// dropped to make room.
	wrap bool
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

// without is it with the keys in taken left out. A key does one thing on a
// screen, and an entry that teaches the keys of a pager has to leave the ones a
// row action or a toggle of the same screen has already claimed to them: `d`
// pages the pane of a text and checks a note off on the list of notes.
func without(it hintItem, taken []string) hintItem {
	it.keys = slices.DeleteFunc(slices.Clone(it.keys), func(k string) bool { return slices.Contains(taken, k) })
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
	return fitBar(width, maxLines, false, items...)
}

// fitBar is fitHintBar, and for the bars of the forms, whose rows are shared
// out: see layout.
func fitBar(width, maxLines int, balance bool, items ...hintItem) string {
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
		bar, lines := layout(items, keep, width, maxLines, balance)
		if width <= 0 || lines <= maxLines {
			return bar
		}
		// Highest rank goes first, and within a rank the rightmost — so the
		// bar shortens from its tail, which is where the eye stops looking. A
		// sentence is not in the running: it is what the bar is saying, and it
		// has already been fitted to the rows the bar has.
		victim, worst := -1, -1
		for i, k := range keep {
			if k && !items[i].wrap && items[i].rank >= worst {
				victim, worst = i, items[i].rank
			}
		}
		if victim < 0 {
			return bar // nothing left to drop; a long bar beats no bar
		}
		keep[victim] = false
	}
}

// barCell is one piece of a laid-out row. fixed marks a row of a wrapped
// sentence, which is drawn where it fell and is not moved to even a bar out.
type barCell struct {
	text  string
	w     int
	fixed bool
}

// layout packs the kept hints into lines, greedily, and reports how many it
// took. Hints are never split: a half-rendered key is worse than a missing
// one. The ellipsis is packed like any other part, so a bar that had to drop
// something cannot overflow by announcing it.
//
// A sentence (hintItem.wrap) leads its own rows when it takes more than one,
// the first marked and the rest indented under it, and the hints carry on from
// the end of its last row. It is fitted to leave that row room for how to leave
// the screen (rankLeave), since a bar that said what was wrong and not how to
// get out of it is the one the ranks above exist to prevent. Its ellipsis is
// left out when no row is left to put it on: the sentence has filled the bar,
// and "…" on a third row would be the bar's only overflow.
//
// balance shares two rows out when the second would hold a single hint beside
// a first that holds several: `esc cancel` alone under four others reads as a
// different kind of thing from the keys above it, and the last hint moved down
// leaves both rows a plain list. Only the forms ask for it.
func layout(items []hintItem, keep []bool, width, maxLines int, balance bool) (string, int) {
	sep := theme.Subtle.Render(" · ")
	sepW := lipgloss.Width(sep)
	// What a sentence leaves the end of its last row free for: the hints that
	// say how to leave, which are the last of the rest to be dropped.
	reserve := 0
	for i, it := range items {
		if keep[i] && !it.wrap && it.rank <= rankLeave {
			reserve += sepW + lipgloss.Width(it.display+" "+it.label)
		}
	}

	var parts [][]string
	dropped := false
	sentence := false
	for i, it := range items {
		if !keep[i] {
			dropped = true
			continue
		}
		switch {
		case it.wrap && width > 0:
			parts = append(parts, sentenceRows(it, width, maxLines, reserve))
			sentence = true
		case it.style != nil:
			parts = append(parts, []string{it.style.Render(it.display + " " + it.label)})
		default:
			parts = append(parts, []string{hint(it.display, it.label)})
		}
	}
	if len(parts) == 0 {
		return "", 0
	}

	var (
		rows [][]barCell
		cur  []barCell
		curW int
	)
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, cur)
			cur, curW = nil, 0
		}
	}
	begin := func(c barCell) {
		cur, curW = append(make([]barCell, 0, 4), c), 1+c.w
	}
	put := func(c barCell) {
		switch {
		case curW == 0:
			begin(c)
		case width > 0 && curW+sepW+c.w > width:
			flush()
			begin(c)
		default:
			cur, curW = append(cur, c), curW+sepW+c.w
		}
	}
	for _, p := range parts {
		if len(p) == 1 {
			put(barCell{text: p[0], w: lipgloss.Width(p[0])})
			continue
		}
		flush()
		for _, r := range p[:len(p)-1] {
			rows = append(rows, []barCell{{text: r, w: lipgloss.Width(r), fixed: true}})
		}
		last := p[len(p)-1]
		begin(barCell{text: last, w: lipgloss.Width(last), fixed: true})
	}
	if dropped {
		more := barCell{text: theme.Subtle.Render("…"), w: 1}
		noRoom := sentence && width > 0 && curW+sepW+more.w > width && len(rows)+1 >= maxLines
		if !noRoom {
			put(more)
		}
	}
	flush()

	if balance && len(rows) >= 2 {
		prev, last := rows[len(rows)-2], rows[len(rows)-1]
		if moved := prev[len(prev)-1]; len(last) == 1 && !last[0].fixed && len(prev) >= 2 && !moved.fixed &&
			(width <= 0 || 1+moved.w+sepW+last[0].w <= width) {
			rows[len(rows)-2] = prev[:len(prev)-1]
			rows[len(rows)-1] = []barCell{moved, last[0]}
		}
	}

	lines := make([]string, len(rows))
	for i, row := range rows {
		texts := make([]string, len(row))
		for j, c := range row {
			texts[j] = c.text
		}
		lines[i] = " " + strings.Join(texts, sep)
	}
	return strings.Join(lines, "\n"), len(lines)
}

// sentenceRows is a sentence wrapped to the width, each row styled as the
// entry is, the first led by its mark and the rest indented to stand under the
// words. One that will not fit the rows the bar has loses its middle (elide).
func sentenceRows(it hintItem, width, maxLines, reserve int) []string {
	w := max(width-3, 8) // the bar's lead, and the mark or the indent that stands for it
	if reserve >= w/2 {
		reserve = 0 // no room to keep any: the sentence is what the bar is for
	}
	lines := wrapped(it.label, w, maxLines, reserve)
	if lines == nil {
		lines = wrapped(elide(it.label, w, maxLines, reserve), w, maxLines, 0)
	}
	if lines == nil {
		lines = []string{ansi.Truncate(it.label, w, "…")}
	}
	rows := make([]string, len(lines))
	for i, line := range lines {
		lead := "  "
		if i == 0 {
			lead = it.display + " "
		}
		rows[i] = lead + line
		if it.style != nil {
			rows[i] = it.style.Render(rows[i])
		}
	}
	return rows
}

// wrapped is text in rows of w cells, or nil when it needs more than maxLines of
// them or would leave the last of those less than reserve cells short of w.
func wrapped(text string, w, maxLines, reserve int) []string {
	lines := strings.Split(ansi.Wrap(text, w, ""), "\n")
	if len(lines) > maxLines || (len(lines) == maxLines && ansi.StringWidth(lines[len(lines)-1]) > w-reserve) {
		return nil
	}
	return lines
}

// elide cuts the middle out of text until what is left wraps into maxLines rows
// of w cells, the last with reserve cells free. The start stays, since it says
// what went wrong, and so does more of the end, since that is where a remedy
// goes; the cut is marked, and made between words where there is a word to cut
// at.
//
// A refusal is "what went wrong — its hint", and a third of the room is about
// what a short message takes, so the start ended with the message and the cut
// began on the first words of the hint: "drop the trailing slash" went, and
// the reasons after it stayed. When the message fits in half the room it is
// kept whole and the start goes on into the hint by a third of what is left.
func elide(text string, w, maxLines, reserve int) string {
	capacity := maxLines*w - reserve - 2 // the mark of the cut and the space after it
	headCells := capacity / 3
	if message, _, ok := strings.Cut(text, " — "); ok {
		if lead := ansi.StringWidth(message) + 3; lead <= capacity/2 { // 3 for the dash and its spaces
			headCells = lead + (capacity-lead)/3
		}
	}
	total := ansi.StringWidth(text)
	head := ansi.Truncate(text, headCells, "")
	if len(head) < len(text) && text[len(head)] != ' ' {
		if i := strings.LastIndexByte(head, ' '); i > 0 {
			head = head[:i]
		}
	}
	for tailCells := capacity - headCells; tailCells > 0; tailCells-- {
		tail := ansi.TruncateLeft(text, total-tailCells, "")
		if from := len(text) - len(tail); from > 0 && text[from-1] != ' ' {
			if i := strings.IndexByte(tail, ' '); i >= 0 {
				tail = tail[i+1:]
			}
		}
		cut := strings.TrimRight(head, " ") + "… " + strings.TrimLeft(tail, " ")
		if wrapped(cut, w, maxLines, reserve) != nil {
			return cut
		}
	}
	return ansi.Truncate(text, w*maxLines-reserve, "…")
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
	if m.dialog != nil && screen == m.mode {
		return m.dialogItems()
	}
	items := m.screenItems(screen)
	// A call waiting is the one thing every screen that can answer it leads
	// with: the line above says it, and this is the key that answers. The agent
	// tile's own `w waiting` already is that, and says it once.
	if screen == m.mode && m.answersWaiting() && !teachesKey(items, "w") {
		items = append([]hintItem{action("w", "answer")}, items...)
	}
	// The overlay's key is advertised on exactly the screens that answer it
	// (help.go), last so the bar drops it first: it is the fallback for a
	// bar that had to drop something, not a thing to keep at their expense.
	if m.helpOffered(screen) {
		items = append(items, item(bindHelp))
	}
	return items
}

// teachesKey is whether some entry teaches key.
func teachesKey(items []hintItem, key string) bool {
	for _, it := range items {
		for _, k := range it.keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// screenItems is one screen's own vocabulary, before the overlay's key.
func (m Model) screenItems(screen mode) []hintItem {
	switch screen {
	case modeDashboard:
		return m.dashFooterItems()
	case modeResult:
		return m.resultFooterItems()
	case modePlugins:
		return m.pluginFooterItems()
	case modeProfiles:
		return m.profileFooterItems()
	case modeProfilePlugins:
		// An environment holding no plugin says "Press n to add one", and the
		// bar offered to configure, set a credential for and delete nothing.
		if row, ok := m.openProfile(); ok && len(row.conns) == 0 {
			return []hintItem{item(bindNew), alias(item(bindBack), "left", "h"), item(bindQuit)}
		}
		return []hintItem{
			item(bindColumn), alias(item(bindConfig), "enter", "e"), item(bindNew),
			item(bindSecret), item(bindRemove),
			alias(item(bindBack), "left", "h"), item(bindQuit),
		}
	case modeBrowse:
		if m.list.FilterState() == list.Filtering {
			return filteringItems()
		}
		// While a filter is applied the first esc clears it and a second leaves
		// (browseKeys), so the one word "back" was untrue for the first press.
		back := item(bindBack)
		if m.list.FilterState() == list.FilterApplied {
			back = labelled(bindBack, "clear filter")
		}
		return []hintItem{
			item(bindColumn), item(bindPage), item(bindEnds), labelled(bindOpen, "run"), item(bindAdd),
			labelled(bindSearch, "filter"), back, item(bindQuit),
		}
	case modeForm:
		var hints []hintItem
		escWord, fastWord := "cancel", "submit"
		if m.form != nil {
			hints = fieldHints(m.form.form, len(m.form.fields), m.form.completes)
			if m.form.environmentAbove() {
				hints = append(hints, item(bindEnvironment))
			}
			escWord = escLabel(m.form.form)
			if escWord == "cancel" && m.form.firstStage != nil {
				escWord = "back"
			}
			// The first stage of a form that asks in two only moves on to the
			// second, whichever of the keys is pressed.
			if !m.form.final {
				fastWord = "next"
			}
		}
		return append(hints,
			labelled(bindOpen, m.formEnterWord()), labelled(bindFastSubmit, fastWord), labelled(bindBack, escWord),
		)
	case modeTheme:
		var hints []hintItem
		if m.themePick != nil {
			return append(fieldHints(m.themePick.form, 1, nil),
				labelled(bindOpen, "edit"), item(bindFastSubmit), labelled(bindBack, escLabel(m.themePick.form)),
			)
		}
		if m.themeForm != nil {
			// Every box in the theme editor completes — the palette names
			// and the current values — so the predicate is a constant.
			hints = fieldHints(m.themeForm.form, len(themeFieldOrder), func(*huh.Input) bool { return true })
		}
		// The editor ends in a save, which is what enter does on its last box.
		word := "next"
		if m.themeForm != nil && onLastField(m.themeForm.form) {
			word = "save"
		}
		return append(hints,
			labelled(bindOpen, word), item(bindFastSubmit), labelled(bindBack, "cancel"),
		)
	case modePick:
		var hints []hintItem
		verb, esc := "choose", "cancel"
		if m.pick != nil {
			hints = fieldHints(m.pick.form, 1, nil)
			verb, esc = m.pick.kind.verb(), escLabel(m.pick.form)
		}
		return append(hints,
			labelled(bindOpen, verb), item(bindFastSubmit), labelled(bindBack, esc),
		)
	case modeRunning:
		// "stop it" over "cancel": cancel is already this binding's label
		// on the form screens, where it means "discard what I typed", and
		// what is stopped here is a call that may already have reached the
		// far end. "leave it running" was the old label, and esc never did
		// that — runningKeys cancels the context and flashes "cancelled".
		return []hintItem{
			alias(labelled(bindBack, "stop it"), "q"),
			action("ctrl+c", "quit"),
		}
	case modeConfirm:
		return m.confirmFooterItems()
	}
	return nil
}

// filteringItems is the bar while the catalogue's filter box holds the
// keyboard, where q, / and + are letters of a query and esc clears it rather
// than leaving: the dashboard's search bar has the same bar for the same
// reason (searchEditingItems), and the catalogue went on advertising "q quit"
// and "esc back" over a box that typed one and cancelled the other.
func filteringItems() []hintItem {
	return []hintItem{
		{display: "↑↓", label: "select", rank: rankPrimary, keys: []string{"up", "down"}},
		labelled(bindOpen, "apply"), labelled(bindBack, "clear"),
		{display: "ctrl+c", label: "quit", rank: rankLeave, keys: []string{"ctrl+c"}},
	}
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
		// The names as a listing shows them (textclean.Name), and not drawn raw:
		// a profile's name and a plugin key are a config file's, which a drop-in
		// or a repository can bring, and this is the line read to know exactly
		// what goes. A right-to-left override reordered what followed it and an
		// escape sequence could hide it; quoted, the oddness is what is seen.
		label := "delete profile " + textclean.Name(m.armedDelete) + " and revoke the grants naming it"
		if screen == modeProfilePlugins {
			label = "remove " + textclean.Name(m.armedDelete) + " from " + textclean.Name(m.profileOpen)
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
	// An armed answer replaces the bar for the same reason: while one key
	// answers, the line that names the call is what the screen is for.
	if m.armed != nil && screen == m.mode {
		return fitHintBar(m.width, footerMaxLines, m.armedItems()...)
	}
	items := m.footerItems(screen)
	// A form's validation errors, in the bar rather than under the fields.
	// huh's own error line is off (form.go says why: it grew the form into a
	// clipped panel), and this is where the message lands instead — beside
	// the key that was just pressed, styled as the refusal it is, and never
	// moving the fields it is about.
	if screen == m.mode {
		bad := theme.BadText
		var refusals []hintItem
		for _, err := range m.formErrors(screen) {
			// Cleaned for the flash's reason below: a validator's message can
			// quote what was typed or pasted.
			//
			// Wrapped across the rows the bar has, not cut at the width: the
			// end of a refusal is what to do about it, and it was the end that
			// went. It leads the bar for the flash's reason as well, so the
			// hints that give way are the ones after it.
			refusals = append(refusals, hintItem{
				display: "✗", label: textclean.Terminal(err.Error()), rank: rankNotice, style: &bad, wrap: true,
			})
		}
		items = append(refusals, items...)
	}
	if m.flash != "" {
		// Packed with everything else rather than appended after it, which is
		// the whole fix: it used to be glued on past the width fitHintBar had
		// just packed to, so a long confirmation ran off the edge or soft
		// wrapped — and dashRowsVisible budgets the tile grid from
		// lipgloss.Height(dashFooter()), which then reported one line while
		// the terminal drew two. A saved-profile message stole a row the grid
		// had already given to a tile and scrolled the header off the top.
		//
		// rankNotice, so it is the last thing dropped: it is the answer to the
		// key just pressed, which is more use in that moment than any hint
		// beside it, esc and q included. Both fit: the flash is cut to one line
		// and the bar has two.
		//
		// And first rather than last. A long flash fills its line, and the
		// ellipsis of a bar that dropped something is packed after the final
		// part, so with the flash at the end "…" took a line of its own, the
		// bar was three lines, and fitHintBar dropped how to leave to get
		// back to two. Ahead of the hints, the rest of the bar and its "…"
		// share the second line.
		mark, style := "✓", theme.GoodText
		switch m.flash {
		case m.flashBad:
			mark, style = "✗", theme.BadText
		case m.flashGuide:
			mark, style = "→", theme.Subtle
		}
		// Truncated as well as packed. Every other entry is two words and is
		// never split — "a half-rendered key is worse than a missing one" —
		// but a confirmation is a sentence, so the one that does not fit has
		// to give ground rather than run off the edge.
		//
		// And cleaned first, as every pane cleans the data it draws. A flash
		// is often somebody else's words: an error's message, kubectl's
		// first line of stderr, which can be an API server's own text, the
		// names a listing found. It was drawn as it came, so an override in
		// a server's error reordered the footer. Display only, so a
		// character that reorders text is spelled out rather than dropped.
		flash := textclean.Terminal(m.flash)
		// A refusal on a form is the one flash with a remedy at its end, the
		// connection editor's most of all (`rta plugin install pg installs it`),
		// and is wrapped like the form's own errors. Any other is a sentence
		// of confirmation, and is cut.
		refusal := m.flash == m.flashBad && formScreen(screen)
		if m.width > 0 && !refusal {
			flash = ansi.Truncate(flash, max(m.width-3, 8), "…")
		}
		items = append([]hintItem{{display: mark, label: flash, rank: rankNotice, style: &style, wrap: refusal}}, items...)
	}
	return fitBar(m.width, footerMaxLines, formScreen(screen), items...)
}

// formScreen is whether the screen is a form, whose bar speaks of the boxes and
// whose refusals are about what was typed in them.
func formScreen(screen mode) bool {
	switch screen {
	case modeForm, modeTheme, modePick:
		return true
	}
	return false
}

// refuse is a flash that says what the key just pressed did not do: a save
// that failed, a copy that found nothing, a row that cannot take the action.
//
// **It is drawn as a refusal, with the cross a form's error gets.** Every
// flash wore the green check mark of success, so "config not saved: permission
// denied" read as the confirmation of the save beside it. The mark follows the
// text and not a flag that outlives it: a later flash is a different string,
// and a key clears it, so a refusal can never colour what replaced it.
func (m *Model) refuse(msg string) {
	m.flash, m.flashBad = msg, msg
}

// guide is a flash that says what to do next and reports nothing done: "pick a
// row, then d done". It wore the green check of a success, which read as the
// confirmation of a press that had only opened a list; the same rule as refuse,
// that the mark follows the text.
func (m *Model) guide(msg string) {
	m.flash, m.flashGuide = msg, msg
}

// refused is refuse for the helpers that hand their line back to be assigned,
// `m.flash = m.trustSelected()`: the mark follows the text, and the text that
// comes back is the one that was refused.
func (m *Model) refused(msg string) string {
	m.refuse(msg)
	return msg
}

// formErrors is what the form on screen refuses to accept right now: huh's
// per-field validation messages, for the footer to show.
func (m Model) formErrors(screen mode) []error {
	switch screen {
	case modeForm:
		if m.form != nil && m.form.form != nil {
			return m.form.form.Errors()
		}
	case modeTheme:
		if m.themePick != nil && m.themePick.form != nil {
			return m.themePick.form.Errors()
		}
		if m.themeForm != nil && m.themeForm.form != nil {
			return m.themeForm.form.Errors()
		}
	case modePick:
		if m.pick != nil && m.pick.form != nil {
			return m.pick.form.Errors()
		}
	}
	return nil
}
