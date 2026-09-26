// Package toolcall is the grammar of a capability call a *model* makes —
// the argument validation, the required check, and the JSON Schema the
// arguments were published under.
//
// It sits upstream of internal/mcp rather than inside it, because this is
// the boundary grammar rather than one channel's implementation of it: a
// second channel putting capability calls in front of a model would have to
// hold to the same rules, and the way to guarantee that is for the rules to
// have one home. rta shipped such a second channel once, and the grammar
// moved here when it did.
package toolcall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Holding a call to the schema it was shown: every argument an agent sent
// is validated against the declaration before anything runs, and refusals
// name what was accepted rather than only what was not.

// Decode reads a call's arguments with every number exact: an integer int64
// holds arrives as an int64, any other number a float64 holds as a float64,
// and a whole number past int64 as the literal that was sent.
//
// json.Unmarshal hands every number over as a float64, which holds an
// integer exactly only up to 2^53, and the refusals that followed quoted
// what the float64 had made of it. int64's largest value rounded up to 2^63
// and was refused as "past what an integer holds", although the CLI reads
// it and refuses it by its range; one below int64's smallest rounded to the
// smallest and was refused as that, a number the caller never sent. Kept as
// the literal, a number nothing reads is refused by the host's own rule,
// quoting it.
//
// A nil map for `null`, as json.Unmarshal gives: the caller decides what an
// absent object means.
func Decode(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var values map[string]any
	if err := dec.Decode(&values); err != nil {
		return nil, err
	}
	// A Decoder stops after the first value where json.Unmarshal refused
	// anything after it, and the two must read one body the same way.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the arguments object is followed by more")
	}
	for name, v := range values {
		if n, ok := v.(json.Number); ok {
			values[name] = exact(n)
		}
	}
	return values, nil
}

// exact is n as the Go number the host reads it as, or n itself when no Go
// number holds it the way it was written.
func exact(n json.Number) any {
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err == nil {
		return i
	}
	if errors.Is(err, strconv.ErrRange) {
		return n
	}
	x, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || (x == math.Trunc(x) && (x < math.MinInt64 || x >= math.MaxInt64)) {
		return n
	}
	return x
}

