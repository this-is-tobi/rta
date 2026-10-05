package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Answering: what the keys do where you decide something about an agent.
//
// Three questions an operator is asked, on two screens. Is this parked call
// allowed to run; is this grant taken back; are all of them. Each used to open
// a form, because the declarations ask for one — agent.allow stops for its
// ttl, role and passphrase, grant.revoke for nine boxes — and a form for a
// question with a one-word answer is a form somebody walks through on enter
// without reading: the allow form never said which call it was about.
//
// So the answer is one key and one line, and the form is a second key away for
// the person who wants the other inputs.
//
//   - `a` on a parked call says what it would allow, in one line in the footer,
//     and `enter` allows exactly that call, once. Granting access stops for
//     a confirmation, which is the direction that cannot be taken back once a
//     secret has been read; what changed is that the confirmation shows the
//     call and its preview where the form never did. `A` opens the form, for a
//     standing grant (ttl) or a whole role.
//   - `x` on a grant row revokes that grant, at once. Deny is one key for the
//     reason this is: taking access away is the fail-safe direction, and a
//     revoke the operator did not mean is one `grant.allow` away. It runs only
//     when the row names exactly one grant, which is the roster's own `exact`
//     switch; a row whose cells could not be read back opens the form, because
//     without it a revoke would widen to every grant of the same target.
//   - `X` on the roster revokes every grant, behind the capability's own dry
//     run, which says how many and which: that is the confirmation, and it is
//     the one every destructive run already gets.
//
// All of it is keyed by the capability the key leads to, in the shell, the way
// cellReader and rowNamesOne key what a roster's cells mean: the declarations
// say which keys exist and where they lead, and this is the shell's own
// reading of the three it answers differently. A declaration that could say
// "run this one on its row's identity after a line" would be the better home
// for it; until one can, the table is here and the declared keys are left as
// they are.

const (
	allowCapability  = "agent.allow"
	revokeCapability = "grant.revoke"
	rosterCapability = "grant.list"
	lockCapability   = "lock.add"
)

// armedAnswer is a one-key answer that has said what it would do and waits for
// the key that confirms it. It holds the record the key was pressed on, and not
// the row: the queue refreshes under the cursor, and the call that gets
// answered has to be the one the line named.
type armedAnswer struct {
	act  capAction
	base map[string]any
	line string
	// done is what the footer says once it has run, in the past tense: the
	// capability's own "agent.allow done" does not say which call.
	done string
}

// answer is the shell's reading of a key that leads to one of the answers
// above: it reports whether it took the key, and the caller carries on with
// the declared behaviour — the form — when it did not.
func (m Model) answer(a capAction, base map[string]any) (tea.Model, tea.Cmd, bool) {
	// The form is what A asks for, and the form is what an answer aimed at a
	// remote server is: that one is signed, and its passphrase is asked.
	if a.form || len(m.aimedElsewhere(a.cap)) > 0 {
		return m, nil, false
	}
	switch a.cap.ID {
	case allowCapability:
		id, _ := base["id"].(string)
		call, ok := waitingByID(id)
		if !ok {
			m.refuse("that call is not waiting any more — it was answered, or it ran out")
			return m, nil, true
		}
		m.armed = &armedAnswer{act: a, base: base, line: call.allowLine(), done: call.allowedLine()}
		return m, nil, true
	case revokeCapability:
		if a.from == rosterCapability && a.src == srcRow && base["exact"] == true {
			a.bare = true
			nm, cmd := m.runSeeded(a, base)
			return nm, cmd, true
		}
	}
	return m, nil, false
}

// armedKeys is the confirming key's turn. enter answers, A opens the form for
// the same call, ctrl+c quits, and every other key disarms and is spent doing
// so: a cancel that also moved the cursor would make the safe answer do
// something, the reason the profile panes' delete gate works the same way.
func (m Model) armedKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	armed := m.armed
	m.armed = nil
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "enter":
		armed.act.bare = true
		m.said = armed.done
		nm, cmd := m.runSeeded(armed.act, armed.base)
		return nm, cmd, true
	case "A":
		armed.act.bare = false
		nm, cmd := m.runSeeded(armed.act, armed.base)
		return nm, cmd, true
	}
	m.flash = "nothing allowed — the call is still waiting"
	return m, nil, true
}

// armedItems is the footer while an answer waits for its key. The line is the
// whole of what it says, cut to fit, because a confirmation that does not
// name the call is the form again.
func (m Model) armedItems() []hintItem {
	label := m.armed.line
	if m.width > 0 {
		label = ansi.Truncate(label, max(m.width-8, 8), "…")
	}
	return []hintItem{
		{display: "enter", label: label, rank: rankAction, keys: []string{"enter"}},
		{display: "A", label: "for a while, or a role", rank: rankAction, keys: []string{"A"}},
		labelled(bindBack, "cancel"),
	}
}

