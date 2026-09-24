package plugin

import (
	"context"
	"fmt"
	"slices"
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
// And a third thing, which neither constraint covered: a value for a number
// that is not one an accessor can read. Request.Int reads a string, a boolean
// or an integer past what int holds as 0, so a check that let those through —
// "there is nothing here to hold them to" — handed the handler the one value
// the bounds exist to keep from it. `net ping` over MCP with a timeout of
// 2^63 passed the schema (int64 of it saturates on arm64, so the integer
// check held), was left alone as a number that does not fit, and reached
// time.NewTicker(0): `rta mcp serve` exited on one call.

// CheckInputs reports the first input whose value is outside what its field
// declares — a number no accessor can read, not one of its Options, or
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
		if verr := checkNumber(c, f, v); verr != nil {
			return verr
		}
		if verr := checkOptions(c, f, req); verr != nil {
			return verr
		}
		if verr := checkBoundsOf(c, f, v); verr != nil {
			return verr
		}
	}
	return nil
}

// checkNumber refuses a value for an Int or a Float that the accessor would
// read as the zero. core.input.range when the field is bounded, because what
// the caller needs to know is the range; core.input.type when it is not.
func checkNumber(c Capability, f Field, v any) *view.Error {
	var readable bool
	want := "a whole number for " + f.Name
	switch f.Type {
	case Int:
		_, readable = toInt(v)
	case Float:
		_, readable = toFloat(v)
		want = "a number for " + f.Name
	default:
		return nil
	}
	if readable {
		return nil
	}
	if bounds := f.Bounds(); bounds != "" {
		return view.Errorf("core.input.range", "%s takes a %s %s, not %s", c.ID, f.Name, bounds, shown(v)).
			WithHint("the range is declared: `rta explain " + c.ID + "` names it beside the input")
	}
	return view.Errorf("core.input.type", "%s takes %s, not %s", c.ID, want, shown(v)).
		WithHint("`rta explain " + c.ID + "` says what each input takes")
}

// shown is a refused value the way a refusal quotes it: text in quotes, so
// the string "0" is not mistaken for the number it looks like.
func shown(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprint(v)
}

func checkOptions(c Capability, f Field, req Request) *view.Error {
	if len(f.Options) == 0 {
		return nil
	}
	var values []string
	switch f.Type {
	case String:
		values = []string{req.String(f.Name)}
	case StringSlice:
		values = req.StringSlice(f.Name)
	default:
		return nil
	}
	for _, v := range values {
		if v == "" || slices.Contains(f.Options, v) {
			continue
		}
		return view.Errorf("core.input.option", "%s takes one of %s for %s, not %q",
			c.ID, strings.Join(f.Options, ", "), f.Name, v).
			WithHint("the set is closed: `rta explain " + c.ID + "` lists it beside the input")
	}
	return nil
}

func checkBoundsOf(c Capability, f Field, v any) *view.Error {
	if want, ok := f.Range(v); !ok {
		return view.Errorf("core.input.range", "%s takes a %s %s, not %s", c.ID, f.Name, want, shown(v)).
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
// itself when no input declares Options or bounds, or is a number. The
// registry installs it on every capability it holds, which is what makes the
// check the host's rather than something each surface has to remember.
func GuardInputs(c Capability) Handler {
	run := c.Run
	constrained := slices.ContainsFunc(c.Inputs, func(f Field) bool {
		return len(f.Options) > 0 || f.Min != nil || f.Max != nil || f.Type == Int || f.Type == Float
	})
	if run == nil || !constrained {
		return run
	}
	return func(ctx context.Context, req Request) (view.View, error) {
		if verr := CheckInputs(c, req); verr != nil {
			return nil, verr
		}
		return run(ctx, req)
	}
}