// Validate checks every argument a model-facing caller actually sent
// against the declaration. Exported because the boundary grammar has one
// home rather than one copy per channel: a fix here lands on every caller
// by construction.
//
// Two kinds of refusal, told apart by their codes. A value of the wrong
// shape — a string where the schema says integer, an argument the tool does
// not have — is core.mcp.badargs: a call the published schema already
// ruled out. A value of the right shape that the declaration still does not
// take — none of the Options, outside Min and Max — is the host's own
// refusal, plugin.CheckInputs', with the code and the words the CLI answers
// the same mistake with. It is checked here, on what was sent, rather than
// left to the guard in front of the handler: that guard runs after the
// grant gate, so a call it was always going to refuse spent a --max-uses
// grant and asked the operator to approve it first.
//
// A Local field is never type-checked: it is stripped regardless of what
// arrived, so validating a value about to be discarded would only produce a
// confusing error about a field the model does not even know exists.
func Validate(c plugin.Capability, values map[string]any) *view.Error {
	declared := make(map[string]bool, len(c.Inputs)+1)
	check := func(f plugin.Field) *view.Error {
		v, given := values[f.Name]
		if !given {
			return nil
		}
		if err := checkFieldType(f, v); err != nil {
			return view.Errorf("core.mcp.badargs", "%s: %v", f.Name, err).
				WithHint(fmt.Sprintf("%s expects %s", f.Name, SchemaTypeName(f.Type)))
		}
		return held(c, f, v)
	}
	for _, f := range c.Inputs {
		declared[f.Name] = true
		if f.Local {
			continue
		}
		if verr := check(f); verr != nil {
			return verr
		}
	}
	// "detail" arrives under a name no Field declares — the host injects it
	// for every Detailed capability and InputSchema publishes it as a
	// boolean. Refusing it as unknown would break the only way an agent can
	// ask for a detail view, on every Detailed capability at once. It gets
	// the same scrutiny a declared boolean would: {"detail": "true"} reaches
	// Request.Bool, which reads a string as false, so the compact summary
	// came back looking exactly like an honoured request for the full page.
	if c.Detailed {
		declared["detail"] = true
		if verr := check(plugin.Field{Name: "detail", Type: plugin.Bool}); verr != nil {
			return verr
		}
	}
	// "profile" arrives the same way and for the same reason: the host owns
	// the name, no Field declares it, and InputSchema publishes it wherever an
	// operator has a connection configured. Type-checked as a string here so
	// {"profile": 3} is an error rather than a value takeProfile has to decide
	// about, and accepted whether or not the schema advertised it — a caller
	// naming a profile on an install that has none is answered by the gate
	// (which refuses it for want of a grant), not by a message about the
	// argument, so that the operator's inventory stays out of the reply.
	if plugin.Profilable(c) {
		declared["profile"] = true
		if verr := check(plugin.Field{Name: "profile", Type: plugin.String}); verr != nil {
			return verr
		}
	}
	// Everything left is a name this tool does not have. Accepting it
	// silently made a one-character typo indistinguishable from a deliberate
	// call: sys_ps {"limt": 3} answered with every process on the machine at
	// the default limit, isError unset, so a model read a complete answer to
	// a question it never asked. A Local field's name is declared and so
	// survives this check — it is not a typo but a guess at a credential, and
	// the answer to a guess is to drop the value unread, which handler does a
	// moment later. Refusing it instead would confirm to the model that the
	// input the schema deliberately hides is there.
	var unknown []string
	for name := range values {
		if !declared[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// A Go map iterates in a different order every run, and the same wrong
	// call answering with its mistakes in a different order each time is
	// noise for whoever has to read two of them.
	sort.Strings(unknown)
	quoted := make([]string, len(unknown))
	for i, name := range unknown {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	plural := ""
	if len(unknown) > 1 {
		plural = "s"
	}
	return view.Errorf("core.mcp.badargs", "unknown argument%s: %s", plural, strings.Join(quoted, ", ")).
		WithHint(acceptedHint(c))
}

// held is the host's refusal of v for f, or nil: plugin.CheckInputs over
// this one value, so the rule and its wording have the one home the guard in
// front of every handler already uses.
//
// The option miss keeps a hint of its own. The enum is held exactly here,
// though the CLI and a config file take an option in another case and spell
// it as declared: the schema published it, and a client validating against
// it would have refused "HEX" before it was sent — while the host's hint
// sends the reader to `rta explain` for a set the message has already named.
func held(c plugin.Capability, f plugin.Field, v any) *view.Error {
	verr := plugin.CheckInputs(c, plugin.NewRequest(map[string]any{f.Name: v}, false, false))
	if verr != nil && verr.Code == "core.input.option" {
		return verr.WithHint(f.Name + " takes one of " + strings.Join(f.Options, ", ") + ", spelled exactly as listed")
	}
	return verr
}

// acceptedHint names what this tool does take, because "unknown argument" on
// its own costs the round trip the schema was published to save.
func acceptedHint(c plugin.Capability) string {
	// Exactly what InputSchema puts in "properties", in the same order: a
	// hint that named an argument the schema does not offer, or omitted one
	// it does, would send a model round again for a different reason.
	names := make([]string, 0, len(c.Inputs)+1)
	for _, f := range c.Inputs {
		if !f.Local {
			names = append(names, f.Name)
		}
	}
	if c.Detailed {
		names = append(names, "detail")
	}
	if len(names) == 0 {
		return "this tool takes no arguments"
	}
	return "accepted arguments: " + strings.Join(names, ", ")
}

// Require enforces the schema's "required" list before the gate, on the
// values the call will run with as far as they are known there — after the
// operator's config and the declared defaults have filled what the caller
// left out, so a default satisfies its own field's requirement. What it
// requires, and how it refuses, is the host's own rule (plugin.Missing and
// plugin.MissingInput): the guard in front of every handler asks the same
// question of the finished request, and a call refused here gets the code
// and the words it would, one step sooner and without spending a use. A
// Piped input is required here as well: the CLI reads it from a pipe when it
// is left out, and a model-facing channel has no pipe to read.
//
// Two kinds of input are left to that guard, because a layer after this one
// may still fill them and refusing here would refuse a call that runs:
//
//   - A Local input, which is never suppliable over MCP and which only the
//     operator's config, environment or profile can give. Requiring it of the
//     caller would make a capability declaring it permanently uncallable.
//   - With a profile named, any input the profile may fill
//     (plugin.ProfileFillable). The profile is resolved after consent on
//     purpose, so an input only it sets is absent here; refused here, a
//     connection whose database was required and set by the profile could
//     not be reached through the profile at all.
func Require(c plugin.Capability, values map[string]any, profiled bool) *view.Error {
	for _, f := range plugin.Missing(c, values, plugin.SurfaceMCP) {
		if f.Local || (profiled && plugin.ProfileFillable(c, f)) {
			continue
		}
		return plugin.MissingInput(c, f, plugin.SurfaceMCP)
	}
	return nil
}

// checkFieldType reports whether v is a shape Field.Type accepts, matching
// what InputSchema actually publishes for it. The shape only: whether the
// declaration takes the value is held's question.
func checkFieldType(f plugin.Field, v any) error {
	switch f.Type {
	case plugin.Int:
		isNumber, whole := wholeNumber(v)
		if !isNumber {
			return fmt.Errorf("must be an integer, got %s", JSONKind(v))
		}
		if !whole {
			return fmt.Errorf("must be an integer, got a non-integer number")
		}
	case plugin.Float:
		if isNumber, _ := wholeNumber(v); !isNumber {
			return fmt.Errorf("must be a number, got %s", JSONKind(v))
		}
	case plugin.Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be a boolean, got %s", JSONKind(v))
		}
	case plugin.StringSlice, plugin.SecretSlice:
		if err := checkStringSlice(v); err != nil {
			return err
		}
	default: // String, Text, Secret, Path
		if _, ok := v.(string); !ok {
			return fmt.Errorf("must be a string, got %s", JSONKind(v))
		}
	}
	return nil
}

// wholeNumber reports whether v is a number, and whether a whole one.
//
// Whole is all it says, and deliberately not "an integer the host can read".
// Whether 2^63 fits is the host's to answer (plugin.CheckInputs), by the
// field's range when it declares one: this used to refuse it as "past what
// an integer holds", a different code and different words from the CLI's
// refusal of the same number. Nothing is converted to decide it, because
// int64(n) for an n outside int64 is whatever the CPU does with it — arm64
// saturates, and 2^63 once compared equal to itself on the way back and
// passed as a readable integer.
func wholeNumber(v any) (isNumber, whole bool) {
	switch n := v.(type) {
	case int64, int:
		return true, true
	case float64:
		// NaN is not equal to its own Trunc; an infinity is, and is refused
		// by the host as a number nothing reads.
		return true, n == math.Trunc(n)
	case json.Number:
		// Decode leaves only a number no Go number holds as written, and
		// every one of those is whole — past int64, or past float64 — but a
		// second channel may hand over any literal.
		x, err := strconv.ParseFloat(n.String(), 64)
		return true, errors.Is(err, strconv.ErrRange) || (err == nil && x == math.Trunc(x))
	}
	return false, false
}

// checkStringSlice accepts what the schema publishes (an array of strings)
// and, deliberately, one more shape it does not: a bare string. That has to
// match what plugin.Request.StringSlice itself accepts — a caller sending
// {"key": "x"} instead of {"key": ["x"]} means one value, not none, and this
// boundary disagreeing with the accessor is exactly what let a per-key
// kv.env grant widen into exporting the whole store. Validation and
// coercion must read a scalar the same way, or fixing one reopens the other.
func checkStringSlice(v any) error {
	switch vv := v.(type) {
	case string:
		return nil
	case []any:
		for _, e := range vv {
			if _, ok := e.(string); !ok {
				return fmt.Errorf("must be an array of strings, got %s in it", JSONKind(e))
			}
		}
		return nil
	default:
		return fmt.Errorf("must be a string or an array of strings, got %s", JSONKind(vv))
	}
}

// JSONKind names a decoded JSON value the way somebody reading an error
// would think of it, not the way Go's %T would.
func JSONKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64, int64, int, json.Number:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
