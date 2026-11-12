package sdktest

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A declaration whose config key breaks a rule every key follows fails the
// suite, as Serve refuses to start it: a dotted key is one capability's own.
func TestAKeyThatBreaksTheKeyRulesFailsTheSuite(t *testing.T) {
	c := ok()
	c.Inputs = append(c.Inputs, plugin.Field{Name: "depth", Type: plugin.Int, Config: "tree.depth", Help: "levels"})
	rec := &recorder{}
	if checkDeclaration(rec, plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{c}}) ||
		!strings.Contains(rec.errText(), `config key "tree.depth", and a dotted key is one capability's own`) {
		t.Errorf("a dotted key of no capability passed:\n%s", rec.errText())
	}
}
