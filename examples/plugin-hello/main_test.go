package main

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/sdk/sdktest"
)

// The example is held to the suite a scaffolded plugin ships with. It is the
// file an author reads first and copies from, and nothing held what it
// declares to the rules the suite holds theirs to: its description told an
// agent to ask for CSV "with -o csv", a flag no agent and no TUI has.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(func(string) map[string]map[string]any {
		return map[string]map[string]any{"hello.greet": {"name": "world"}}
	}))
}
