package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/recent"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/textclean"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// suggestTimeout bounds every Suggest call made while building one form.
// Suggestions are a convenience; a form that takes a second to appear is not.
const suggestTimeout = 500 * time.Millisecond

// capForm collects capability inputs interactively. Field values bind to
// strings while editing and convert to their declared types on submit. A
// Prefill-capable capability runs as two capForms: identity fields first,
// then the remaining fields seeded with current values (base carries the
// stage-one answers).
type capForm struct {
	form     *huh.Form
	cap      plugin.Capability
	fields   []plugin.Field // the fields this stage collects
	base     map[string]any // request values this form does not collect itself
	final    bool           // completing this form runs the capability
	bindings map[string]*string
	bools    map[string]*bool
	slices   map[string]*[]string // multi-select bindings
	// inputs is each free-text widget by field name, kept so a handler can
	// reach one after the form is built — the cluster completion sets a
	// fetched suggestion list on exactly one of them (complete.go), which no
	// binding can express: a binding is the field's value, not its widget.
	//
	// Every huh.Input the form builds, not only the completable ones. It held
	// just those until tab's meaning moved out of huh, and then a map missing
	// the masked and path boxes was a map that could not tell "the cursor is
	// in a box with nothing to complete" from "the cursor is in a picker" —
	// so tab died on exactly the fields it had just been fixed for.
	inputs map[string]*huh.Input
	// offers is the suggestion function each free-text field was built with,
	// kept because tab has to ask the same question the widget answers — does
	// anything on offer extend what is typed — and huh exposes neither the list
	// it last computed nor the ghost it is drawing. Recomputing from a second
	// expression of "what this field suggests" would be a copy free to drift;
	// this is the one the field is wired to.
	offers map[string]func() []string
	// suggested is what a cluster fetch last offered each field, because the
	// widget cannot be asked: tab's fetch-or-accept split (needsFetch) reads
	// it to know whether anything on offer still extends what is typed.
	suggested map[string][]string
	// fetchedFor is the value each field was last fetched for, which is what
	// turns tab's third answer — "nothing left to do, so move on" — from a
	// guess into a fact. Without it a completing field is the one place in
	// the app where tab does not eventually advance: at the end of a
	// coordinate every press refetches, finds nothing deeper, and stays,
	// which is the inconsistency somebody notices as "tab works on some
	// fields and not others".
	fetchedFor map[string]string
	// liveGot is what a live fetch last brought back per field, behind its
	// own lock: applyCompletion writes it on the event loop, and suggestion
	// functions read it off the loop — the same split syncs exists for, on
	// data no binding holds. Without it the keystroke channel's re-evaluation
	// (recents only, Candidates gates live) would clobber a landed fetch off
	// the widget at the next keystroke.
	liveMu  sync.Mutex
	liveGot map[string][]string
	// offered is the field list this run form was asked to collect, before
	// forwardFilled dropped what a forward answers — what a rebuild on the
	// environment the picker now names must start from, since the drop is
	// exactly what changes with the pick (reseedOnPickerMove).
	offered []plugin.Field
	// builtOn is the picker binding's value when the form was built, which
	// is what "the picker moved" is measured against.
	builtOn string
	// kubeCoord is the connection's `kube:` coordinate when this form edits a
	// credential under one — the context and namespace a Secret reference
	// completes against. Empty everywhere else.
	kubeCoord string
	// syncs is the same string bindings behind their locks, for the one thing
	// that reads them off the event loop: computing a suggestion. See bind.
	syncs   map[string]*syncString
	confirm *bool // non-nil when a destructive confirmation gates the run
	// configTarget is non-empty when completing this form writes a
	// plugins.<section> block instead of running a capability — the plugin
	// config editor (internal/render/tui/pluginconfig.go). It reuses capForm
	// because its fields genuinely are plugin.Field values collected from a
	// plugin's own capabilities; only what happens on submit differs, and
	// configOrigin is what the submit path needs to compute the section
	// heading (bare for a built-in, pinned to the artifact otherwise).
	configTarget string
	configOrigin registry.Origin
	// profileTarget/profileEditing mark the profile editor, which reuses this
	// form for the same reason the plugin config editor does: a profile's
	// `set:` block is keyed by Field.Config, and configFields already collects
	// exactly those with their declared types, pickers, bounds and help.
	// profileTarget is the name being edited, empty for a new one — so
	// profileEditing is what distinguishes "creating" from "not a profile
	// form", which an empty name alone could not.
	profileTarget  string
	profileEditing bool
	// credentialEditing marks the credential form, which edits the same
	// profile through a different question: not what it points at, but where
	// its password comes from.
	credentialEditing bool
	// connEditing marks the editor for one plugin inside an environment, whose
	// profileTarget is the plugins: key rather than the profile name.
	connEditing bool
	// seed is what the form opened showing, kept so submit can tell an answer
	// somebody gave from a value the form displayed. See displayed.
	seed map[string]any
	// derived names the seeded inputs the FORM put there — resolved from the
	// environment, the operator's config, or the field's own declaration —
	// as opposed to the ones a person gave, now or in a previous run. A
	// derived value is a display: dropped at submit when unchanged, so the
	// run re-derives it from the layer it belongs to, where a layer the form
	// cannot know yet (a port-forward's endpoint, opened per call) can win
	// over the default the box showed. runForm marks everything it seeded
	// that the caller did not give; the connection and config editors mark
	// the fields their file does not state.
	derived map[string]bool
	// used is what this operator has supplied before, read once when the form
	// is built rather than per keystroke: huh re-evaluates every field's
	// suggestions on every message, and eight fields would be eight file
	// reads to learn the same thing. A form is short-lived, so a run that
	// happens while one is open shows up in the next one.
	used recent.Values
	// show holds the fields that are only asked about under some condition,
	// keyed by field name. Empty for every form that does not branch, which is
	// all of them but one. See hideUnless.
	show map[string]func(*capForm) bool
	// heads is the section title rendered above a named field, keyed by that
	// field's name. See withSection.
	heads map[string]string
}

