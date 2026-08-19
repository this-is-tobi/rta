// Package mcp bridges the capability registry to a Model Context Protocol
// server. Every capability becomes an MCP tool generated from the same
// declared inputs the CLI uses — zero per-capability work (PROJECT.md §6.1).
//
// Safety gate: only read capabilities are exposed by default. Write requires
// an explicit opt-in; destructive requires a per-capability allowlist. The
// gate is enforced host-side — annotations are advisory for clients, never
// our enforcement mechanism.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Options configures which capabilities are exposed.
type Options struct {
	// AllowWrite exposes write-class capabilities.
	AllowWrite bool
	// AllowDestructive lists capability IDs allowed despite being destructive.
	AllowDestructive []string
}

func (o Options) destructiveAllowed(id string) bool {
	for _, allowed := range o.AllowDestructive {
		if allowed == id {
			return true
		}
	}
	return false
}

// exposed reports whether a capability passes the safety gate.
func (o Options) exposed(c plugin.Capability) bool {
	switch c.Safety {
	case plugin.Read:
		return true
	case plugin.Write:
		return o.AllowWrite
	case plugin.Destructive:
		return o.destructiveAllowed(c.ID)
	default:
		return false
	}
}

// ToolName converts a capability ID to an MCP-safe tool name
// (dots are not universally accepted in tool names).
func ToolName(capID string) string { return strings.ReplaceAll(capID, ".", "_") }

// NewServer builds an MCP server exposing the registry's capabilities.
func NewServer(reg *registry.Registry, version string, opts Options) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "rta",
		Title:   "Rule Them All",
		Version: version,
	}, nil)

	for _, c := range reg.Capabilities() {
		if !opts.exposed(c) {
			continue
		}
		server.AddTool(toolDef(c), handler(c))
	}
	return server
}

// toolDef maps a capability to an MCP tool: schema from declared inputs,
// annotations from the safety class (PROJECT.md §6.1 mapping table).
func toolDef(c plugin.Capability) *sdk.Tool {
	falseHint, trueHint := false, true
	ann := &sdk.ToolAnnotations{
		Title:          c.Summary,
		IdempotentHint: c.Idempotent,
	}
	switch c.Safety {
	case plugin.Read:
		ann.ReadOnlyHint = true
	case plugin.Write:
		ann.DestructiveHint = &falseHint
	case plugin.Destructive:
		ann.DestructiveHint = &trueHint
	}

	desc := c.Summary
	if c.Description != "" {
		desc += "\n\n" + c.Description
	}
	desc += fmt.Sprintf("\n\nSafety: %s. Returns a JSON view envelope discriminated by \"type\".", c.Safety)
	if grant.Required(c) {
		// Say it in the description as well as enforcing it, so a model asks
		// the person for a grant instead of retrying a call that cannot work.
		desc += "\n\nRequires a grant a person issued for this capability"
		if c.Scope != "" {
			desc += fmt.Sprintf(" (optionally narrowed to one %q)", c.Scope)
		}
		desc += ". You cannot issue one yourself — ask the operator to run `rta grant allow " + c.ID + "`."
	}

	return &sdk.Tool{
		Name:        ToolName(c.ID),
		Description: desc,
		Annotations: ann,
		InputSchema: InputSchema(c),
	}
}

