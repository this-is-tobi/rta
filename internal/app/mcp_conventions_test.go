package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The conventions every tool an agent is offered keeps, held over the real
// catalogue the way the golden file holds its shape. A golden diff shows what
// changed; these say what may never be true of any tool, so a new capability
// is refused by the test rather than found by a model.

// authored is the part of a published description a plugin wrote: between the
// frame markers, and so without rta's own lines after it, which are the one
// place a dotted ID or an `rta ...` command belongs, since whose command it
// is is said right there.
func authored(t *testing.T, description string) string {
	t.Helper()
	open, closing := plugin.AuthoredOpen+"\n", "\n"+plugin.AuthoredClose
	rest, ok := strings.CutPrefix(description, open)
	if !ok {
		t.Fatalf("the description does not open on the frame:\n%s", description)
	}
	text, _, ok := strings.Cut(rest, closing)
	if !ok {
		t.Fatalf("the description does not close its frame:\n%s", description)
	}
	return text
}

// capabilities maps each tool the real catalogue publishes to its declaration.
func capabilities(t *testing.T) map[string]plugin.Capability {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]plugin.Capability{}
	for _, c := range reg.Capabilities() {
		byName[plugin.ToolName(c.ID)] = c
	}
	return byName
}

// A model chooses between tools by their descriptions and calls them by name:
// the plugin's text names a capability the way every surface can, by its ID,
// and over MCP that is a tool called something else.
func TestNoDescriptionSendsAModelToADottedName(t *testing.T) {
	tools := surface(t, mcp.Options{})
	offered := map[string]bool{}
	for _, tl := range tools {
		offered[tl.Name] = true
	}
	dotted := regexp.MustCompile(`[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+`)
	for _, tl := range tools {
		for _, word := range dotted.FindAllString(authored(t, tl.Description), -1) {
			if name := plugin.ToolName(word); offered[name] {
				t.Errorf("%s: the description says %q, and the tool is %q", tl.Name, word, name)
			}
		}
	}
}

// A description may only send a model to a capability it has. One that exists
// and is never a tool, because it answers to the person at the terminal
// alone, is a name the tool list does not hold and a call that is answered as
// an unknown tool and written to the record as a probe.
func TestNoDescriptionSendsAModelToACapabilityThatIsNotATool(t *testing.T) {
	tools := surface(t, mcp.Options{})
	offered := map[string]bool{}
	for _, tl := range tools {
		offered[tl.Name] = true
	}
	known := capabilities(t)
	dotted := regexp.MustCompile(`[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+`)
	for _, tl := range tools {
		for _, word := range dotted.FindAllString(authored(t, tl.Description), -1) {
			if name := plugin.ToolName(word); known[name].ID == word && !offered[name] {
				t.Errorf("%s: the description names %q, which exists and is not offered as a tool", tl.Name, word)
			}
		}
	}
}

// Every input is a typed, described argument: a schema property with no type
// is one a client cannot check, and one with no description is a guess.
func TestEveryInputIsTypedAndDescribed(t *testing.T) {
	for _, tl := range surface(t, mcp.Options{}) {
		props, _ := tl.Schema["properties"].(map[string]any)
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			typ, _ := p["type"].(string)
			if typ == "" {
				t.Errorf("%s: input %q has no type", tl.Name, name)
			}
			if desc, _ := p["description"].(string); strings.TrimSpace(desc) == "" {
				t.Errorf("%s: input %q has no description", tl.Name, name)
			}
			if typ == "array" {
				if items, _ := p["items"].(map[string]any); items["type"] == nil {
					t.Errorf("%s: input %q is a list that does not say what it lists", tl.Name, name)
				}
			}
		}
	}
}

// A number of time is no use to a model without its unit: it will send 30
// to a field that wants milliseconds as readily as one that wants seconds.
func TestATimeInputSaysItsUnit(t *testing.T) {
	unit := regexp.MustCompile(`(?i)\b(?:milli|micro)?seconds?\b|\bminutes?\b|\bhours?\b|\bdays?\b|\bms\b`)
	for _, tl := range surface(t, mcp.Options{}) {
		props, _ := tl.Schema["properties"].(map[string]any)
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			if p["type"] != "integer" && p["type"] != "number" {
				continue
			}
			switch name {
			case "timeout", "wait", "ttl", "interval", "delay", "duration", "age", "warn-days":
			default:
				continue
			}
			if desc, _ := p["description"].(string); !unit.MatchString(desc) {
				t.Errorf("%s: %q is a time and does not say in what unit: %q", tl.Name, name, desc)
			}
		}
	}
}
