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
// by Resolve, so what reaches this is a value naming none of them.

// CheckInputs reports the first input whose value is outside what its field
// declares — not one of its Options, or outside its Min and Max — or nil. An
// input nobody supplied is left to the handler, which is where a field with no
// Default decides what nothing means, and so is a number that is not one: the
// typed accessors already read that as the zero.
func CheckInputs(c Capability, req Request) *view.Error {
	values := req.Values()
	for _, f := range c.Inputs {
		v, present := values[f.Name]
		if !present {
			continue
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
		return view.Errorf("core.input.range", "%s takes a %s %s, not %v", c.ID, f.Name, want, v).
			WithHint("the range is declared: `rta explain " + c.ID + "` names it beside the input")
	}
	return nil
}

// Range reports whether v is inside f's Min and Max, and when it is not, what
// the declaration allows — "from 1 to 65535", "of at least 1". A value that is
// not a number, and a field that is not numeric or declares no bound, are in
// range: there is nothing here to hold them to.
//
// One definition for every place that holds a value to its bounds: the host,
// before a handler runs, and a TUI form, as the value is typed.
func (f Field) Range(v any) (want string, ok bool) {
	if f.Min == nil && f.Max == nil {
		return "", true
	}
	var n float64
	switch f.Type {
	case Int:
		i, isInt := toInt(v)
		if !isInt {
			return "", true
		}
		n = float64(i)
	case Float:
		x, isFloat := toFloat(v)
		if !isFloat {
			return "", true
		}
		n = x
	default:
		return "", true
	}
	lo, hasLo := toFloat(f.Min)
	hi, hasHi := toFloat(f.Max)
	if (!hasLo || n >= lo) && (!hasHi || n <= hi) {
		return "", true
	}
	switch {
	case hasLo && hasHi:
		return fmt.Sprintf("from %v to %v", f.Min, f.Max), false
	case hasLo:
		return fmt.Sprintf("of at least %v", f.Min), false
	}
	return fmt.Sprintf("of at most %v", f.Max), false
}

// GuardInputs is c's handler with CheckInputs in front of it, or the handler
// itself when no input declares Options or bounds. The registry installs it on
// every capability it holds, which is what makes the check the host's rather
// than something each surface has to remember.
func GuardInputs(c Capability) Handler {
	run := c.Run
	constrained := slices.ContainsFunc(c.Inputs, func(f Field) bool {
		return len(f.Options) > 0 || f.Min != nil || f.Max != nil
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