// formOption tunes a form beyond the fields it collects.
type formOption func(*capForm)

// hideUnless asks about a field only while want holds.
//
// A form that shows a box it will not read is a form that has to explain
// itself — the credential editor used to carry "only used when referencing
// one" and "only used when storing a new one" as help text under two fields
// that were always both there, which is a footnote standing in for a design.
// The question that decides is directly above them, so the honest thing is for
// the follow-up to be the one that question implies.
//
// The predicate takes the form because it reads a *sibling field's live
// binding*, which does not exist until the form is built.
//
// huh hides groups, never single fields (charm.land/huh/v2 Group.WithHideFunc
// is the only hook there is), so this puts the field in a group of its own —
// which is why it is an option rather than something a field could declare:
// grouping changes how the whole form paginates, and that is a decision about
// the form.
func hideUnless(name string, want func(*capForm) bool) formOption {
	return func(cf *capForm) {
		if cf.show == nil {
			cf.show = map[string]func(*capForm) bool{}
		}
		cf.show[name] = want
	}
}

// withSection puts a heading above one field, and by doing so puts every
// field after it under that heading.
//
// **A form is a list of questions until something says which of them are the
// same question.** The connection editor asks three unrelated things — which
// plugin, how to reach it, and what this environment changes about it — in one
// flat column of identical-looking boxes, and the operator has to infer the
// boundaries from a `set.` prefix. A heading is one line and it turns the
// column into three short answers.
//
// A huh Note rather than a group, deliberately: huh renders one group per
// page, so grouping for legibility would have cost an extra keypress between
// every section — the opposite of what this is for. A Note skips itself when
// the cursor moves (charm.land/huh/v2, NewNote defaults skip: true), so the
// heading is passed over rather than tabbed into.
func withSection(field, title string) formOption {
	return func(cf *capForm) {
		if cf.heads == nil {
			cf.heads = map[string]string{}
		}
		cf.heads[field] = title
	}
}

// sectioned splices the headings in, keeping fields and names index-aligned —
// groups() reads names[i] to find the field a widget was built from, and a
// Note is built from none.
func (cf *capForm) sectioned(fields []huh.Field, names []string) ([]huh.Field, []string) {
	if len(cf.heads) == 0 {
		return fields, names
	}
	outF := make([]huh.Field, 0, len(fields)+len(cf.heads))
	outN := make([]string, 0, len(names)+len(cf.heads))
	for i, field := range fields {
		if i < len(names) {
			if head, ok := cf.heads[names[i]]; ok {
				outF = append(outF, huh.NewNote().Title(sectionRule(head)))
				outN = append(outN, "")
			}
		}
		outF = append(outF, field)
		if i < len(names) {
			outN = append(outN, names[i])
		}
	}
	return outF, outN
}

// sectionRule is what a heading looks like: a named rule.
//
// huh gives a Note's title the same bold primary as a field's, so a heading
// written as plain words is a field name with no box under it — which is
// worse than no heading at all. Pre-styling it does not work either: a style
// applied over text that already carries ANSI ends at the first reset inside
// it, the lesson profileCovers records. So the distinction is typographic
// rather than chromatic, and it is the one this app already uses for exactly
// this — the band lists draw a section as a rule with a name in it.
func sectionRule(title string) string { return "── " + title + " " + strings.Repeat("─", 4) }

// hidden reports whether a field is currently not being asked about.
func (cf *capForm) hidden(name string) bool {
	want, conditional := cf.show[name]
	return conditional && !want(cf)
}

// groups partitions the built widgets into what huh renders as pages: one for
// everything unconditional, and one of its own for each field that comes and
// goes. Fields keep their declared order, so a form with no conditions is a
// single group exactly as before.
func (cf *capForm) groups(fields []huh.Field, names []string) []*huh.Group {
	fields, names = cf.sectioned(fields, names)
	if len(cf.show) == 0 {
		return []*huh.Group{huh.NewGroup(fields...)}
	}
	var (
		out []*huh.Group
		run []huh.Field
	)
	flush := func() {
		if len(run) > 0 {
			out = append(out, huh.NewGroup(run...))
			run = nil
		}
	}
	for i, field := range fields {
		var want func(*capForm) bool
		if i < len(names) {
			want = cf.show[names[i]]
		}
		if want == nil {
			run = append(run, field)
			continue
		}
		flush()
		out = append(out, huh.NewGroup(field).WithHideFunc(func() bool { return !want(cf) }))
	}
	flush()
	return out
}

