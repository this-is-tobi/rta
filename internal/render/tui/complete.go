package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/textclean"
	"github.com/this-is-tobi/rule-them-all/internal/tunnel"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Completing a coordinate or a Secret reference from the cluster, on tab.
//
// Tab is this app's one completion key — "tab completes paths", "press tab",
// "tab completes" — and these two fields keep it that way: with a suggestion
// on offer tab accepts it, and with nothing left to accept it fetches the
// segment under the cursor, which is what tab has meant in a shell for
// decades (kubectl's own completion runs `kubectl get` on it). The state
// decides, never a second key.
//
// The boundary §8 draws still holds. Every other suggestion in this package
// is computed locally and may refresh per keystroke; these fields complete
// from an apiserver, and a read of somebody's cluster must be something the
// operator did, not something typing caused. Tab is not typing — and on
// these fields it cannot be navigation either, because it never leaves them
// (enter advances) — so a tab here is always a completion press.
//
// A field a plugin marks Live (Field.Live) is the third completer on the
// same key and the same rules: the read runs in the plugin's own pinned
// process, with what the run would get (plugin.LiveRequest), only when tab
// finds nothing left to accept — see completeFromService.

// completeTimeout bounds one fetch. Tighter than bindTimeout: this is a
// convenience racing an operator's patience, not a call they asked to run —
// a cluster that cannot list in three seconds is one they will out-type.
// A var only for the tests, which raise it: three seconds of wall clock
// inside a full -race -shuffle run measures the machine's load as much as
// the code, and this package has already recorded that lesson once.
var completeTimeout = 3 * time.Second

// completeMsg is one fetch's answer, delivered back to the event loop.
type completeMsg struct {
	// form is the capForm the fetch was made for, compared by identity: an
	// answer for a form that has since closed is dropped, not applied to
	// whichever same-named field a later form happens to hold.
	form  *capForm
	field string
	c     tunnel.Completion
	err   *view.Error
}

// completionTarget is the field a tab would cluster-complete right now: the
// coordinate field of the connection editor, or the Secret-reference field of
// the credential editor — and only while it is the one focused. A fetch for a
// field the cursor is not in would answer a question nobody is looking at.
func (m Model) completionTarget() (field, coord string, ok bool) {
	cf := m.form
	if cf == nil {
		return "", "", false
	}
	switch {
	case cf.connEditing:
		field = profileKubeField
	case cf.credentialEditing:
		field, coord = credKubeField, cf.kubeCoord
	default:
		return "", "", false
	}
	in, offered := cf.inputs[field]
	if !offered || cf.form.GetFocusedField() != huh.Field(in) {
		return "", "", false
	}
	return field, coord, true
}

// needsFetch decides which of tab's two jobs this press is, on one question:
// does anything on offer strictly extend the box? If so the tab is the
// accept, and the widget handles it. If not there is nothing to take — the
// box is empty, or exactly equals what was accepted a keypress ago, or has
// been typed past the last answer — and the tab fetches for the segment the
// cursor is in.
//
// No separator special-case, and that is a lesson, not an omission: a first
// draft treated a trailing `/` as "empty segment, fetch", which also
// described the box the moment a fetch *landed* — so the tab meant to accept
// the ghost refetched instead, forever. The extends-or-fetch question alone
// carries the whole rhythm, because an accept leaves the box equal to the
// offer and equal is not an extension.
//
// The one box that must fetch regardless is the empty one — every offer
// "extends" it, but bubbles matches suggestions only against non-empty text,
// so the widget's accept would be a dead key there.
func needsFetch(value string, offered []string) bool {
	if value == "" {
		return true
	}
	low := strings.ToLower(value)
	for _, o := range offered {
		if lo := strings.ToLower(o); strings.HasPrefix(lo, low) && len(lo) > len(low) {
			return false
		}
	}
	return true
}

// settled reports that tab has already fetched this field for exactly this
// value and nothing came back that extends it. It is the difference between
// "there is more to complete" and "this is as far as completion goes", which
// nothing else in the form can tell apart — an empty answer and an answer
// that was never asked for look identical from the widget.
func (cf *capForm) settled(field, value string) bool {
	last, asked := cf.fetchedFor[field]
	return asked && last == value
}

