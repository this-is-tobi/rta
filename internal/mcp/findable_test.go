package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A word a task is said in is often none of the words of a tool's name or
// summary, and a client that defers its tools finds one only by a search over
// them. A capability's keywords are those words, so the model is shown them —
// inside the block marked as the plugin's, with the rest of the plugin's text,
// where a plugin cannot speak as rta and rta's own lines keep the last word.
func TestAToolShowsTheWordsItIsAlsoCalled(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.thing.get", Summary: "get a thing", Safety: plugin.Read,
		Keywords: []string{"fetch", "retrieve"},
	}
	desc := toolDef(c, Options{}).Description
	line := "Also called: fetch, retrieve."
	at := strings.Index(desc, line)
	if at < 0 {
		t.Fatalf("the keywords never reach the tool:\n%s", desc)
	}
	if end := strings.Index(desc, plugin.AuthoredClose); at > end {
		t.Errorf("the keywords stand outside the plugin's block, where they would read as rta's own words:\n%s", desc)
	}
	if !strings.HasSuffix(desc, "Safety: read.") {
		t.Errorf("rta's line no longer has the last word:\n%s", desc)
	}

	c.Keywords = nil
	if desc := toolDef(c, Options{}).Description; strings.Contains(desc, "Also called") {
		t.Errorf("a capability with no keywords is said to be also called something:\n%s", desc)
	}
}
