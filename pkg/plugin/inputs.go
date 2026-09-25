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
// outside its Min and Max — or nil. An input nobody supplied is left to the
// handler, which is where a field with no Default decides what nothing means;
// so is one present as nil, which says the same thing.
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
		where = "the config's plugins." + Namespace(c.ID) + "." + o.key
		change = "`rta explain " + c.ID + "` names the file"
	}
	out := *verr
	out.Message += ", which " + where + " sets"
	if req.Surface() == SurfaceMCP {
		out.Hint = "the operator can change it (" + where + "); an argument naming " +
			f.Name + " overrides it for this call"
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
		return fmt.Sprintf("from %v to %v", f.Min, f.Max)
	case hasLo:
		return fmt.Sprintf("of at least %v", f.Min)
	case hasHi:
		return fmt.Sprintf("of at most %v", f.Max)
	}
	return ""
}

// GuardInputs is c's handler with CheckInputs in front of it, or the handler
// itself when c declares no input, since every input has a shape to hold. The
// registry installs it on every capability it holds, which is what makes the
// check the host's rather than something each surface has to remember.
func GuardInputs(c Capability) Handler {
	run := c.Run
	if run == nil || len(c.Inputs) == 0 {
		return run
	}
	return func(ctx context.Context, req Request) (view.View, error) {
		if verr := CheckInputs(c, req); verr != nil {
			return nil, verr
		}
		return run(ctx, req)
	}
}
