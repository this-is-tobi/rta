package plugin

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// What a declaration says an input accepts, held to by the host on every
// surface, before any handler runs.
//
// Two declared constraints, and each was kept by some surfaces and not
// others. Options "enumerates every value this input accepts": the TUI offered
// a picker and MCP an enum, but the CLI and the operator's config passed
// whatever was written, so each handler decided alone what an unlisted value
// meant — net.dns refused one by name while `gen token --encoding b64` printed
// hex and `net listen --proto tpc` said nothing was listening. Min and Max were
// held to everywhere, by clamping: which kept a handler from ever seeing
// `--timeout 0`, the value that once took `mcp serve` down, and answered
// questions nobody asked — `net listen --port 70000` reported on port 65535,
// and `sys ps --limit 0` printed one row.
//
// Both are refusals now, naming what was declared. A value that names an
// option in another case has already been rewritten to the declared spelling
// by Resolve, so what reaches this is a value naming none of them; and a
// number from the operator's config or a profile has already been held inside
// this capability's range by Resolve, because one key there serves several
// capabilities that bound it differently (clampInt has the cases). What is
// refused for its range is what the caller sent on this call.
//
// And a third thing, which neither constraint covered: a value the input's
// accessor cannot read as its type. Every accessor is a type assertion, so
// such a value is not refused and not ignored — it is read as the zero.
// Request.Int reads a string, a boolean or an integer past what int holds as
// 0, so a check that let those through — "there is nothing here to hold them
// to" — handed the handler the one value the bounds exist to keep from it:
// `net ping` over MCP with a timeout of 2^63 passed the schema (int64 of it
// saturates on arm64, so the integer check held), was left alone as a number
// that does not fit, and reached time.NewTicker(0), and `rta mcp serve`
// exited on one call. Request.String reads a boolean as "", which is how
// mysql's `tls: true` in the config — a String offering false, preferred,
// true and skip-verify — reached go-sql-driver as no TLS at all; and
// Request.Bool reads `tls: "true"` or `tls: yes` as false, which ran a redis
// connection, AUTH password and all, in plaintext. Every shape is refused
// now, on every type.

// CheckInputs reports the first input whose value is outside what its field
// declares — of a shape its accessor cannot read, not one of its Options, or
// outside its Min and Max — or nil. An input nobody supplied is not its
// question, nor one present as nil, which says the same thing: whether the
// call may go without it is CheckRequired's, and for an input the call may go
// without, the handler is where a field with no Default decides what nothing
// means. Kept apart because this one is also asked of a single value, before
// the rest of the call is known (internal/toolcall).
func CheckInputs(c Capability, req Request) *view.Error {
	values := req.Values()
	for _, f := range c.Inputs {
		v, present := values[f.Name]
		if !present || v == nil {
			continue
		}
		verr, how := checkShape(c, f, v)
		if verr == nil {
			verr = checkOptions(c, f, v)
		}
		if verr == nil {
			verr = checkBoundsOf(c, f, v)
		}
		if verr != nil {
			return fromSource(verr, how, c, f, req)
		}
	}
	return nil
}

// fromSource readdresses a refusal of a value the caller did not send.
// Worded for the caller, it read as a flag somebody typed — `encoding: b64`
// in the config answered a bare `rta gen token` with "gen.token takes one of
// hex, base64, base64url, base32 for encoding, not "b64"" — and its hint
// sent them to `rta explain` for a set the message had already named, while
// the file holding the value went unmentioned. Over MCP it told an agent
// about a value only the operator can change.
//
// So it says which layer set the value, and the hint says who can change it
// and how the caller can step round it for one call: a value given on the
// call beats both layers. Only a Request built by ResolveRequest knows; one
// built from a bare map is refused in the caller's words as before.
//
// Except for a Local input over MCP, which is nearly every connection
// setting: the bridge strips an argument naming one unread, so "an argument
// naming port overrides it for this call" sent an agent to retry with an
// input its schema hides, into the identical refusal. Only the operator can
// change that one, and the hint says so and no more.
//
// how is what to write there instead, for a value whose shape is the
// problem — "write it there as a bare number, without quotes" — and "" for
// one whose content is, where "change it there" is the whole instruction.
// The caller's hint said it and this replaced it, so the one sentence the
// operator needed was dropped from the case it was written for.
func fromSource(verr *view.Error, how string, c Capability, f Field, req Request) *view.Error {
	o, ok := req.origins[f.Name]
	if !ok {
		return verr
	}
	where := "the profile " + strconv.Quote(o.profile)
	change := "`rta profile show " + o.profile + "` shows the block it is set in"
	if o.profile == "" {
		where, change = "the profile in use", "`rta profile list` names it"
	}
	if o.key != "" {
		section := o.section
		if section == "" {
			section = Namespace(c.ID)
		}
		where = "the config's plugins." + section + "." + o.key
		change = "`rta explain " + c.ID + "` names the file"
	}
	out := *verr
	out.Message += ", which " + where + " sets"
	if req.Surface() == SurfaceMCP {
		out.Hint = "the operator can change it (" + where + "); an argument naming " +
			f.Name + " overrides it for this call"
		if f.Local {
			out.Hint = "only the operator can change it (" + where + ")"
		}
		return &out
	}
	if how == "" {
		how = "change it there"
	}
	out.Hint = how + " — " + change + " — or give " + f.Name +
		" on the call to override it for one run"
	return &out
}

