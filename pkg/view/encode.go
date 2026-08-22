package view

import (
	"encoding/json"
	"fmt"
)

// Envelope is the discriminated wire form of a View: {"type": ..., ...body}.
// It is the single encoding shared by --output json|yaml and the MCP bridge,
// so scripts and agents can switch on "type".
type Envelope struct {
	Type string `json:"type"`
	View View   `json:"-"`
}

// TypeOf returns the discriminator string for a View.
func TypeOf(v View) string {
	switch v.(type) {
	case Text:
		return "text"
	case KeyValue:
		return "keyvalue"
	case Table:
		return "table"
	case Tree:
		return "tree"
	case Chart:
		return "chart"
	case Sections:
		return "sections"
	case *Error:
		return "error"
	default:
		return "unknown"
	}
}

// MarshalJSON wraps a section's child view in its own envelope, so nested
// views stay discriminated all the way down and agents can switch on "type"
// at any depth.
func (s Section) MarshalJSON() ([]byte, error) {
	// The id is emitted, and it is the field that matters to a machine: Title
	// is prose meant for a person and free to change, while ID is the stable
	// handle a script or an agent addresses a section by — which is why
	// plugin.Page assigns one and cli.go's renderer comments say the id
	// exists so a script can name a section. Dropping it here meant no
	// machine consumer could, on any surface that encodes.
	//
	// Omitted when empty rather than emitted blank, since a section built by
	// hand need not have one and `"id":""` is not an identifier.
	return json.Marshal(struct {
		ID    string   `json:"id,omitempty"`
		Title string   `json:"title"`
		View  Envelope `json:"view"`
	}{ID: s.ID, Title: s.Title, View: Envelope{View: s.View}})
}

// MarshalJSON flattens the view body next to the type discriminator.
//
// A nil View is legal and encodes as {"type":"unknown"}. That is the rule the
// rest of the contract already assumes — Redact skips a nil section view,
// prettySections skips it, Page.Put ignores it, and a handler may return
// (nil, nil) — and this encoder was the one place that disagreed, by
// panicking. It marshals the view, unmarshals into a map and writes the
// discriminator into it; a nil view round-trips through `null`, which leaves
// the map nil, and assigning into a nil map takes the process down.
//
// The path is not exotic. It is every `-o json`, every `-o yaml`, and every
// MCP tools/call, and on the MCP path the panic happens inside the server's
// per-call goroutine, which no caller-side recover can contain: one plugin
// returning a titled section it could not fill kills the server for every
// other tool the agent had open.
func (e Envelope) MarshalJSON() ([]byte, error) {
	body, err := json.Marshal(e.View)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	m["type"] = TypeOf(e.View)
	return json.Marshal(m)
}

// ToMap returns the envelope as a generic map, for non-JSON encoders (YAML).
func ToMap(v View) (map[string]any, error) {
	raw, err := json.Marshal(Envelope{View: v})
	if err != nil {
		return nil, fmt.Errorf("encoding view: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decoding view envelope: %w", err)
	}
	return m, nil
}