// **One sentence has to be true of tab on every field: it completes when
// there is something to complete, and moves on when there is not.**
//
// It was true of every field except the completing ones, and the exception
// is what somebody reports as "tab goes to the next field here and completes
// there". A completing field bound tab to accept-or-fetch and Next to enter
// alone, so at the end of a coordinate — nothing deeper to offer — every
// press refetched the same segment and stayed, and the key that leaves the
// field was a different one than on the field above it.
//
// Segment-at-a-time still needs tab to stay while there is a segment to take;
// that part was right and is unchanged. What is added is the third answer:
// once a fetch has been made for exactly this text and brought back nothing
// that extends it, there is no completion left to offer, and tab means what
// it means everywhere else. huh.NextField is that, without synthesizing a
// keypress the field would have to interpret.
func (m Model) advanceForm() (tea.Model, tea.Cmd) { return m, huh.NextField }

// completeFromCluster handles tab in a form: fetch the focused segment's
// completions in the background when there is nothing left to accept, move
// on when a fetch already found nothing, and otherwise hand the key to the
// form — the widget's own accept on a segmented field, the ordinary
// accept-and-advance everywhere else.
func (m Model) completeFromCluster(msg tea.Msg) (tea.Model, tea.Cmd) {
	field, coord, ok := m.completionTarget()
	if !ok {
		return m.completeFromService(msg)
	}
	cf := m.form
	partial := strings.TrimSpace(*cf.bindings[field])
	if !needsFetch(partial, cf.suggested[field]) {
		return m.updateForm(msg)
	}
	if cf.settled(field, partial) {
		return m.advanceForm()
	}
	// Recorded before the fetch rather than after it, so a cluster that
	// cannot answer costs one press and not every press: the error is
	// flashed, and the next tab leaves the field instead of asking again.
	cf.fetchedFor[field] = partial
	m.flash = "completing…"
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
		defer cancel()
		var (
			c    tunnel.Completion
			verr *view.Error
		)
		if coord == "" {
			c, verr = tunnel.CompleteKube(ctx, partial)
		} else {
			c, verr = tunnel.CompleteSecretRef(ctx, coord, partial)
		}
		return completeMsg{form: cf, field: field, c: c, err: verr}
	}
}

// liveTarget is the field a tab would service-complete right now: focused,
// and marked Live by the plugin that declared it. The editors' synthetic
// capabilities declare no Live input, so this is naturally a question about
// run forms and prefill stages.
func (m Model) liveTarget() (plugin.Field, bool) {
	cf := m.form
	if cf == nil {
		return plugin.Field{}, false
	}
	for _, f := range cf.fields {
		if !f.Live || f.Suggest == nil {
			continue
		}
		if in, ok := cf.inputs[f.Name]; ok && cf.form.GetFocusedField() == huh.Field(in) {
			return f, true
		}
	}
	return plugin.Field{}, false
}