// checkShape refuses a value the field's accessor cannot read as its type,
// which it would hand the handler as the zero. Refused, never coerced, for
// StatedTypeProblem's reason: reading "yes" as true is a guess about a value
// that may decide whether a connection is encrypted, and the operator is the
// one who knows what they meant.
//
// Named by the value's shape, never by the value: this runs over the
// operator's config and profiles, and a file is one mistyped block away from
// a credential. how is fromSource's, what to write in the file instead.
func checkShape(c Capability, f Field, v any) (verr *view.Error, how string) {
	var want, give string
	switch f.Type {
	case Int, Float:
		return checkNumber(c, f, v)
	case String, Text, Path, Secret:
		if _, ok := v.(string); ok {
			return nil, ""
		}
		want = "text"
		give, how = "give it as text — in YAML, quoted, so it is not read as a number, a boolean or a date",
			"quote it there"
	case Bool:
		if _, ok := v.(bool); ok {
			return nil, ""
		}
		want = "true or false"
		give, how = "give it as `true` or `false`, unquoted — a quoted `\"true\"` is text, and so is a bare `yes`",
			"write it there unquoted, as `true` or `false`"
	case StringSlice, SecretSlice:
		switch v.(type) {
		case []string, []any, string:
			return nil, ""
		}
		want = "a list"
		give, how = "give it as a list, `[a, b]`, or as one value", "write it there as `[a, b]`, or as one value"
	default:
		// A type Validate would have refused. No opinion, as
		// StatedTypeProblem has none.
		return nil, ""
	}
	return view.Errorf("core.input.type", "%s takes %s for %s, not %s", c.ID, want, f.Name, statedShape(v)).
		WithHint(give), how
}

// checkNumber refuses a value for an Int or a Float that no accessor reads as
// a number, which Request.Int would hand the handler as the zero —
// core.input.range when the field is bounded, core.input.type when it is not.
//
// Worded by what the value is, not by the range. `limit: "3"` in the config
// was told sys.ps "takes a limit from 1 to 1000, not "3"": 3 is inside that
// range, and the quotes — the one thing wrong — were left for the operator to
// spot, who would sooner edit the number. The CLI's flag parser and MCP's
// type check refuse text before this runs, so text reaching it came from a
// file, and is named by its shape: never echoed, since a file is one
// mistyped block away from a credential. A number is shown, since what is
// wrong with it is its value.
//
// how is fromSource's: what to write in the file instead.
func checkNumber(c Capability, f Field, v any) (verr *view.Error, how string) {
	var readable bool
	want := "a whole number"
	switch f.Type {
	case Int:
		_, readable = toInt(v)
	case Float:
		_, readable = toFloat(v)
		want = "a number"
	default:
		return nil, ""
	}
	if readable {
		return nil, ""
	}
	code := "core.input.type"
	if bounds := f.Bounds(); bounds != "" {
		code, want = "core.input.range", want+" "+bounds
	}
	got := statedShape(v)
	if f.Type == Int && fractional(v) {
		return view.Errorf(code, "%s takes %s for %s, not %v", c.ID, want, f.Name, v).
			WithHint("give it as a whole number"), "write it there as a whole number"
	}
	if got == "a number" {
		return view.Errorf(code, "%s takes %s for %s, not %v", c.ID, want, f.Name, v).
			WithHint("`rta explain " + c.ID + "` names what it takes beside the input"), ""
	}
	bare := "a bare number"
	if got == "text" {
		bare += ", without quotes"
	}
	return view.Errorf(code, "%s takes %s for %s, not %s", c.ID, want, f.Name, got).
		WithHint("give it as " + bare), "write it there as " + bare
}

func checkOptions(c Capability, f Field, given any) *view.Error {
	if len(f.Options) == 0 {
		return nil
	}
	for _, v := range optionValues(f, given) {
		if v == "" || slices.Contains(f.Options, v) {
			continue
		}
		return view.Errorf("core.input.option", "%s takes one of %s for %s, not %q",
			c.ID, strings.Join(f.Options, ", "), f.Name, v).
			WithHint("the set is closed: `rta explain " + c.ID + "` lists it beside the input")
	}
	return nil
}

