package app

import (
	"os"
	"testing"

	"github.com/this-is-tobi/rta/internal/testenv"
)

// A data directory of this binary's own, for the whole package.
//
// Running a capability now records what it was given, so a later completion
// can offer it back (internal/recent). Without this a test's fixture values
// land in the developer's own ~/.local/share/rta and come back as suggestions
// in their real shell — a dirty test, and a surprising thing to do to
// somebody's machine. Set for every test rather than in the helpers, because
// the rule is about the package and a helper is something a new test can
// forget to call; a test that wants its own directory still overrides it.
//
// COLUMNS is cleared for the same reason. Pretty output is shaped to it
// whenever it is set, a pipe included, and the commands these tests run
// render through termWidth — so a shell rc, an IDE terminal or `watch` that
// exports it wrapped every refusal and every explain page at that width,
// and assertions written for one unbroken line failed on that machine
// alone. A test that wants a width says so itself, with t.Setenv.
//
// The person's own configuration is out of reach for a worse reason. NewRoot loads
// the config file and the environment that is switched on, so the tests that build a
// root without naming a config file of their own read whatever the developer's
// `~/.config/rta/config.yaml` holds, and a `theme:`, an `output:` or a profile there
// changes what they draw. testenv.Isolate points HOME, every XDG directory and
// RTA_CONFIG at an empty place of this binary's own; a test that wants a config file
// names it with t.Setenv, as the config tests do.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rta-app-tests")
	if err != nil {
		panic(err)
	}
	if err := testenv.Isolate(dir); err != nil {
		panic(err)
	}
	os.Setenv("RTA_DATA_DIR", dir)
	os.Unsetenv("COLUMNS")
	// Nobody is at the keyboard: a destructive run is refused for want of
	// --yes, as it is in a script, and not asked about on whatever terminal
	// `go test` was started from. A test of the question says otherwise.
	confirmTerminal = func() bool { return false }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
