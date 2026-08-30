package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
)

// **A sentence of English above somebody's JSON is a sentence they have to
// strip out of what they copied off the screen.**
//
// The notice about artifacts found and not run printed whenever stderr was a
// terminal, which is the right question about whether anybody is watching and
// the wrong one on its own. `rta plugin trust -o json` at a prompt put a
// paragraph of prose above the object it printed, and the output a person
// reads off their screen is the output they paste into a parser.
//
// It is not enough that the two are on different streams. They are on the
// same *screen*, which is where the copy comes from.
func TestTheStartupNoticeStaysOutOfMachineReadableOutput(t *testing.T) {
	SetUntrustedPlugins([]pluginhost.Untrusted{
		{Name: "weather", Path: "/usr/local/bin/rta-plugin-weather", Digest: strings.Repeat("cd", 32)},
	})
	t.Cleanup(func() { SetUntrustedPlugins(nil) })

	var machine bytes.Buffer
	WarnUntrustedPlugins(&machine, true)
	if machine.Len() != 0 {
		t.Errorf("a run asking for a parseable format got prose: %q", machine.String())
	}

	// …and a person reading prose still gets told. A trust gate's failure
	// mode is silence, so the fix must not be "say nothing".
	var human bytes.Buffer
	WarnUntrustedPlugins(&human, false)
	for _, want := range []string{"weather", "not run", "rta plugin trust"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("the notice no longer says %q: %q", want, human.String())
		}
	}
}

// An artifact whose name something already answers to is not a pending
// decision — approving it earns a namespace collision on the next start — so
// it is not counted among the plugins waiting, or offered that remedy.
func TestACollidingArtifactIsNotOfferedTrustAsTheRemedy(t *testing.T) {
	SetUntrustedPlugins([]pluginhost.Untrusted{
		{Name: "kv", Path: "/usr/local/bin/rta-plugin-kv", Digest: strings.Repeat("ab", 32), Taken: true},
	})
	t.Cleanup(func() { SetUntrustedPlugins(nil) })

	var out bytes.Buffer
	WarnUntrustedPlugins(&out, false)
	got := out.String()
	if !strings.Contains(got, "already registered") {
		t.Errorf("a colliding artifact is not described as one: %q", got)
	}
	if strings.Contains(got, "installed and not run") {
		t.Errorf("a colliding artifact was counted among the ones waiting to be trusted: %q", got)
	}
}

// The wiring, not just the function.
//
// The notice's condition has two halves and only one of them was reachable
// from a test: with stderr never a terminal under `go test`, a version that
// passed the format check the wrong way round would have looked identical.
// So the terminal question is a var, and this drives the real root command
// with a real `-o json` to check the half that was broken.
func TestTheRootCommandDecidesTheNoticeByTheRequestedFormat(t *testing.T) {
	saved := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = saved })

	SetUntrustedPlugins([]pluginhost.Untrusted{
		{Name: "weather", Path: "/usr/local/bin/rta-plugin-weather", Digest: strings.Repeat("cd", 32)},
	})
	t.Cleanup(func() { SetUntrustedPlugins(nil) })

	reg := connRegistry(t)
	if _, errOut, err := runWith(t, reg, "", "db", "status"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(errOut, "installed and not run") {
		t.Errorf("a person reading prose was not told a decision is waiting: %q", errOut)
	}
	out, errOut, err := runWith(t, reg, "", "db", "status", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "installed and not run") {
		t.Errorf("a run asking for json got prose beside it: %q", errOut)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("stdout is not valid json:\n%s", out)
	}

	// And the other half of the condition, which the format check must not
	// be allowed to replace: a script's stderr is somewhere nobody is
	// reading, and repeating a pending decision into it on every invocation
	// is how the message stops being read anywhere.
	stderrIsTerminal = func() bool { return false }
	if _, errOut, err := runWith(t, reg, "", "db", "status"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(errOut, "installed and not run") {
		t.Errorf("the notice was written to a stream nobody is watching: %q", errOut)
	}
}
