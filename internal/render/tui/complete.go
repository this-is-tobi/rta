package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
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

// completionKeyMap keeps tab in the field: accept the suggestion, stay put.
//
// The form-wide keymap binds tab to accept *and* Next, which is right for a
// whole-value suggestion — the box is finished, moving on is the point. A
// coordinate is accepted a segment at a time, and a tab that hopped to the
// next field after each segment would make the one flow this field exists
// for cost a shift+tab per segment. Enter still advances, exactly as it does
// everywhere; this is tab meaning what it means in a shell, scoped to the
// two fields completed like one.
func completionKeyMap() *huh.KeyMap {
	km := formKeyMap()
	km.Input.Next = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))
	return km
}

// segmented marks one field as cluster-completed: tab stays in the field.
// Called after the form is built, because huh.NewForm applies the form-wide
// keymap to every field at construction and would clobber this.
func (cf *capForm) segmented(name string) {
	if in, ok := cf.inputs[name]; ok {
		in.WithKeyMap(completionKeyMap())
	}
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

// completeFromCluster handles tab in a form: fetch the focused segment's
// completions in the background when there is nothing left to accept, and
// otherwise hand the key to the form — the widget's own accept on a
// segmented field, the ordinary accept-and-advance everywhere else.
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
		return m.updateForm(msg)
	}
	cf := m.form
	partial := strings.TrimSpace(*cf.bindings[f.Name])
	if !needsFetch(partial, cf.suggested[f.Name]) {
		return m.updateForm(msg)
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