// displayed reports whether raw is still exactly what the *environment* put in
// the box.
//
// Such a box is a display of where the call would go, not an answer somebody
// gave, and the difference decides which server is reached. formSeed fills it
// from the environment the picker at the top of the same form names — and the
// picker can then be changed. A seeded value handed back as a caller value
// beats the profile layer (plugin.Resolve, deliberately: `--profile prod
// --host x` connects to x), so a box nobody touched would pin the call to the
// environment the operator just navigated away from while the picker above it
// named the one they chose. That is the failure profiles exist to prevent,
// rebuilt inside the form that offers them.
//
// Dropping it costs nothing when nothing changed: Resolve re-derives the same
// value from the same layer. An edited box is a real answer and still wins.
//
// Three provenances, three answers. A **derived** value is a display,
// dropped when it still equals what was shown. A **seeded but unmarked**
// value is an answer a person already gave — a previous run's input, a
// prefill record's contents, a file value an editor keeps — and dropping one
// because it happens to match the environment would take a deliberate
// `--host` away on the second run. And an **unmarked, unseeded** box is
// showing the field's own declared default: on a final form that is a
// display too, because the run re-derives the same default when nothing
// outranks it — which is precisely when something should, and the day
// something does (a forward's endpoint, a config key) the displayed default
// handed back as a caller value was what outran it. Text boxes only: a
// picker or a confirm entered through is choosing what it shows, and a
// stage-one form keeps everything, because its values become stage two's
// base rather than being re-derived (see startForm).
func (cf *capForm) displayed(f plugin.Field, current any) bool {
	if cf.derived[f.Name] {
		if seeded, ok := cf.seed[f.Name]; ok {
			return seedString(seeded) == seedString(current)
		}
		return seedString(f.Default) == seedString(current)
	}
	if _, given := cf.seed[f.Name]; given {
		return false
	}
	if !cf.final || len(f.Options) > 0 || f.Type == plugin.Bool || f.Type.Repeatable() {
		return false
	}
	return f.Default != nil && seedString(f.Default) == seedString(current)
}

// seedString renders a seed value the way newCapForm put it in the widget, so
// "unchanged" is compared against what was actually on screen.
// It doubles as the comparison displayed makes, which is why it is normalised
// rather than rendered: a seed of int 5 and a box holding "5" are the same
// answer, and so are a []string{"a","b"} and the "a, b" it was typed as.
func seedString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(textclean.Terminal(t))
	case []string:
		return strings.Join(t, ", ")
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// hasInputs reports whether opening this capability warrants a form: any
// field the user might want to set (required or optional).
func hasInputs(c plugin.Capability) bool { return len(c.Inputs) > 0 }

