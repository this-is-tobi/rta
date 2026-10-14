package toolcall

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// kv.status lost its `detail` to kv.overview, and an agent holding the schema it
// was handed before was told only that the argument is unknown, with the list of
// what the tool takes: nothing that says where the detail went.
func TestAnArgumentThatMovedSaysWhereItWent(t *testing.T) {
	c := plugin.Capability{ID: "kv.status", Inputs: []plugin.Field{{Name: "passphrase", Type: plugin.String}}}
	verr := Validate(c, map[string]any{"detail": true})
	if verr == nil {
		t.Fatal("an argument the tool does not take was accepted")
	}
	if !strings.Contains(verr.Hint, "\"detail\" moved to the `kv_overview` tool") {
		t.Errorf("hint = %q, want where `detail` went", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "accepted arguments") {
		t.Errorf("hint = %q, lost the list of what the tool takes", verr.Hint)
	}

	other := plugin.Capability{ID: "kv.list", Inputs: []plugin.Field{{Name: "folder", Type: plugin.String}}}
	if verr := Validate(other, map[string]any{"detail2": true}); verr == nil || strings.Contains(verr.Hint, "moved") {
		t.Errorf("an unknown argument of another tool was told it moved: %v", verr)
	}
}
