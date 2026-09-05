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
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Holding a call to the schema it was shown: every argument an agent sent
// is validated against the declaration before anything runs, and refusals
// name what was accepted rather than only what was not.

// Validate checks every argument a model-facing caller actually sent
// against the declaration. Exported because the boundary grammar has one
// home rather than one copy per channel: a fix here lands on every caller
// by construction.
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
		return nil
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

// Require enforces the schema's "required" list on the final value map —
// after defaults have filled in what the caller left out, so a declared
// default satisfies its own field's requirement. Local fields are exempt:
// they are never suppliable over MCP, so requiring one here would make a
// capability that declares Required on a Local field permanently
// uncallable — a contradiction for the plugin author to avoid, not
// something this boundary should enforce.
func Require(c plugin.Capability, values map[string]any) *view.Error {
	for _, f := range c.Inputs {
		if !f.Required || f.Local {
			continue
		}
		if _, given := values[f.Name]; !given {
			return view.Errorf("core.mcp.badargs", "%s is required", f.Name).
				WithHint(fmt.Sprintf("pass %q in the arguments", f.Name))
		}
	}
	return nil
}

// checkFieldType reports whether v is a shape Field.Type accepts, matching
// what InputSchema actually publishes for it.
func checkFieldType(f plugin.Field, v any) error {
	switch f.Type {
	case plugin.Int:
		n, ok := v.(float64)
		if !ok {
			return fmt.Errorf("must be an integer, got %s", JSONKind(v))
		}
		if n != float64(int64(n)) {
			return fmt.Errorf("must be an integer, got a non-integer number")
		}
	case plugin.Float:
		if _, ok := v.(float64); !ok {
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
	if len(f.Options) > 0 {
		return checkEnum(f, v)
	}
	return nil
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

// checkEnum reports whether v (already type-checked) is drawn from f.Options.
func checkEnum(f plugin.Field, v any) error {
	allowed := func(s string) bool {
		for _, o := range f.Options {
			if o == s {
				return true
			}
		}
		return false
	}
	items := []string{}
	switch vv := v.(type) {
	case string:
		items = append(items, vv)
	case []any:
		for _, e := range vv {
			items = append(items, e.(string)) // checkFieldType already proved this
		}
	case float64:
		// checkFieldType already proved this is whole for plugin.Int; Float
		// gets the general form. Either way, an Options entry is a string
		// an operator wrote by hand ("1", "2.5") — the same shape this must
		// produce, or a numeric field's enum could never actually match.
		if f.Type == plugin.Int {
			items = append(items, strconv.FormatInt(int64(vv), 10))
		} else {
			items = append(items, strconv.FormatFloat(vv, 'g', -1, 64))
		}
	case bool:
		items = append(items, strconv.FormatBool(vv))
	}
	for _, s := range items {
		if !allowed(s) {
			return fmt.Errorf("%q is not one of: %s", s, strings.Join(f.Options, ", "))
		}
	}
	return nil
}

// JSONKind names a decoded JSON value the way somebody reading an error
// would think of it, not the way Go's %T would.
func JSONKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
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