// optionValues is v as the strings an Options list is compared with: what the
// field's accessor reads it as. Only the two text types, because Validate
// refuses Options on any other. Validate reads a Default through this too, so
// a declaration cannot default to a value it would refuse.
//
// Nothing for a value the accessor would not read as the declared type:
// checkShape has refused it before this is asked.
func optionValues(f Field, v any) []string {
	switch f.Type {
	case String:
		if s, ok := v.(string); ok {
			return []string{s}
		}
	case StringSlice:
		return stringSlice(v)
	}
	return nil
}

func checkBoundsOf(c Capability, f Field, v any) *view.Error {
	if want, ok := f.Range(v); !ok {
		return view.Errorf("core.input.range", "%s takes a %s %s, not %v", c.ID, f.Name, want, v).
			WithHint("the range is declared: `rta explain " + c.ID + "` names it beside the input")
	}
	return nil
}

// Range reports whether v is inside f's Min and Max, and when it is not, what
// the declaration allows — "from 1 to 65535", "of at least 1". A field that is
// not numeric or declares no bound is in range: there is nothing here to hold
// a value to. A value no accessor reads as a number is outside a bounded
// field's range, because the zero it would be read as is not a value anybody
// sent, and may well be the one the bound exists to keep out.
//
// One definition for every place that holds a value to its bounds: the host,
// before a handler runs, and a TUI form, as the value is typed.
func (f Field) Range(v any) (want string, ok bool) {
	bounds := f.Bounds()
	if bounds == "" {
		return "", true
	}
	var n float64
	switch f.Type {
	case Int:
		i, isInt := toInt(v)
		if !isInt {
			return bounds, false
		}
		n = float64(i)
	case Float:
		x, isFloat := toFloat(v)
		if !isFloat {
			return bounds, false
		}
		n = x
	}
	lo, hasLo := toFloat(f.Min)
	hi, hasHi := toFloat(f.Max)
	if (!hasLo || n >= lo) && (!hasHi || n <= hi) {
		return "", true
	}
	return bounds, false
}

// Bounds is what f's Min and Max allow, in the words a refusal uses — "from
// 1 to 65535", "of at least 1" — or "" for a field that is not numeric or
// declares neither. `rta explain` prints it beside the input, which is where
// every range refusal sends the person reading it.
func (f Field) Bounds() string {
	if f.Type != Int && f.Type != Float {
		return ""
	}
	_, hasLo := toFloat(f.Min)
	_, hasHi := toFloat(f.Max)
	switch {
	case hasLo && hasHi:
		return "from " + NumberText(f.Min) + " to " + NumberText(f.Max)
	case hasLo:
		return "of at least " + NumberText(f.Min)
	case hasHi:
		return "of at most " + NumberText(f.Max)
	}
	return ""
}

