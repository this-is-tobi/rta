package toolcall

import (
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// InputSchema builds the JSON Schema for a capability's declared inputs, as
// the MCP server publishes it in tools/list. profiles is the connections the
// operator configured for its namespace, and config is the operator's
// section for its plugin — the one the server's calls are filled from.
//
// Local fields are omitted: they are credentials the host resolves from its
// own environment, and putting one in a tool schema invites a model to
// supply or echo it (plugin.Field.Local).
func InputSchema(c plugin.Capability, profiles []string, config map[string]any) map[string]any {
	props := map[string]any{}
	var required []string
	// What a call sending nothing and naming no profile is refused for: the
	// operator's config and the declared defaults laid by the call's own
	// Resolve, and read by the call's own Missing, which is the question
	// Require asks. Resolve lays each input on its own, so this is, input by
	// input, what any call leaving that input out is refused for — see the
	// "required" list below for why the list has to be exactly that.
	unfilled := map[string]bool{}
	for _, f := range plugin.Missing(c, plugin.Resolve(c, plugin.Inputs{Config: config}), plugin.SurfaceMCP) {
		unfilled[f.Name] = true
	}
	for _, f := range c.Inputs {
		if f.Local {
			continue
		}
		prop := map[string]any{"description": f.Help}
		switch f.Type {
		case plugin.Path:
			prop["type"] = "string"
			// Whose filesystem this is cannot be inferred from a field called
			// "file": a model reading that has every reason to think of its
			// own working directory, and the path is resolved on the machine
			// running rta. Saying so is the difference between a relative path
			// that works and one that quietly means something else.
			prop["description"] = strings.TrimSpace(f.Help + " (a path on the machine running rta)")
		case plugin.Duration:
			prop["type"] = "string"
			// A string with a grammar, and a range that no schema keyword can
			// hold for text, so it is said in the description: a model
			// reading "timeout: string" sends 30, which is refused for its
			// missing unit, and a client enforcing the pattern catches that
			// before the round trip.
			prop["pattern"] = plugin.DurationPattern
			prop["description"] = strings.TrimSpace(f.Help + " (" + durationText(f) + ")")
		case plugin.Int:
			prop["type"] = "integer"
		case plugin.Bool:
			prop["type"] = "boolean"
		case plugin.Float:
			prop["type"] = "number"
		case plugin.StringSlice, plugin.SecretSlice:
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "string"}
		default:
			prop["type"] = "string"
		}
		// Not an empty one: it is what a call leaving the input out gets, and
		// "" or an empty list gets it nothing — the handler reads either as it
		// reads an input nobody gave. "default": "" told an agent a value was
		// there, and, before Validate refused the pair, sat beside "required".
		// A false or a zero is a value, and is published.
		if f.Default != nil && !emptyDefault(f.Default) {
			prop["default"] = f.Default
		}
		// A closed set belongs in the schema, where a client can enforce it
		// and a model can read it: guessing "PTR" at a field that wants "ptr"
		// should not cost a round trip to find out.
		if len(f.Options) > 0 {
			if f.Type.Repeatable() {
				prop["items"] = map[string]any{"type": "string", "enum": f.Options}
			} else {
				prop["enum"] = f.Options
			}
		}
		// A declared bound belongs in the schema for the same reason a closed
		// set does. The host refuses a value outside it regardless
		// (plugin.CheckInputs), so this is not the enforcement — it is telling
		// a model the range instead of letting it find the edge by sending a
		// zero.
		if f.Type == plugin.Int || f.Type == plugin.Float {
			if f.Min != nil {
				prop["minimum"] = f.Min
			}
			if f.Max != nil {
				prop["maximum"] = f.Max
			}
		}
		props[f.Name] = prop
		// A Piped input is optional only on the CLI, which reads a pipe when
		// it is left out. There is none here, and a schema leaving it out of
		// "required" told an agent that a call without it was one the tool
		// answers — beside a description saying the value is read from
		// standard input when not given. Asked by the rule the host refuses a
		// call with, so the list published and the list held are one list.
		//
		// Which is also why an input the operator's config gives a value is
		// left out. The call is held to the list after the config has filled
		// what the agent left out (Require), so a call without it runs — and
		// the schema listed it anyway, so a client validating arguments against
		// the schema refused to send db_query {} to a server whose config names
		// the database, a call rta would have run. An input a declared default
		// fills is left out for the same reason, and both are read off what
		// Resolve lays (unfilled) rather than off each layer on its own:
		// `database: ""` in the config lays an empty text over whatever default
		// the input declares, and the call is refused for it, where a test of
		// the config key and one of the default, each alone, would have taken
		// the input off the list. What a profile fills stays required: which
		// profile a call names is the call's to say, and this is one list for
		// every call, whichever profile it names and whether that profile sets
		// the input at all. The list is sent once and still agrees with every
		// call after it because the config is resolved once, when rta starts
		// (pluginconf.Resolve in cmd/rta), and each call reads that same
		// resolution; a config read per call would need the list resent.
		if unfilled[f.Name] {
			required = append(required, f.Name)
		}
	}
	// Capability.Detailed is a real input everywhere except the schema: the
	// host injects a "detail" value, the CLI exposes --detail, and the tool
	// description copied from Capability.Description tells the model what it
	// does — while the schema published alongside offered no way to ask for
	// it. An agent could only reach the richest views in the catalogue by
	// sending an undeclared argument, which a schema-enforcing client strips.
	if c.Detailed {
		props["detail"] = map[string]any{
			"type":        "boolean",
			"description": "return the full detailed view instead of the compact summary",
			"default":     false,
		}
	}
	// The one thing an agent may say about where a call goes: the *name* of a
	// connection the operator wrote in their own file. Never an address, a
	// port, a user or a credential — every one of those stays Local, absent
	// from this schema and deleted from the arguments whatever arrives.
	// An index into somebody else's list is a different kind of thing from a
	// destination, and it is the difference the whole feature rests on.
	//
	// Published only where the operator has actually configured a profile for
	// this namespace. Otherwise the schema is byte-identical to what it was
	// before profiles existed and additionalProperties:false refuses the name
	// outright — so nothing changes for anybody who has not opted in.
	//
	// **No enum**, deliberately, though the names are right here. Listing an
	// operator's whole connection inventory to an agent that has been granted
	// none of it is disclosure, and the argument for an enum — that a model
	// would otherwise guess and fail — assumes a self-service retry loop that
	// does not exist here: only a person can issue a grant, so every wrong
	// guess terminates at that person anyway, and the refusal names the exact
	// command including the string the agent supplied.
	if len(profiles) > 0 && plugin.Profilable(c) {
		props["profile"] = map[string]any{
			"type": "string",
			"description": "name one of the connections the operator configured. " +
				"Refused unless a person has issued a grant naming this exact profile.",
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
		// The bridge refuses an argument it does not recognise
		// (ValidateArgs), and a schema that stayed silent about that let
		// a client discover the rule by being refused. Saying it here lets a
		// schema-enforcing client catch sys_ps {"limt": 3} before it becomes
		// a round trip, and tells every other client which half of a rejected
		// call was wrong.
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// emptyDefault reports whether a declared default gives a call nothing: empty
// text or a list with nothing in it, typed nil included — what
// plugin.Missing counts as no value.
func emptyDefault(v any) bool {
	switch v := v.(type) {
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}

// durationText is how a Duration input says what it takes: the unit every
// value carries and the range it is held to, when it declares one.
func durationText(f plugin.Field) string {
	text := "a duration with its unit, such as 30s, 5m or 2h"
	if bounds := f.Bounds(); bounds != "" {
		text += "; " + bounds
	}
	return text
}

// SchemaTypeName names a Field.Type the way InputSchema described it, so the
// hint matches what the schema actually says.
func SchemaTypeName(t plugin.FieldType) string {
	switch t {
	case plugin.Int:
		return "an integer"
	case plugin.Float:
		return "a number"
	case plugin.Bool:
		return "a boolean"
	case plugin.StringSlice, plugin.SecretSlice:
		return "an array of strings"
	case plugin.Duration:
		return "a string with its unit, such as 30s, 5m or 2h"
	default:
		return "a string"
	}
}