// newCapForm builds the huh form for a subset of a capability's inputs.
// defaults (from Prefill) override declared field defaults. Destructive
// capabilities get a trailing explicit confirmation on their final stage.
//
// base is everything the request already carries that this form will not ask
// about — a previous stage's answers, and the reserved inputs no field can
// hold. It used to be taken here only to give Suggest its context, while
// values() read a field two callers had to remember to set separately; a third
// caller passing base and nothing else silently dropped it, which is exactly
// how the `e` form lost the detail toggle.
func newCapForm(c plugin.Capability, fs []plugin.Field, defaults map[string]any, final bool,
	base map[string]any, opts ...formOption) *capForm {
	cf := &capForm{
		cap:        c,
		fields:     fs,
		final:      final,
		base:       base,
		seed:       defaults,
		bindings:   map[string]*string{},
		bools:      map[string]*bool{},
		slices:     map[string]*[]string{},
		inputs:     map[string]*huh.Input{},
		offers:     map[string]func() []string{},
		suggested:  map[string][]string{},
		fetchedFor: map[string]string{},
		syncs:      map[string]*syncString{},
		used:       recent.Load(),
	}
	var (
		fields []huh.Field
		// names[i] is the plugin field fields[i] was built from, which is what
		// groups needs to find the conditional ones again.
		names []string
	)
	for _, f := range fs {
		f := f
		names = append(names, f.Name)
		if v, ok := defaults[f.Name]; ok {
			// A Prefill default is a runtime value — the record's current
			// contents — and replaces the declared one that Validate checked.
			// Cleaned here, once, rather than at each of the six places a
			// default is turned into a string for a widget.
			if str, ok := v.(string); ok {
				v = textclean.Terminal(str)
			}
			f.Default = v
		}
		// A closed set is a list to pick from, not a string to spell. This is
		// the surface where that difference is free: nothing invalid can be
		// submitted, and nobody has to read the help text to learn the words.
		if len(f.Options) > 0 {
			switch f.Type {
			case plugin.StringSlice, plugin.SecretSlice:
				fields = append(fields, cf.multiSelect(f))
			default:
				fields = append(fields, cf.selectOne(f))
			}
			continue
		}
		switch f.Type {
		case plugin.Bool:
			v := false
			if d, ok := f.Default.(bool); ok {
				v = d
			}
			cf.bools[f.Name] = &v
			fields = append(fields, huh.NewConfirm().
				Title(fieldTitle(f.Name)).
				Description(f.Help).
				Affirmative("yes").Negative("no").
				Value(&v))
		case plugin.Text:
			// No suggestion list: huh's Text has no Suggestions of any kind
			// (charm.land/huh/v2, field_text.go), and a body written in five
			// lines with $EDITOR behind it is not a field anybody completes.
			// Capability.validate refuses Suggest on a Text input, so nothing
			// is being dropped here — see pkg/plugin.
			fields = append(fields, huh.NewText().
				Title(fieldTitle(f.Name)).
				// ctrl+e opens $EDITOR on the body, which is how a note gets
				// written in the tool you write in. It works by default; it
				// is said here because an affordance nobody knows about is
				// not an affordance.
				Description(fieldDescription(f)+" — ctrl+e opens $EDITOR").
				ExternalEditor(true).
				Lines(5).
				Accessor(cf.bind(f.Name, defaultString(f))).
				Validate(validatorFor(f)))
		case plugin.StringSlice:
			// A []string default (from a declared Field.Default or a
			// Prefill result, e.g. a task's current tags) must render as
			// "a, b" — fmt.Sprint would print "[a b]" and break re-submission.
			typed := cf.bind(f.Name, defaultString(f))
			fields = append(fields, cf.record(f.Name, cf.completing(huh.NewInput().
				Title(fieldTitle(f.Name)).
				Description(completionHint(f, fieldDescription(f)+" (comma-separated)")).
				Accessor(typed).
				Validate(validatorFor(f)), f, typed, lastItem)))
		case plugin.Path:
			// The filesystem is re-read on every keystroke, which is what
			// makes it a completion rather than a list, and whatever the field
			// declared for itself goes first.
			typed := cf.bind(f.Name, defaultString(f))
			fields = append(fields, cf.record(f.Name, cf.completing(huh.NewInput().
				Title(fieldTitle(f.Name)).
				Description(fieldDescription(f)+" — tab completes paths").
				Accessor(typed).
				Validate(validatorFor(f)), f, typed, walkingDisk)))
		case plugin.Secret, plugin.SecretSlice:
			// Never completed, and that is a rule rather than an omission: a
			// suggestion list renders in plain text beside a box that is
			// masked precisely so the value is not on screen. Capability
			// validate refuses Suggest on a Secret for the same reason.
			//
			// A SecretSlice is masked rather than comma-hinted, which is the
			// arm it shares with Secret and not the one it shares with
			// StringSlice: the box takes the same comma-separated text — the
			// same typedValue splits it — but it must not be on screen while
			// somebody types it, and the hint would be the only thing telling
			// them so. That trade is the reason this is masked-and-terse
			// rather than legible-and-visible.
			fields = append(fields, cf.record(f.Name, huh.NewInput().
				Title(fieldTitle(f.Name)).
				Description(commaHint(f)).
				EchoMode(huh.EchoModePassword).
				Accessor(cf.bind(f.Name, defaultString(f))).
				Validate(validatorFor(f))))
		default:
			typed := cf.bind(f.Name, defaultString(f))
			fields = append(fields, cf.record(f.Name, cf.completing(huh.NewInput().
				Title(fieldTitle(f.Name)).
				Description(completionHint(f, fieldDescription(f))).
				Accessor(typed).
				Validate(validatorFor(f)), f, typed, wholeBox)))
		}
	}
	if final && c.Safety == plugin.Destructive {
		ok := false
		cf.confirm = &ok
		fields = append(fields, huh.NewConfirm().
			Title(fmt.Sprintf("%s is destructive — run it?", c.ID)).
			Description("This cannot be undone.").
			Affirmative("run").Negative("cancel").
			Value(&ok))
	}
	// After the bindings exist, because a condition reads one of them.
	for _, opt := range opts {
		opt(cf)
	}
	cf.form = huh.NewForm(cf.groups(fields, names)...).WithKeyMap(formKeyMap())
	return cf
}

// formKeyMap is huh's default with tab moved: it completes, and it is no
// longer also the key that leaves the field.
//
// huh binds AcceptSuggestion to ctrl+e (charm.land/huh/v2, keymap.go). rta has
// been telling people to press tab since the first Path field — "tab completes
// paths" under one, "press tab" under the plugin picker — so every completion
// this app computes was reachable only by a key nothing mentions. Tab is the
// completion key in every shell and every editor, which is why the
// documentation kept saying so.
//
// **Binding it to Next as well looked like the way to keep both meanings, and
// it silently destroyed the first one.** Input.Update tests Next before
// AcceptSuggestion and returns early when the value does not validate
// (field_input.go), so on a required box that is still empty the textinput
// never saw the key at all: no completion, no move, nothing — which is a
// plugin picker whose own help says "press tab" and does not respond to it.
// Where the value did validate, the two meanings fired on the same press, so
// accepting a path threw the cursor out of the field and there was no way to
// take the next segment.
//
// So huh no longer decides. Tab arrives at completeLocally, which asks the one
// question that separates the meanings — is there anything on offer that
// extends what is typed — and either hands the key to the widget or advances
// the form itself. Enter keeps Next, on every field, as it always did.
func formKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Input.AcceptSuggestion = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete"))
	km.Input.Next = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))
	return km
}

// record files one widget under the field it was built for and hands it back,
// so a construction stays one expression. Every free-text box goes in,
// completable or not: the map is how tab tells a box from a picker.
func (cf *capForm) record(name string, in *huh.Input) *huh.Input {
	cf.inputs[name] = in
	return in
}