// NumberText spells a declared number the way a person types it back. %v
// writes a float64 of a million — which is what an untyped 1e6 in a
// declaration arrives as — as "1e+06", a spelling no flag parser takes, so a
// hint quoting it would hand the person a value they cannot type. A whole
// number is written as one, from its integer form so a bound near the int64
// edge keeps every digit, and anything else in the shortest form that reads
// back the same.
func NumberText(v any) string {
	if i, ok := toInt(v); ok {
		return strconv.Itoa(i)
	}
	if n, ok := toFloat(v); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// What a call must have, held to by the host on every surface, before any
// handler runs.
//
// Each surface held presence its own way, and nothing stood behind them:
// cobra's required flags and the positional arity on the CLI, a check of
// the resolved values there for an input the config can fill, the schema's
// "required" list over MCP, a validator on each box in a TUI form. The TUI's
// list picker had no validator, so enter submitted a form with nothing
// picked, and the handler ran without an input the CLI and MCP refuse to
// leave out — reading it, as every accessor reads an input nobody gave, as
// the zero. The surfaces keep their own checks, because each refuses sooner
// and in its own medium: at parse time with the usage beside it, before a
// grant is spent, in the box as it is typed. This is what holds a surface
// that forgot.
//
// Asked of the values the handler is about to run with, after every layer —
// the caller, the profile and the forward it opens, the host's environment,
// the config, the declared default — so a layer that fills an input fills it
// before this looks, and nothing after this fills one: the handler is next.

// RequiredOn reports whether a call arriving through s must carry a value for
// f: a Required input on every surface, and a Piped one on the surfaces with
// no pipe behind them — MCP, whose standard input is the agent's request
// stream, and the TUI, which owns the terminal. The CLI reads the pipe when a
// Piped input is left out, and an in-process caller is left to the handler,
// which reads no pipe off the CLI and answers an empty one itself.
func (f Field) RequiredOn(s Surface) bool {
	if f.Required {
		return true
	}
	return f.Piped && (s == SurfaceMCP || s == SurfaceTUI)
}

// Missing is every input a call arriving through s must carry and does not,
// in declared order: absent, nil, empty text, or a list with nothing in it.
// Each of those reaches a handler exactly as an input nobody gave — String
// reads "" and StringSlice reads nil for all four — so a check that counted
// an empty one as present held the handler to nothing it could tell apart.
// A number or a switch is never empty: 0 and false are values somebody gave.
func Missing(c Capability, values map[string]any, s Surface) []Field {
	var out []Field
	for _, f := range c.Inputs {
		if !f.RequiredOn(s) {
			continue
		}
		if v, present := values[f.Name]; present && !empty(v) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func empty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}

// CheckRequired reports the first input a request must carry and does not —
// see Missing — or nil, named the way the request's surface names it.
func CheckRequired(c Capability, req Request) *view.Error {
	if missing := Missing(c, req.Values(), req.Surface()); len(missing) > 0 {
		return MissingInput(c, missing[0], req.Surface())
	}
	return nil
}

// MissingInput is the host's refusal of a call arriving through s without f.
// One code on every surface, core.input.missing, beside core.input.type,
// .option and .range: the CLI's refusal of a config-backed input nothing
// supplied already had it, and MCP's refusal of a left-out argument answered
// the same mistake with core.mcp.badargs — the code for a value of the wrong
// shape — so a caller branching on the code heard two different mistakes.
//
// The input is named as the surface names it, because the name is what the
// reader has to type back: --host or <key> at a terminal, the argument "key"
// to an agent, the box in a form. And the hint says who can give it. An agent
// is never told to pass a Local input: the schema hides it and the bridge
// drops it unread, so "pass it" would send the agent round into this same
// refusal, and only the operator can supply one.
func MissingInput(c Capability, f Field, s Surface) *view.Error {
	const code = "core.input.missing"
	config := ""
	if f.Config != "" {
		config = ", or set " + f.Config + " in your rta config"
	}
	switch s {
	case SurfaceCLI:
		if f.Positional {
			return view.Errorf(code, "%s needs <%s>", c.ID, f.Name).
				WithHint("give it as an argument — `rta " + strings.Join(c.Words(), " ") +
					" --help` says where")
		}
		hint := "pass --" + f.Name + config
		if f.Local && f.EnvFallback {
			hint += ", or export $" + LocalEnvVar(c.ID, f.Name)
		}
		return view.Errorf(code, "%s needs --%s", c.ID, f.Name).WithHint(hint)
	case SurfaceMCP:
		if f.Local {
			// With neither a config key nor a variable, nothing but a
			// person at a terminal gives it — as a flag or an argument,
			// whichever it is, so the hint names neither.
			hint := "ask the operator to run it from their own terminal"
			switch {
			case f.Config != "":
				hint = "ask the operator to set it in the rta config"
			case f.EnvFallback:
				hint = "ask the operator to set it in the environment rta mcp serve runs in"
			}
			return view.Errorf(code, "%s needs %s, which only the operator can give", c.ID, f.Name).
				WithHint(hint)
		}
		return view.Errorf(code, "%s needs the argument %q", c.ID, f.Name).
			WithHint(fmt.Sprintf("pass %q in the arguments", f.Name))
	case SurfaceTUI:
		return view.Errorf(code, "%s needs %s", c.ID, f.Name).
			WithHint("fill in the " + f.Name + " box" + config)
	}
	return view.Errorf(code, "%s needs %s", c.ID, f.Name)
}

// CheckRequest is everything GuardInputs holds a call to — what it must carry,
// then what each value declares — for a caller that has to refuse before the
// handler rather than through it: the MCP bridge, which refunds a grant's use
// for a call no handler ran, and sdktest, which drives a declaration with no
// registry in front of it.
func CheckRequest(c Capability, req Request) *view.Error {
	if verr := CheckRequired(c, req); verr != nil {
		return verr
	}
	return CheckInputs(c, req)
}

// GuardInputs is c's handler with CheckRequest in front of it, or the handler
// itself when c declares no input, since there is then nothing to carry and
// nothing to hold. The registry installs it on every capability it holds,
// which is what makes the check the host's rather than something each
// surface has to remember.
func GuardInputs(c Capability) Handler {
	run := c.Run
	if run == nil || len(c.Inputs) == 0 {
		return run
	}
	return func(ctx context.Context, req Request) (view.View, error) {
		if verr := CheckRequest(c, req); verr != nil {
			return nil, verr
		}
		return run(ctx, req)
	}
}
