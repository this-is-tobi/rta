package app

import (
	"os"
	"testing"
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
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rta-app-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("RTA_DATA_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
