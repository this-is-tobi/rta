package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
)

// A call parked for you, shown wherever you are.
//
// With `rta mcp serve --consent` an agent's call that needs a grant nobody
// issued waits for an answer, on a clock: ten minutes at most, and the agent
// is stopped for all of them. The only place the TUI said so was the agent
// tile's body, the sixth tile at 100x30 and under the fold at 80x24, and only
// while the dashboard was the screen: open any other view and a call that
// parked while you read it was not on screen at all. Pressing `w` before that
// tile was selected did nothing, because `w` was the tile's own key.
//
// So the queue is looked at on its own clock, whatever is on screen, and one
// line above every screen says what is waiting and the key that answers it,
// with the call itself when there is exactly one. It is one row taken from the
// screen below and only while something waits, which is the same trade the
// footer makes for a message: a screen that is a row shorter for a minute is
// cheaper than a consent request that ran out unseen.
//
// The count is read from the queue itself and not from the agent tile, which
// is a view of the same files drawn every five seconds and only while the
// dashboard is open. The read is the one `agent pending` makes, so a request
// whose display disagrees with its digest is not counted here either.

// waitingPoll is how often the queue is looked at. A parked call can wait up
// to ten minutes and the operator is usually in another window, so what matters
// is that a notification and the line agree within a breath of each other, and
// a directory listing every two seconds costs nothing.
const waitingPoll = 2 * time.Second

// bannerMinRows is the shortest terminal that gives a row to the line. Below it
// the screen has nothing to spare, and the footer's own notice still says
// something is waiting.
const bannerMinRows = 12

// queueCapability is the screen `w` opens.
const queueCapability = "agent.pending"

// waitingCall is one parked call as the line shows it.
type waitingCall struct {
	agent string
	// call is the capability and the records it names, as the queue spells them.
	call string
	// would is what the capability said it would do, first line only.
	would string
}

// waitingMsg is the queue as it was read.
type waitingMsg struct{ calls []waitingCall }

// readWaiting reads the queue off disk.
func readWaiting() []waitingCall {
	reqs, err := consent.Pending()
	if err != nil {
		return nil
	}
	out := make([]waitingCall, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, callOf(r))
	}
	return out
}

// waitingByID is the one parked call with that id, if it is still waiting.
func waitingByID(id string) (waitingCall, bool) {
	r, ok := consent.Find(id)
	if !ok {
		return waitingCall{}, false
	}
	return callOf(r), true
}

// callOf is a request as the screen says it. Everything in it came from an
// agent's call, so it is cleaned the way every other agent-written string is
// before it can reach the terminal.
func callOf(r consent.Request) waitingCall {
	call := textclean.Terminal(r.Cap)
	if records := textclean.Records(r.Scopes); records != "" {
		call += " " + records
	}
	would, _, _ := strings.Cut(strings.TrimSpace(r.Preview), "\n")
	return waitingCall{agent: textclean.Name(r.Agent), call: call, would: textclean.Terminal(would)}
}

// pollWaiting reads the queue after delay and says what it found.
func (m Model) pollWaiting(delay time.Duration) tea.Cmd {
	read := m.readWaiting
	if read == nil {
		read = readWaiting
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return waitingMsg{calls: read()} })
}

// bannerRows is how many rows the line takes from every screen right now.
func (m Model) bannerRows() int {
	if len(m.waiting) == 0 || m.height+m.banner < bannerMinRows {
		return 0
	}
	return 1
}

// settleBanner takes the line's row off the screen's height, or gives it back,
// and says nothing about what that does to the panes: the caller refits them
// when the count moved. The height moves by the difference rather than being
// recomputed from the terminal's, so a model whose size was set directly keeps
// the size it was given.
func (m Model) settleBanner() Model {
	want := m.bannerRows()
	m.height += m.banner - want
	m.banner = want
	return m
}

