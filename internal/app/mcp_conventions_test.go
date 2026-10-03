package app

import (
	"regexp"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The conventions every tool an agent is offered keeps, held over the real
// catalogue the way the golden file holds its shape. A golden diff shows what
// changed; these say what may never be true of any tool, so a new capability
// is refused by the test rather than found by a model.

// rtaLine is the sentence rta itself appends to a tool that needs a grant,
// where the operator's command is spelled with the capability's dotted ID.
// It is the one place a dotted ID or an `rta ...` command belongs: whose
// command it is is said right there.
var rtaLine = regexp.MustCompile(`(?s)\n\nRequires a grant.*$`)

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
		text := rtaLine.ReplaceAllString(tl.Description, "")
		for _, word := range dotted.FindAllString(text, -1) {
			if name := plugin.ToolName(word); offered[name] {
				t.Errorf("%s: the description says %q, and the tool is %q", tl.Name, word, name)
			}
		}
	}
}
