package app

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// --no-profile is a person's way out of the environment that is switched on,
// for one call: none of its values, none of its credentials. Its fence is one
// of those, so the call is not held to a list that belongs to a connection it
// does not use, and on a terminal the line above the result says which
// environment was left (TestTheRootCommandSaysWhichEnvironmentTheCallLeft). An
// agent has no such flag (it is on no MCP tool), so the fence still holds
// everything an agent sends.
func TestNoProfileLeavesTheFenceWithTheRestOfTheEnvironment(t *testing.T) {
	reg := fenceCLIRegistry(t)
	run := session(t, reg)
	if err := os.WriteFile(config.Path(), []byte(fencedCLI), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}

	if _, errOut, err := run("app", "status", "--namespace", "prod"); err == nil ||
		!strings.Contains(errOut, `--namespace is outside what profile "staging" reaches`) {
		t.Fatalf("the switched-on environment did not hold the call: %v %q", err, errOut)
	}

	out, errOut, err := run("app", "status", "--namespace", "prod", "--no-profile")
	if err != nil {
		t.Fatalf("a call that leaves the environment was held to its fence: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, `ran namespace="prod"`) {
		t.Errorf("the call did not run on what was typed: %q", out)
	}
}