// bannerLine is the line itself: how many calls wait, the key that answers
// them where it is one, and the call when it is the only one.
func (m Model) bannerLine() string {
	n := len(m.waiting)
	line := theme.WarnText.Render("● " + format.CountOf(n, "call") + " waiting")
	switch {
	case m.onQueue():
	case m.answersWaiting():
		line += theme.Subtle.Render(" — ") + theme.Key.Render("w") + theme.Subtle.Render(" to answer")
	default:
		line += theme.Subtle.Render(" — "+leaveHint(m.mode)+", then ") + theme.Key.Render("w")
	}
	if n == 1 {
		line += theme.Subtle.Render(" · " + m.waiting[0].describe())
	}
	return ansi.Truncate(line, max(m.width, 1), "…")
}

// leaveHint is how to get off a screen that takes every letter as text.
func leaveHint(screen mode) string {
	if screen == modeRunning {
		return "esc to stop it"
	}
	return "esc"
}

// describe is the call in one phrase: who asks for what, and what it would do.
func (w waitingCall) describe() string {
	s := w.call
	if w.agent != "" {
		s = w.agent + " asks " + s
	}
	if w.would != "" {
		s += " — " + w.would
	}
	return s
}

// allowedLine is allowLine once it has been done.
func (w waitingCall) allowedLine() string {
	if w.agent != "" {
		return "allowed " + w.agent + "'s " + w.call + " once"
	}
	return "allowed " + w.call + " once"
}

// allowLine is what pressing a would do, said as the question it answers.
func (w waitingCall) allowLine() string {
	s := "allow " + w.call + " once"
	if w.agent != "" {
		s = "allow " + w.agent + "'s " + w.call + " once"
	}
	if w.would != "" {
		s += " — " + w.would
	}
	return s
}

// onQueue is whether the screen already is the waiting calls, or the page of
// one of them: the line has nothing to point at there.
func (m Model) onQueue() bool {
	return m.mode == modeResult && (m.current.ID == queueCapability || m.current.ID == "agent.show")
}

// answersWaiting is whether `w` opens the queue from the screen on show: a call
// waits, the screen's letters are commands and not text, and nothing on it has
// the key already. The forms, the pickers and the filter boxes take every
// letter as text, and a screen that is already the queue has nowhere to go.
func (m Model) answersWaiting() bool {
	if len(m.waiting) == 0 || m.help || m.onQueue() {
		return false
	}
	if _, ok := m.reg.Capability(queueCapability); !ok {
		return false
	}
	switch m.mode {
	case modeDashboard:
		if a, claimed := m.selectedAction("w"); claimed && a.cap.ID != queueCapability {
			return false
		}
		return !m.searchEditing
	case modeResult, modeConfirm:
		return !m.wTakenHere()
	case modePlugins, modeProfiles, modeProfilePlugins:
		return m.armedDelete == ""
	case modeBrowse:
		return m.list.FilterState() != list.Filtering
	}
	return false
}

// wTakenHere is whether the capability on show gives `w` a meaning of its own,
// which a plugin may: the key is the capability's to declare, and the queue
// does not take it from one. The same goes for a selected tile, above.
func (m Model) wTakenHere() bool {
	for _, a := range capActions(m.reg, m.current.ID) {
		if a.key == "w" {
			return true
		}
	}
	_, toggled := m.toggleFor("w")
	return toggled
}

// openQueue is `w`: the queue of parked calls, from wherever the key was
// pressed, and esc from it goes back to the dashboard.
func (m Model) openQueue() (tea.Model, tea.Cmd, bool) {
	c, ok := m.reg.Capability(queueCapability)
	if !ok {
		return m, nil, false
	}
	m.origin = modeDashboard
	m.trail = nil
	m.current = c
	m.lastValues, m.lastYes = nil, false
	m.row = 0
	m.refreshPending = false
	m.searchEditing, m.query, m.searchSel = false, "", 0
	return m, m.startRun(c, nil, false), true
}
