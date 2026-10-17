package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
)

// Asking first.
//
// A key that costs something to undo asks before it acts, in the shape a person
// already knows from every web page that has put a question over its content:
// a box over the screen, a title that is the question, a sentence saying what
// will happen, and two buttons named for what they do. The conventions are the
// platform's (the <dialog> element and the alertdialog role, the APG's modal
// dialog pattern) and are kept whole because they are what people expect of a
// modal and not because the terminal needs them:
//
//   - It is modal. While it is up every key goes to it, a wheel turn or a paste
//     does nothing underneath, and the screen behind is drawn dimmed. A stray
//     letter is ignored and does not dismiss it: a web dialog does not close
//     because a key was pressed, and the one that did would cancel an answer the
//     person had not given.
//   - esc cancels, enter takes the focused button, tab and the arrows move the
//     focus, and y and n answer without moving it, the keys the command line's
//     own prompt has taught. ctrl+c still quits, because the key that leaves the
//     program must not become a "no".
//   - The buttons are verbs, `Lock store` and `Cancel`, never OK and Yes.
//   - Focus starts on the answer that cannot lose anything the person cares
//     about: the action itself where it is the fail-safe direction, as locking
//     is, and Cancel where it is not.
//   - There is no "don't ask again". The questions are few, each is rare and
//     costs something, and a setting that turned the consent step off would be
//     writable by anything running as the user.
//
// The dialog holds data and not closures, the way the armed answer does
// (answers.go): which question it is, the words, the focus. What a yes does is
// the switch in answerDialog, where the next question joins it.

type dialogID int

const (
	dialogLockStore dialogID = iota + 1
)

const (
	focusConfirm = iota
	focusCancel
)

// dialogWidth is the widest the box is drawn: wide enough for a sentence to be
// read at a glance and narrow enough to leave the dimmed screen visible around it.
const dialogWidth = 62

// dialogGap is the space between the two buttons.
const dialogGap = 3

type dialog struct {
	id              dialogID
	title, body     string
	confirm, cancel string
	focus           int
}

// openDialog puts a question over the current screen. The dialog is replaced
// whole and never edited in place, so a copy of the model taken before it
// opened does not see it change.
func (m Model) openDialog(d dialog) Model {
	m.dialog = &d
	return m
}

// dialogKeys is every key press while a dialog is up. It always reports the key
// handled: nothing falls through to the screen underneath.
func (m Model) dialogKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	d := *m.dialog
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit, true
	case "enter", " ", "space":
		nm, cmd := m.answerDialog(d.focus == focusConfirm)
		return nm, cmd, true
	case "y", "Y":
		nm, cmd := m.answerDialog(true)
		return nm, cmd, true
	case "n", "N", "esc":
		nm, cmd := m.answerDialog(false)
		return nm, cmd, true
	case "tab", "right", "l", "shift+tab", "left", "h":
		d.focus = 1 - d.focus
		m.dialog = &d
	}
	return m, nil, true
}

// answerDialog closes the dialog and does what the answer says.
func (m Model) answerDialog(yes bool) (tea.Model, tea.Cmd) {
	d := *m.dialog
	m.dialog = nil
	switch d.id {
	case dialogLockStore:
		return m.lockStoreAnswer(yes)
	}
	return m, nil
}

// dialogItems is the bar while a dialog is up: its own vocabulary and nothing of
// the screen underneath, whose keys are not answered.
func (m Model) dialogItems() []hintItem {
	d := *m.dialog
	rest := []hintItem{
		{display: "n/esc", label: "cancel", rank: rankPrimary, keys: []string{"n", "N", "esc"}},
		{display: "tab", label: "switch", rank: rankExtra, keys: []string{"tab", "shift+tab", "left", "right", "h", "l"}},
		{display: "ctrl+c", label: "quit", rank: rankLeave, keys: []string{"ctrl+c"}},
	}
	// With the focus on Cancel, enter says no and y is the only way to the other
	// button without moving, so it is named then and only then; on the confirming
	// button enter and y are one answer and are named once.
	if d.focus == focusCancel {
		return append([]hintItem{
			{display: "enter", label: strings.ToLower(d.cancel), rank: rankNotice, keys: []string{"enter", " ", "space"}},
			{display: "y", label: strings.ToLower(d.confirm), rank: rankPrimary, keys: []string{"y", "Y"}},
		}, rest...)
	}
	return append([]hintItem{{
		display: "enter", label: strings.ToLower(d.confirm), rank: rankNotice,
		keys: []string{"enter", " ", "space", "y", "Y"},
	}}, rest...)
}

// dialogGeometry is where a dialog is drawn: the box, and the cells of its two
// buttons, in screen coordinates. Drawing and click-testing both ask it, so a
// click lands on the button the person is looking at.
type dialogGeometry struct {
	lines           []string
	x, y            int
	buttonRow       int
	confirm, cancel [2]int // [from, to) columns within the screen
	boxWidth        int
}

