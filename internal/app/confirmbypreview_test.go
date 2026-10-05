package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
)

// The commands that are not capabilities ask the same question a capability
// does, at a terminal: `profile rm` and `plugin remove` were the ones that
// still sent a person away to type the command again with a flag.
func TestProfileRemoveAsksAPersonAtATerminal(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	before := configOf(t)

	asked := askedOf(t, "n\n", nil)
	out, errOut, err := run("profile", "rm", "staging", "--no-color")
	if ExitCode(err) != 3 {
		t.Fatalf("a no: err = %v (exit %d), want exit 3", err, ExitCode(err))
	}
	if configOf(t) != before {
		t.Error("the profile was removed although the answer was no")
	}
	if !strings.Contains(errOut, "would remove") || strings.Contains(out, "would remove") {
		t.Errorf("the preview belongs on stderr, not stdout: stdout %q, stderr %q", out, errOut)
	}
	if len(*asked) != 1 {
		t.Errorf("asked %q, want one question", *asked)
	}

	askedOf(t, "y\n", nil)
	out, errOut, err = run("profile", "rm", "staging", "--no-color")
	if err != nil {
		t.Fatalf("a yes: %v %q", err, errOut)
	}
	if !strings.Contains(out, "removed") || configOf(t) == before {
		t.Errorf("a yes did not remove the profile: stdout %q, config %s", out, configOf(t))
	}
}

func TestPluginRemoveAsksAPersonAtATerminal(t *testing.T) {
	run := session(t, registry.New())
	store := filepath.Join(plugindist.StoreDir(), "probe", strings.Repeat("ab", 32))
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}

	askedOf(t, "n\n", nil)
	_, errOut, err := run("plugin", "remove", "probe", "--no-color")
	if ExitCode(err) != 3 {
		t.Fatalf("a no: err = %v (exit %d), want exit 3", err, ExitCode(err))
	}
	if _, statErr := os.Stat(store); statErr != nil {
		t.Errorf("the store entry was removed although the answer was no: %v", statErr)
	}
	if !strings.Contains(errOut, "would remove") {
		t.Errorf("stderr = %q, want what would be removed", errOut)
	}

	askedOf(t, "y\n", nil)
	if _, errOut, err := run("plugin", "remove", "probe", "--no-color"); err != nil {
		t.Fatalf("a yes: %v %q", err, errOut)
	}
	if _, statErr := os.Stat(store); statErr == nil {
		t.Error("the store entry is still there after a yes")
	}
}

// A name nothing manages is reported, never asked about, at a terminal as
// anywhere.
func TestPluginRemoveOfAGhostIsNotAskedAbout(t *testing.T) {
	run := session(t, registry.New())
	asked := askedOf(t, "y\n", nil)
	_, _, err := run("plugin", "remove", "ghost")
	if verr := refusal(t, err); verr.Code != "plugin.remove.unknown" {
		t.Errorf("code = %q, want plugin.remove.unknown", verr.Code)
	}
	if len(*asked) != 0 {
		t.Errorf("a plugin nothing manages was asked about: %q", *asked)
	}
}

// A sweep that finds nothing is not a question: the answer is the answer.
func TestASweepWithNothingToDoAsksNothing(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "remove", "--all"},
		{"plugin", "untrust", "--all"},
		{"plugin", "prune"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			run := session(t, registry.New())
			asked := askedOf(t, "y\n", nil)
			out, errOut, err := run(args...)
			if err != nil {
				t.Fatalf("%v failed: %v (%s)", args, err, errOut)
			}
			if len(*asked) != 0 {
				t.Errorf("asked %q although there was nothing to do", *asked)
			}
			if !strings.Contains(out, "no plugin") && !strings.Contains(out, "nothing") {
				t.Errorf("stdout = %q, want it to say there was nothing", out)
			}
		})
	}
}

// Without a terminal nothing changes: the refusal, the exit 3 and no question.
func TestTheSweepsStillRefuseWhereNobodyIsTyping(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "remove", "--all"},
		{"plugin", "untrust", "--all"},
		{"plugin", "prune"},
		{"profile", "rm", "staging"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			run := session(t, setRegistry(t))
			if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"); err != nil {
				t.Fatalf("%v %q", err, errOut)
			}
			asked := askedOf(t, "y\n", nil)
			confirmTerminal = func() bool { return false }
			_, _, err := run(args...)
			if ExitCode(err) != 3 {
				t.Errorf("%v: exit %d, want 3", args, ExitCode(err))
			}
			if len(*asked) != 0 {
				t.Errorf("asked %q where nobody is typing", *asked)
			}
		})
	}
}