// syncString is a huh.Accessor guarding one field's value with a mutex.
//
// mirror is the plain string the rest of the form reads — cf.values() when the
// form is submitted, and huh's own change detection, both on the event loop
// goroutine that writes it. Only Get crosses a goroutine boundary, and only it
// needs the lock on the read side.
type syncString struct {
	mu     sync.Mutex
	value  string
	mirror *string
}

func (s *syncString) Get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *syncString) Set(value string) {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
	*s.mirror = value
}

// completionHint says so under a field that has one. An affordance nobody
// knows about is not an affordance, and this is the one place a person can be
// told — the footer speaks for the screen, not for the field under the cursor.
func completionHint(f plugin.Field, desc string) string {
	if f.Suggest == nil {
		return desc
	}
	return desc + " — tab completes"
}

// defaultString renders a field's default the way its widget shows it.
func defaultString(f plugin.Field) string {
	if def, ok := f.Default.([]string); ok {
		return strings.Join(def, ", ")
	}
	if f.Default == nil {
		return ""
	}
	return fmt.Sprint(f.Default)
}

// bind registers one string-valued field and returns the accessor huh writes
// through.
//
// Every such field is reached through a locked accessor rather than a bare
// pointer, because two goroutines touch it: huh writes on the event loop as
// you type, and runs the suggestion function in a bubbletea command. Only the
// Path field used to do this, which was enough while suggestions were computed
// once at build time; now that a suggestion reads the rest of the form, a plain
// `Value(&v)` on any of them is a data race on a string — caught by `go test
// -race`, and quite capable of tearing a header in a real session.
//
// The plain pointer stays as the mirror, because everything else in the form
// reads it on the event loop and only Get crosses a goroutine boundary.
func (cf *capForm) bind(name, initial string) *syncString {
	v := initial
	cf.bindings[name] = &v
	s := &syncString{value: initial, mirror: &v}
	cf.syncs[name] = s
	return s
}

// matchOn says which part of what has been typed a suggestion has to extend.
type matchOn int

const (
	// wholeBox: the field holds one value, so a suggestion is a whole answer.
	wholeBox matchOn = iota
	// lastItem: a comma-separated list, so only the item being typed matters.
	// bubbles matches a suggestion against the *entire* box (textinput.go), so
	// without this `note add --tag` typing "recipe, ita" matches nothing —
	// completion silently stopped working after the first item.
	lastItem
	// walkingDisk: a path, completed directory by directory off the filesystem.
	walkingDisk
)

// completing attaches live completion to a free-text input: tab completes it,
// and nothing is taken away — a suggestion list is not a validator.
//
// Recomputed as the form is filled in rather than once when it is built, which
// is what lets a suggestion depend on a sibling. Field.Suggest is documented to
// receive "what the caller has supplied so far"; the CLI honours that
// (internal/app.candidates rebuilds the request per keystroke) and the TUI froze
// one request at build time, so a capability like `grant allow`, whose `scope`
// completes from whichever `target` was named, offered nothing here while
// completing perfectly from a shell.
//
// huh re-evaluates when the hash of the bound values changes, so the binding is
// every string in the form: typing in any box refreshes what the others offer.
func (cf *capForm) completing(in *huh.Input, f plugin.Field, typed *syncString, how matchOn) *huh.Input {
	// Nothing declared, nothing to walk and nothing remembered: no list, and
	// no per-keystroke work for a field that will never have one.
	//
	// The remembered half is not an optimisation, it is the whole feature.
	// Without it this returned early for every plain String with no Suggest —
	// which is exactly the set internal/recent exists for, a bucket and a
	// database and a schema and a vault path — so the values were recorded on
	// every run and offered on none of them.
	if how == wholeBox && f.Suggest == nil && len(cf.used.For(cf.cap.ID, f.Name)) == 0 {
		return in
	}
	offer := func() []string {
		declared := cf.candidates(f)
		switch how {
		case walkingDisk:
			return pathSuggestions(typed.Get(), declared)
		case lastItem:
			return extending(typed.Get(), declared)
		default:
			return declared
		}
	}
	cf.offers[f.Name] = offer
	return in.SuggestionsFunc(offer, cf.watched())
}

// watched is what huh hashes to decide a suggestion is stale: every string the
// form currently holds.
//
// Read on the event loop by huh itself, so the pointers rather than the locked
// accessors — the lock is for the suggestion function, which runs elsewhere.
func (cf *capForm) watched() []*string {
	out := make([]*string, 0, len(cf.fields))
	for _, f := range cf.fields {
		if b, ok := cf.bindings[f.Name]; ok {
			out = append(out, b)
		}
	}
	return out
}

