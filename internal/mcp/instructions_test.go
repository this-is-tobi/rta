package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// What is true of every tool is said once, at the handshake, and not again in
// each description a model reads to choose between them.
func TestTheHandshakeCarriesWhatIsTrueOfEveryTool(t *testing.T) {
	s := connect(t, Options{})
	got := s.InitializeResult().Instructions
	for _, want := range []string{`"type"`, `"hint"`, `"total"`, "operator", "grant", "roots"} {
		if !strings.Contains(got, want) {
			t.Errorf("the instructions never say %s:\n%s", want, got)
		}
	}
	// They are paid for once per session by every client, so they are held to
	// a size: growing past it means a per-tool fact has been put in the wrong place.
	if len(got) > 1024 {
		t.Errorf("the instructions are %d bytes, over the 1024 they are held to", len(got))
	}
}

func TestADescriptionDoesNotRepeatTheEnvelope(t *testing.T) {
	c := plugin.Capability{ID: "demo.thing.get", Summary: "get a thing", Safety: plugin.Read}
	desc := toolDef(c, Options{}).Description
	if strings.Contains(desc, "envelope") {
		t.Errorf("the description restates the result shape the handshake gives:\n%s", desc)
	}
	if !strings.HasSuffix(desc, "Safety: read.") {
		t.Errorf("the description does not end on its safety class:\n%s", desc)
	}
}

// A capability that wrote a text for the model is read by the model in place
// of the Description a person reads at a terminal, and one that wrote none is
// read as before.
func TestAToolShowsTheAgentTextWhenTheCapabilityWroteOne(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.thing.get", Summary: "get a thing", Safety: plugin.Read,
		Description: "Pass --long at the terminal to see every column.",
		Agent:       "Returns one row per thing.",
	}
	desc := toolDef(c, Options{}).Description
	if !strings.Contains(desc, "Returns one row per thing.") {
		t.Errorf("the agent text is not what the tool says:\n%s", desc)
	}
	if strings.Contains(desc, "--long") {
		t.Errorf("the terminal's description reached the tool:\n%s", desc)
	}
	c.Agent = ""
	if desc := toolDef(c, Options{}).Description; !strings.Contains(desc, "--long") {
		t.Errorf("a capability with no agent text lost its description:\n%s", desc)
	}
}
