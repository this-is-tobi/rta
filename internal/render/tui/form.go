package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	huh "charm.land/huh/v2"

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
	base     map[string]any // values collected by an earlier stage
	final    bool           // completing this form runs the capability
	bindings map[string]*string
	bools    map[string]*bool
	slices   map[string]*[]string // multi-select bindings
	confirm  *bool                // non-nil when a destructive confirmation gates the run
}

// hasInputs reports whether opening this capability warrants a form: any
// field the user might want to set (required or optional).
func hasInputs(c plugin.Capability) bool { return len(c.Inputs) > 0 }

// newCapForm builds the huh form for a subset of a capability's inputs.
// defaults (from Prefill) override declared field defaults. Destructive
// capabilities get a trailing explicit confirmation on their final stage.
func newCapForm(c plugin.Capability, fs []plugin.Field, defaults map[string]any, final bool, base map[string]any) *capForm {
	cf := &capForm{
		cap:      c,
		fields:   fs,
		final:    final,
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
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		values = append(values, plugin.CandidateValue(entry))
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
