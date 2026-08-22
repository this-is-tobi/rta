package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

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
	confirm  *bool                // non-nil when a destructive confirmation gates the run
	// configTarget is non-empty when completing this form writes a
	// plugins.<section> block instead of running a capability — the plugin
	// config editor (internal/render/tui/pluginconfig.go). It reuses capForm
	// because its fields genuinely are plugin.Field values collected from a
	// plugin's own capabilities; only what happens on submit differs, and
	// configOrigin is what the submit path needs to compute the section
	// heading (bare for a built-in, pinned to the artifact otherwise).
	configTarget string
	configOrigin registry.Origin
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
func newCapForm(c plugin.Capability, fs []plugin.Field, defaults map[string]any, final bool, base map[string]any) *capForm {
	cf := &capForm{
		cap:      c,
		fields:   fs,
		final:    final,
		base:     base,
		bindings: map[string]*string{},
		bools:    map[string]*bool{},
		slices:   map[string]*[]string{},
	}
	// What earlier stages already answered is available to Suggest, so a
	// suggestion can depend on it (the tags on *this* task, not all of them).
	sugCtx, cancel := context.WithTimeout(context.Background(), suggestTimeout)
	defer cancel()
	sugReq := plugin.NewRequest(base, false, false).WithSurface(plugin.SurfaceTUI)

	var fields []huh.Field
	for _, f := range fs {
		f := f
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
			case plugin.StringSlice:
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
				Title(f.Name).
				Description(f.Help).
				Affirmative("yes").Negative("no").
				Value(&v))
		case plugin.Text:
			v := ""
			if f.Default != nil {
				v = fmt.Sprint(f.Default)
			}
			cf.bindings[f.Name] = &v
			fields = append(fields, huh.NewText().
				Title(f.Name).
				// ctrl+e opens $EDITOR on the body, which is how a note gets
				// written in the tool you write in. It works by default; it
				// is said here because an affordance nobody knows about is
				// not an affordance.
				Description(fieldDescription(f)+" — ctrl+e opens $EDITOR").
				ExternalEditor(true).
				Lines(5).
				Value(&v).
				Validate(validatorFor(f)))
		case plugin.StringSlice:
			// A []string default (from a declared Field.Default or a
			// Prefill result, e.g. a task's current tags) must render as
			// "a, b" — fmt.Sprint would print "[a b]" and break re-submission.
			v := ""
			if def, ok := f.Default.([]string); ok {
				v = strings.Join(def, ", ")
			} else if f.Default != nil {
				v = fmt.Sprint(f.Default)
			}
			cf.bindings[f.Name] = &v
			fields = append(fields, suggest(huh.NewInput().
				Title(f.Name).
				Description(fieldDescription(f)+" (comma-separated)").
				Value(&v).
				Validate(validatorFor(f)), f, sugCtx, sugReq))
		case plugin.Path:
			v := ""
			if f.Default != nil {
				v = fmt.Sprint(f.Default)
			}
			cf.bindings[f.Name] = &v
			// Declared candidates are resolved once, here; the filesystem is
			// re-read on every keystroke, which is what makes it a completion
			// rather than a list. huh re-evaluates when the bound value
			// changes, so the binding is the field's own value.
			//
			// The value is reached through a locked accessor rather than a
			// bare pointer, because these two touch it from different
			// goroutines: huh writes it on the event loop as you type, and
			// runs the suggestion function in a bubbletea command. A plain
			// `Value(&v)` here is a data race on a string — caught by
			// `go test -race`, and quite capable of tearing a header in a
			// real session.
			typed := &syncString{value: v, mirror: &v}
			declared := candidateValues(f, sugCtx, sugReq)
			fields = append(fields, huh.NewInput().
				Title(f.Name).
				Description(fieldDescription(f)+" — tab completes paths").
				Accessor(typed).
				SuggestionsFunc(func() []string { return pathSuggestions(typed.Get(), declared) }, &v).
				Validate(validatorFor(f)))
		case plugin.Secret:
			v := ""
			if f.Default != nil {
				v = fmt.Sprint(f.Default)
			}
			cf.bindings[f.Name] = &v
			fields = append(fields, huh.NewInput().
				Title(f.Name).
				Description(fieldDescription(f)).
				EchoMode(huh.EchoModePassword).
				Value(&v).
				Validate(validatorFor(f)))
		default:
			v := ""
			if f.Default != nil {
				v = fmt.Sprint(f.Default)
			}
			cf.bindings[f.Name] = &v
			fields = append(fields, suggest(huh.NewInput().
				Title(f.Name).
				Description(fieldDescription(f)).
				Value(&v).
				Validate(validatorFor(f)), f, sugCtx, sugReq))
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
	cf.form = huh.NewForm(huh.NewGroup(fields...))
	return cf
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

// suggest attaches what exists right now to a free-text input: tab completes
// it, and nothing is taken away — a suggestion list is not a validator.
func suggest(in *huh.Input, f plugin.Field, ctx context.Context, req plugin.Request) *huh.Input {
	values := candidateValues(f, ctx, req)
	if len(values) == 0 {
		return in
	}
	return in.Suggestions(values)
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
	v := ""
	if f.Default != nil {
		v = fmt.Sprint(f.Default)
	}
	cf.bindings[f.Name] = &v

	opts := make([]huh.Option[string], 0, len(f.Options)+1)
	if !f.Required && f.Default == nil {
		opts = append(opts, huh.NewOption("(none)", ""))
	}
	for _, o := range f.Options {
		opts = append(opts, huh.NewOption(o, o))
	}
	return huh.NewSelect[string]().
		Title(f.Name).
		Description(fieldDescription(f)).
		Options(opts...).
		Value(&v)
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
		Title(f.Name).
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
		if b, ok := cf.bools[f.Name]; ok {
			out[f.Name] = *b
			continue
		}
		if sl, ok := cf.slices[f.Name]; ok {
			if len(*sl) > 0 {
				out[f.Name] = *sl
			}
			continue
		}
		raw := strings.TrimSpace(*cf.bindings[f.Name])
		if raw == "" {
			if f.Default != nil {
				out[f.Name] = f.Default
			}
			continue
		}
		switch f.Type {
		case plugin.Int:
			v, _ := strconv.Atoi(raw)
			out[f.Name] = v
		case plugin.Float:
			v, _ := strconv.ParseFloat(raw, 64)
			out[f.Name] = v
		case plugin.StringSlice:
			parts := strings.Split(raw, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			out[f.Name] = parts
		default:
			out[f.Name] = raw
		}
	}
	return out
}

// confirmed reports whether a destructive run was explicitly approved.
func (cf *capForm) confirmed() bool { return cf.confirm == nil || *cf.confirm }