// InputSchema builds the JSON Schema for a capability's declared inputs.
// It is exported because the CLI (`rta explain`) reuses it.
//
// Local fields are omitted: they are credentials the host resolves from its
// own environment, and putting one in a tool schema invites a model to
// supply or echo it (plugin.Field.Local).
func InputSchema(c plugin.Capability) map[string]any {
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
	schema := map[string]any{
		"type":       "object",
		"properties": props,
		// The bridge refuses an argument it does not recognise
		// (validateGivenArgs), and a schema that stayed silent about that let
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

func handler(c plugin.Capability) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		values := map[string]any{}
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &values); err != nil {
				return errResult(view.Errorf("core.mcp.badargs", "invalid arguments: %v", err)), nil
			}
		}
		// The published schema says integer, enum, array-of-string — and
		// nothing between here and the handler enforces any of that. The SDK
		// says so itself: unmarshalling and validating against the schema are
		// the caller's responsibility (go-sdk's Server.AddTool doc comment).
		// Without this, a wrong-typed argument was indistinguishable from an
		// omitted one — Request.Int and Request.String both return the zero
		// value on a type mismatch rather than reporting one — so
		// sys_ps {"limit": "3"} (schema: integer) silently returned every
		// process at the default limit instead of three, no error, no
		// warning. Checked against what the caller actually sent, before
		// defaults or Local-stripping touch the map: a default is our own
		// value and always well-typed, so only what arrived over the wire
		// needs the scrutiny.
		if verr := validateGivenArgs(c, values); verr != nil {
			return errResult(verr), nil
		}
		// Declared defaults apply to omitted arguments, exactly like the CLI.
		// Local fields are dropped whatever the caller sent: they are absent
		// from the schema, so anything arriving under that name was guessed,
		// and a guessed credential is the one case worth discarding rather
		// than acting on. Dropped, not refused the way an undeclared name is:
		// an error naming the field would confirm to the model that the
		// credential input exists, which is the disclosure Local is for.
		for _, f := range c.Inputs {
			if f.Local {
				delete(values, f.Name)
				continue
			}
			if _, given := values[f.Name]; !given && f.Default != nil {
				values[f.Name] = f.Default
			}
		}
		if verr := requireArgs(c, values); verr != nil {
			return errResult(verr), nil
		}
		// The exposure gate said this agent may in principle make this kind of
		// call. A grant says a person allowed this one, on this record, now —
		// the second half of the MCP equivalent of a confirmation. Enforcing
		// it here rather than in each handler is what makes it a property of
		// the surface instead of something a plugin can forget.
		// Authorize and spend in one step. Checking first and spending after
		// the call left a window every concurrent caller fitted through: the
		// go-sdk dispatches each tools/call in its own goroutine, so two
		// pipelined requests both cleared an unlocked check against a
		// MaxUses:1 grant and both received the secret. Reserve decides and
		// increments under the same lock, and hands back a refund for the
		// case the old ordering existed to protect — a call that fails must
		// not burn a one-time grant that delivered nothing.
		release, verr := grant.Reserve(c, values)
		if verr != nil {
			return errResult(verr), nil
		}
		v, err := c.Run(ctx, plugin.NewRequest(plugin.Resolve(c, values), false, true).WithSurface(plugin.SurfaceMCP))
		if err != nil {
			release()
			return errResult(view.AsError(err, c.ID+".failed")), nil
		}
		return viewResult(v)
	}
}

// validateGivenArgs checks every argument the caller actually supplied
// against its declared Field — type, and closed-set membership when the
// field declares Options — and refuses any name the schema does not offer.
// A Local field is never type-checked: it is stripped regardless of what
// arrived, so validating a value about to be discarded would only produce a
// confusing error about a field the model does not even know exists.
func validateGivenArgs(c plugin.Capability, values map[string]any) *view.Error {
	declared := make(map[string]bool, len(c.Inputs)+1)
	check := func(f plugin.Field) *view.Error {
		v, given := values[f.Name]
		if !given {
			return nil
		}
		if err := checkFieldType(f, v); err != nil {
			return view.Errorf("core.mcp.badargs", "%s: %v", f.Name, err).
				WithHint(fmt.Sprintf("%s expects %s", f.Name, schemaTypeName(f.Type)))
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

// requireArgs enforces the schema's "required" list on the final value map —
// after defaults have filled in what the caller left out, so a declared
// default satisfies its own field's requirement. Local fields are exempt:
// they are never suppliable over MCP, so requiring one here would make a
// capability that declares Required on a Local field permanently
// uncallable — a contradiction for the plugin author to avoid, not
// something this boundary should enforce.
func requireArgs(c plugin.Capability, values map[string]any) *view.Error {
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
			return fmt.Errorf("must be an integer, got %s", jsonKind(v))
		}
		if n != float64(int64(n)) {
			return fmt.Errorf("must be an integer, got a non-integer number")
		}
	case plugin.Float:
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("must be a number, got %s", jsonKind(v))
		}
	case plugin.Bool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be a boolean, got %s", jsonKind(v))
		}
	case plugin.StringSlice:
		if err := checkStringSlice(v); err != nil {
			return err
		}
	default: // String, Text, Secret, Path
		if _, ok := v.(string); !ok {
			return fmt.Errorf("must be a string, got %s", jsonKind(v))
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
				return fmt.Errorf("must be an array of strings, got %s in it", jsonKind(e))
			}
		}
		return nil
	default:
		return fmt.Errorf("must be a string or an array of strings, got %s", jsonKind(vv))
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
	}
	for _, s := range items {
		if !allowed(s) {
			return fmt.Errorf("%q is not one of: %s", s, strings.Join(f.Options, ", "))
		}
	}
	return nil
}

// jsonKind names a decoded JSON value the way somebody reading an error
// would think of it, not the way Go's %T would.
func jsonKind(v any) string {
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

// schemaTypeName names a Field.Type the way InputSchema described it, so the
// hint matches what the schema actually says.
func schemaTypeName(t plugin.FieldType) string {
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

// viewResult encodes a view as both text (JSON envelope) and structured
// content. Redacted fields are masked here too — an MCP caller reaches this
// path without a human present, so it gets the same masking guarantee as
// every other renderer (pkg/view.Redact).
func viewResult(v view.View) (*sdk.CallToolResult, error) {
	m, err := view.ToMap(view.Redact(v))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(raw)}},
		StructuredContent: m,
	}, nil
}

func errResult(e *view.Error) *sdk.CallToolResult {
	raw, _ := json.Marshal(view.Envelope{View: e})
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}},
	}
}
