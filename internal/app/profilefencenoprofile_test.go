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

// The ways across a fence that its refusal names all change the profile for
// good, and the one that does not — leaving the environment for this call, which
// the instance refusal already offers — was the one left unsaid. Offered to a
// call that did not name an environment, as that refusal does: one that named it
// with --profile asked for it.
func TestAFenceRefusalOffersNoProfileToACallThatNamedNoEnvironment(t *testing.T) {
	reg := fenceCLIRegistry(t)
	run := session(t, reg)
	if err := os.WriteFile(config.Path(), []byte(fencedCLI), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	const offer = "or --no-profile, to leave staging for this call"

	for name, args := range map[string][]string{
		"a value outside the list": {"app", "status", "--namespace", "prod"},
		"a blank":                  {"app", "status", "--namespace", ""},
		"a widening switch":        {"app", "status", "--namespace", "staging", "--all-namespaces"},
	} {
		_, errOut, err := run(args...)
		if err == nil || !strings.Contains(errOut, "core.profile.fenced") {
			t.Fatalf("%s: the call was not refused by the fence: %v\n%s", name, err, errOut)
		}
		if !strings.Contains(errOut, offer) {
			t.Errorf("%s: the refusal does not offer the way out that costs the profile nothing:\n%s", name, errOut)
		}
	}

	// A call that named the environment asked for it: leaving it is not what it
	// wanted, and the flag beside --profile is refused outright.
	_, errOut, err := run("app", "status", "--profile", "staging", "--namespace", "prod")
	if err == nil || !strings.Contains(errOut, "core.profile.fenced") {
		t.Fatalf("a named environment did not hold the call: %v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "--no-profile") {
		t.Errorf("a call that named the environment was offered a way out of it:\n%s", errOut)
	}
}