// candidates asks a field what it can be completed to, given what the rest of
// the form currently holds, with what this operator has actually used behind
// whatever the field declared for itself.
//
// Behind, not in front: a declared list is authoritative — those are the tags
// that exist — while a shortlist is a convenience. For the inputs it matters
// most for (a bucket, a database, a vault path) there is no declared list at
// all and the shortlist is the whole of it.
func (cf *capForm) candidates(f plugin.Field) []string {
	ctx, cancel := context.WithTimeout(context.Background(), suggestTimeout)
	defer cancel()
	// SurfaceCompletion, and without any credential the form is holding — see
	// plugin.CompletionRequest. This is the tab key with nobody waiting to
	// answer anything, and a Suggest that would otherwise prompt or take a
	// visible moment must be able to tell the difference.
	req := plugin.CompletionRequest(cf.cap, cf.snapshot())
	out := candidateValues(f, ctx, req)
	// A live field's listing arrives on the deliberate channel and lands in
	// liveGot (applyCompletion); this merge is what keeps it on the widget
	// when the keystroke channel re-evaluates. In front of the recents,
	// because it is what the service said exists right now.
	if f.Live {
		out = append(out, cf.liveItems(f.Name)...)
	}
	seen := make(map[string]bool, len(out))
	for _, v := range out {
		seen[v] = true
	}
	for _, v := range cf.used.For(cf.cap.ID, f.Name) {
		if !seen[v] {
			out = append(out, v)
		}
	}
	return out
}

// liveItems and setLiveItems are liveGot behind its lock — see the field.
func (cf *capForm) liveItems(name string) []string {
	cf.liveMu.Lock()
	defer cf.liveMu.Unlock()
	return append([]string(nil), cf.liveGot[name]...)
}

func (cf *capForm) setLiveItems(name string, items []string) {
	cf.liveMu.Lock()
	defer cf.liveMu.Unlock()
	if cf.liveGot == nil {
		cf.liveGot = map[string][]string{}
	}
	cf.liveGot[name] = append([]string(nil), items...)
}

// snapshot is what the form holds right now, safe to read off the event loop.
//
// Strings only, plus whatever earlier stages already answered. A Confirm's bool
// and a MultiSelect's slice are bound by plain pointer and would be a race to
// read here; no suggestion in the repo depends on one, and the day one does the
// answer is to give those two an accessor as well rather than to read them
// unlocked.
func (cf *capForm) snapshot() map[string]any {
	out := make(map[string]any, len(cf.base)+len(cf.syncs))
	for k, v := range cf.base {
		out[k] = v
	}
	for _, f := range cf.fields {
		s, bound := cf.syncs[f.Name]
		if !bound || cf.hidden(f.Name) {
			continue
		}
		if raw := strings.TrimSpace(s.Get()); raw != "" {
			out[f.Name] = typedValue(f, raw)
		}
	}
	return out
}

// extending keeps the suggestions that continue the last comma-separated item,
// rewritten so each one extends the whole box — which is what bubbles matches
// against.
func extending(typed string, declared []string) []string {
	head, fragment := "", typed
	if i := strings.LastIndexByte(typed, ','); i >= 0 {
		// The head keeps exactly the spacing that was typed rather than
		// imposing ", ". bubbles matches by prefix against the whole box, so a
		// rewritten "recipe, italian" does not match somebody who typed
		// "recipe,ita" — and `a,b` with no space is the ordinary way people
		// type a comma list.
		rest := typed[i+1:]
		gap := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
		head, fragment = typed[:i+1]+gap, strings.TrimSpace(rest)
	}
	out := make([]string, 0, len(declared))
	for _, d := range declared {
		if !strings.HasPrefix(strings.ToLower(d), strings.ToLower(fragment)) {
			continue
		}
		out = append(out, head+d)
	}
	return out
}

// candidateValues asks a field what it can be completed to, stripped of the
// descriptions that belong to shell completion — it has a column for them,
// whereas here they would be typed into the field.
func candidateValues(f plugin.Field, ctx context.Context, req plugin.Request) []string {
	raw := f.Candidates(ctx, req)
	if len(raw) == 0 {
		return nil
	}
	// Suggest runs at form time and returns whatever is out there right now —
	// tags from a store, hostnames from a file, keys somebody else wrote. It
	// never passes through Validate, which only sees the declaration, so this
	// is the one place those strings can be cleaned.
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		values = append(values, textclean.Terminal(plugin.CandidateValue(entry)))
	}
	return values
}

// selectOne renders a closed set as a picker.
//
// An optional field with no default gets an explicit empty choice, because
// "none of these" has to be expressible and leaving the cursor somewhere is
// not a way to say it. A field that has a default already offers that: the
// default is the "leave it alone" answer, and a "(none)" beside it would only
// raise the question of which one means what.
func (cf *capForm) selectOne(f plugin.Field) huh.Field {
	// Through bind like every other string field, so a picker's answer is
	// visible to a sibling's suggestion — which is the common shape: choose the
	// thing, then complete something about it.
	typed := cf.bind(f.Name, defaultString(f))

	opts := make([]huh.Option[string], 0, len(f.Options)+1)
	if !f.Required && f.Default == nil {
		opts = append(opts, huh.NewOption("(none)", ""))
	}
	for _, o := range f.Options {
		opts = append(opts, huh.NewOption(o, o))
	}
	return huh.NewSelect[string]().
		Title(fieldTitle(f.Name)).
		Description(fieldDescription(f)).
		Options(opts...).
		Accessor(typed)
}

