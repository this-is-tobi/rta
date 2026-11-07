package toolcall

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Every property a schema generates is paid for once per tool that has it, and
// no schema can share text with another: JSON Schema's $ref is per document, and
// MCP has no definitions that span tools. The words are therefore the lever,
// and these hold the ones that repeat on most tools to a size, so a sentence
// cannot creep back a clause at a time.

func TestTheDetailInputIsAShortSwitchWithNoDefault(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Detailed: true}
	detail := InputSchema(c, nil, nil)["properties"].(map[string]any)["detail"].(map[string]any)
	if detail["type"] != "boolean" || detail["description"] != DetailHelp {
		t.Errorf("detail is %v, want a boolean described as %q", detail, DetailHelp)
	}
	if _, has := detail["default"]; has {
		t.Errorf("detail publishes a default, which a boolean left out already is: %v", detail)
	}
	if len(detail["description"].(string)) > 30 {
		t.Errorf("detail says %q, which every tool with a detailed view repeats", detail["description"])
	}
}

func TestTheProfileInputStandsAloneInFewWords(t *testing.T) {
	c := plugin.Capability{ID: "pg.query", Safety: plugin.Read, Inputs: []plugin.Field{
		{Name: "host", Type: plugin.String, Config: "host", Local: true},
	}}
	schema := InputSchema(c, []string{"staging"}, nil)
	profile := schema["properties"].(map[string]any)["profile"].(map[string]any)
	text, _ := profile["description"].(string)
	// The handshake sentence about profiles is written when the server starts, so
	// a client that connected before the first profile existed holds this
	// property without it: the words here must say it is required and needs a
	// grant on their own.
	for _, want := range []string{"required", "grant"} {
		if !strings.Contains(text, want) {
			t.Errorf("the profile input never says %q: %q", want, text)
		}
	}
	if len(text) > 100 {
		t.Errorf("the profile input is %d bytes, over the 100 every tool that takes one repeats: %q", len(text), text)
	}
}

func TestAPathInputIsDescribedByItsOwnHelpAlone(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Inputs: []plugin.Field{
		{Name: "file", Type: plugin.Path, Help: "the file to read"},
	}}
	file := InputSchema(c, nil, nil)["properties"].(map[string]any)["file"].(map[string]any)
	if file["type"] != "string" || file["description"] != "the file to read" {
		t.Errorf("a path is %v, want a string described by its own help: whose filesystem it names is the handshake's", file)
	}
}
