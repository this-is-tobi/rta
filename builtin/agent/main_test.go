package agent

import (
	"os"
	"testing"

	"github.com/this-is-tobi/rta/internal/testenv"
)

// The person's own configuration is out of reach of every test in the package.
//
// The tests of `show` and of the consent page load the configuration without naming a
// file of their own, and read whatever the developer's `~/.config/rta/config.yaml`
// holds, so a `theme:` or a profile there could change what they drew.
// testenv.Isolate points HOME, every XDG directory, RTA_CONFIG and RTA_DATA_DIR
// at an empty place of this binary's own; a test that wants one of its own names it
// with t.Setenv. Set for every test rather than in the helpers, because the rule is
// about the package and a helper is something a new test can forget to call.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rta-agent-tests")
	if err != nil {
		panic(err)
	}
	if err := testenv.Isolate(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
