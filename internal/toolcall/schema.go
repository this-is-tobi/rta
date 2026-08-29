package toolcall

import (
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Name converts a capability ID to an MCP-safe tool name
// (dots are not universally accepted in tool names).
func Name(capID string) string { return strings.ReplaceAll(capID, ".", "_") }

// agentText builds the description a model reads, with the part rta wrote
// separated from the part the plugin wrote.
//

// InputSchema builds the JSON Schema for a capability's declared inputs.
// The CLI (`rta explain`) and both model-facing channels consume it.
//
// Local fields are omitted: they are credentials the host resolves from its
// own environment, and putting one in a tool schema invites a model to
// supply or echo it (plugin.Field.Local).
func InputSchema(c plugin.Capability, profiles []string) map[string]any {
	props := map[string]any{}
	var required []string
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
		case plugin.Int:
			prop["type"] = "integer"
		case plugin.Bool:
			prop["type"] = "boolean"
		case plugin.Float:
			prop["type"] = "number"
		case plugin.StringSlice:
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "string"}
		default:
			prop["type"] = "string"
		}
		if f.Default != nil {
			prop["default"] = f.Default
		}
		// A closed set belongs in the schema, where a client can enforce it
		// and a model can read it: guessing "PTR" at a field that wants "ptr"
		// should not cost a round trip to find out.
		if len(f.Options) > 0 {
			if f.Type == plugin.StringSlice {
				prop["items"] = map[string]any{"type": "string", "enum": f.Options}
			} else {
				prop["enum"] = f.Options
			}
		}
		// A declared bound belongs in the schema for the same reason a closed
		// set does. The host clamps regardless (plugin.Resolve), so this is
		// not the enforcement — it is telling a model the range instead of
		// letting it find the edge by sending a zero.
		if f.Type == plugin.Int || f.Type == plugin.Float {
			if f.Min != nil {
				prop["minimum"] = f.Min
			}
			if f.Max != nil {
				prop["maximum"] = f.Max
			}
		}
		props[f.Name] = prop
		if f.Required {
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
	case plugin.StringSlice:
		return "an array of strings"
	default:
		return "a string"
	}
}