// multiSelect renders a closed set that takes several values at once.
func (cf *capForm) multiSelect(f plugin.Field) huh.Field {
	var v []string
	switch def := f.Default.(type) {
	case []string:
		v = append(v, def...)
	case string:
		if def != "" {
			v = append(v, def)
		}
	}
	cf.slices[f.Name] = &v

	opts := make([]huh.Option[string], 0, len(f.Options))
	for _, o := range f.Options {
		opts = append(opts, huh.NewOption(o, o).Selected(contains(v, o)))
	}
	return huh.NewMultiSelect[string]().
		Title(fieldTitle(f.Name)).
		Description(fieldDescription(f) + " (space to toggle)").
		Options(opts...).
		Value(&v)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// fieldTitle is what a field's box is headed with — f.Name for a capability's
// own input, which is also its CLI flag name and MCP schema property, so the
// three already agree without help. It only does anything for the three
// prefixes this package itself adds to keep a synthesized form's internal
// binding keys from colliding with a real plugin's own field names
// (profileform.go's "profile-"/"credential-" constants, and the "set."
// rename configFields() gives a plugin's own key when it rides inside a
// connection editor) — those prefixes exist for cf.bindings/cf.bools, a map
// keyed across every field on the form at once, and were never meant to be
// read. Before this, they were: a box titled "set.host" or "profile-kube" is
// an implementation detail on screen instead of the word an operator typed.
//
// A real plugin field spelled with one of these prefixes on purpose is not a
// case this codebase has (see the audit behind config.Connection.TunnelTLS's
// naming), and stripping a prefix nothing declared is a no-op, so this is
// safe to apply unconditionally rather than only inside profileform.go.
func fieldTitle(name string) string {
	for _, prefix := range []string{profileSetPrefix, "profile-", "credential-"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest
		}
	}
	return name
}

// commaHint describes a masked box that nonetheless takes a list. A
// SecretSlice cannot carry the completion hint StringSlice gets — nothing is
// completed into a masked field — so the shape has to be said in words or it
// is not said at all.
func commaHint(f plugin.Field) string {
	if f.Type == plugin.SecretSlice {
		return fieldDescription(f) + " (comma-separated)"
	}
	return fieldDescription(f)
}

func fieldDescription(f plugin.Field) string {
	d := f.Help
	extra := string(f.Type)
	if f.Required {
		extra += ", required"
	}
	if d == "" {
		return extra
	}
	return d + " (" + extra + ")"
}

// validatorFor enforces required presence and type shape while typing.
func validatorFor(f plugin.Field) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			if f.Required {
				return fmt.Errorf("%s is required", f.Name)
			}
			return nil
		}
		switch f.Type {
		case plugin.Int:
			if _, err := strconv.Atoi(s); err != nil {
				return fmt.Errorf("must be an integer")
			}
		case plugin.Float:
			if _, err := strconv.ParseFloat(s, 64); err != nil {
				return fmt.Errorf("must be a number")
			}
		}
		return nil
	}
}

// formAdvanceLimit bounds advanceFormBySyntheticEnter's field-by-field
// loop — generous for any form this app builds (capForm's longest is pg's
// connection fields plus a handful more), and a defensive cap rather than
// a number any real form is meant to reach.
const formAdvanceLimit = 32

// settleLimit bounds settleForm's own message-cascade loop — one field's
// worth of huh-internal messaging (see settleForm's comment for why that
// is itself more than one message), generous enough for the deepest
// group this app builds.
const settleLimit = 64

// advanceFormBySyntheticEnter drives f forward exactly as a real, repeated
// Enter press would — the only way to advance a huh.Form field by field
// that still runs each field's own validation.
//
// Deliberately not Form.NextField: that sends nextFieldMsg straight to the
// active group, which blurs the current field (which does validate, on
// every field type) and advances the selector unconditionally — nothing
// checks the result. A real Enter reaches the *focused field's own*
// Update first, and every field type's default keymap matches its
// Next/Submit binding on "enter" (charm.land/huh/v2, keymap.go): Input,
// Select, MultiSelect, Confirm, Text, FilePicker and Note all validate on
// that key and only emit NextField if it passed. Sending the same key
// synthetically is what preserves that gate — bypassing it would mean
// "quickly submit" could silently carry forward whatever partial or
// invalid text a field was left holding.
//
// Stops the moment the currently focused field reports an error — the
// same thing a person pressing Enter on it would see — or once the form
// completes or aborts. A two-stage prefill form stops at the boundary
// for a more basic reason than any of that: afterFormUpdate builds the
// second stage's capForm (and its *huh.Form) only once the first
// completes, so there is nothing of the second stage yet for this
// function to have advanced through — the caller drives it again,
// separately, against whatever afterFormUpdate swapped in.
func advanceFormBySyntheticEnter(f *huh.Form) *huh.Form {
	for i := 0; i < formAdvanceLimit && f.State == huh.StateNormal; i++ {
		f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyEnter})
		if len(f.Errors()) > 0 {
			break
		}
	}
	return f
}

