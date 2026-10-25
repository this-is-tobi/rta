package tui

import (
	"maps"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of the call a form sent, when it names one of that form's boxes
// (view.Error.Input), is answered on the form: opened again on what was typed,
// the cursor on the box, and the reason on the bar, as a login answers a wrong
// password. It was a page, whose `r` ran the same value again and whose `e`
// opened the form at its first box, and which of a dozen boxes to fix was for
// the reader to work out from a sentence. The unlock form answered a wrong
// passphrase this way first, keyed on two codes; this is that answer for every
// form, keyed on the input a refusal names.

// submission is the form a run was sent from, kept until the run answers.
type submission struct {
	// seq is the run the form started, as runSeq counts them: a destructive
	// capability's preview until enter on the confirmation starts the run
	// itself, whose seq it moves to (confirmKeys).
	seq int
	// form is the form as it was submitted, and values what it sent.
	form   *capForm
	values map[string]any
	// lastValues and lastYes are what the model held before the form sent its
	// run, and what a form opened again gives back: that run showed nothing,
	// and `r` on the result `e` was pressed on repeats what that result shows.
	lastValues map[string]any
	lastYes    bool
	// held is the result on screen when the form was submitted, the one `e`
	// opened it from, which a refusal the start of a run makes itself writes
	// over (startRun), and which esc on the form opened again goes back to.
	held resultMsg
}

// submitting is the record of the form on screen, about to send its run.
func (m Model) submitting() *submission {
	return &submission{form: m.form, values: m.form.values(), lastValues: m.lastValues, lastYes: m.lastYes,
		held: m.result}
}

// sent keeps sub for the run just started, and answers at once a refusal that
// starting it made itself: a connection's fence is held before anything runs,
// and its refusal is on screen with no message to wait for (startRun).
func (m Model) sent(sub *submission, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	sub.seq = m.runSeq
	m.submitted = sub
	if m.mode == modeResult && m.result.err != nil && m.result.seq == m.runSeq {
		m.submitted = nil
		if nm, reopened, ok := m.reopenOn(sub, m.result.cap, m.result.err); ok {
			return nm, reopened
		}
	}
	return m, cmd
}

// reopenOn opens sub's form again on the box verr names, and says false when
// verr names none of them: no input, one the capability does not declare, or
// one the form did not put on screen — a row's identity, a box the store's
// session answered, a stage the form is not on and cannot go back to. Nor
// when the refusal is Retryable: the value was fine and a second try may not
// be refused, which `r` on the page is for. Those keep the page they had.
//
// answered is the capability whose run answered with verr, which has to be the
// one the form sent: the seqs say which run is whose, and this says it again.
func (m Model) reopenOn(sub *submission, answered plugin.Capability, verr *view.Error) (Model, tea.Cmd, bool) {
	if sub == nil || sub.form == nil || verr == nil || verr.Retryable || answered.ID != sub.form.cap.ID {
		return m, nil, false
	}
	c := sub.form.cap
	f, ok := c.ErrorInput(verr)
	if !ok {
		return m, nil, false
	}
	var form *capForm
	switch {
	case sub.form.asks(f.Name):
		form = m.againFrom(sub)
	case sub.form.firstStage != nil && isKeyField(c, f.Name):
		form = m.stageOne(c, sub.form.firstStage, sub.form.fromResult)
		// The second stage's answers wait for it, laid over what the record
		// holds once the first is answered again (afterFormUpdate): the key
		// was refused, not what was typed under it.
		form.secondAnswers = withoutKeys(c, withoutSecrets(c, asked(c, sub.values)))
	}
	if form == nil {
		return m, nil, false
	}
	nm := m
	nm.submitted = nil
	nm.current, nm.form, nm.mode, nm.previewing = c, form, modeForm, false
	nm.result = sub.held
	nm.lastValues, nm.lastYes = sub.lastValues, sub.lastYes
	nm.fitForm()
	note, ok := nm.form.answerAgain(f.Name, sub.values)
	if !ok {
		return m, nil, false
	}
	nm.refuse(refusalLine(verr, note))
	return nm, nm.form.form.Init(), true
}

// answerAgain readies a form built again after a refusal for its answer: the
// cursor on the first of the boxes it has to be given again — the one the
// refusal names (refused), and every credential the call was sent with (sent),
// which the form never holds — and each such credential refusing to be left
// empty, so the call cannot go out without it unasked. note is what the bar
// says of those credentials beside the refusal, and ok is false when the cursor
// could not reach its box.
//
// The first of them and not the refused box, because the walk there does not
// validate what it passes, and a credential emptied before the refused box was
// a box nothing asked again: codec.jwt went out with no token, and was refused
// for it on a form that had emptied the key just typed. And refusing to be left
// empty, because an empty credential box is not always a refusal waiting to
// happen: keys.add without its passphrase writes the key unencrypted, and
// codec.jwt without its key decodes the token unverified.
func (cf *capForm) answerAgain(refused string, sent map[string]any) (note string, ok bool) {
	cf.cleared = nil
	var names []string
	for i, f := range cf.fields {
		if i >= len(cf.widgets) || !f.Type.Sensitive() || cf.hidden(f.Name) || !wasGiven(sent[f.Name]) {
			continue
		}
		if cf.cleared == nil {
			cf.cleared = map[string]bool{}
		}
		cf.cleared[f.Name] = true
		if f.Name != refused {
			names = append(names, plugin.SurfaceTUI.InputName(f.Name))
		}
	}
	target := refused
	for _, f := range cf.fields {
		if f.Name == refused || cf.cleared[f.Name] {
			target = f.Name
			break
		}
	}
	if !cf.focusField(target) {
		return "", false
	}
	switch len(names) {
	case 0:
		return "", true
	case 1:
		return names[0] + " was cleared: give it again", true
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] +
		" were cleared: give them again", true
}

// wasGiven reports whether v is a value a box was given: text that is not only
// white space, or a list with something in it.
func wasGiven(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) != ""
	case []string:
		return len(t) > 0
	case []any:
		return len(t) > 0
	}
	return v != nil
}

