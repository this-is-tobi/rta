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

// The handshake names what a server offers and how a tool is named, from the
// tools offered and no others: an area a server does not offer is not one an
// agent should search for, and one it hides over HTTP is not one to advertise.
func TestTheHandshakeNamesTheAreasOfferedAndOnlyThose(t *testing.T) {
	offered := []plugin.Capability{
		{ID: "kv.get", Safety: plugin.Read},
		{ID: "kube.pod.list", Safety: plugin.Read},
		{ID: "kv.set", Safety: plugin.Write},
	}
	got := handshakeInstructions(offered, Options{})
	if want := areasInstructions + "kube, kv."; !strings.Contains(got, want) {
		t.Errorf("the handshake does not name the areas, once each and sorted, want %q:\n%s", want, got)
	}
	if got := handshakeInstructions(nil, Options{}); strings.Contains(got, "area_thing_action") {
		t.Errorf("a server offering nothing names areas:\n%s", got)
	}
}