// settleForm feeds msg through f and, for as long as doing so produces a
// further Cmd, runs that Cmd and feeds its own resulting Msg through f in
// turn — the settling a real tea.Program gives every message for free (a
// Cmd's effect lands only once its Msg reaches Update) and a single,
// direct call to Form.Update does not.
//
// This is not a hypothetical gap: submitting a huh field on its own Enter
// key does not flip the form's State within that one Update call. It
// returns a Cmd (NextField) whose *message*, once fed back through
// Update, is what actually advances the group's selector; advancing past
// a group's *last* field needs a second such round (nextGroupMsg) before
// the form's own State finally changes. Both are ordinary huh-internal
// messages, unexported and invisible outside this package, so there is no
// way to fast-forward past them except running the same loop a real
// Program does: execute what Update asks for, and feed the result back in
// (BatchMsg fans out to more than one such result at once — walked here
// rather than left to bubbletea's own goroutine-per-command executor,
// whose ordering across *separate* messages is exactly the thing this
// function exists to not depend on).
func settleForm(f *huh.Form, msg tea.Msg) *huh.Form {
	queue := []tea.Msg{msg}
	for i := 0; i < settleLimit && len(queue) > 0; i++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		model, cmd := f.Update(next)
		if nf, ok := model.(*huh.Form); ok {
			f = nf
		}
		if cmd == nil {
			continue
		}
		resolved, ok := resolveCmd(cmd)
		if !ok {
			continue
		}
		if batch, ok := resolved.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if m, ok := resolveCmd(sub); ok {
					queue = append(queue, m)
				}
			}
			continue
		}
		queue = append(queue, resolved)
	}
	return f
}

// resolveCmdBudget bounds how long settleForm waits for one Cmd to
// produce its message.
//
// Generous for anything huh's own field-to-field or group-to-group
// advance actually does — validating a string, moving a selector index,
// building a view — which is pure, in-memory and measured in
// microseconds (confirmed against this exact form construction while
// diagnosing the timeout this const exists to apply). Nowhere near the
// interval a cursor's own blink Cmd blocks for — Focus starts one, by
// design perpetual, that a real session leaves running for as long as
// the field stays focused. settleForm has no way to recognize that Cmd
// before calling it: cursor.Blink returns a fresh closure over a new
// context each call, not a stable, comparable function value, so identity
// cannot be checked in advance. Racing it against this budget is what
// stands in for that recognition — it does not care what the slow Cmd
// was, only that it was not part of the cascade this function exists to
// drain, so it is fine to guess wrong for a Cmd this app never happens to
// build; the risk runs the other way, toward a real (if unlikely) 20ms
// validation step being mistaken for a timer, which is why the margin
// here is wide relative to what has actually been measured.
const resolveCmdBudget = 20 * time.Millisecond

// resolveCmd runs cmd and reports whether it produced its message within
// resolveCmdBudget. ok is false for a Cmd that is still running past that
// point — assumed to be a timer or animation command rather than part of
// the cascade settleForm is draining. The goroutine it started is not
// killed; it finishes on its own, sends into a channel large enough that
// the send never blocks whether or not anyone is still receiving, and is
// then collected — exactly as inert as it would be if a real session had
// moved on before a cursor's next blink fired.
func resolveCmd(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(resolveCmdBudget):
		return nil, false
	}
}

// values converts the collected bindings into a typed request value map,
// including anything an earlier stage already collected.
func (cf *capForm) values() map[string]any {
	out := map[string]any{}
	for k, v := range cf.base {
		out[k] = v
	}
	for _, f := range cf.fields {
		// A question nobody was asked has no answer. Nothing depends on this
		// today — the credential editor recomputes what it needs — but a form
		// that hands back a hidden field's default is a form that acts on an
		// answer the operator never gave, which is worth not having.
		if cf.hidden(f.Name) {
			continue
		}
		v, given := cf.answer(f)
		// Checked for every kind of widget, not only the text boxes. The three
		// branches used to return before reaching it, so a Bool the
		// environment had filled in — s3's `tls`, whose own declaration calls
		// plaintext-against-a-configured-HTTPS-endpoint the downgrade it
		// exists to prevent — came back as a caller value and beat the profile
		// layer. Changing the picker from a local MinIO to production then
		// moved the endpoint and left the transport setting behind, which is
		// worse than either half on its own.
		if !given || cf.displayed(f, v) {
			continue
		}
		out[f.Name] = v
	}
	return out
}

// answer is what one widget currently holds, in the type its field declared,
// and whether it holds anything at all.
func (cf *capForm) answer(f plugin.Field) (any, bool) {
	if b, ok := cf.bools[f.Name]; ok {
		return *b, true
	}
	if sl, ok := cf.slices[f.Name]; ok {
		return *sl, len(*sl) > 0
	}
	bound, ok := cf.bindings[f.Name]
	if !ok {
		return nil, false
	}
	raw := strings.TrimSpace(*bound)
	if raw != "" {
		return typedValue(f, raw), true
	}
	// An emptied box answers nothing. The declared default used to stand in
	// here, and it stood in as a *caller* value — which beat the environment
	// the picker names, and in the config editor, where absent means
	// cleared, resurrected the very value the operator had just deleted.
	// Resolve lays the default at its own layer, so silence loses nothing:
	// the run gets the same default when nothing outranks it, which is
	// exactly when something should.
	return nil, false
}

// typedValue converts one box's text to the type its field declared. Shared by
// submit and by the snapshot a suggestion reads, so a Suggest sees a sibling
// exactly as the handler will.
func typedValue(f plugin.Field, raw string) any {
	switch f.Type {
	case plugin.Int:
		v, _ := strconv.Atoi(raw)
		return v
	case plugin.Float:
		v, _ := strconv.ParseFloat(raw, 64)
		return v
	case plugin.StringSlice, plugin.SecretSlice:
		parts := strings.Split(raw, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	default:
		return raw
	}
}

// confirmed reports whether a destructive run was explicitly approved.
func (cf *capForm) confirmed() bool { return cf.confirm == nil || *cf.confirm }