// answerKeys are the two capitals that answer on the screens that have the
// lower-case key: A for the form of the allow `a` shortcut, X for every grant.
func (m Model) answerKeys(key string, tbl view.Table) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "A":
		for _, a := range capActions(m.reg, m.current.ID) {
			if a.cap.ID == allowCapability && a.key == "a" {
				a.form = true
				nm, cmd := m.runAction(a, tbl)
				return nm, cmd, true
			}
		}
	case "X":
		if m.current.ID == rosterCapability {
			nm, cmd := m.revokeAll()
			return nm, cmd, true
		}
	}
	return m, nil, false
}

// answerHints are the capitals above, for the footer of a screen that answers
// to them: beside the key they extend, so the pair reads as one idea.
func (m Model) answerHints(a capAction) []hintItem {
	if a.cap.ID == allowCapability && a.key == "a" {
		return []hintItem{action("A", "allow longer")}
	}
	return nil
}

// revokeAll takes back every grant on this machine, after the dry run that
// says how many: the destructive gate's own screen, reached for a capability
// that is a write because a roster emptied is as hard to put back.
func (m Model) revokeAll() (tea.Model, tea.Cmd) {
	c, ok := m.reg.Capability(revokeCapability)
	if !ok {
		return m, nil
	}
	if aim := m.aimedElsewhere(m.current); len(aim) > 0 {
		m.refuse("X revokes this machine's grants, and this roster is another server's")
		return m, nil
	}
	c.Inputs = hereOnly(c.Inputs)
	m.refreshPending = c.Flash
	m.subjectGone = false
	return m, m.startConfirm(c, map[string]any{"all": true})
}

// aboutCall is what the form for c says it is about when that is one parked
// call: the allow form is reached from a queue that may hold several, and
// its title used to be the capability's summary whichever call it was for.
func aboutCall(c plugin.Capability, base map[string]any) string {
	if c.ID != allowCapability {
		return ""
	}
	id, _ := base["id"].(string)
	if call, ok := waitingByID(id); ok {
		return call.describe()
	}
	return ""
}

// guardIdle is the inputs of c a form should ask: the same, less the guard's
// passphrase box when the guard is off and so nothing would read it. The box
// is on agent.allow for the one case that needs it — a standing grant under a
// guard — and was drawn for everybody, under a label that named a guard most
// machines do not have.
func guardIdle(c plugin.Capability) []plugin.Field {
	if c.ID != allowCapability || guard.Enabled() {
		return c.Inputs
	}
	out := make([]plugin.Field, 0, len(c.Inputs))
	for _, f := range c.Inputs {
		if f.Name == guard.PassphraseField.Name && f.Help == guard.PassphraseField.Help {
			continue
		}
		out = append(out, f)
	}
	return out
}

// suggested is what a form for c opens with when nothing it was pressed on
// supplies it: the one thing a person under pressure should not retype. The
// lock form's name is the agent that is connected — only when that is one
// agent, because a lock on the wrong name, in an incident, looks exactly like
// one on the right one.
func (m Model) suggested(c plugin.Capability) map[string]any {
	if c.ID != lockCapability {
		return nil
	}
	if name, ok := connectedAgent(); ok {
		return map[string]any{"name": name}
	}
	return nil
}

// connectedAgent is the name every open server runs as, when they all run as
// one.
func connectedAgent() (string, bool) {
	open, err := session.List()
	if err != nil {
		return "", false
	}
	name := ""
	for _, s := range open {
		switch {
		case s.Agent == "":
			continue
		case name == "":
			name = s.Agent
		case name != s.Agent:
			return "", false
		}
	}
	return name, name != ""
}

// openAction opens the capability an action on a tile leads to, with whatever
// the shell can suggest for it.
func (m Model) openAction(c plugin.Capability) (tea.Model, tea.Cmd) {
	if sug := m.suggested(c); sug != nil && hasInputs(c) {
		m.current = c
		m.trail = nil
		m.row = 0
		m.refreshPending = false
		return m.startForm(c, sug)
	}
	return m.open(c)
}

// rowActionOnATile is what a key that acts on a row does on a tile, which has
// none: it opens the page whose rows they are, and says what to do there.
// Opening the form instead meant retyping, from a glance at a tile, what the
// row would have supplied.
//
// The page is the one the tile drew, a list with a row to stand on, and not the
// detail page a capability opens on by default: that one is several tables
// and a few paragraphs, and no row of it can be picked.
func (m Model) rowActionOnATile(a capAction) (tea.Model, tea.Cmd) {
	t := m.tiles[m.selected]
	values := t.runValues()
	if t.cap.Detailed {
		values = over(values, map[string]any{"detail": false})
	}
	m.current = t.cap
	m.lastValues, m.lastYes = values, false
	m.trail = nil
	m.row = 0
	m.flash = fmt.Sprintf("pick a row, then %s %s", a.key, a.label)
	return m, m.startRun(t.cap, values, false)
}