// completeFromService is tab on a field whose plugin marked its Suggest Live:
// resolve what the run would see — on the event loop, exactly as startRun
// does — and ask the plugin off it, bounded by completeTimeout.
//
// The environment resolves before the fetch because the picker's answer
// decides which credentials the listing runs with, and resolveProfile is an
// event-loop call (its bind cache is the loop's to touch). What is never
// done here is a dial: a connection naming a cluster is refused with the
// reason, because a forward opens per call and a completion is
// not a call — the plugin would list against a port nothing listens on.
func (m Model) completeFromService(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, ok := m.liveTarget()
	if !ok {
		return m.completeLocally(msg)
	}
	cf := m.form
	partial := strings.TrimSpace(*cf.bindings[f.Name])
	if !needsFetch(partial, cf.suggested[f.Name]) {
		return m.updateForm(msg)
	}
	if cf.settled(f.Name, partial) {
		return m.advanceForm()
	}
	c := cf.cap
	// Resolved from the full values and stripped only for the request, the
	// same split startRun makes: the picker's answer is exactly what decides
	// which environment's credentials the listing runs with, and a first cut
	// stripped it before resolving — so tab completed against the switched-on
	// environment while the picker above the field named another.
	all := cf.values()
	values := withoutPicker(c, all)
	// The coordinate is checked before anything is fetched, which is why this
	// is pickedConn + fillConn rather than resolveProfile: a tab that is
	// about to be refused must not read a credential first. Resolving first
	// meant a `kube:`-referenced Secret was fetched — a real access in the
	// cluster's audit log, caused by a keypress that then did nothing — and
	// the fetch stalled the update loop for it.
	name, conn, filled, verr := m.pickedConn(c, all)
	if verr != nil {
		m.flash = verr.Message
		return m, nil
	}
	if conn.Tunnelled() {
		what := "coordinate"
		if conn.TunnelKey() == "ssh" {
			what = "ssh target"
		}
		m.flash = "completion cannot reach " + name + "'s " + what + " — the forward opens per call"
		return m, nil
	}
	if filled == nil && name != "" {
		if filled, verr = m.fillConn(name, conn, c, values); verr != nil {
			m.flash = verr.Message
			return m, nil
		}
	}
	resolved := plugin.Resolve(c, plugin.Inputs{
		Caller: values, Profile: filled, ProfileName: name, Config: m.configFor(c),
	})
	// The box's own text is the partial, whatever any layer had in mind for
	// this field: a listing narrows on what is typed, not on a default.
	resolved[f.Name] = partial
	req := plugin.LiveRequest(resolved)
	suggest := f.Suggest
	what := f.Name + " completions"
	// Same as the cluster path: one fetch per value, so a service that
	// answers nothing leaves tab free to move on rather than asking again.
	cf.fetchedFor[f.Name] = partial
	m.flash = "completing…"
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
		defer cancel()
		// Cleaned like every other Suggest answer (candidateValues): these
		// strings come from a service and go onto a terminal.
		raw := suggest(ctx, req)
		items := make([]string, 0, len(raw))
		for _, entry := range raw {
			items = append(items, textclean.Terminal(plugin.CandidateValue(entry)))
		}
		return completeMsg{form: cf, field: f.Name,
			c: tunnel.Completion{Items: items, Names: items, What: what}}
	}
}

// completeLocally is tab on every other field, and it is where the sentence
// above is finally true of all of them: accept what is on offer, say what is
// on offer when the box is too empty for the widget to show it, and otherwise
// move on.
//
// Nothing here reaches a network. The lists are a plugin's own Suggest, a
// directory listing, or what this machine ran last — computed already, for
// the ghost the widget draws — so the only question is which of tab's
// meanings this press is, and needsFetch is the same question the two
// fetching paths ask.
//
// The middle case is the one worth naming. bubbles matches suggestions only
// against non-empty text (textinput.updateSuggestions), so an untouched box
// has no ghost and the widget has nothing to accept — which is exactly the
// box somebody presses tab in first, and on a plugin picker whose help says
// "press tab" the answer cannot be silence. The names themselves are the
// answer, the same one the cluster fetch gives an empty coordinate; the press
// after it moves on, because a key that only ever listed would be the dead
// key this whole rule exists to remove.
func (m Model) completeLocally(msg tea.Msg) (tea.Model, tea.Cmd) {
	cf := m.form
	name, ok := m.focusedInput()
	if !ok {
		// A picker, a confirm, a multi-select: nothing to complete, and huh's
		// own tab already advances them.
		return m.updateForm(msg)
	}
	typed := strings.TrimSpace(*cf.bindings[name])
	var offered []string
	if offer := cf.offers[name]; offer != nil {
		offered = offer()
	}
	switch tabOn(typed, offered, cf.settled(name, typed)) {
	case tabAccepts:
		// The widget takes the ghost and the cursor stays, which is what lets
		// a path be walked a segment at a time.
		return m.updateForm(msg)
	case tabLists:
		cf.fetchedFor[name] = typed
		m.flash = listing(offered)
		return m, nil
	}
	return m.advanceForm()
}

// tabMeaning is which of tab's three jobs one press is.
type tabMeaning int

