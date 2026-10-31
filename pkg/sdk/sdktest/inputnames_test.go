package sdktest

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A WithInputs key naming no capability, or an input its capability does not
// declare, fails the suite. Its values reached nothing: the scaffold's entry
// for a capability the author had renamed stayed, and the suite went green
// over the one it had become, driven without it.
func TestWithInputsNamingNothingFails(t *testing.T) {
	p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{ok()}}
	rec := &recorder{}
	checkGivenInputs(rec, p, map[string]map[string]any{
		"demo.greet":     {"name": "world"},
		"demo.item.list": {"limit": 5, "limt": 5},
		// Another plugin's, from a map that supplies a catalogue: not demo's to judge.
		"other.thing.set": {"name": "x"},
	})
	if strings.Contains(rec.errText(), "other.thing.set") {
		t.Errorf("another plugin's key was held to demo's declaration:\n%s", rec.errText())
	}
	if !strings.Contains(rec.errText(), `"demo.greet", which is no capability of demo`) {
		t.Errorf("a key naming no capability was accepted:\n%s", rec.errText())
	}
	if !strings.Contains(rec.errText(), `demo.item.list an input "limt" it does not declare`) {
		t.Errorf("an input the capability does not declare was accepted:\n%s", rec.errText())
	}
	if strings.Contains(rec.errText(), `"limit"`) {
		t.Errorf("a declared input was refused:\n%s", rec.errText())
	}

	// "detail" is the host's own input on a Detailed capability, and only there.
	detailed := ok()
	detailed.Detailed = true
	for _, tc := range []struct {
		c      plugin.Capability
		refuse bool
	}{{detailed, false}, {ok(), true}} {
		rec := &recorder{}
		checkGivenInputs(rec, plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{tc.c}},
			map[string]map[string]any{"demo.item.list": {"detail": true}})
		if refused := len(rec.errs) > 0; refused != tc.refuse {
			t.Errorf("detail on a capability with Detailed=%v: refused=%v %s", tc.c.Detailed, refused, rec.errText())
		}
	}
}
