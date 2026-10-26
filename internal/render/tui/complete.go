package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/boxlist"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/internal/tunnel"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
// That boundary still holds. Every other suggestion in this package
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
	// offer says whose the items are, which the line under the form then says.
	offer offerKind
}

// offerKind is whose entries a completion offers. The profile's own are not the
// service's answer, and why the press fell back to them is two statements: that the
// service said nothing, and that it said names the connection does not reach.
type offerKind int

const (
	// offerService: the items are what the service answered, held to the fence.
	offerService offerKind = iota
	// offerNoAnswer: the service listed none, or could not — a Suggest has no way
	// to say which.
	offerNoAnswer
	// offerOutside: the service listed names and the connection reaches none of
	// them. It did answer, and the line must not say it did not.
	offerOutside
)

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
	case cf.adHocFrom != nil:
		return m.adHocCompletionTarget()
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
	typed := strings.TrimSpace(*cf.bindings[field])
	// The box of an ad hoc credential holds the whole reference, scheme
	// included, and only what follows the scheme is a `<secret>/<key>` to
	// complete — so the offers are put back behind it, and what the box holds
	// is still what they extend.
	lead := ""
	if cf.adHocFrom != nil && field != adHocKubeField {
		lead = adHocClusterScheme
	}
	partial := strings.TrimPrefix(typed, lead)
	if !needsFetch(typed, cf.suggested[field]) {
		return m.updateForm(msg)
	}
	if cf.settled(field, typed) {
		return m.advanceForm()
	}
	// Recorded before the fetch rather than after it, so a cluster that
	// cannot answer costs one press and not every press: the error is
	// flashed, and the next tab leaves the field instead of asking again.
	cf.fetchedFor[field] = typed
	m.guide("completing…")
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
		if lead != "" {
			for i, item := range c.Items {
				c.Items[i] = lead + item
			}
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

// liveBox is what a tab on a Live box reads out of it.
type liveBox struct {
	// typed is the box as tab's accept-or-fetch-or-advance decision and the
	// record of what was fetched for (capForm.settled) see it.
	typed string
	// partial is the text the request carries under the field's own name.
	partial string
	// onOffer is what the last answer still has to offer, in the shape the
	// widget draws it against typed, so needsFetch asks it the one question
	// every completing box asks.
	onOffer []string
	// listed is the elements of a list box ahead of the one being typed, which
	// an answer leaves out.
	listed []string
}

// liveBox reads the box of a Live field the way its type holds a value.
//
// A String is one value: the box, trimmed, is the partial, and what the last
// fetch brought back is what is on offer. A StringSlice is a list in one box,
// and what a press completes is the element being typed — the text after the
// last comma, as the box reads it (boxlist.Last) — so the partial is that,
// what is on offer is rewritten as the whole boxes the widget draws
// (boxlist.Extend, which leaves out the elements already listed, the rule the
// keystroke channel's ghosts already follow), and the box is kept as typed,
// since the spacing after a comma is what those whole boxes begin with.
func (cf *capForm) liveBox(f plugin.Field) liveBox {
	raw := *cf.bindings[f.Name]
	if f.Type != plugin.StringSlice {
		typed := strings.TrimSpace(raw)
		return liveBox{typed: typed, partial: typed, onOffer: cf.suggested[f.Name]}
	}
	_, partial, _ := boxlist.Last(raw)
	return liveBox{
		typed: raw, partial: partial, listed: boxlist.Listed(raw),
		onOffer: boxlist.Extend(raw, cf.suggested[f.Name]),
	}
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
//
// A list box is asked about its last element and nothing else (liveBox): the
// plugin is handed the one name being typed, and the elements the box lists
// already are taken out of its answer here, so a Suggest neither tracks what
// was chosen nor can offer it twice.
func (m Model) completeFromService(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, ok := m.liveTarget()
	if !ok {
		return m.completeLocally(msg)
	}
	cf := m.form
	box := cf.liveBox(f)
	if !needsFetch(box.typed, box.onOffer) {
		return m.updateForm(msg)
	}
	if cf.settled(f.Name, box.typed) {
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
	// is pickedConn, with the credential fetched by the listing below: a tab
	// that is about to be refused must not read a credential first. Resolving
	// first meant a `kube:`-referenced Secret was fetched — a real access in
	// the cluster's audit log, caused by a keypress that then did nothing.
	name, conn, filled, verr := m.pickedConn(c, all)
	if verr != nil {
		m.refuse(verr.Message)
		return m, nil
	}
	if conn.Tunnelled() {
		what := "coordinate"
		if conn.TunnelKey() == "ssh" {
			what = "ssh target"
		}
		m.refuse("completion cannot reach " + name + "'s " + what + " — the forward opens per call")
		return m, nil
	}
	if words := m.heldByFence(name, conn, c, f, values, box.partial); words != "" {
		// A press with nothing to offer settles like any other (capForm.settled):
		// the next tab leaves the box, which is what the line says. Without it
		// a live box above a fenced one still blank asked again on every tab, and
		// only enter moved on.
		cf.fetchedFor[f.Name] = box.typed
		m.refuse(words + " — tab moves on")
		return m, nil
	}
	cfg := m.configFor(c)
	// The connection's fence resolves too, as it does for a run: a sibling a
	// fence of one entry fills, or a list it fills whole, is what the call will
	// carry, and a listing that depends on it (the clusters of the namespaces)
	// is otherwise asked about nothing.
	//
	// And it is the one fence of this press. The answer below is held to it as
	// well, and not to the copy the form took when it was built (capForm.fence):
	// this is the connection whose credentials the listing runs with and whose
	// allowlist the run will be gated by, so a config edited under an open form
	// moves the request and the answer together, never one without the other.
	fence := profile.FenceOf(conn)
	what := f.Name + " completions"
	// Same as the cluster path: one fetch per value, so a service that
	// answers nothing leaves tab free to move on rather than asking again.
	cf.fetchedFor[f.Name] = box.typed
	m.guide("completing…")
	return m, func() tea.Msg {
		// The credential is read here, with the listing, and not before the
		// press returned, for runCmd's reason: a `kube:` Secret is a cluster
		// round trip and a store held by a passphrase a second of scrypt, and
		// on the update loop the form froze for it with no key answered. Its
		// refusal is the press's answer, flashed as the listing's would be.
		if filled == nil && name != "" {
			var verr *view.Error
			if filled, verr = fillRefs(context.Background(), name, conn, c, values); verr != nil {
				return completeMsg{form: cf, field: f.Name, err: verr}
			}
		}
		resolved := plugin.Resolve(c, plugin.Inputs{
			Caller: values, Profile: filled, ProfileName: name, Config: cfg.values,
			ConfigSection: cfg.section, Fence: fence,
		})
		// The box's own text is the partial, whatever any layer had in mind for
		// this field: a listing narrows on what is typed, not on a default. For a
		// list that text is the element being typed, as the string it is: the
		// chosen ones are no business of the plugin's, and req.StringSlice reads
		// the one name as a list of one.
		resolved[f.Name] = box.partial
		req := plugin.LiveRequest(resolved)
		ctx, cancel := context.WithTimeout(context.Background(), completeTimeout)
		defer cancel()
		// Held to the rule every other Suggest answer is (candidateValues):
		// these strings come from a service, go onto a terminal, and are
		// typed into the box as they are offered.
		//
		// And to the connection's fence: the listing is the service's whole
		// answer, which is every namespace or bucket the connection can see,
		// and the connection says it reaches fewer.
		//
		// A list box takes each name once, so the ones it lists already are
		// not candidates: the answer is counted and named for the box as it
		// stands, and a press that leaves nothing says so.
		//
		// And when nothing the fence holds is left of the answer — the service
		// listed none, or could not (a Suggest has no way to say which), or
		// listed only what the connection does not reach — the fence's own
		// entries are what the box offers, as on the keystroke channel, where
		// Narrow gives a Live box the allowlist. A credential bound in each of
		// the namespaces cannot list them cluster-wide, and "no completions" over
		// a box whose profile names the namespaces would be the one answer that
		// is not true. Narrow and not a second reading of the fence, so the two
		// channels cannot offer different lists; narrowed to the text, since the
		// service would have been, and so the line counts what the box can take.
		//
		// The last of the three is told apart from the first two, since the
		// service did answer and a line saying it did not would be false; what
		// it answered is never listed, as it is the service's whole view.
		answer := f.LiveCandidates(ctx, req)
		raw := fence.Filter(f, answer)
		offer := offerService
		if len(raw) == 0 {
			raw = startingWith(fence.Narrow(f, nil), box.partial)
			offer = offerNoAnswer
			if len(answer) > 0 {
				offer = offerOutside
			}
		}
		items := make([]string, 0, len(raw))
		for _, entry := range raw {
			if v := plugin.CandidateValue(entry); !textclean.Deceives(v) && !slices.Contains(box.listed, v) {
				items = append(items, v)
			}
		}
		return completeMsg{form: cf, field: f.Name, offer: offer,
			c: tunnel.Completion{Items: items, Names: items, What: what}}
	}
}

// heldByFence is the press's look at the connection's fence before anything is
// read, and the sentence for the flash line when the press is outside it — empty
// when it is not.
//
// A press reads the service with what the other boxes hold, and the fence is
// about what a call asks of the service: a cluster listing of the namespaces a
// sibling box names reads a namespace the fence leaves out, exactly as the run
// would, and what the answer is filtered to afterwards cannot unread it. So the
// check is the run's (profile.FenceBreached, which binds the connection's layer
// the way profile.Fenced does), made on the values the form holds and before the
// credential is resolved, since a press that is about to be refused must not read
// one — the reason the coordinate above is checked first as well.
//
// The box being completed is held on its own terms, since what it holds is the
// start of a value and not a value of the call, and a press that asked "is `sh`
// one of the namespaces" would hold every first keystroke. Its value is left out
// of the run's check (plugin.Inputs.Unfinished); its fence stays in it, because a
// switch that reads past the box is a way across whatever the box holds. Every
// other fenced input is held as a run holds it — a blank one included, which a run
// refuses as the widest reach there is — and so is a switch that reads past one.
//
// What the box holds is also what the plugin is handed, and a Suggest may read at
// it: the keys of a bucket under the prefix typed. The filter below drops what the
// fence does not hold from the answer, but it cannot unread the listing, so a fenced
// box is held when its text continues no entry of the fence (continuesAnEntry) —
// `sh` for an entry `shop` passes, `secrets/` for `backups/` does not. An empty box
// is the start of every entry, and a box the profile does not fence is not held by
// its text.
//
// The sentence names the input and not the allowlist, and never the text typed: the
// line is on a screen somebody else may be reading, and the fence is the operator's
// inventory. The run's own refusal (profile.FenceRefusal) lists what the connection
// reaches, which is for the person who has to change the call.
func (m Model) heldByFence(ref string, conn config.Connection, c plugin.Capability, f plugin.Field,
	values map[string]any, partial string,
) string {
	cfg := m.configFor(c)
	b := profile.FenceBreached(ref, conn, c, plugin.Inputs{
		Caller: values, Config: cfg.values, ConfigSection: cfg.section, Unfinished: f.Name,
	}, os.LookupEnv)
	if b == nil && !continuesAnEntry(profile.FenceOf(conn), f, partial) {
		b = &plugin.FenceBreach{Input: f}
	}
	if b == nil {
		return ""
	}
	input := profile.FenceInput(b.Input, plugin.SurfaceTUI)
	switch {
	case b.Widened():
		return fmt.Sprintf("%s reads past %s, which profile %q fences, so the service was not asked",
			profile.FenceInput(b.Switch, plugin.SurfaceTUI), input, ref)
	case b.Blank:
		return fmt.Sprintf("%s is blank, and profile %q fences it, so the service was not asked", input, ref)
	}
	return fmt.Sprintf("%s is outside what profile %q reaches, so the service was not asked", input, ref)
}

// continuesAnEntry says whether text, the start of a value for f, is the start of
// one the fence allows: an entry that begins with it, the entry itself included.
// Compared as the fence compares a value, case and all, since a listing under `SH`
// is a listing the run's `SH` would be refused for. Empty text begins every entry,
// and an input the fence does not name has none to fall short of.
func continuesAnEntry(fence plugin.Fence, f plugin.Field, text string) bool {
	if !fence.Fenced(f) {
		return true
	}
	return slices.ContainsFunc(fence.Offer(f), func(entry string) bool {
		return strings.HasPrefix(entry, text)
	})
}

// startingWith keeps the entries that continue partial, in the case-insensitive
// way a box compares a suggestion with what is typed (boxlist.Extend, the widget).
func startingWith(entries []string, partial string) []string {
	low := strings.ToLower(partial)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(strings.ToLower(plugin.CandidateValue(entry)), low) {
			out = append(out, entry)
		}
	}
	return out
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
		m.refuse(msg.err.Message)
		return m, nil
	}
	if len(msg.c.Items) == 0 {
		m.refuse("no " + msg.c.What)
		return m, nil
	}
	// A field with a suggestion function is told through the form, not through
	// the widget: Input.Suggestions clears that function, and a reload already
	// queued by a keystroke reads it on another goroutine when it runs — a
	// race, and a call of nil when the clearing came first. The fetch lands in
	// the store the function merges from (capForm.fetched), and the form is
	// sent a message so huh sees what it watches has changed. A field with no
	// function has no reload to race, and takes the list directly.
	_, wired := m.form.offers[msg.field]
	if in, ok := m.form.inputs[msg.field]; ok && !wired {
		in.Suggestions(msg.c.Items)
	}
	// Remembered so the next tab can tell accept from fetch (needsFetch): the
	// widget cannot be asked what it is offering, but this is what it was told.
	m.form.suggested[msg.field] = msg.c.Items
	if wired {
		m.form.setFetched(msg.field, msg.c.Items)
	}
	// Two sentences for two states. bubbles matches suggestions only against
	// a non-empty value, so on an empty box there is no ghost yet and the
	// names themselves are the answer; once something is typed the ghost is
	// on screen and what is worth saying is how to drive it.
	//
	// An offer that is the profile's own says whose it is and why, in a guide's
	// colour: the service did not confirm that any of it exists, and a check mark
	// would.
	empty := strings.TrimSpace(*m.form.bindings[msg.field]) == ""
	why := ""
	switch msg.offer {
	case offerNoAnswer:
		why = "the service gave no answer; "
	case offerOutside:
		why = "none of the service's names are in the profile's fence; "
	}
	switch {
	case why != "" && empty:
		m.guide(why + "the profile's entries for " + msg.field + ": " + strings.Join(msg.c.Names, ", "))
	case why != "":
		m.guide(fmt.Sprintf("%s%d of the profile's entries for %s — ↓ cycles, tab completes",
			why, len(msg.c.Items), msg.field))
	case empty:
		m.flash = msg.c.What + ": " + strings.Join(msg.c.Names, ", ")
	default:
		m.flash = fmt.Sprintf("%d %s — ↓ cycles, tab completes", len(msg.c.Items), msg.c.What)
	}
	if wired {
		return m.updateForm(refetchedMsg{})
	}
	return m, nil
}

// refetchedMsg reaches the form after a fetch landed in it, and means nothing
// to any field: every message a form is given ends in huh asking its fields
// whether what they watch has changed, which is all this is sent for.
type refetchedMsg struct{}