const (
	// tabAccepts: something on offer extends the box, so the ghost is on
	// screen and the widget takes it — in place.
	tabAccepts tabMeaning = iota
	// tabLists: nothing to accept *yet*, because the box is empty and bubbles
	// matches only against non-empty text. The names are the answer.
	tabLists
	// tabAdvances: nothing left to complete. Tab is navigation again.
	tabAdvances
)

// tabOn is the whole rule, on the state of one box: what it holds, what it
// can be completed to, and whether its list has already been said out loud
// for exactly this text.
//
// A free function on three values because two forms in this package have to
// answer tab identically — the capability form and the theme editor — and a
// rule expressed twice is a rule that drifts. That is not hypothetical: they
// were sharing a keymap and not a decision, so the day tab's meaning moved
// out of huh, the theme editor's every box went dead and the capability
// form's did not.
func tabOn(typed string, offered []string, listed bool) tabMeaning {
	if !needsFetch(typed, offered) {
		return tabAccepts
	}
	if typed == "" && len(offered) > 0 && !listed {
		return tabLists
	}
	return tabAdvances
}

// focusedInput names the free-text field the cursor is in, or reports that it
// is not in one. huh answers with a widget and this package keys everything by
// the plugin field's name, so the identity comparison is the join.
func (m Model) focusedInput() (string, bool) {
	cf := m.form
	if cf == nil || cf.form == nil {
		return "", false
	}
	focused := cf.form.GetFocusedField()
	for name, in := range cf.inputs {
		if focused == huh.Field(in) && cf.bindings[name] != nil {
			return name, true
		}
	}
	return "", false
}

// listedAtMost bounds what an empty box's tab says out loud. A path field
// offers a whole directory (up to maxPathSuggestions) and a flash is one
// line: past a handful the list stops being an answer and becomes a wall,
// and the count is the more useful half of it anyway.
const listedAtMost = 8

// listing is that line: the names, and how many were not named.
func listing(offered []string) string {
	if len(offered) <= listedAtMost {
		return strings.Join(offered, ", ")
	}
	return fmt.Sprintf("%s … and %d more", strings.Join(offered[:listedAtMost], ", "),
		len(offered)-listedAtMost)
}

// applyCompletion lands one fetch's answer: suggestions onto the widget, and
// a flash that says what came back.
//
// Failure is one line and the field keeps taking typing — completion is an
// assist, never a gate, and the operator whose RBAC allows the forward but
// not the listing is a configuration this app expects, not one it punishes.
func (m Model) applyCompletion(msg completeMsg) (tea.Model, tea.Cmd) {
	if m.form == nil || m.form != msg.form {
		return m, nil
	}
	if msg.err != nil {
		m.flash = msg.err.Message
		return m, nil
	}
	if len(msg.c.Items) == 0 {
		m.flash = "no " + msg.c.What
		return m, nil
	}
	if in, ok := m.form.inputs[msg.field]; ok {
		in.Suggestions(msg.c.Items)
	}
	// Remembered so the next tab can tell accept from fetch (needsFetch): the
	// widget cannot be asked what it is offering, but this is what it was told.
	m.form.suggested[msg.field] = msg.c.Items
	// A live field's keystroke channel re-evaluates its suggestions as the
	// form changes and would clobber this landing off the widget — so the
	// fetch also lands in the locked store that channel merges from
	// (capForm.liveGot).
	for _, f := range m.form.fields {
		if f.Name == msg.field && f.Live {
			m.form.setLiveItems(msg.field, msg.c.Items)
		}
	}
	// Two sentences for two states. bubbles matches suggestions only against
	// a non-empty value, so on an empty box there is no ghost yet and the
	// names themselves are the answer; once something is typed the ghost is
	// on screen and what is worth saying is how to drive it.
	if strings.TrimSpace(*m.form.bindings[msg.field]) == "" {
		m.flash = msg.c.What + ": " + strings.Join(msg.c.Names, ", ")
	} else {
		m.flash = fmt.Sprintf("%d %s — ↓ cycles, tab completes", len(msg.c.Items), msg.c.What)
	}
	return m, nil
}