func (m Model) dialogGeometry() dialogGeometry {
	d := *m.dialog
	width := min(dialogWidth, max(m.width-4, 10))
	inner := width - 4

	styles := theme.Form(true)
	if m.noColor {
		styles = theme.FormMono(true)
	}
	button := func(label string, focused bool) string {
		style := styles.Focused.BlurredButton
		if focused {
			style = styles.Focused.FocusedButton
		}
		return style.Render(label)
	}
	confirm := button(textclean.Terminal(d.confirm), d.focus == focusConfirm)
	cancel := button(textclean.Terminal(d.cancel), d.focus == focusCancel)
	buttons := lipgloss.Width(confirm) + dialogGap + lipgloss.Width(cancel)
	pad := max((inner-buttons)/2, 0)
	buttonLine := strings.Repeat(" ", pad) + confirm + strings.Repeat(" ", dialogGap) + cancel

	body := strings.Split(ansi.Wrap(textclean.Terminal(d.body), inner, " "), "\n")
	// The buttons are the one thing a short terminal may not lose, so the body
	// gives way first: the question is already in the title.
	if room := m.height - 6; room > 0 && len(body) > room {
		body = append(body[:max(room-1, 0)], "…")
	}
	rows := append(append(body, ""), buttonLine)

	box := panel(panelHead{Title: textclean.Terminal(d.title), Right: "esc cancels"}, strings.Join(rows, "\n"), width, 0, true)
	lines := strings.Split(box, "\n")
	g := dialogGeometry{lines: lines, boxWidth: width}
	g.x = max((m.width-width)/2, 0)
	g.y = max((m.height-len(lines))/2, 0)
	g.buttonRow = g.y + 1 + len(body) + 1
	g.confirm = [2]int{g.x + 2 + pad, g.x + 2 + pad + lipgloss.Width(confirm)}
	g.cancel = [2]int{g.confirm[1] + dialogGap, g.confirm[1] + dialogGap + lipgloss.Width(cancel)}
	return g
}

// dialogClick answers a click while a dialog is up. A button focuses and takes
// the answer, as a click does on a web page; anywhere else does nothing, since
// a backdrop that dismissed the dialog would cancel it for a click aimed at the
// screen behind.
func (m Model) dialogClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	g := m.dialogGeometry()
	x, y := msg.X, msg.Y-m.banner
	if y != g.buttonRow {
		return m, nil
	}
	switch {
	case x >= g.confirm[0] && x < g.confirm[1]:
		return m.answerDialog(true)
	case x >= g.cancel[0] && x < g.cancel[1]:
		return m.answerDialog(false)
	}
	return m, nil
}

// overlayDialog draws the dialog over the screen that is behind it: the
// screen dimmed to one muted colour, as a backdrop is, and the box centred.
func (m Model) overlayDialog(base string) string {
	g := m.dialogGeometry()
	lines := strings.Split(base, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	dim := func(s string) string { return theme.Subtle.Render(s) }
	for i, line := range lines {
		plain := ansi.Strip(line)
		inBox := i >= g.y && i < g.y+len(g.lines)
		if !inBox {
			lines[i] = dim(plain)
			continue
		}
		left := ansi.Truncate(plain, g.x, "")
		left += strings.Repeat(" ", max(g.x-lipgloss.Width(left), 0))
		right := ansi.TruncateLeft(plain, g.x+g.boxWidth, "")
		lines[i] = dim(left) + g.lines[i-g.y] + dim(right)
	}
	return strings.Join(lines, "\n")
}

// lockStoreDialog is the question behind U while the store is unlocked.
//
// The default is the lock itself. Locking is the direction that narrows what
// is open, so a reflex enter lands on the safe state; what it costs is the
// passphrase again, and the sentence says so, along with what stops working.
func lockStoreDialog(left time.Duration) dialog {
	remaining := ""
	if left > 0 {
		remaining = " (" + profile.ShortDuration(left) + " left)"
	}
	return dialog{
		id:    dialogLockStore,
		title: "Lock the store?",
		body: "This forgets the passphrase held for this sitting" + remaining + ". Tiles that read a credential " +
			"from the store fail on their next refresh, and kv actions ask for the passphrase again. " +
			"Unlocking takes the passphrase.",
		confirm: "Lock store",
		cancel:  "Cancel",
		focus:   focusConfirm,
	}
}

// lockStoreAnswer is what a yes or a no does to the store.
func (m Model) lockStoreAnswer(yes bool) (tea.Model, tea.Cmd) {
	if !yes {
		m.flash = "store still unlocked"
		return m, nil
	}
	// The sitting can end while the question is open, and a lock that finds
	// nothing to lock says so rather than reporting something it did not do.
	if _, held := kv.SessionUntil(); !held {
		m.flash = "the store had already locked itself"
		return m, nil
	}
	kv.ForgetSession()
	m.flash = "store locked — U unlocks it again"
	// Hoisted so the model is read after the calls that change it, not in the
	// same expression as them: the order of those is the compiler's to choose.
	bind := tea.Batch(m.syncActive(), m.syncTiles())
	return m, bind
}