// withoutKeys is values without c's first-stage inputs (keyFields).
func withoutKeys(c plugin.Capability, values map[string]any) map[string]any {
	keys, _ := keyFields(c)
	out := maps.Clone(values)
	for _, k := range keys {
		delete(out, k.Name)
	}
	return out
}

// againFrom is the form sub sent, built again on what it was answered: the same
// boxes on the same base, each answer back in its box but a credential's, which
// is asked again for the reason startForm gives — typed for one call, it is not
// sent silently with a call edited to go somewhere else — and the screen it was:
// where esc goes back to, what the head says it is about.
//
// nil for a form that cannot be built again: one no run form was built as, or a
// second stage whose first stage answered with a secret, which it does not keep
// to send with the second.
func (m Model) againFrom(sub *submission) *capForm {
	was := sub.form
	if was.offered == nil {
		return nil
	}
	c := was.cap
	if was.firstStage != nil {
		keys, _ := keyFields(c)
		for _, k := range keys {
			if _, carried := was.base[k.Name]; carried && k.Type.Sensitive() {
				return nil
			}
		}
	}
	base := withStoreSession(c, withoutSecrets(c, over(was.base, unasked(c, sub.values))))
	form := m.runForm(c, was.offered, withoutSecrets(c, asked(c, sub.values)), base)
	form.inherit(was)
	kept := was.keptTouched()
	form.edited, form.stepEdited = was.edited || kept, was.stepEdited || kept
	return form
}

// refusedAtStageOne answers a first stage whose answer the record itself
// refused — the note there is no note 7 of, asked for to fetch what the second
// stage edits — on that stage, when the refusal names its box, and not on a
// page with the answer gone.
func (m Model) refusedAtStageOne(err error, answers map[string]any) (Model, tea.Cmd, bool) {
	c := m.current
	verr := view.AsError(err, c.ID+".prefill.failed")
	f, ok := c.ErrorInput(verr)
	if !ok || verr.Retryable || !isKeyField(c, f.Name) {
		return m, nil, false
	}
	back := &firstStage{carry: m.form.base, answers: answers, about: m.form.about, touched: m.form.touched()}
	nm := m
	nm.form = m.stageOne(c, back, m.form.fromResult)
	nm.fitForm()
	note, ok := nm.form.answerAgain(f.Name, answers)
	if !ok {
		return m, nil, false
	}
	nm.refuse(refusalLine(verr, note))
	return nm, nm.form.form.Init(), true
}

// refusalLine is a refusal as the bar of the form it is answered on says it:
// what was wrong, what to do about it, and the note of what the form came back
// without (answerAgain).
func refusalLine(verr *view.Error, note string) string {
	line := verr.Message
	if verr.Hint != "" {
		line += " — " + verr.Hint
	}
	if note != "" {
		line += " · " + note
	}
	return line
}

// isKeyField reports whether name is one of the inputs c's first stage asks:
// the record's identity, when c edits a record in two stages (keyFields).
func isKeyField(c plugin.Capability, name string) bool {
	keys, _ := keyFields(c)
	for _, k := range keys {
		if k.Name == name {
			return true
		}
	}
	return false
}

// asks reports whether name is a box this form puts on screen.
func (cf *capForm) asks(name string) bool {
	if cf.hidden(name) {
		return false
	}
	for i, f := range cf.fields {
		if f.Name == name {
			return i < len(cf.widgets)
		}
	}
	return false
}

// keptTouched says whether a box a form built again on its answers gives back
// (againFrom) differs from what it opened with — touched, without the boxes it
// does not give back: a credential is asked again, so one typed in is not an
// answer the new form holds, and esc on it has nothing of it to ask about. The
// marks a form carries from the one it replaced are its caller's to add.
func (cf *capForm) keptTouched() bool {
	kept := func(in map[string]string) map[string]string {
		out := make(map[string]string, len(in))
		for name, text := range in {
			out[name] = text
		}
		for _, f := range cf.fields {
			if f.Type.Sensitive() {
				delete(out, f.Name)
			}
		}
		return out
	}
	return !maps.Equal(kept(cf.state()), kept(cf.opened))
}
