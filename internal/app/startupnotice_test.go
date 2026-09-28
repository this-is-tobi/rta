package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/pkg/view"
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

// What could not be loaded is said once per cause, naming every plugin the
// cause stopped, and a coded refusal is drawn with its hint. The launch makes
// a new refusal for each plugin it tries, so the same words from two
// launches are one cause; the same words under another code or hint, or a
// problem that wraps nothing, are not.
func TestLoadProblemsAreSaidOncePerCauseAndCodedOnesKeepTheirHint(t *testing.T) {
	saved := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = saved })
	stderrIsTerminal = func() bool { return false }

	tooLong := func() error {
		return view.Errorf("plugin.tmpdir.toolong", "TMPDIR is too long").WithHint("use a shorter one")
	}
	unconfined := errors.New("sandbox-exec is not available")
	var out bytes.Buffer
	ReportLoadProblems(&out, []error{
		fmt.Errorf("plugin one: %w", tooLong()),
		fmt.Errorf("plugin two: %w", unconfined),
		fmt.Errorf("plugin three: %w", tooLong()),
		fmt.Errorf("plugin four: %w", unconfined),
		fmt.Errorf("plugin five: %w",
			view.Errorf("plugin.tmpdir.toolong", "TMPDIR is too long").WithHint("another hint")),
		fmt.Errorf("plugin six: %w", errors.New("starting plugin /bin/six: exit status 1")),
		errors.New("plugin seven is the same binary as the one already loaded as one"),
	}, nil)
	want := strings.Join([]string{
		"ERROR plugin.tmpdir.toolong plugins one, three: TMPDIR is too long",
		"HINT use a shorter one",
		"rta: plugins two, four: sandbox-exec is not available",
		"ERROR plugin.tmpdir.toolong plugin five: TMPDIR is too long",
		"HINT another hint",
		"rta: plugin six: starting plugin /bin/six: exit status 1",
		"rta: plugin seven is the same binary as the one already loaded as one",
	}, "\n") + "\n"
	if out.String() != want {
		t.Errorf("said:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A load problem on a terminal is drawn in colour unless the command line
// says --no-color. The lines it replaced were plain text, and it is drawn
// before the command line is parsed, so the flag the command then honours
// was not there yet to refuse the badges.
func TestALoadProblemHonoursNoColorOnTheCommandLine(t *testing.T) {
	savedErr, savedOut := stderrIsTerminal, isTTY
	t.Cleanup(func() { stderrIsTerminal, isTTY = savedErr, savedOut })
	stderrIsTerminal = func() bool { return true }
	isTTY = func() bool { return true }

	problems := []error{fmt.Errorf("plugin one: %w",
		view.Errorf("plugin.tmpdir.toolong", "TMPDIR is too long").WithHint("use a shorter one"))}
	escape := string(rune(0x1b))
	for _, c := range []struct {
		args   []string
		colour bool
	}{
		{nil, true},
		{[]string{"--no-color", "sys", "host"}, false},
		{[]string{"sys", "host", "--no-color=true"}, false},
		{[]string{"--no-color", "--no-color=false"}, true},
		{[]string{"plugin", "dev", "--", "--no-color"}, true},
	} {
		var out bytes.Buffer
		ReportLoadProblems(&out, problems, c.args)
		if got := strings.Contains(out.String(), escape); got != c.colour {
			t.Errorf("%q: colour %v, want %v: %q", c.args, got, c.colour, out.String())
		}
		if !strings.Contains(out.String(), "use a shorter one") {
			t.Errorf("%q: the hint is gone: %q", c.args, out.String())
		}
	}
}
