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
	for _, want := range []string{`"type"`, `"hint"`, `"input"`, `"total"`, "operator", "grant", "roots", "grant allow <id>"} {
		if !strings.Contains(got, want) {
			t.Errorf("the instructions never say %s:\n%s", want, got)
		}
	}
	// They are paid for once per session by every client, so they are held to
	// a size: growing past it means a per-tool fact has been put in the wrong
	// place. The figure moved from 1024 when the grant command left every tool
	// that needs one and came here, once: about 100 bytes against some 6 KB.
	if len(got) > 1200 {
		t.Errorf("the instructions are %d bytes, over the 1200 they are held to", len(got))
	}
}

// The command to get a grant is the handshake's, once, and a tool says only
// that it needs one: the same sentence with one word changed stood at the end of
// every tool that costs a grant.
func TestNoDescriptionCarriesTheGrantCommand(t *testing.T) {
	for name, c := range map[string]plugin.Capability{
		"a write":       {ID: "demo.thing.set", Summary: "set a thing", Safety: plugin.Write, Scope: "key"},
		"a read":        {ID: "demo.thing.get", Summary: "get a thing", Safety: plugin.Read, NeedsGrant: true},
		"a destructive": {ID: "demo.thing.rm", Summary: "remove a thing", Safety: plugin.Destructive},
	} {
		desc := toolDef(c, Options{}).Description
		if !strings.HasSuffix(desc, "\n\n"+grantSentence) {
			t.Errorf("%s does not end on %q:\n%s", name, grantSentence, desc)
		}
		if strings.Contains(desc, "grant allow") || strings.Contains(desc, "--profile") {
			t.Errorf("%s carries the grant command:\n%s", name, desc)
		}
	}
	free := toolDef(plugin.Capability{ID: "demo.thing.get", Summary: "get a thing", Safety: plugin.Read}, Options{}).Description
	if strings.Contains(free, "grant") {
		t.Errorf("a free read mentions a grant:\n%s", free)
	}
}

// A list that is cut and cannot say how long the whole is leaves its total out
// (git_log), and the rule above it defines only a total: with no sentence for
// the absent one, an agent read a log of exactly its limit as a repository of
// that many commits, which is the reading the missing total was meant to stop.
func TestTheHandshakeSaysWhatATableWithNoTotalMeans(t *testing.T) {
	got := connect(t, Options{}).InitializeResult().Instructions
	for _, want := range []string{"more than the rows sent", "has none", "a warning", "continues"} {
		if !strings.Contains(got, want) {
			t.Errorf("the handshake never says %q about a table with no total:\n%s", want, got)
		}
	}
}

// A read is not always free: one that reaches a destination the caller chooses,
// or hands over what the operator set up, costs a grant like a write does. The
// handshake used to give the safety class as the whole rule, and a model that
// took it at its word spent a call on a read that was going to be refused.
func TestTheHandshakeDoesNotCallEveryReadFree(t *testing.T) {
	got := connect(t, Options{}).InitializeResult().Instructions
	if strings.Contains(got, "even one that only returns data") {
		t.Errorf("the handshake gives the safety class as the whole grant rule:\n%s", got)
	}
	for _, want := range []string{"above read", "some reads", "own text says"} {
		if !strings.Contains(got, want) {
			t.Errorf("the handshake never says %q about which tools need a grant:\n%s", want, got)
		}
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
